package instruction

import (
	"context"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
)

// ScheduleRetry decides what happens to a delivery after a retryable failure:
// another attempt with exponential backoff, or DEAD_LETTER once the attempt
// budget is spent.
type ScheduleRetry struct {
	clock     ports.Clock
	randFloat func() float64
}

// NewScheduleRetry builds the instruction. randFloat supplies the jitter and
// must return a value in [0,1); pass nil to disable jitter.
func NewScheduleRetry(clock ports.Clock, randFloat func() float64) *ScheduleRetry {
	return &ScheduleRetry{clock: clock, randFloat: randFloat}
}

// Invoke moves the delivery to RETRYING with a scheduled next attempt, or to
// DEAD_LETTER when entity.MaxAttempts attempts have been made.
func (i *ScheduleRetry) Invoke(ctx context.Context, delivery *entity.Delivery, result entity.AttemptResult) error {
	if err := ctx.Err(); err != nil {
		return errs.Wrapf(err, "schedule retry")
	}
	now := i.clock.Now(ctx)
	code := statusCodePtr(result)

	if entity.AttemptsExhausted(delivery.AttemptCount) {
		if err := delivery.MarkDeadLetter(code, "attempt budget exhausted: "+result.Reason(), now); err != nil {
			return errs.Wrapf(err, "dead-letter delivery %s", delivery.ID)
		}
		return nil
	}

	next := entity.NextAttemptAt(now, delivery.AttemptCount, i.randFloat)
	if err := delivery.MarkRetrying(next, code, result.Reason(), now); err != nil {
		return errs.Wrapf(err, "schedule retry for delivery %s", delivery.ID)
	}
	return nil
}

func statusCodePtr(result entity.AttemptResult) *int {
	if result.StatusCode == 0 {
		return nil
	}
	code := result.StatusCode
	return &code
}
