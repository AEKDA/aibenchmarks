package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports"
	"webhookdispatcher/internal/application/ports/mocks"
)

func TestPublishEventCreatesDeliveries(t *testing.T) {
	ctx := context.Background()
	now := mustTime(t, "2026-01-01T00:00:00Z")
	const subsID = "11111111-1111-1111-1111-111111111111"

	eventRepo := mocks.NewEventRepoMock(t)
	eventRepo.SaveWithinMock.Set(func(_ context.Context, key string, ev entity.Event, del []entity.Delivery) (ports.OutboxResult, error) {
		if key != "k1" {
			t.Fatalf("key=%q want k1", key)
		}
		if ev.Type != "order.created" || string(ev.Payload) != "{}" {
			t.Fatalf("bad event: %+v", ev)
		}
		if len(del) != 1 || del[0].Status != entity.StatusPending || del[0].SubscriptionID != uuid.MustParse(subsID) {
			t.Fatalf("bad deliveries: %+v", del)
		}
		return ports.OutboxResult{EventID: ev.ID, DeliveryIDs: []uuid.UUID{del[0].ID}}, nil
	})

	subRepo := mocks.NewSubscriptionRepoMock(t)
	subRepo.GetByEventTypeMock.Set(func(_ context.Context, et string) ([]entity.Subscription, error) {
		return []entity.Subscription{{ID: uuid.MustParse(subsID), URL: "https://s/h", Secret: "k"}}, nil
	})

	uc := NewPublishEvent(eventRepo, subRepo)
	out, err := uc.Invoke(ctx, PublishEventIn{Type: "order.created", Payload: []byte("{}"), IdempotencyKey: "k1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if out.Duplicate {
		t.Fatal("новое событие не должно помечаться дубликатом")
	}
}

func TestPublishEventDuplicate(t *testing.T) {
	ctx := context.Background()
	now := mustTime(t, "2026-01-01T00:00:00Z")

	const existingID = "22222222-2222-2222-2222-222222222222"
	eventRepo := mocks.NewEventRepoMock(t)
	eventRepo.SaveWithinMock.Set(func(_ context.Context, _ string, _ entity.Event, _ []entity.Delivery) (ports.OutboxResult, error) {
		return ports.OutboxResult{EventID: uuid.MustParse(existingID), Duplicate: true}, nil
	})

	subRepo := mocks.NewSubscriptionRepoMock(t)
	subRepo.GetByEventTypeMock.Set(func(_ context.Context, _ string) ([]entity.Subscription, error) {
		return nil, nil
	})

	uc := NewPublishEvent(eventRepo, subRepo)
	out, err := uc.Invoke(ctx, PublishEventIn{Type: "order.created", Payload: []byte("{}"), IdempotencyKey: "k1", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Duplicate {
		t.Fatal("повторный вызов должен помечаться дубликатом")
	}
	if out.EventID != uuid.MustParse(existingID) {
		t.Fatalf("EventID=%v want %v", out.EventID, existingID)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}