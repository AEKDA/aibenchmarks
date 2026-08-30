package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/instruction"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
)

// webhookBody is the payload delivered to subscribers. event_id is stable
// across retries so subscribers can deduplicate an at-least-once delivery.
type webhookBody struct {
	EventID    uuid.UUID       `json:"event_id"`
	DeliveryID uuid.UUID       `json:"delivery_id"`
	Type       string          `json:"type"`
	Attempt    int             `json:"attempt"`
	CreatedAt  time.Time       `json:"created_at"`
	Payload    json.RawMessage `json:"payload"`
}

// ProcessDelivery performs one attempt for a claimed delivery: build the body,
// wait for the target host's rate limit, sign, send, classify the result and
// persist the new state.
type ProcessDelivery struct {
	deliveries    ports.DeliveryRepository
	events        ports.EventRepository
	subscriptions ports.SubscriptionRepository
	sender        ports.WebhookSender
	limiter       ports.RateLimiter
	clock         ports.Clock
	sign          *instruction.SignPayload
	scheduleRetry *instruction.ScheduleRetry
}

// NewProcessDelivery builds the use case.
func NewProcessDelivery(
	deliveries ports.DeliveryRepository,
	events ports.EventRepository,
	subscriptions ports.SubscriptionRepository,
	sender ports.WebhookSender,
	limiter ports.RateLimiter,
	clock ports.Clock,
	sign *instruction.SignPayload,
	scheduleRetry *instruction.ScheduleRetry,
) *ProcessDelivery {
	return &ProcessDelivery{
		deliveries:    deliveries,
		events:        events,
		subscriptions: subscriptions,
		sender:        sender,
		limiter:       limiter,
		clock:         clock,
		sign:          sign,
		scheduleRetry: scheduleRetry,
	}
}

// Invoke runs one delivery attempt. The delivery must already be claimed, that
// is in SENDING. Errors returned here leave the delivery claimed; the stale
// reaper puts it back into the queue.
func (u *ProcessDelivery) Invoke(ctx context.Context, delivery *entity.Delivery) error {
	if err := ctx.Err(); err != nil {
		return errs.Wrapf(err, "process delivery")
	}
	if delivery.Status != entity.StatusSending {
		return errs.Conflictf("delivery %s must be SENDING to be processed, got %s", delivery.ID, delivery.Status)
	}

	sub, err := u.subscriptions.GetByID(ctx, delivery.SubscriptionID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			// The subscription is gone; no retry can ever succeed.
			return u.finish(ctx, delivery, func() error {
				return delivery.MarkDeadLetter(nil, "subscription no longer exists", u.clock.Now(ctx))
			})
		}
		return errs.Wrapf(err, "load subscription %s", delivery.SubscriptionID)
	}

	event, err := u.events.GetByID(ctx, delivery.EventID)
	if err != nil {
		if errors.Is(err, errs.ErrNotFound) {
			return u.finish(ctx, delivery, func() error {
				return delivery.MarkDeadLetter(nil, "event no longer exists", u.clock.Now(ctx))
			})
		}
		return errs.Wrapf(err, "load event %s", delivery.EventID)
	}

	body, err := json.Marshal(webhookBody{
		EventID:    event.ID,
		DeliveryID: delivery.ID,
		Type:       event.Type,
		Attempt:    delivery.AttemptCount,
		CreatedAt:  event.CreatedAt,
		Payload:    event.Payload,
	})
	if err != nil {
		return errs.Wrapf(err, "marshal webhook body for delivery %s", delivery.ID)
	}

	// Throttle per target host before doing anything expensive.
	if err := u.limiter.Wait(ctx, sub.Host(), sub.MaxRPS); err != nil {
		return errs.Wrapf(err, "rate limit host %s", sub.Host())
	}

	signature, err := u.sign.Invoke(ctx, body, sub.Secret)
	if err != nil {
		return errs.Wrapf(err, "sign delivery %s", delivery.ID)
	}

	result, err := u.sender.Send(ctx, ports.SendRequest{
		URL:       sub.URL,
		Body:      body,
		Signature: signature,
		EventID:   event.ID,
		EventType: event.Type,
	})
	if err != nil {
		return errs.Wrapf(err, "send delivery %s", delivery.ID)
	}

	now := u.clock.Now(ctx)
	return u.finish(ctx, delivery, func() error {
		switch result.Outcome() {
		case entity.OutcomeSuccess:
			return delivery.MarkDelivered(result.StatusCode, now)
		case entity.OutcomeRetryable:
			return u.scheduleRetry.Invoke(ctx, delivery, result)
		default:
			code := result.StatusCode
			return delivery.MarkDeadLetter(&code, "non-retryable response: "+result.Reason(), now)
		}
	})
}

// finish applies a state transition and persists the delivery.
func (u *ProcessDelivery) finish(ctx context.Context, delivery *entity.Delivery, transition func() error) error {
	if err := transition(); err != nil {
		return errs.Wrapf(err, "transition delivery %s", delivery.ID)
	}
	if err := u.deliveries.Update(ctx, delivery); err != nil {
		return errs.Wrapf(err, "persist delivery %s", delivery.ID)
	}
	return nil
}
