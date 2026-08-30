package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/example/webhookdispatcher/internal/application/usecase"
)

func TestCreateSubscriptionStoresSubscription(t *testing.T) {
	repo := newFakeSubscriptions()
	uc := usecase.NewCreateSubscription(repo, fixedClock{testNow})

	out, err := uc.Invoke(context.Background(), ports.CreateSubscriptionInput{
		URL:    "https://example.com/hook",
		Secret: "s3cr3t",
		Events: []string{"order.created"},
		MaxRPS: 10,
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("saved %d subscriptions, want 1", len(repo.saved))
	}
	if out.ID != repo.saved[0].ID || out.MaxRPS != 10 || !out.Active {
		t.Fatalf("unexpected output: %+v", out)
	}
	if repo.saved[0].Secret != "s3cr3t" {
		t.Fatal("secret must be persisted")
	}
}

func TestCreateSubscriptionRejectsInvalidInput(t *testing.T) {
	repo := newFakeSubscriptions()
	uc := usecase.NewCreateSubscription(repo, fixedClock{testNow})

	_, err := uc.Invoke(context.Background(), ports.CreateSubscriptionInput{
		URL: "not-a-url", Secret: "s", Events: []string{"a"}, MaxRPS: 1,
	})
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if len(repo.saved) != 0 {
		t.Fatal("invalid input must not reach the repository")
	}
}

func TestCreateSubscriptionPropagatesRepositoryError(t *testing.T) {
	repo := newFakeSubscriptions()
	repo.saveErr = errors.New("db down")
	uc := usecase.NewCreateSubscription(repo, fixedClock{testNow})

	_, err := uc.Invoke(context.Background(), ports.CreateSubscriptionInput{
		URL: "https://example.com", Secret: "s", Events: []string{"a"}, MaxRPS: 1,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCreateSubscriptionRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo := newFakeSubscriptions()
	_, err := usecase.NewCreateSubscription(repo, fixedClock{testNow}).
		Invoke(ctx, ports.CreateSubscriptionInput{URL: "https://example.com", Secret: "s", Events: []string{"a"}, MaxRPS: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
