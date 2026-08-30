package usecase

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/errs"
	"webhookdispatcher/internal/application/ports/mocks"
)

func TestCreateSubscriptionInvoke(t *testing.T) {
	ctx := context.Background()

	t.Run("успешная регистрация", func(t *testing.T) {
		repo := mocks.NewSubscriptionRepoMock(t)
		var saved entity.Subscription
		repo.SaveMock.Set(func(_ context.Context, s entity.Subscription) error {
			saved = s
			return nil
		})

		uc := NewCreateSubscription(repo)
		in := CreateSubscriptionIn{
			URL: "https://s.example/hook", Secret: "shh", Events: []string{"order.created"}, MaxRPS: 5,
		}
		got, err := uc.Invoke(ctx, in)
		if err != nil {
			t.Fatalf("ожидалась ошибка nil, получена: %v", err)
		}
		if got.ID == [16]byte{} {
			t.Fatal("ожидался сгенерированный ID")
		}
		if !reflect.DeepEqual(entity.Subscription{
			ID: got.ID, URL: in.URL, Secret: in.Secret,
			Events: in.Events, MaxRPS: in.MaxRPS,
		}, saved) {
			t.Fatalf("Save не получил полную подписку:\nожидалось: %+v\nполучено: %+v", got, saved)
		}
	})

	t.Run("ошибка repo.Save пробрасывается", func(t *testing.T) {
		repo := mocks.NewSubscriptionRepoMock(t)
		repo.SaveMock.Set(func(_ context.Context, _ entity.Subscription) error {
			return errors.New("save failed")
		})

		uc := NewCreateSubscription(repo)
		_, err := uc.Invoke(ctx, CreateSubscriptionIn{
			URL: "https://s.example/hook", Secret: "shh",
		})
		if err == nil {
			t.Fatal("ожидалась ошибка от repo.Save")
		}
	})
}

func TestCreateSubscriptionValidation(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		in      CreateSubscriptionIn
		wantErr error
	}{
		{
			name:    "пустой URL",
			in:      CreateSubscriptionIn{URL: "", Secret: "shh"},
			wantErr: errs.ErrInvalid,
		},
		{
			name:    "некорректный URL",
			in:      CreateSubscriptionIn{URL: "not-a-url", Secret: "shh"},
			wantErr: errs.ErrInvalid,
		},
		{
			name:    "пустой Secret",
			in:      CreateSubscriptionIn{URL: "https://s.example/hook", Secret: ""},
			wantErr: errs.ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Не настраиваем ожидание вызова — если валидация работает корректно,
			// repo.Save вызван не будет, и мок вернёт zero value (nil).
			// Факт возврата errs.ErrInvalid гарантирует, что мы вышли до вызова Save.
			repo := mocks.NewSubscriptionRepoMock(t)

			uc := NewCreateSubscription(repo)
			_, err := uc.Invoke(ctx, tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ожидалась ошибка %v, получена: %v", tc.wantErr, err)
			}
		})
	}
}
