package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/errs"
	"webhookdispatcher/internal/application/ports/mocks"
)

func TestGetDeliveryInvoke(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()

	t.Run("позитивный", func(t *testing.T) {
		repo := mocks.NewDeliveryRepoMock(t)
		repo.GetByIDMock.Set(func(_ context.Context, got uuid.UUID) (entity.Delivery, error) {
			return entity.Delivery{ID: got, Status: entity.StatusPending}, nil
		})
		uc := NewGetDelivery(repo)
		d, err := uc.Invoke(ctx, id)
		if err != nil {
			t.Fatalf("ожидалась ошибка nil: %v", err)
		}
		if d.ID != id {
			t.Fatalf("ID=%v", d.ID)
		}
		if d.Status != entity.StatusPending {
			t.Fatalf("Status=%v", d.Status)
		}
	})

	t.Run("errs.ErrNotFound пробрасывается", func(t *testing.T) {
		repo := mocks.NewDeliveryRepoMock(t)
		repo.GetByIDMock.Set(func(_ context.Context, _ uuid.UUID) (entity.Delivery, error) {
			return entity.Delivery{}, errs.ErrNotFound
		})
		uc := NewGetDelivery(repo)
		_, err := uc.Invoke(ctx, id)
		if !errors.Is(err, errs.ErrNotFound) {
			t.Fatalf("ожидалась %v, получена: %v", errs.ErrNotFound, err)
		}
	})
}
