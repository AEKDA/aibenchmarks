package usecase

import (
	"context"
	"testing"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports/mocks"
)

func TestCreateSubscriptionInvoke(t *testing.T) {
	ctx := context.Background()
	repo := mocks.NewSubscriptionRepoMock(t)
	var saved entity.Subscription
	repo.SaveMock.Set(func(_ context.Context, s entity.Subscription) error {
		saved = s
		return nil
	})

	uc := NewCreateSubscription(repo)
	got, err := uc.Invoke(ctx, CreateSubscriptionIn{
		URL: "https://s.example/hook", Secret: "shh", Events: []string{"order.created"}, MaxRPS: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == [16]byte{} {
		t.Fatal("ожидался сгенерированный ID")
	}
	if got.URL != saved.URL {
		t.Fatalf("Save не получил подписку: %+v", saved)
	}
}