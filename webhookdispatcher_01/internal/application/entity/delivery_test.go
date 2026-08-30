package entity_test

import (
	"errors"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/google/uuid"
)

func TestNewDeliveryStartsPending(t *testing.T) {
	d := entity.NewDelivery(uuid.New(), uuid.New(), now)
	if d.Status != entity.StatusPending {
		t.Fatalf("Status = %s, want PENDING", d.Status)
	}
	if d.AttemptCount != 0 || d.NextAttemptAt != nil || d.LastStatusCode != nil {
		t.Fatalf("unexpected fresh delivery: %+v", d)
	}
}

// applyTransition drives the delivery towards `to` via the public marker
// methods so the table test below can cover every status pair.
func applyTransition(d *entity.Delivery, to entity.DeliveryStatus) error {
	code := 200
	switch to {
	case entity.StatusSending:
		return d.MarkSending(now)
	case entity.StatusDelivered:
		return d.MarkDelivered(code, now)
	case entity.StatusRetrying:
		return d.MarkRetrying(now.Add(time.Second), &code, "retry", now)
	case entity.StatusDeadLetter:
		return d.MarkDeadLetter(&code, "gave up", now)
	default:
		return errors.New("PENDING is not reachable by a marker method")
	}
}

func TestTransitionTable(t *testing.T) {
	allowed := map[entity.DeliveryStatus]map[entity.DeliveryStatus]bool{
		entity.StatusPending:  {entity.StatusSending: true},
		entity.StatusRetrying: {entity.StatusSending: true, entity.StatusDeadLetter: true},
		entity.StatusSending:  {entity.StatusDelivered: true, entity.StatusRetrying: true, entity.StatusDeadLetter: true},
	}
	for _, from := range entity.AllStatuses() {
		for _, to := range entity.AllStatuses() {
			if to == entity.StatusPending {
				continue // no marker method targets PENDING
			}
			t.Run(string(from)+"->"+string(to), func(t *testing.T) {
				d := entity.NewDelivery(uuid.New(), uuid.New(), now)
				d.Status = from
				err := applyTransition(d, to)
				if allowed[from][to] {
					if err != nil {
						t.Fatalf("allowed transition failed: %v", err)
					}
					if d.Status != to {
						t.Fatalf("Status = %s, want %s", d.Status, to)
					}
					return
				}
				if !errors.Is(err, errs.ErrConflict) {
					t.Fatalf("err = %v, want ErrConflict", err)
				}
				if d.Status != from {
					t.Fatalf("status changed to %s on a rejected transition", d.Status)
				}
			})
		}
	}
}

func TestCanTransitionMatchesLifecycle(t *testing.T) {
	if !entity.CanTransition(entity.StatusPending, entity.StatusSending) {
		t.Fatal("PENDING -> SENDING must be allowed")
	}
	if entity.CanTransition(entity.StatusPending, entity.StatusDelivered) {
		t.Fatal("PENDING -> DELIVERED must be rejected")
	}
	for _, terminal := range []entity.DeliveryStatus{entity.StatusDelivered, entity.StatusDeadLetter} {
		for _, to := range entity.AllStatuses() {
			if entity.CanTransition(terminal, to) {
				t.Fatalf("%s is terminal but allows -> %s", terminal, to)
			}
		}
	}
}

func TestMarkSendingCountsAttempt(t *testing.T) {
	d := entity.NewDelivery(uuid.New(), uuid.New(), now)
	if err := d.MarkSending(now); err != nil {
		t.Fatalf("MarkSending: %v", err)
	}
	if d.AttemptCount != 1 {
		t.Fatalf("AttemptCount = %d, want 1", d.AttemptCount)
	}
	if d.LockedAt == nil {
		t.Fatal("LockedAt must be set while SENDING")
	}
}

func TestMarkDeliveredRecordsCodeAndClearsSchedule(t *testing.T) {
	d := entity.NewDelivery(uuid.New(), uuid.New(), now)
	mustSend(t, d)
	if err := d.MarkDelivered(204, now); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if d.LastStatusCode == nil || *d.LastStatusCode != 204 {
		t.Fatalf("LastStatusCode = %v, want 204", d.LastStatusCode)
	}
	if d.NextAttemptAt != nil || d.LockedAt != nil {
		t.Fatalf("delivered delivery must not stay scheduled or locked: %+v", d)
	}
}

func TestMarkRetryingSchedulesNextAttempt(t *testing.T) {
	d := entity.NewDelivery(uuid.New(), uuid.New(), now)
	mustSend(t, d)
	code := 503
	next := now.Add(2 * time.Second)
	if err := d.MarkRetrying(next, &code, "unexpected response status", now); err != nil {
		t.Fatalf("MarkRetrying: %v", err)
	}
	if d.NextAttemptAt == nil || !d.NextAttemptAt.Equal(next) {
		t.Fatalf("NextAttemptAt = %v, want %v", d.NextAttemptAt, next)
	}
	if d.LockedAt != nil {
		t.Fatal("retrying delivery must be unlocked")
	}
	if d.LastError == "" {
		t.Fatal("LastError must record the cause")
	}
}

func TestMarkDeadLetterClearsSchedule(t *testing.T) {
	d := entity.NewDelivery(uuid.New(), uuid.New(), now)
	mustSend(t, d)
	if err := d.MarkDeadLetter(nil, "non retryable status", now); err != nil {
		t.Fatalf("MarkDeadLetter: %v", err)
	}
	if d.NextAttemptAt != nil {
		t.Fatal("dead-lettered delivery must not be scheduled")
	}
}

func mustSend(t *testing.T, d *entity.Delivery) {
	t.Helper()
	if err := d.MarkSending(now); err != nil {
		t.Fatalf("MarkSending: %v", err)
	}
}
