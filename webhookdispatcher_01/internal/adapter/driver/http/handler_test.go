package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adapter "github.com/example/webhookdispatcher/internal/adapter/driver/http"
	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
)

// --- use-case stubs -------------------------------------------------------

type stubCreateSubscription struct {
	out ports.CreateSubscriptionOutput
	err error
	in  ports.CreateSubscriptionInput
}

func (s *stubCreateSubscription) Invoke(_ context.Context, in ports.CreateSubscriptionInput) (ports.CreateSubscriptionOutput, error) {
	s.in = in
	return s.out, s.err
}

type stubPublishEvent struct {
	out ports.PublishEventOutput
	err error
	in  ports.PublishEventInput
}

func (s *stubPublishEvent) Invoke(_ context.Context, in ports.PublishEventInput) (ports.PublishEventOutput, error) {
	s.in = in
	return s.out, s.err
}

type stubGetDelivery struct {
	delivery *entity.Delivery
	err      error
}

func (s *stubGetDelivery) Invoke(context.Context, uuid.UUID) (*entity.Delivery, error) {
	return s.delivery, s.err
}

func newServer(t *testing.T, create *stubCreateSubscription, publish *stubPublishEvent, get *stubGetDelivery) *stdhttp.ServeMux {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return adapter.NewRouter(adapter.NewHandlers(create, publish, get, logger))
}

func do(t *testing.T, mux *stdhttp.ServeMux, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// --- subscriptions --------------------------------------------------------

func TestCreateSubscriptionReturns201WithoutSecret(t *testing.T) {
	id := uuid.New()
	create := &stubCreateSubscription{out: ports.CreateSubscriptionOutput{
		ID: id, URL: "https://example.com/hook", Events: []string{"order.created"}, MaxRPS: 10, Active: true,
	}}
	mux := newServer(t, create, &stubPublishEvent{}, &stubGetDelivery{})

	rec := do(t, mux, stdhttp.MethodPost, "/api/v1/subscriptions",
		`{"url":"https://example.com/hook","secret":"s3cr3t","events":["order.created"],"max_rps":10}`, nil)

	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if create.in.Secret != "s3cr3t" || create.in.MaxRPS != 10 {
		t.Fatalf("use case received %+v", create.in)
	}
	body := rec.Body.String()
	if strings.Contains(body, "s3cr3t") || strings.Contains(body, "secret") {
		t.Fatalf("response leaks the secret: %s", body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("response is not json: %v", err)
	}
	if out["id"] != id.String() {
		t.Fatalf("id = %v, want %s", out["id"], id)
	}
}

func TestCreateSubscriptionRejectsBadJSON(t *testing.T) {
	mux := newServer(t, &stubCreateSubscription{}, &stubPublishEvent{}, &stubGetDelivery{})
	rec := do(t, mux, stdhttp.MethodPost, "/api/v1/subscriptions", `{"url":`, nil)
	if rec.Code != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateSubscriptionRejectsEmptyBody(t *testing.T) {
	mux := newServer(t, &stubCreateSubscription{}, &stubPublishEvent{}, &stubGetDelivery{})
	rec := do(t, mux, stdhttp.MethodPost, "/api/v1/subscriptions", ``, nil)
	if rec.Code != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// --- events ---------------------------------------------------------------

func TestPublishEventReturns201(t *testing.T) {
	eventID := uuid.New()
	publish := &stubPublishEvent{out: ports.PublishEventOutput{EventID: eventID, DeliveryCount: 2}}
	mux := newServer(t, &stubCreateSubscription{}, publish, &stubGetDelivery{})

	rec := do(t, mux, stdhttp.MethodPost, "/api/v1/events",
		`{"type":"order.created","payload":{"order_id":42}}`,
		map[string]string{adapter.IdempotencyKeyHeader: "key-1"})

	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body)
	}
	if publish.in.IdempotencyKey != "key-1" || publish.in.Type != "order.created" {
		t.Fatalf("use case received %+v", publish.in)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not json: %v", err)
	}
	if out["event_id"] != eventID.String() {
		t.Fatalf("event_id = %v, want %s", out["event_id"], eventID)
	}
}

func TestPublishEventRequiresIdempotencyKey(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"missing", nil},
		{"empty", map[string]string{adapter.IdempotencyKeyHeader: ""}},
		{"blank", map[string]string{adapter.IdempotencyKeyHeader: "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			publish := &stubPublishEvent{}
			mux := newServer(t, &stubCreateSubscription{}, publish, &stubGetDelivery{})
			rec := do(t, mux, stdhttp.MethodPost, "/api/v1/events", `{"type":"order.created"}`, tc.headers)
			if rec.Code != stdhttp.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if publish.in.Type != "" {
				t.Fatal("the use case must not be invoked without an idempotency key")
			}
		})
	}
}

func TestPublishEventReportsDeduplication(t *testing.T) {
	publish := &stubPublishEvent{out: ports.PublishEventOutput{EventID: uuid.New(), Deduplicated: true}}
	mux := newServer(t, &stubCreateSubscription{}, publish, &stubGetDelivery{})
	rec := do(t, mux, stdhttp.MethodPost, "/api/v1/events", `{"type":"order.created"}`,
		map[string]string{adapter.IdempotencyKeyHeader: "key-1"})
	if rec.Code != stdhttp.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"deduplicated":true`) {
		t.Fatalf("body = %s, want deduplicated true", rec.Body)
	}
}

// --- deliveries -----------------------------------------------------------

func TestGetDeliveryReturns200(t *testing.T) {
	d := entity.NewDelivery(uuid.New(), uuid.New(), time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	if err := d.MarkSending(d.CreatedAt); err != nil {
		t.Fatalf("MarkSending: %v", err)
	}
	code := 503
	if err := d.MarkRetrying(d.CreatedAt.Add(time.Second), &code, "unexpected response status", d.CreatedAt); err != nil {
		t.Fatalf("MarkRetrying: %v", err)
	}
	mux := newServer(t, &stubCreateSubscription{}, &stubPublishEvent{}, &stubGetDelivery{delivery: d})

	rec := do(t, mux, stdhttp.MethodGet, "/api/v1/deliveries/"+d.ID.String(), "", nil)
	if rec.Code != stdhttp.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not json: %v", err)
	}
	if out["id"] != d.ID.String() || out["status"] != "RETRYING" {
		t.Fatalf("unexpected body: %v", out)
	}
	if out["attempt_count"].(float64) != 1 {
		t.Fatalf("attempt_count = %v, want 1", out["attempt_count"])
	}
	if out["next_attempt_at"] == nil || out["last_status_code"].(float64) != 503 {
		t.Fatalf("failure details missing: %v", out)
	}
	if out["event_id"] != d.EventID.String() || out["subscription_id"] != d.SubscriptionID.String() {
		t.Fatalf("identifiers missing: %v", out)
	}
}

func TestGetDeliveryRejectsMalformedID(t *testing.T) {
	mux := newServer(t, &stubCreateSubscription{}, &stubPublishEvent{}, &stubGetDelivery{})
	rec := do(t, mux, stdhttp.MethodGet, "/api/v1/deliveries/not-a-uuid", "", nil)
	if rec.Code != stdhttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// --- error mapping --------------------------------------------------------

func TestDomainErrorsMapToStatusCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", errs.NotFoundf("delivery"), stdhttp.StatusNotFound},
		{"conflict", errs.Conflictf("bad transition"), stdhttp.StatusConflict},
		{"already exists", errs.Wrapf(errs.ErrAlreadyExists, "key"), stdhttp.StatusConflict},
		{"invalid input", errs.Invalidf("bad url"), stdhttp.StatusBadRequest},
		{"internal", errors.New("pq: relation \"deliveries\" does not exist"), stdhttp.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := newServer(t, &stubCreateSubscription{}, &stubPublishEvent{}, &stubGetDelivery{err: tc.err})
			rec := do(t, mux, stdhttp.MethodGet, "/api/v1/deliveries/"+uuid.NewString(), "", nil)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			var out map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("error body is not json: %v", err)
			}
			if out["error"] == "" || out["error"] == nil {
				t.Fatalf("error body is empty: %v", out)
			}
		})
	}
}

func TestInternalErrorHidesDetails(t *testing.T) {
	secret := `pq: password authentication failed for user "app"; SELECT secret FROM subscriptions`
	mux := newServer(t, &stubCreateSubscription{}, &stubPublishEvent{}, &stubGetDelivery{err: errors.New(secret)})

	rec := do(t, mux, stdhttp.MethodGet, "/api/v1/deliveries/"+uuid.NewString(), "", nil)
	if rec.Code != stdhttp.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"pq:", "SELECT", "password", "secret"} {
		if strings.Contains(body, leak) {
			t.Fatalf("500 body leaks %q: %s", leak, body)
		}
	}
}

func TestUnknownRouteAndMethod(t *testing.T) {
	mux := newServer(t, &stubCreateSubscription{}, &stubPublishEvent{}, &stubGetDelivery{})
	if rec := do(t, mux, stdhttp.MethodGet, "/api/v1/unknown", "", nil); rec.Code != stdhttp.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec := do(t, mux, stdhttp.MethodDelete, "/api/v1/events", "", nil); rec.Code != stdhttp.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
