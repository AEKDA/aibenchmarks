package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports/mocks"
)

func TestProcessDeliveryDelivered(t *testing.T) {
	ctx := context.Background()
	d := entity.Delivery{ID: uuid.New(), EventID: uuid.New(), SubscriptionID: uuid.New(), Payload: []byte("{}"), Status: entity.StatusSending, Attempt: 1}

	subRepo := mocks.NewSubscriptionRepoMock(t)
	subRepo.GetByIDMock.Set(func(_ context.Context, id uuid.UUID) (entity.Subscription, error) {
		return entity.Subscription{ID: id, URL: "https://s/h", Secret: "shh", MaxRPS: 1}, nil
	})
	rl := mocks.NewRateLimiterMock(t)
	rl.AllowMock.Set(func(_ context.Context, host string) error { return nil })
	sender := mocks.NewSenderMock(t)
	sender.SendMock.Set(func(_ context.Context, url, ua, sig string, payload []byte) (int, error) {
		if url != "https://s/h" {
			t.Fatalf("url=%q", url)
		}
		if !strings.HasPrefix(sig, "sha256=") {
			t.Fatalf("sig=%q", sig)
		}
		return 200, nil
	})
	deliveryRepo := mocks.NewDeliveryRepoMock(t)
	deliveryRepo.MarkOutcomeMock.Set(func(_ context.Context, dd entity.Delivery) error {
		if dd.Status != entity.StatusDelivered {
			t.Fatalf("status=%v", dd.Status)
		}
		return nil
	})

	uc := NewProcessDelivery(deliveryRepo, subRepo, sender, rl, func() time.Time { return mustTime(t, "2026-01-01T00:00:00Z") })
	if err := uc.Invoke(ctx, d); err != nil {
		t.Fatal(err)
	}
}

func TestProcessDeliveryNetworkFailureRetries(t *testing.T) {
	ctx := context.Background()
	d := entity.Delivery{ID: uuid.New(), EventID: uuid.New(), SubscriptionID: uuid.New(), Payload: []byte("{}"), Status: entity.StatusSending, Attempt: 1}

	subRepo := mocks.NewSubscriptionRepoMock(t)
	subRepo.GetByIDMock.Set(func(_ context.Context, id uuid.UUID) (entity.Subscription, error) {
		return entity.Subscription{ID: id, URL: "https://s/h", Secret: "shh"}, nil
	})
	rl := mocks.NewRateLimiterMock(t)
	rl.AllowMock.Set(func(_ context.Context, host string) error { return nil })
	sender := mocks.NewSenderMock(t)
	sender.SendMock.Set(func(_ context.Context, url, ua, sig string, payload []byte) (int, error) {
		return 0, errors.New("connection reset")
	})
	deliveryRepo := mocks.NewDeliveryRepoMock(t)
	deliveryRepo.MarkOutcomeMock.Set(func(_ context.Context, dd entity.Delivery) error {
		if dd.Status != entity.StatusRetrying {
			t.Fatalf("status=%v want RETRYING", dd.Status)
		}
		return nil
	})

	uc := NewProcessDelivery(deliveryRepo, subRepo, sender, rl, func() time.Time { return mustTime(t, "2026-01-01T00:00:00Z") })
	if err := uc.Invoke(ctx, d); err != nil {
		t.Fatal(err)
	}
}

func TestProcessDeliveryExhaustedAttemptsDeadLetter(t *testing.T) {
	ctx := context.Background()
	d := entity.Delivery{ID: uuid.New(), EventID: uuid.New(), SubscriptionID: uuid.New(), Payload: []byte("{}"), Status: entity.StatusSending, Attempt: entity.MaxAttempts}

	subRepo := mocks.NewSubscriptionRepoMock(t)
	subRepo.GetByIDMock.Set(func(_ context.Context, id uuid.UUID) (entity.Subscription, error) {
		return entity.Subscription{ID: id, URL: "https://s/h", Secret: "shh"}, nil
	})
	rl := mocks.NewRateLimiterMock(t)
	rl.AllowMock.Set(func(_ context.Context, host string) error { return nil })
	sender := mocks.NewSenderMock(t)
	sender.SendMock.Set(func(_ context.Context, url, ua, sig string, payload []byte) (int, error) {
		return 500, nil
	})
	deliveryRepo := mocks.NewDeliveryRepoMock(t)
	deliveryRepo.MarkOutcomeMock.Set(func(_ context.Context, dd entity.Delivery) error {
		if dd.Status != entity.StatusDeadLetter {
			t.Fatalf("status=%v want DEAD_LETTER", dd.Status)
		}
		return nil
	})

	uc := NewProcessDelivery(deliveryRepo, subRepo, sender, rl, func() time.Time { return mustTime(t, "2026-01-01T00:00:00Z") })
	if err := uc.Invoke(ctx, d); err != nil {
		t.Fatal(err)
	}
}

func TestProcessDeliveryGetByIDError(t *testing.T) {
	ctx := context.Background()
	d := entity.Delivery{ID: uuid.New(), EventID: uuid.New(), SubscriptionID: uuid.New(), Payload: []byte("{}"), Status: entity.StatusSending, Attempt: 1}

	wantErr := errors.New("subscription not found")
	subRepo := mocks.NewSubscriptionRepoMock(t)
	subRepo.GetByIDMock.Set(func(_ context.Context, _ uuid.UUID) (entity.Subscription, error) {
		return entity.Subscription{}, wantErr
	})
	rl := mocks.NewRateLimiterMock(t)
	sender := mocks.NewSenderMock(t)
	deliveryRepo := mocks.NewDeliveryRepoMock(t)

	uc := NewProcessDelivery(deliveryRepo, subRepo, sender, rl, func() time.Time { return mustTime(t, "2026-01-01T00:00:00Z") })
	gotErr := uc.Invoke(ctx, d)
	if gotErr == nil {
		t.Fatal("ожидалась ошибка, got nil")
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("gotErr=%v want %v", gotErr, wantErr)
	}
}
