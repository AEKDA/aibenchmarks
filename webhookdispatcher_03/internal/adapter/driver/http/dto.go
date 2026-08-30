package http

import (
	"encoding/json"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
)

// SubscriptionRequest тело запроса регистрации подписчика.
type SubscriptionRequest struct {
	URL    string   `json:"url"`
	Secret string   `json:"secret"`
	Events []string `json:"events"`
	MaxRPS int      `json:"max_rps"`
}

// EventRequest тело запроса публикации события. Payload передаётся как есть.
type EventRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// SubscriptionResponse ответ создания подписки.
type SubscriptionResponse struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
}

// PublishResponse ответ публикации события: id и флаг дубликата.
type PublishResponse struct {
	ID        uuid.UUID `json:"id"`
	Duplicate bool      `json:"duplicate"`
}

// DeliveryResponse ответ получения статуса доставки.
type DeliveryResponse struct {
	ID             uuid.UUID             `json:"id"`
	EventID        uuid.UUID             `json:"event_id"`
	SubscriptionID uuid.UUID             `json:"subscription_id"`
	Status         entity.DeliveryStatus `json:"status"`
	Attempt        int                   `json:"attempt"`
	LastHTTPStatus int                   `json:"last_http_status"`
}

// deliveryToResponse преобразует сущность доставки в DTO ответа.
func deliveryToResponse(d entity.Delivery) DeliveryResponse {
	return DeliveryResponse{
		ID:             d.ID,
		EventID:        d.EventID,
		SubscriptionID: d.SubscriptionID,
		Status:         d.Status,
		Attempt:        d.Attempt,
		LastHTTPStatus: d.LastHTTPStatus,
	}
}
