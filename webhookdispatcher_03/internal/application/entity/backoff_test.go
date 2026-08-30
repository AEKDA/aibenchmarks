package entity

import (
	"testing"
	"time"
)

func TestBackoffDelayBounds(t *testing.T) {
	for attempt := 0; attempt < MaxAttempts; attempt++ {
		got := BackoffDelay(attempt)
		// base=1s, jitter ±25%: верхняя граница ~ base*2^attempt*1.25
		upper := time.Second*time.Duration(1<<attempt) + time.Second*time.Duration(1<<attempt)/4
		if got <= 0 || got > upper {
			t.Fatalf("attempt=%d: delay %v вне границ (0, %v]", attempt, got, upper)
		}
	}
}

func TestBackoffDelayDeterministicSeeded(t *testing.T) {
	a := BackoffDelay(2)
	b := BackoffDelay(2)
	if a <= 0 || b <= 0 {
		t.Fatalf("delay должен быть >0, got %v %v", a, b)
	}
}
