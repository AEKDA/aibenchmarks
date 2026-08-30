package http

import (
	"context"
	"encoding/json"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/errs"
	"webhookdispatcher/internal/application/ports"
	"webhookdispatcher/internal/application/ports/mocks"
	"webhookdispatcher/internal/application/usecase"
)

// doRequest выполняет запрос через хендлер и возвращает записанный ответ.
func doRequest(t *testing.T, h nethttp.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCreateSubscription(t *testing.T) {
	t.Run("валидная регистрация → 201 с id", func(t *testing.T) {
		repo := mocks.NewSubscriptionRepoMock(t)
		repo.SaveMock.Set(func(_ context.Context, _ entity.Subscription) error { return nil })

		h := NewHandler(usecase.NewCreateSubscription(repo), nil, nil)
		rec := doRequest(t, h, nethttp.MethodPost, "/api/v1/subscriptions",
			`{"url":"https://s.example/hook","secret":"shh","events":["order.created"],"max_rps":5}`, nil)
		if rec.Code != nethttp.StatusCreated {
			t.Fatalf("status=%d want %d; body=%s", rec.Code, nethttp.StatusCreated, rec.Body.String())
		}
		var resp SubscriptionResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.ID == uuid.Nil {
			t.Fatal("ожидался id подписки")
		}
	})

	t.Run("некорректный URL → 400", func(t *testing.T) {
		repo := mocks.NewSubscriptionRepoMock(t)

		h := NewHandler(usecase.NewCreateSubscription(repo), nil, nil)
		rec := doRequest(t, h, nethttp.MethodPost, "/api/v1/subscriptions",
			`{"url":"not-a-url","secret":"shh"}`, nil)
		if rec.Code != nethttp.StatusBadRequest {
			t.Fatalf("status=%d want %d", rec.Code, nethttp.StatusBadRequest)
		}
	})
}

func TestPublishEvent(t *testing.T) {
	t.Run("валидное событие с ключом → 200 и id", func(t *testing.T) {
		eventRepo := mocks.NewEventRepoMock(t)
		eventRepo.SaveWithinMock.Set(func(_ context.Context, key string, ev entity.Event, _ []entity.Delivery) (ports.OutboxResult, error) {
			if key != "k1" {
				t.Fatalf("key=%q want k1", key)
			}
			return ports.OutboxResult{EventID: ev.ID, Duplicate: false}, nil
		})
		subRepo := mocks.NewSubscriptionRepoMock(t)
		subRepo.GetByEventTypeMock.Set(func(_ context.Context, et string) ([]entity.Subscription, error) {
			if et != "order.created" {
				t.Fatalf("type=%q", et)
			}
			return []entity.Subscription{{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), URL: "https://s/h", Secret: "k"}}, nil
		})

		h := NewHandler(nil, usecase.NewPublishEvent(eventRepo, subRepo), nil)
		rec := doRequest(t, h, nethttp.MethodPost, "/api/v1/events",
			`{"type":"order.created","payload":{"a":1}}`,
			map[string]string{"Idempotency-Key": "k1"})
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("status=%d want %d; body=%s", rec.Code, nethttp.StatusOK, rec.Body.String())
		}
		var resp PublishResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.ID == uuid.Nil {
			t.Fatal("ожидался id события")
		}
		if resp.Duplicate {
			t.Fatal("новое событие не должно быть дубликатом")
		}
	})

	t.Run("без Idempotency-Key → 400", func(t *testing.T) {
		eventRepo := mocks.NewEventRepoMock(t)
		subRepo := mocks.NewSubscriptionRepoMock(t)

		h := NewHandler(nil, usecase.NewPublishEvent(eventRepo, subRepo), nil)
		rec := doRequest(t, h, nethttp.MethodPost, "/api/v1/events",
			`{"type":"order.created"}`, nil)
		if rec.Code != nethttp.StatusBadRequest {
			t.Fatalf("status=%d want %d", rec.Code, nethttp.StatusBadRequest)
		}
	})

	t.Run("ErrConflict из хранилища → 409", func(t *testing.T) {
		eventRepo := mocks.NewEventRepoMock(t)
		eventRepo.SaveWithinMock.Set(func(context.Context, string, entity.Event, []entity.Delivery) (ports.OutboxResult, error) {
			return ports.OutboxResult{}, errs.ErrConflict
		})
		subRepo := mocks.NewSubscriptionRepoMock(t)
		subRepo.GetByEventTypeMock.Set(func(context.Context, string) ([]entity.Subscription, error) {
			return nil, nil
		})

		h := NewHandler(nil, usecase.NewPublishEvent(eventRepo, subRepo), nil)
		rec := doRequest(t, h, nethttp.MethodPost, "/api/v1/events",
			`{"type":"order.created","payload":{}}`,
			map[string]string{"Idempotency-Key": "k1"})
		if rec.Code != nethttp.StatusConflict {
			t.Fatalf("status=%d want %d; body=%s", rec.Code, nethttp.StatusConflict, rec.Body.String())
		}
	})
}

func TestGetDelivery(t *testing.T) {
	id := uuid.New()

	t.Run("найдена → 200 со статусом", func(t *testing.T) {
		repo := mocks.NewDeliveryRepoMock(t)
		repo.GetByIDMock.Set(func(_ context.Context, got uuid.UUID) (entity.Delivery, error) {
			return entity.Delivery{ID: got, Status: entity.StatusPending, Attempt: 1}, nil
		})

		h := NewHandler(nil, nil, usecase.NewGetDelivery(repo))
		rec := doRequest(t, h, nethttp.MethodGet, "/api/v1/deliveries/"+id.String(), "", nil)
		if rec.Code != nethttp.StatusOK {
			t.Fatalf("status=%d want %d; body=%s", rec.Code, nethttp.StatusOK, rec.Body.String())
		}
		var resp DeliveryResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp.ID != id || resp.Status != entity.StatusPending {
			t.Fatalf("resp=%+v", resp)
		}
	})

	t.Run("не найдена → 404", func(t *testing.T) {
		repo := mocks.NewDeliveryRepoMock(t)
		repo.GetByIDMock.Set(func(context.Context, uuid.UUID) (entity.Delivery, error) {
			return entity.Delivery{}, errs.ErrNotFound
		})

		h := NewHandler(nil, nil, usecase.NewGetDelivery(repo))
		rec := doRequest(t, h, nethttp.MethodGet, "/api/v1/deliveries/"+id.String(), "", nil)
		if rec.Code != nethttp.StatusNotFound {
			t.Fatalf("status=%d want %d", rec.Code, nethttp.StatusNotFound)
		}
	})
}
