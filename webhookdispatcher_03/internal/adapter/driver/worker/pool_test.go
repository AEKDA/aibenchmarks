package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports/mocks"
	"webhookdispatcher/internal/application/usecase"
)

// TestRunStopsOnCancel проверяет, что пул доставляет задачи, а после отмены
// контекста Run возвращается, не оставляя висящих горутин (проверяется -race).
func TestRunStopsOnCancel(t *testing.T) {
	subID := uuid.New()
	job := entity.Delivery{
		ID: uuid.New(), EventID: uuid.New(), SubscriptionID: subID,
		Payload: []byte("{}"), Status: entity.StatusPending,
	}

	// claim — реальный ClaimNext поверх minimock-репо: всегда отдаёт одну задачу,
	// уважая отмену ctx (как настоящий репозиторий со SKIP LOCKED).
	deliveryRepo := mocks.NewDeliveryRepoMock(t)
	deliveryRepo.ClaimNextMock.Set(func(ctx context.Context, limit int, now time.Time) ([]entity.Delivery, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []entity.Delivery{job}, nil
	})

	var sent atomic.Int32

	// process — реальный ProcessDelivery с minimock-портами.
	deliveryRepo.MarkOutcomeMock.Set(func(_ context.Context, _ entity.Delivery) error { return nil })
	subRepo := mocks.NewSubscriptionRepoMock(t)
	subRepo.GetByIDMock.Set(func(_ context.Context, id uuid.UUID) (entity.Subscription, error) {
		return entity.Subscription{ID: id, URL: "https://s/h", Secret: "shh"}, nil
	})
	rl := mocks.NewRateLimiterMock(t)
	rl.AllowMock.Set(func(_ context.Context, host string) error { return nil })
	sender := mocks.NewSenderMock(t)
	sender.SendMock.Set(func(_ context.Context, url, ua, sig string, payload []byte) (int, error) {
		sent.Add(1)
		return 200, nil
	})

	claim := usecase.NewClaimNext(deliveryRepo)
	process := usecase.NewProcessDelivery(deliveryRepo, subRepo, sender, rl)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, 3, claim, process, 5*time.Millisecond)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run вернул ошибку: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run не завершился после отмены контекста (утечка горутин)")
	}

	if sent.Load() == 0 {
		t.Fatal("пул не доставил ни одной задачи до отмены")
	}
}
