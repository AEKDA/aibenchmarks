package entity

import (
	"github.com/google/uuid"
	"time"
)

// DeliveryStatus статус доставки.
type DeliveryStatus string

// Статусы жизненного цикла доставки.
const (
	StatusPending    DeliveryStatus = "PENDING"
	StatusSending    DeliveryStatus = "SENDING"
	StatusDelivered  DeliveryStatus = "DELIVERED"
	StatusRetrying   DeliveryStatus = "RETRYING"
	StatusDeadLetter DeliveryStatus = "DEAD_LETTER"
)

// Delivery одна доставка события подписчику (event × subscription).
type Delivery struct {
	ID             uuid.UUID
	EventID        uuid.UUID
	SubscriptionID uuid.UUID
	Status         DeliveryStatus
	Attempt        int
	NextAttemptAt  time.Time
	Payload        []byte
	LastHTTPStatus int
}

// Start переводит задачу в SENDING и увеличивает счётчик попыток.
func (d *Delivery) Start() {
	d.Status = StatusSending
	d.Attempt++
}

// ScheduleFrom применяет исход запроса: доставлен, retry или dead letter.
func (d *Delivery) ScheduleFrom(o Outcome) {
	switch o {
	case OutcomeDelivered:
		d.Status = StatusDelivered
	case OutcomeDead:
		d.Status = StatusDeadLetter
	default:
		d.Status = StatusRetrying
		d.NextAttemptAt = time.Now().UTC().Add(BackoffDelay(d.Attempt))
	}
}
