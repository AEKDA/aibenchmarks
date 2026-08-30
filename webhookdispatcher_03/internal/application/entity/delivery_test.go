package entity

import (
	"testing"
)

func TestDeliveryStart(t *testing.T) {
	d := Delivery{}
	d.Start()
	if d.Status != StatusSending || d.Attempt != 1 {
		t.Fatalf("после Start: status=%v attempt=%d (хотят SENDING, 1)", d.Status, d.Attempt)
	}
}

func TestDeliveryScheduleNeverExceedsAttempts(t *testing.T) {
	d := Delivery{Status: StatusSending, Attempt: 1}
	// не хватает попыток → dead letter
	d.ScheduleFrom(OutcomeDead)
	if d.Status != StatusDeadLetter {
		t.Fatalf("OutcomeDead → хотят DEAD_LETTER, got %v", d.Status)
	}
}
