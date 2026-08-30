package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/usecase"
	"github.com/google/uuid"
)

func TestGetDeliveryReturnsDelivery(t *testing.T) {
	repo := newFakeDeliveries()
	d := entity.NewDelivery(uuid.New(), uuid.New(), testNow)
	repo.byID[d.ID] = d

	got, err := usecase.NewGetDelivery(repo).Invoke(context.Background(), d.ID)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got.ID != d.ID {
		t.Fatalf("id = %s, want %s", got.ID, d.ID)
	}
}

func TestGetDeliveryNotFound(t *testing.T) {
	_, err := usecase.NewGetDelivery(newFakeDeliveries()).Invoke(context.Background(), uuid.New())
	if !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
