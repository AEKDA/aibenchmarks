package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/example/webhookdispatcher/internal/application/usecase"
)

func mustSubscription(t *testing.T, url string, events []string) *entity.Subscription {
	t.Helper()
	sub, err := entity.NewSubscription(url, "secret", events, 10, testNow)
	if err != nil {
		t.Fatalf("NewSubscription: %v", err)
	}
	return sub
}

func publishInput(key string) ports.PublishEventInput {
	return ports.PublishEventInput{
		IdempotencyKey: key,
		Type:           "order.created",
		Payload:        json.RawMessage(`{"order_id":42}`),
	}
}

func TestPublishEventCreatesOneDeliveryPerMatchingSubscription(t *testing.T) {
	subs := newFakeSubscriptions()
	subs.add(mustSubscription(t, "https://a.example.com/hook", []string{"order.created"}))
	subs.add(mustSubscription(t, "https://b.example.com/hook", []string{"order.created"}))
	subs.add(mustSubscription(t, "https://c.example.com/hook", []string{"order.paid"}))
	events := newFakeEvents()
	uc := usecase.NewPublishEvent(events, subs, fixedClock{testNow})

	out, err := uc.Invoke(context.Background(), publishInput("key-1"))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.Deduplicated {
		t.Fatal("first publish must not be marked as deduplicated")
	}
	if out.DeliveryCount != 2 || events.deliveryCount() != 2 {
		t.Fatalf("deliveries = %d/%d, want 2", out.DeliveryCount, events.deliveryCount())
	}
	for _, d := range events.deliveries {
		if d.Status != entity.StatusPending || d.AttemptCount != 0 {
			t.Fatalf("delivery must start PENDING with 0 attempts: %+v", d)
		}
		if d.EventID != out.EventID {
			t.Fatal("delivery must reference the stored event")
		}
	}
}

func TestPublishEventWithoutMatchingSubscriptionsSucceeds(t *testing.T) {
	subs := newFakeSubscriptions()
	subs.add(mustSubscription(t, "https://a.example.com/hook", []string{"order.paid"}))
	events := newFakeEvents()

	out, err := usecase.NewPublishEvent(events, subs, fixedClock{testNow}).
		Invoke(context.Background(), publishInput("key-1"))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.DeliveryCount != 0 || events.deliveryCount() != 0 {
		t.Fatalf("deliveries = %d, want 0", events.deliveryCount())
	}
	if _, err := events.FindByIdempotencyKey(context.Background(), "key-1"); err != nil {
		t.Fatalf("event must still be stored: %v", err)
	}
}

func TestPublishEventIsIdempotent(t *testing.T) {
	subs := newFakeSubscriptions()
	subs.add(mustSubscription(t, "https://a.example.com/hook", []string{"order.created"}))
	events := newFakeEvents()
	uc := usecase.NewPublishEvent(events, subs, fixedClock{testNow})

	first, err := uc.Invoke(context.Background(), publishInput("key-1"))
	if err != nil {
		t.Fatalf("first Invoke: %v", err)
	}
	second, err := uc.Invoke(context.Background(), publishInput("key-1"))
	if err != nil {
		t.Fatalf("second Invoke: %v", err)
	}
	if second.EventID != first.EventID {
		t.Fatalf("event id changed: %s vs %s", second.EventID, first.EventID)
	}
	if !second.Deduplicated {
		t.Fatal("repeated key must be reported as deduplicated")
	}
	if events.deliveryCount() != 1 {
		t.Fatalf("deliveries = %d, want 1", events.deliveryCount())
	}
}

func TestPublishEventLosingTheKeyRaceReturnsExistingEvent(t *testing.T) {
	subs := newFakeSubscriptions()
	subs.add(mustSubscription(t, "https://a.example.com/hook", []string{"order.created"}))
	events := newFakeEvents()
	winner, err := entity.NewEvent("key-1", "order.created", json.RawMessage(`{}`), testNow)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	// The concurrent writer only becomes visible when our own write is rejected.
	events.forceExists = true
	events.preexisting = winner

	out, err := usecase.NewPublishEvent(events, subs, fixedClock{testNow}).
		Invoke(context.Background(), publishInput("key-1"))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if out.EventID != winner.ID || !out.Deduplicated {
		t.Fatalf("out = %+v, want winner %s deduplicated", out, winner.ID)
	}
	if events.deliveryCount() != 0 {
		t.Fatalf("rejected write must not leave deliveries, got %d", events.deliveryCount())
	}
}

func TestPublishEventDifferentKeysCreateDifferentEvents(t *testing.T) {
	subs := newFakeSubscriptions()
	subs.add(mustSubscription(t, "https://a.example.com/hook", []string{"order.created"}))
	events := newFakeEvents()
	uc := usecase.NewPublishEvent(events, subs, fixedClock{testNow})

	first, err := uc.Invoke(context.Background(), publishInput("key-1"))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	second, err := uc.Invoke(context.Background(), publishInput("key-2"))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if first.EventID == second.EventID {
		t.Fatal("different keys must create different events")
	}
	if events.deliveryCount() != 2 {
		t.Fatalf("deliveries = %d, want 2", events.deliveryCount())
	}
}

func TestPublishEventRequiresIdempotencyKeyAndType(t *testing.T) {
	uc := usecase.NewPublishEvent(newFakeEvents(), newFakeSubscriptions(), fixedClock{testNow})
	cases := []ports.PublishEventInput{
		{IdempotencyKey: "", Type: "order.created"},
		{IdempotencyKey: "key", Type: ""},
	}
	for _, in := range cases {
		if _, err := uc.Invoke(context.Background(), in); !errors.Is(err, errs.ErrInvalidInput) {
			t.Fatalf("err = %v, want ErrInvalidInput", err)
		}
	}
}

func TestPublishEventPropagatesStorageFailure(t *testing.T) {
	subs := newFakeSubscriptions()
	subs.add(mustSubscription(t, "https://a.example.com/hook", []string{"order.created"}))
	events := newFakeEvents()
	events.saveErr = errors.New("tx aborted")

	_, err := usecase.NewPublishEvent(events, subs, fixedClock{testNow}).
		Invoke(context.Background(), publishInput("key-1"))
	if err == nil {
		t.Fatal("expected error")
	}
	if events.deliveryCount() != 0 {
		t.Fatal("failed transaction must not leave deliveries")
	}
}
