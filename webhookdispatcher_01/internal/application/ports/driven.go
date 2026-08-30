// Package ports declares the interfaces the application layer owns.
//
// Driven ports (this file) are implemented by outbound adapters and called from
// use cases. Driver ports (driver.go) are implemented by use cases and called
// from inbound adapters. Every method takes a context as its first argument.
package ports

import (
	"context"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/google/uuid"
)

// SubscriptionRepository stores and queries webhook subscribers.
type SubscriptionRepository interface {
	// Save persists a new subscription.
	Save(ctx context.Context, sub *entity.Subscription) error
	// GetByID returns one subscription, or errs.ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*entity.Subscription, error)
	// FindByEventType returns every active subscription interested in eventType.
	FindByEventType(ctx context.Context, eventType string) ([]*entity.Subscription, error)
}

// EventRepository stores published events together with their outbox rows.
type EventRepository interface {
	// SaveEventWithDeliveries writes the event and its deliveries atomically.
	// It returns errs.ErrAlreadyExists when the idempotency key is taken.
	SaveEventWithDeliveries(ctx context.Context, event *entity.Event, deliveries []*entity.Delivery) error
	// FindByIdempotencyKey returns the event stored under key, or errs.ErrNotFound.
	FindByIdempotencyKey(ctx context.Context, key string) (*entity.Event, error)
	// GetByID returns one event, or errs.ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*entity.Event, error)
}

// DeliveryRepository stores delivery tasks and hands ready ones to workers.
type DeliveryRepository interface {
	// GetByID returns one delivery, or errs.ErrNotFound.
	GetByID(ctx context.Context, id uuid.UUID) (*entity.Delivery, error)
	// ClaimReady atomically takes up to limit deliveries that are PENDING or
	// RETRYING with next_attempt_at <= now into SENDING, so that no other
	// worker can claim the same rows.
	ClaimReady(ctx context.Context, limit int, now time.Time) ([]*entity.Delivery, error)
	// Update persists the current state of a delivery.
	Update(ctx context.Context, delivery *entity.Delivery) error
	// ReleaseStale moves deliveries stuck in SENDING since before lockedBefore
	// back to RETRYING and reports how many were released.
	ReleaseStale(ctx context.Context, lockedBefore time.Time) (int, error)
}

// SendRequest is one outbound webhook call, already signed by the domain.
type SendRequest struct {
	URL       string
	Body      []byte
	Signature string
	EventID   uuid.UUID
	EventType string
}

// WebhookSender performs one delivery attempt. It reports transport problems
// through the returned AttemptResult; the error return is reserved for
// programming or setup failures that are not attempt outcomes.
type WebhookSender interface {
	Send(ctx context.Context, req SendRequest) (entity.AttemptResult, error)
}

// RateLimiter throttles outbound calls per target host. Wait blocks until the
// call is allowed, or returns the context error when the context ends first.
type RateLimiter interface {
	Wait(ctx context.Context, host string, rps int) error
}

// Clock is the domain's view of time, so use cases stay testable.
type Clock interface {
	Now(ctx context.Context) time.Time
}
