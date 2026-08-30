package worker_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/adapter/driver/worker"
	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/google/uuid"
)

type fakeClaim struct {
	mu      sync.Mutex
	batches [][]*entity.Delivery
	err     error
	calls   int
}

func (f *fakeClaim) Invoke(_ context.Context, _ int) ([]*entity.Delivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if len(f.batches) == 0 {
		return nil, nil
	}
	batch := f.batches[0]
	f.batches = f.batches[1:]
	return batch, nil
}

type fakeProcess struct {
	mu        sync.Mutex
	processed []uuid.UUID
	failFor   map[uuid.UUID]bool
	done      chan struct{}
	want      int
}

func newFakeProcess(want int) *fakeProcess {
	return &fakeProcess{failFor: map[uuid.UUID]bool{}, done: make(chan struct{}), want: want}
}

func (f *fakeProcess) Invoke(_ context.Context, d *entity.Delivery) error {
	f.mu.Lock()
	f.processed = append(f.processed, d.ID)
	reached := len(f.processed) == f.want
	shouldFail := f.failFor[d.ID]
	f.mu.Unlock()
	if reached {
		close(f.done)
	}
	if shouldFail {
		return errors.New("subscriber unreachable")
	}
	return nil
}

func (f *fakeProcess) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.processed)
}

type fakeRelease struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeRelease) Invoke(context.Context, time.Duration) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return 0, f.err
}

func (f *fakeRelease) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func deliveries(n int) []*entity.Delivery {
	out := make([]*entity.Delivery, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, entity.NewDelivery(uuid.New(), uuid.New(), time.Now()))
	}
	return out
}

func testConfig() worker.Config {
	return worker.Config{
		Size:            4,
		BatchSize:       5,
		PollInterval:    5 * time.Millisecond,
		ReleaseInterval: 5 * time.Millisecond,
		StaleAfter:      time.Minute,
	}
}

func TestPoolProcessesClaimedDeliveries(t *testing.T) {
	batch := deliveries(5)
	claim := &fakeClaim{batches: [][]*entity.Delivery{batch}}
	process := newFakeProcess(len(batch))
	release := &fakeRelease{}

	pool := worker.New(claim, process, release, testConfig(), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	select {
	case <-process.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("processed %d deliveries, want %d", process.count(), len(batch))
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
	if release.count() == 0 {
		t.Fatal("the stale reaper never ran")
	}
}

func TestPoolKeepsGoingAfterAFailedDelivery(t *testing.T) {
	batch := deliveries(4)
	claim := &fakeClaim{batches: [][]*entity.Delivery{batch}}
	process := newFakeProcess(len(batch))
	process.failFor[batch[0].ID] = true
	process.failFor[batch[2].ID] = true

	pool := worker.New(claim, process, &fakeRelease{}, testConfig(), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	select {
	case <-process.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("processed %d deliveries, want %d", process.count(), len(batch))
	}
	cancel()
	<-done
}

func TestPoolSurvivesClaimFailures(t *testing.T) {
	claim := &fakeClaim{err: errors.New("db down")}
	release := &fakeRelease{err: errors.New("db down")}
	pool := worker.New(claim, newFakeProcess(0), release, testConfig(), discardLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := pool.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	claim.mu.Lock()
	calls := claim.calls
	claim.mu.Unlock()
	if calls < 2 {
		t.Fatalf("claim called %d times, want the poller to keep retrying", calls)
	}
}

func TestPoolStopsEveryGoroutineOnCancel(t *testing.T) {
	before := runtime.NumGoroutine()

	claim := &fakeClaim{}
	pool := worker.New(claim, newFakeProcess(0), &fakeRelease{}, testConfig(), discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	// Goroutines exit asynchronously; give the runtime a moment to settle.
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines leaked: %d before, %d after", before, runtime.NumGoroutine())
}

// blockingProcess holds an attempt until released, so a shutdown can be
// observed while a delivery is in flight.
type blockingProcess struct {
	started  chan struct{}
	release  chan struct{}
	mu       sync.Mutex
	finished bool
	ctxLive  bool
}

func (b *blockingProcess) Invoke(ctx context.Context, _ *entity.Delivery) error {
	close(b.started)
	<-b.release
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finished = true
	// A live context here means the attempt could still persist its outcome.
	b.ctxLive = ctx.Err() == nil
	return nil
}

func TestPoolLetsInFlightAttemptsFinishAfterCancel(t *testing.T) {
	process := &blockingProcess{started: make(chan struct{}), release: make(chan struct{})}
	claim := &fakeClaim{batches: [][]*entity.Delivery{deliveries(1)}}
	cfg := testConfig()
	cfg.Size = 1
	cfg.ShutdownGrace = 5 * time.Second

	pool := worker.New(claim, process, &fakeRelease{}, cfg, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	<-process.started
	cancel() // shutdown while the attempt is in flight

	select {
	case <-done:
		t.Fatal("Run returned before the in-flight attempt finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(process.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the attempt finished")
	}

	process.mu.Lock()
	defer process.mu.Unlock()
	if !process.finished {
		t.Fatal("the in-flight attempt was abandoned")
	}
	if !process.ctxLive {
		t.Fatal("the in-flight attempt lost its context and could not persist its outcome")
	}
}

func TestPoolCancelsInFlightAttemptsAfterGrace(t *testing.T) {
	process := &blockingProcess{started: make(chan struct{}), release: make(chan struct{})}
	claim := &fakeClaim{batches: [][]*entity.Delivery{deliveries(1)}}
	cfg := testConfig()
	cfg.Size = 1
	cfg.ShutdownGrace = 50 * time.Millisecond

	pool := worker.New(claim, process, &fakeRelease{}, cfg, discardLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()

	<-process.started
	cancel()
	time.Sleep(200 * time.Millisecond) // grace expires while the attempt hangs
	close(process.release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the grace period")
	}
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.ctxLive {
		t.Fatal("the attempt context should have been cancelled once the grace expired")
	}
}
