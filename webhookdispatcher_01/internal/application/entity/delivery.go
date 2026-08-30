package entity

import (
	"time"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/google/uuid"
)

// DeliveryStatus is a node of the delivery state machine.
type DeliveryStatus string

// The delivery lifecycle: PENDING -> SENDING -> DELIVERED | RETRYING -> DEAD_LETTER.
const (
	StatusPending    DeliveryStatus = "PENDING"
	StatusSending    DeliveryStatus = "SENDING"
	StatusDelivered  DeliveryStatus = "DELIVERED"
	StatusRetrying   DeliveryStatus = "RETRYING"
	StatusDeadLetter DeliveryStatus = "DEAD_LETTER"
)

// AllStatuses lists every valid delivery status.
func AllStatuses() []DeliveryStatus {
	return []DeliveryStatus{StatusPending, StatusSending, StatusDelivered, StatusRetrying, StatusDeadLetter}
}

// allowedTransitions is the single source of truth for the state machine.
var allowedTransitions = map[DeliveryStatus]map[DeliveryStatus]bool{
	StatusPending:  {StatusSending: true},
	StatusRetrying: {StatusSending: true, StatusDeadLetter: true},
	StatusSending:  {StatusDelivered: true, StatusRetrying: true, StatusDeadLetter: true},
	// DELIVERED and DEAD_LETTER are terminal.
}

// CanTransition reports whether from -> to is a legal state change.
func CanTransition(from, to DeliveryStatus) bool {
	return allowedTransitions[from][to]
}

// Delivery is one attempt track of one event towards one subscription. It is
// also the outbox row the workers poll.
type Delivery struct {
	ID             uuid.UUID
	EventID        uuid.UUID
	SubscriptionID uuid.UUID
	Status         DeliveryStatus
	AttemptCount   int
	NextAttemptAt  *time.Time
	LastStatusCode *int
	LastError      string
	LockedAt       *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewDelivery creates a delivery task in PENDING with no attempts made yet.
func NewDelivery(eventID, subscriptionID uuid.UUID, now time.Time) *Delivery {
	now = now.UTC()
	return &Delivery{
		ID:             uuid.New(),
		EventID:        eventID,
		SubscriptionID: subscriptionID,
		Status:         StatusPending,
		AttemptCount:   0,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// MarkSending takes the delivery into flight and counts the attempt.
func (d *Delivery) MarkSending(now time.Time) error {
	if err := d.transition(StatusSending); err != nil {
		return err
	}
	now = now.UTC()
	d.AttemptCount++
	d.NextAttemptAt = nil
	d.LockedAt = &now
	d.UpdatedAt = now
	return nil
}

// MarkDelivered records a successful attempt. The delivery is terminal after it.
func (d *Delivery) MarkDelivered(statusCode int, now time.Time) error {
	if err := d.transition(StatusDelivered); err != nil {
		return err
	}
	now = now.UTC()
	code := statusCode
	d.LastStatusCode = &code
	d.LastError = ""
	d.NextAttemptAt = nil
	d.LockedAt = nil
	d.UpdatedAt = now
	return nil
}

// MarkRetrying schedules another attempt at nextAttemptAt.
func (d *Delivery) MarkRetrying(nextAttemptAt time.Time, statusCode *int, cause string, now time.Time) error {
	if err := d.transition(StatusRetrying); err != nil {
		return err
	}
	now = now.UTC()
	next := nextAttemptAt.UTC()
	d.NextAttemptAt = &next
	d.LastStatusCode = copyCode(statusCode)
	d.LastError = cause
	d.LockedAt = nil
	d.UpdatedAt = now
	return nil
}

// MarkDeadLetter gives up on the delivery. The delivery is terminal after it.
func (d *Delivery) MarkDeadLetter(statusCode *int, reason string, now time.Time) error {
	if err := d.transition(StatusDeadLetter); err != nil {
		return err
	}
	now = now.UTC()
	d.NextAttemptAt = nil
	d.LastStatusCode = copyCode(statusCode)
	d.LastError = reason
	d.LockedAt = nil
	d.UpdatedAt = now
	return nil
}

func (d *Delivery) transition(to DeliveryStatus) error {
	if !CanTransition(d.Status, to) {
		return errs.Conflictf("delivery %s: transition %s -> %s is not allowed", d.ID, d.Status, to)
	}
	d.Status = to
	return nil
}

func copyCode(code *int) *int {
	if code == nil {
		return nil
	}
	c := *code
	return &c
}
