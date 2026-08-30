package ratelimiter_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/adapter/driven/ratelimiter"
	"github.com/example/webhookdispatcher/internal/application/errs"
)

func TestWaitEnforcesRPSPerHost(t *testing.T) {
	limiter := ratelimiter.New()
	ctx := context.Background()
	const rps = 2

	start := time.Now()
	// The bucket starts full (burst == rps), so the first `rps` calls are free
	// and the next four cost two seconds in total.
	for i := 0; i < rps+4; i++ {
		if err := limiter.Wait(ctx, "a.example.com", rps); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Fatalf("6 calls at %d rps took %s, want at least 1.5s", rps, elapsed)
	}
}

func TestWaitIsIndependentPerHost(t *testing.T) {
	limiter := ratelimiter.New()
	ctx := context.Background()

	// Drain the bucket of the busy host.
	for i := 0; i < 3; i++ {
		if err := limiter.Wait(ctx, "busy.example.com", 1); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	start := time.Now()
	if err := limiter.Wait(ctx, "idle.example.com", 1); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("an idle host waited %s behind a busy one", elapsed)
	}
}

func TestWaitReturnsOnCanceledContext(t *testing.T) {
	limiter := ratelimiter.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Wait(ctx, "a.example.com", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestWaitStopsWhenContextIsCanceledWhileQueued(t *testing.T) {
	limiter := ratelimiter.New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Burst of 1: the second call has to queue for a second and must give up
	// when the context expires.
	if err := limiter.Wait(ctx, "slow.example.com", 1); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	start := time.Now()
	if err := limiter.Wait(ctx, "slow.example.com", 1); err == nil {
		t.Fatal("expected the queued wait to fail")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("queued wait returned after %s, want it to give up with the context", elapsed)
	}
}

func TestWaitRejectsNonPositiveRPS(t *testing.T) {
	if err := ratelimiter.New().Wait(context.Background(), "a.example.com", 0); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestWaitIsSafeForConcurrentUse(t *testing.T) {
	limiter := ratelimiter.New()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := "a.example.com"
			if i%2 == 0 {
				host = "b.example.com"
			}
			// Vary rps so the retune path is exercised concurrently too.
			if err := limiter.Wait(ctx, host, 50+i%3); err != nil {
				t.Errorf("Wait: %v", err)
			}
		}(i)
	}
	wg.Wait()
}
