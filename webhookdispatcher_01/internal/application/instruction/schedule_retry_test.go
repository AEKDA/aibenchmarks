package instruction_test

import (
	"context"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/instruction"
	"github.com/google/uuid"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now(context.Context) time.Time { return c.t }

var base = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

// deliveryAfterAttempts returns a delivery in SENDING that has already made
// `attempts` attempts, including the one in flight.
func deliveryAfterAttempts(t *testing.T, attempts int) *entity.Delivery {
	t.Helper()
	d := entity.NewDelivery(uuid.New(), uuid.New(), base)
	for i := 0; i < attempts; i++ {
		if err := d.MarkSending(base); err != nil {
			t.Fatalf("MarkSending: %v", err)
		}
		if i < attempts-1 {
			if err := d.MarkRetrying(base, nil, "retry", base); err != nil {
				t.Fatalf("MarkRetrying: %v", err)
			}
		}
	}
	return d
}

func TestScheduleRetrySchedulesWithinBudget(t *testing.T) {
	sched := instruction.NewScheduleRetry(fixedClock{base}, nil)
	for attempts := 1; attempts < entity.MaxAttempts; attempts++ {
		d := deliveryAfterAttempts(t, attempts)
		if err := sched.Invoke(context.Background(), d, entity.AttemptResult{StatusCode: 503}); err != nil {
			t.Fatalf("attempts=%d Invoke: %v", attempts, err)
		}
		if d.Status != entity.StatusRetrying {
			t.Fatalf("attempts=%d status = %s, want RETRYING", attempts, d.Status)
		}
		want := base.Add(entity.BackoffDelay(attempts-1, nil))
		if d.NextAttemptAt == nil || !d.NextAttemptAt.Equal(want) {
			t.Fatalf("attempts=%d NextAttemptAt = %v, want %v", attempts, d.NextAttemptAt, want)
		}
		if d.LastStatusCode == nil || *d.LastStatusCode != 503 {
			t.Fatalf("attempts=%d LastStatusCode = %v, want 503", attempts, d.LastStatusCode)
		}
	}
}

func TestScheduleRetryDeadLettersWhenBudgetSpent(t *testing.T) {
	sched := instruction.NewScheduleRetry(fixedClock{base}, nil)
	d := deliveryAfterAttempts(t, entity.MaxAttempts)
	if err := sched.Invoke(context.Background(), d, entity.AttemptResult{StatusCode: 500}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if d.Status != entity.StatusDeadLetter {
		t.Fatalf("status = %s, want DEAD_LETTER", d.Status)
	}
	if d.NextAttemptAt != nil {
		t.Fatalf("NextAttemptAt = %v, want nil", d.NextAttemptAt)
	}
}

func TestScheduleRetryKeepsNilCodeForTransportFailure(t *testing.T) {
	sched := instruction.NewScheduleRetry(fixedClock{base}, nil)
	d := deliveryAfterAttempts(t, 1)
	if err := sched.Invoke(context.Background(), d, entity.AttemptResult{TimedOut: true}); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if d.LastStatusCode != nil {
		t.Fatalf("LastStatusCode = %v, want nil", d.LastStatusCode)
	}
	if d.LastError == "" {
		t.Fatal("LastError must describe the timeout")
	}
}

func TestScheduleRetryRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := deliveryAfterAttempts(t, 1)
	if err := instruction.NewScheduleRetry(fixedClock{base}, nil).Invoke(ctx, d, entity.AttemptResult{StatusCode: 500}); err == nil {
		t.Fatal("expected context error")
	}
	if d.Status != entity.StatusSending {
		t.Fatalf("status changed to %s on canceled context", d.Status)
	}
}
