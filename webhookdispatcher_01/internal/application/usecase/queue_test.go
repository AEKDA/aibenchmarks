package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/usecase"
	"github.com/google/uuid"
)

func TestClaimDeliveriesPassesLimitAndReturnsClaimed(t *testing.T) {
	repo := newFakeDeliveries()
	repo.claimed = []*entity.Delivery{entity.NewDelivery(uuid.New(), uuid.New(), testNow)}

	got, err := usecase.NewClaimDeliveries(repo, fixedClock{testNow}).Invoke(context.Background(), 25)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("claimed %d, want 1", len(got))
	}
	if repo.claimLimit != 25 {
		t.Fatalf("limit = %d, want 25", repo.claimLimit)
	}
}

func TestClaimDeliveriesRejectsNonPositiveLimit(t *testing.T) {
	repo := newFakeDeliveries()
	if _, err := usecase.NewClaimDeliveries(repo, fixedClock{testNow}).Invoke(context.Background(), 0); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestReleaseStaleDeliveriesUsesThreshold(t *testing.T) {
	repo := newFakeDeliveries()
	repo.released = 3

	got, err := usecase.NewReleaseStaleDeliveries(repo, fixedClock{testNow}).Invoke(context.Background(), time.Minute)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got != 3 {
		t.Fatalf("released = %d, want 3", got)
	}
	if len(repo.releaseArgs) != 1 || !repo.releaseArgs[0].Equal(testNow.Add(-time.Minute)) {
		t.Fatalf("lockedBefore = %v, want %v", repo.releaseArgs, testNow.Add(-time.Minute))
	}
}

func TestReleaseStaleDeliveriesRejectsNonPositiveThreshold(t *testing.T) {
	repo := newFakeDeliveries()
	if _, err := usecase.NewReleaseStaleDeliveries(repo, fixedClock{testNow}).Invoke(context.Background(), 0); !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}
