// Package http is the inbound REST adapter. It translates HTTP requests into
// use-case invocations and domain errors into status codes.
package http

import (
	"encoding/json"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
)

// createSubscriptionRequest is the body of POST /api/v1/subscriptions.
type createSubscriptionRequest struct {
	URL    string   `json:"url"`
	Secret string   `json:"secret"`
	Events []string `json:"events"`
	MaxRPS int      `json:"max_rps"`
}

// createSubscriptionResponse deliberately omits the secret: it must never
// travel back out of the service.
type createSubscriptionResponse struct {
	ID     uuid.UUID `json:"id"`
	URL    string    `json:"url"`
	Events []string  `json:"events"`
	MaxRPS int       `json:"max_rps"`
	Active bool      `json:"active"`
}

func newCreateSubscriptionResponse(out ports.CreateSubscriptionOutput) createSubscriptionResponse {
	return createSubscriptionResponse{
		ID:     out.ID,
		URL:    out.URL,
		Events: out.Events,
		MaxRPS: out.MaxRPS,
		Active: out.Active,
	}
}

// publishEventRequest is the body of POST /api/v1/events.
type publishEventRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// publishEventResponse reports the stored event.
type publishEventResponse struct {
	EventID      uuid.UUID `json:"event_id"`
	Deduplicated bool      `json:"deduplicated"`
	Deliveries   int       `json:"deliveries"`
}

// deliveryResponse is the body of GET /api/v1/deliveries/{id}.
type deliveryResponse struct {
	ID             uuid.UUID  `json:"id"`
	EventID        uuid.UUID  `json:"event_id"`
	SubscriptionID uuid.UUID  `json:"subscription_id"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	NextAttemptAt  *time.Time `json:"next_attempt_at,omitempty"`
	LastStatusCode *int       `json:"last_status_code,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func newDeliveryResponse(d *entity.Delivery) deliveryResponse {
	return deliveryResponse{
		ID:             d.ID,
		EventID:        d.EventID,
		SubscriptionID: d.SubscriptionID,
		Status:         string(d.Status),
		AttemptCount:   d.AttemptCount,
		NextAttemptAt:  d.NextAttemptAt,
		LastStatusCode: d.LastStatusCode,
		LastError:      d.LastError,
		CreatedAt:      d.CreatedAt,
		UpdatedAt:      d.UpdatedAt,
	}
}

// errorResponse is the single error shape of the API.
type errorResponse struct {
	Error string `json:"error"`
}
