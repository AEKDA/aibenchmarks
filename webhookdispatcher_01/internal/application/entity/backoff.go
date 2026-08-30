package entity

import "time"

const (
	// BackoffBase is the first-retry delay before jitter.
	BackoffBase = time.Second
	// MaxAttempts is how many attempts a delivery gets before DEAD_LETTER.
	MaxAttempts = 5
	// JitterFraction is the maximum relative deviation applied to a delay.
	JitterFraction = 0.5

	// maxBackoffShift keeps 2^attempt from overflowing the duration.
	maxBackoffShift = 16
)

// BackoffDelay returns base*2^attempt adjusted by jitter of at most
// ±JitterFraction. attempt is zero-based: 0 is the delay before the first
// retry. randFloat must return a value in [0,1); when nil, no jitter is
// applied. The result is always strictly positive.
func BackoffDelay(attempt int, randFloat func() float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > maxBackoffShift {
		attempt = maxBackoffShift
	}
	base := BackoffBase << uint(attempt)

	factor := 1.0
	if randFloat != nil {
		r := randFloat()
		if r < 0 {
			r = 0
		}
		if r >= 1 {
			r = 1
		}
		// map [0,1] onto [1-JitterFraction, 1+JitterFraction]
		factor = 1 + (r*2-1)*JitterFraction
	}

	delay := time.Duration(float64(base) * factor)
	if delay <= 0 {
		delay = time.Nanosecond
	}
	return delay
}

// NextAttemptAt is the wall-clock time of the retry that follows attemptCount
// completed attempts.
func NextAttemptAt(now time.Time, attemptCount int, randFloat func() float64) time.Time {
	return now.UTC().Add(BackoffDelay(attemptCount-1, randFloat))
}

// AttemptsExhausted reports whether the delivery has used up its budget.
func AttemptsExhausted(attemptCount int) bool { return attemptCount >= MaxAttempts }
