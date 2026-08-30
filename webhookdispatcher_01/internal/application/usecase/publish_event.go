package usecase

import (
	"context"
	"errors"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
)

// PublishEvent accepts an event and fans it out into delivery tasks. The event
// and its deliveries are written in one transaction, and the idempotency key
// guarantees a repeated request creates neither a second event nor extra
// deliveries.
type PublishEvent struct {
	events        ports.EventRepository
	subscriptions ports.SubscriptionRepository
	clock         ports.Clock
}

// NewPublishEvent builds the use case.
func NewPublishEvent(events ports.EventRepository, subscriptions ports.SubscriptionRepository, clock ports.Clock) *PublishEvent {
	return &PublishEvent{events: events, subscriptions: subscriptions, clock: clock}
}

// Invoke stores the event with its deliveries, or returns the identifier of the
// event previously stored under the same idempotency key.
func (u *PublishEvent) Invoke(ctx context.Context, in ports.PublishEventInput) (ports.PublishEventOutput, error) {
	if err := ctx.Err(); err != nil {
		return ports.PublishEventOutput{}, errs.Wrapf(err, "publish event")
	}

	now := u.clock.Now(ctx)
	event, err := entity.NewEvent(in.IdempotencyKey, in.Type, in.Payload, now)
	if err != nil {
		return ports.PublishEventOutput{}, errs.Wrapf(err, "publish event")
	}

	// Fast path: the key was already used by an earlier request.
	if existing, err := u.events.FindByIdempotencyKey(ctx, in.IdempotencyKey); err == nil {
		return ports.PublishEventOutput{EventID: existing.ID, Deduplicated: true}, nil
	} else if !errors.Is(err, errs.ErrNotFound) {
		return ports.PublishEventOutput{}, errs.Wrapf(err, "lookup idempotency key")
	}

	subs, err := u.subscriptions.FindByEventType(ctx, event.Type)
	if err != nil {
		return ports.PublishEventOutput{}, errs.Wrapf(err, "find subscriptions for %s", event.Type)
	}
	deliveries := make([]*entity.Delivery, 0, len(subs))
	for _, sub := range subs {
		deliveries = append(deliveries, entity.NewDelivery(event.ID, sub.ID, now))
	}

	err = u.events.SaveEventWithDeliveries(ctx, event, deliveries)
	switch {
	case err == nil:
		return ports.PublishEventOutput{EventID: event.ID, DeliveryCount: len(deliveries)}, nil
	case errors.Is(err, errs.ErrAlreadyExists):
		// A concurrent request won the race on the idempotency key; the store
		// rejected this write whole, so no duplicate deliveries exist.
		existing, lookupErr := u.events.FindByIdempotencyKey(ctx, in.IdempotencyKey)
		if lookupErr != nil {
			return ports.PublishEventOutput{}, errs.Wrapf(lookupErr, "reload event for idempotency key")
		}
		return ports.PublishEventOutput{EventID: existing.ID, Deduplicated: true}, nil
	default:
		return ports.PublishEventOutput{}, errs.Wrapf(err, "save event with deliveries")
	}
}
