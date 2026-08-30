// Package worker is the inbound background adapter: a pool of goroutines that
// polls the storage port for ready deliveries and drives one attempt each.
package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/ports"
)

// Config tunes the pool.
type Config struct {
	// Size is the number of concurrent delivery workers.
	Size int
	// BatchSize is how many deliveries one poll claims at most.
	BatchSize int
	// PollInterval is how long the poller sleeps when there is no work.
	PollInterval time.Duration
	// ReleaseInterval is how often the stale-delivery reaper runs.
	ReleaseInterval time.Duration
	// StaleAfter is how long a delivery may stay claimed before it is released.
	StaleAfter time.Duration
	// ShutdownGrace is how long in-flight attempts may finish after the pool
	// has been told to stop. Attempts still running when it expires are
	// cancelled, and the stale reaper requeues their deliveries later.
	ShutdownGrace time.Duration
}

func (c Config) withDefaults() Config {
	if c.Size <= 0 {
		c.Size = 1
	}
	if c.BatchSize <= 0 {
		c.BatchSize = c.Size
	}
	if c.PollInterval <= 0 {
		c.PollInterval = time.Second
	}
	if c.ReleaseInterval <= 0 {
		c.ReleaseInterval = 30 * time.Second
	}
	if c.StaleAfter <= 0 {
		c.StaleAfter = time.Minute
	}
	if c.ShutdownGrace <= 0 {
		c.ShutdownGrace = 10 * time.Second
	}
	return c
}

// Pool claims ready deliveries and processes them concurrently.
type Pool struct {
	claim   ports.ClaimDeliveries
	process ports.ProcessDelivery
	release ports.ReleaseStaleDeliveries
	cfg     Config
	logger  *slog.Logger
}

// New builds the pool.
func New(
	claim ports.ClaimDeliveries,
	process ports.ProcessDelivery,
	release ports.ReleaseStaleDeliveries,
	cfg Config,
	logger *slog.Logger,
) *Pool {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pool{claim: claim, process: process, release: release, cfg: cfg.withDefaults(), logger: logger}
}

// Run blocks until ctx is done. Cancelling ctx stops the polling and the
// reaper immediately, while attempts already in flight are given
// Config.ShutdownGrace to finish and persist their outcome. Run returns only
// after every goroutine it started has finished, so it leaks nothing.
func (p *Pool) Run(ctx context.Context) error {
	jobs := make(chan *entity.Delivery)

	// In-flight attempts survive the stop signal for the grace period, so a
	// shutdown does not abandon a delivery mid-attempt.
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()

	var pollers, workers sync.WaitGroup

	pollers.Add(1)
	go func() {
		defer pollers.Done()
		p.poll(ctx, jobs)
	}()

	pollers.Add(1)
	go func() {
		defer pollers.Done()
		p.reap(ctx)
	}()

	for i := 0; i < p.cfg.Size; i++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			p.work(workCtx, worker, jobs)
		}(i)
	}

	workersDone := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersDone)
	}()

	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		select {
		case <-workersDone:
			return
		case <-ctx.Done():
		}
		timer := time.NewTimer(p.cfg.ShutdownGrace)
		defer timer.Stop()
		select {
		case <-workersDone:
		case <-timer.C:
			p.logger.Warn("shutdown grace expired, cancelling in-flight attempts")
			cancelWork()
		}
	}()

	pollers.Wait()
	<-workersDone
	<-watchdogDone
	p.logger.Info("worker pool stopped")
	return nil
}

// poll claims ready deliveries and hands them to the workers. It owns the jobs
// channel and closes it on the way out, which is what stops the workers.
func (p *Pool) poll(ctx context.Context, jobs chan<- *entity.Delivery) {
	defer close(jobs)
	for {
		if ctx.Err() != nil {
			return
		}
		claimed, err := p.claim.Invoke(ctx, p.cfg.BatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.logger.Error("claim deliveries failed", slog.String("error", err.Error()))
			if !sleep(ctx, p.cfg.PollInterval) {
				return
			}
			continue
		}
		if len(claimed) == 0 {
			if !sleep(ctx, p.cfg.PollInterval) {
				return
			}
			continue
		}
		for _, delivery := range claimed {
			select {
			case jobs <- delivery:
			case <-ctx.Done():
				// The remaining claimed deliveries stay in SENDING; the reaper
				// puts them back into the queue after StaleAfter.
				return
			}
		}
	}
}

// work runs one attempt per delivery. A failing delivery is logged and never
// takes the worker down.
func (p *Pool) work(ctx context.Context, worker int, jobs <-chan *entity.Delivery) {
	for delivery := range jobs {
		if err := p.process.Invoke(ctx, delivery); err != nil {
			p.logger.Error("delivery attempt failed",
				slog.Int("worker", worker),
				slog.String("delivery_id", delivery.ID.String()),
				slog.String("error", err.Error()))
		}
	}
}

// reap periodically returns deliveries abandoned in SENDING to the queue.
func (p *Pool) reap(ctx context.Context) {
	for {
		released, err := p.release.Invoke(ctx, p.cfg.StaleAfter)
		switch {
		case err != nil && ctx.Err() == nil:
			p.logger.Error("release stale deliveries failed", slog.String("error", err.Error()))
		case err == nil && released > 0:
			p.logger.Warn("released stale deliveries", slog.Int("count", released))
		}
		if !sleep(ctx, p.cfg.ReleaseInterval) {
			return
		}
	}
}

// sleep waits for d, reporting false when the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
