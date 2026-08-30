package instruction

import (
	"testing"

	"webhookdispatcher/internal/application/entity"
)

func TestScheduleRetryDelivered(t *testing.T) {
	d := &entity.Delivery{Status: entity.StatusSending, Attempt: 1}
	o, err := NewScheduleRetry().Invoke(d, 200)
	if err != nil {
		t.Fatal(err)
	}
	if o != entity.OutcomeDelivered || d.Status != entity.StatusDelivered {
		t.Fatalf("хотят delivered, got outcome=%v status=%v", o, d.Status)
	}
}

func TestScheduleRetryExhaustsAttempts(t *testing.T) {
	d := &entity.Delivery{Status: entity.StatusSending, Attempt: entity.MaxAttempts}
	o, err := NewScheduleRetry().Invoke(d, 500)
	if err != nil {
		t.Fatal(err)
	}
	if o != entity.OutcomeDead || d.Status != entity.StatusDeadLetter {
		t.Fatalf("исчерпаны попытки → dead letter, got %v %v", o, d.Status)
	}
}
