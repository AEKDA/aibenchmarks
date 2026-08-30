package entity_test

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
)

func TestBackoffDelayWithinJitterBounds(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for attempt := 0; attempt < entity.MaxAttempts; attempt++ {
		base := time.Duration(math.Pow(2, float64(attempt))) * entity.BackoffBase
		low := time.Duration(float64(base) * (1 - entity.JitterFraction))
		high := time.Duration(float64(base) * (1 + entity.JitterFraction))
		for i := 0; i < 500; i++ {
			got := entity.BackoffDelay(attempt, r.Float64)
			if got <= 0 {
				t.Fatalf("attempt %d: delay = %s, want > 0", attempt, got)
			}
			if got < low || got > high {
				t.Fatalf("attempt %d: delay = %s, want within [%s, %s]", attempt, got, low, high)
			}
		}
	}
}

func TestBackoffDelayGrowsExponentially(t *testing.T) {
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	for attempt, expected := range want {
		got := entity.BackoffDelay(attempt, nil) // nil rand => no jitter
		if got != expected {
			t.Fatalf("attempt %d: delay = %s, want %s", attempt, got, expected)
		}
	}
}

func TestBackoffDelayJitterVaries(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		seen[entity.BackoffDelay(2, r.Float64)] = true
	}
	if len(seen) < 2 {
		t.Fatalf("jitter produced %d distinct delays, want at least 2", len(seen))
	}
}

func TestBackoffDelayEdgeCases(t *testing.T) {
	if got := entity.BackoffDelay(-5, nil); got != entity.BackoffBase {
		t.Fatalf("negative attempt: delay = %s, want %s", got, entity.BackoffBase)
	}
	if got := entity.BackoffDelay(1000, nil); got <= 0 {
		t.Fatalf("huge attempt: delay = %s, want > 0", got)
	}
	// Extreme jitter sources must still yield a positive delay.
	for _, f := range []func() float64{func() float64 { return 0 }, func() float64 { return 0.999999 }, func() float64 { return -1 }, func() float64 { return 2 }} {
		if got := entity.BackoffDelay(0, f); got <= 0 {
			t.Fatalf("delay = %s, want > 0", got)
		}
	}
}

func TestNextAttemptAtUsesCompletedAttempts(t *testing.T) {
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	got := entity.NextAttemptAt(base, 1, nil)
	if !got.Equal(base.Add(time.Second)) {
		t.Fatalf("NextAttemptAt = %s, want %s", got, base.Add(time.Second))
	}
	got = entity.NextAttemptAt(base, 3, nil)
	if !got.Equal(base.Add(4 * time.Second)) {
		t.Fatalf("NextAttemptAt = %s, want %s", got, base.Add(4*time.Second))
	}
}

func TestAttemptsExhausted(t *testing.T) {
	if entity.AttemptsExhausted(entity.MaxAttempts - 1) {
		t.Fatal("budget must remain before the last attempt")
	}
	if !entity.AttemptsExhausted(entity.MaxAttempts) {
		t.Fatal("budget must be exhausted at MaxAttempts")
	}
}
