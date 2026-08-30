package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/google/uuid"
)

// CreateSubscriptionInput registers one webhook subscriber.
type CreateSubscriptionInput struct {
	URL    string
	Secret string
	Events []string
	MaxRPS int
}

// CreateSubscriptionOutput echoes the stored subscription without its secret.
type CreateSubscriptionOutput struct {
	ID     uuid.UUID
	URL    string
	Events []string
	MaxRPS int
	Active bool
}

// CreateSubscription registers a subscriber.
type CreateSubscription interface {
	Invoke(ctx context.Context, in CreateSubscriptionInput) (CreateSubscriptionOutput, error)
}

// PublishEventInput is one incoming event plus its idempotency key.
type PublishEventInput struct {
	IdempotencyKey string
	Type           string
	Payload        json.RawMessage
}

// PublishEventOutput reports the stored event. Deduplicated is true when the
// idempotency key had already been used and no new work was created.
type PublishEventOutput struct {
	EventID       uuid.UUID
	Deduplicated  bool
	DeliveryCount int
}

// PublishEvent accepts an event and fans it out into delivery tasks.
type PublishEvent interface {
	Invoke(ctx context.Context, in PublishEventInput) (PublishEventOutput, error)
}

// GetDelivery reads the status of a single delivery.
type GetDelivery interface {
	Invoke(ctx context.Context, id uuid.UUID) (*entity.Delivery, error)
}

// ProcessDelivery performs one attempt for a claimed delivery and persists the
// resulting state.
type ProcessDelivery interface {
	Invoke(ctx context.Context, delivery *entity.Delivery) error
}

// ClaimDeliveries hands the worker pool the deliveries that are ready to send.
type ClaimDeliveries interface {
	Invoke(ctx context.Context, limit int) ([]*entity.Delivery, error)
}

// ReleaseStaleDeliveries returns deliveries stuck in SENDING to the queue.
type ReleaseStaleDeliveries interface {
	Invoke(ctx context.Context, staleAfter time.Duration) (int, error)
}
