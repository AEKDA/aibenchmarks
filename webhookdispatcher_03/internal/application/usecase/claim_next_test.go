package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports/mocks"
)

func TestClaimNextInvoke(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("успешный забор задач", func(t *testing.T) {
		repo := mocks.NewDeliveryRepoMock(t)
		repo.ClaimNextMock.Set(func(_ context.Context, limit int, n time.Time) ([]entity.Delivery, error) {
			if limit != 10 {
				t.Fatalf("limit=%d want 10", limit)
			}
			return []entity.Delivery{{ID: uuid.New(), Status: entity.StatusSending}}, nil
		})
		uc := NewClaimNext(repo)
		got, err := uc.Invoke(ctx, now, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("ожидалась 1 задача, got %d", len(got))
		}
	})

	t.Run("ошибка хранилища пробрасывается", func(t *testing.T) {
		wantErr := errors.New("storage unavailable")
		repo := mocks.NewDeliveryRepoMock(t)
		repo.ClaimNextMock.Set(func(_ context.Context, _ int, _ time.Time) ([]entity.Delivery, error) {
			return nil, wantErr
		})
		uc := NewClaimNext(repo)
		_, gotErr := uc.Invoke(ctx, now, 10)
		if gotErr == nil {
			t.Fatal("ожидалась ошибка, got nil")
		}
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("gotErr=%v want %v", gotErr, wantErr)
		}
	})
}
