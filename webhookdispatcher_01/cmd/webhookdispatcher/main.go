// Command webhookdispatcher runs the webhook delivery service: the REST API
// and the background delivery workers, sharing one PostgreSQL pool.
package main

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	stdhttp "net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/example/webhookdispatcher/internal/adapter/driven/clock"
	"github.com/example/webhookdispatcher/internal/adapter/driven/httpsender"
	"github.com/example/webhookdispatcher/internal/adapter/driven/postgres"
	"github.com/example/webhookdispatcher/internal/adapter/driven/ratelimiter"
	httpapi "github.com/example/webhookdispatcher/internal/adapter/driver/http"
	"github.com/example/webhookdispatcher/internal/adapter/driver/worker"
	"github.com/example/webhookdispatcher/internal/application/instruction"
	"github.com/example/webhookdispatcher/internal/application/usecase"
	"github.com/example/webhookdispatcher/internal/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("service stopped with an error", slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Info("service stopped")
}

// run wires every adapter to the use cases and blocks until a termination
// signal arrives, then shuts the HTTP server and the workers down.
func run(logger *slog.Logger) error {
	// A second signal aborts immediately, because stop() is only deferred.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadFromOS()
	if err != nil {
		return err
	}

	pool, err := postgres.NewPool(ctx, cfg.PostgresDSN)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.Migrate(ctx, pool); err != nil {
		return err
	}

	// Driven adapters.
	var (
		subscriptions = postgres.NewSubscriptionRepository(pool)
		events        = postgres.NewEventRepository(pool)
		deliveries    = postgres.NewDeliveryRepository(pool)
		sender        = httpsender.New(cfg.UserAgent, cfg.SendTimeout)
		limiter       = ratelimiter.New()
		systemClock   = clock.New()
	)

	// Application layer.
	var (
		sign          = instruction.NewSignPayload()
		scheduleRetry = instruction.NewScheduleRetry(systemClock, rand.Float64)

		createSubscription = usecase.NewCreateSubscription(subscriptions, systemClock)
		publishEvent       = usecase.NewPublishEvent(events, subscriptions, systemClock)
		getDelivery        = usecase.NewGetDelivery(deliveries)
		claimDeliveries    = usecase.NewClaimDeliveries(deliveries, systemClock)
		releaseStale       = usecase.NewReleaseStaleDeliveries(deliveries, systemClock)
		processDelivery    = usecase.NewProcessDelivery(
			deliveries, events, subscriptions, sender, limiter, systemClock, sign, scheduleRetry)
	)

	// Driver adapters.
	handlers := httpapi.NewHandlers(createSubscription, publishEvent, getDelivery, logger)
	server := &stdhttp.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(handlers),
		ReadHeaderTimeout: 10 * time.Second,
	}
	workers := worker.New(claimDeliveries, processDelivery, releaseStale, worker.Config{
		Size:            cfg.WorkerPoolSize,
		BatchSize:       cfg.BatchSize,
		PollInterval:    cfg.PollInterval,
		ReleaseInterval: cfg.ReleaseInterval,
		StaleAfter:      cfg.StaleThreshold,
		ShutdownGrace:   cfg.ShutdownTimeout,
	}, logger)

	var (
		wg       sync.WaitGroup
		runErr   error
		errOnce  sync.Once
		fail     = func(err error) { errOnce.Do(func() { runErr = err }) }
		serverUp = make(chan struct{})
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		close(serverUp)
		logger.Info("http server listening", slog.String("addr", cfg.HTTPAddr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
			fail(err)
			stop() // a dead listener must bring the workers down too
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		logger.Info("worker pool starting", slog.Int("size", cfg.WorkerPoolSize))
		if err := workers.Run(ctx); err != nil {
			fail(err)
			stop()
		}
	}()

	<-serverUp
	<-ctx.Done()
	logger.Info("shutdown signal received")

	// Stop accepting requests, then let in-flight work finish within the budget.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown failed", slog.String("error", err.Error()))
		fail(err)
		_ = server.Close()
	}

	wg.Wait()
	return runErr
}
