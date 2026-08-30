package entity_test

import (
	"errors"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
)

var now = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func TestNewSubscriptionSuccess(t *testing.T) {
	sub, err := entity.NewSubscription("https://example.com/hook", "s3cr3t", []string{"order.created"}, 10, now)
	if err != nil {
		t.Fatalf("NewSubscription: %v", err)
	}
	if sub.ID.String() == "" || !sub.Active || sub.MaxRPS != 10 {
		t.Fatalf("unexpected subscription: %+v", sub)
	}
	if sub.Host() != "example.com" {
		t.Fatalf("Host() = %q, want example.com", sub.Host())
	}
}

func TestNewSubscriptionRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		secret string
		events []string
		rps    int
	}{
		{"empty url", "", "s", []string{"a"}, 1},
		{"not a url", "not-a-url", "s", []string{"a"}, 1},
		{"non http scheme", "ftp://example.com", "s", []string{"a"}, 1},
		{"no host", "https://", "s", []string{"a"}, 1},
		{"empty secret", "https://example.com", "   ", []string{"a"}, 1},
		{"empty events", "https://example.com", "s", nil, 1},
		{"blank event type", "https://example.com", "s", []string{" "}, 1},
		{"zero rps", "https://example.com", "s", []string{"a"}, 0},
		{"negative rps", "https://example.com", "s", []string{"a"}, -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := entity.NewSubscription(tc.url, tc.secret, tc.events, tc.rps, now)
			if sub != nil {
				t.Fatalf("expected no subscription, got %+v", sub)
			}
			if !errors.Is(err, errs.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestSubscribedTo(t *testing.T) {
	sub, err := entity.NewSubscription("https://example.com/hook", "s", []string{"order.created", "order.paid"}, 1, now)
	if err != nil {
		t.Fatalf("NewSubscription: %v", err)
	}
	if !sub.SubscribedTo("order.paid") {
		t.Fatal("SubscribedTo(order.paid) = false")
	}
	if sub.SubscribedTo("order.shipped") {
		t.Fatal("SubscribedTo(order.shipped) = true")
	}
}
