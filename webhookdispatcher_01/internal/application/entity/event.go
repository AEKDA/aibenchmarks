package entity

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/google/uuid"
)

// Event is a published domain event awaiting fan-out to subscribers.
type Event struct {
	ID             uuid.UUID
	IdempotencyKey string
	Type           string
	Payload        json.RawMessage
	CreatedAt      time.Time
}

// NewEvent validates the caller-supplied fields and builds an event with a
// freshly generated identifier.
func NewEvent(idempotencyKey, eventType string, payload json.RawMessage, now time.Time) (*Event, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, errs.Invalidf("idempotency key is required")
	}
	if strings.TrimSpace(eventType) == "" {
		return nil, errs.Invalidf("event type is required")
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		return nil, errs.Invalidf("event payload must be valid json")
	}
	return &Event{
		ID:             uuid.New(),
		IdempotencyKey: idempotencyKey,
		Type:           eventType,
		Payload:        payload,
		CreatedAt:      now.UTC(),
	}, nil
}
