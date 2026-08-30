package entity_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
)

func TestNewEventSuccess(t *testing.T) {
	ev, err := entity.NewEvent("key-1", "order.created", json.RawMessage(`{"order_id":42}`), now)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if ev.ID.String() == "" || ev.Type != "order.created" || ev.IdempotencyKey != "key-1" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestNewEventDefaultsEmptyPayload(t *testing.T) {
	ev, err := entity.NewEvent("key-1", "order.created", nil, now)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if string(ev.Payload) != "{}" {
		t.Fatalf("Payload = %s, want {}", ev.Payload)
	}
}

func TestNewEventRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		typ     string
		payload json.RawMessage
	}{
		{"empty key", "", "t", nil},
		{"blank key", "   ", "t", nil},
		{"empty type", "k", "", nil},
		{"blank type", "k", " ", nil},
		{"invalid payload", "k", "t", json.RawMessage(`{oops`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := entity.NewEvent(tc.key, tc.typ, tc.payload, now); !errors.Is(err, errs.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}
