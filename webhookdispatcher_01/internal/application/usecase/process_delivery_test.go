package usecase_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/instruction"
	"github.com/example/webhookdispatcher/internal/application/usecase"
	"github.com/google/uuid"
)

type processFixture struct {
	uc       *usecase.ProcessDelivery
	sender   *fakeSender
	limiter  *fakeLimiter
	deliv    *fakeDeliveries
	sub      *entity.Subscription
	event    *entity.Event
	delivery *entity.Delivery
}

// newProcessFixture wires the use case with a claimed delivery already in
// SENDING, as the worker receives it from ClaimReady.
func newProcessFixture(t *testing.T, result entity.AttemptResult, attempts int) *processFixture {
	t.Helper()
	subs := newFakeSubscriptions()
	sub := mustSubscription(t, "https://example.com/hook", []string{"order.created"})
	subs.add(sub)

	events := newFakeEvents()
	ev, err := entity.NewEvent("key-1", "order.created", json.RawMessage(`{"order_id":42}`), testNow)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	events.add(ev)

	deliveries := newFakeDeliveries()
	d := entity.NewDelivery(ev.ID, sub.ID, testNow)
	for i := 0; i < attempts; i++ {
		if err := d.MarkSending(testNow); err != nil {
			t.Fatalf("MarkSending: %v", err)
		}
		if i < attempts-1 {
			if err := d.MarkRetrying(testNow, nil, "retry", testNow); err != nil {
				t.Fatalf("MarkRetrying: %v", err)
			}
		}
	}
	deliveries.byID[d.ID] = d

	sender := &fakeSender{result: result}
	limiter := &fakeLimiter{}
	clock := fixedClock{testNow}

	uc := usecase.NewProcessDelivery(
		deliveries, events, subs, sender, limiter, clock,
		instruction.NewSignPayload(),
		instruction.NewScheduleRetry(clock, nil),
	)
	return &processFixture{uc: uc, sender: sender, limiter: limiter, deliv: deliveries, sub: sub, event: ev, delivery: d}
}

func TestProcessDeliverySuccess(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{StatusCode: 200}, 1)

	if err := f.uc.Invoke(context.Background(), f.delivery); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if f.delivery.Status != entity.StatusDelivered {
		t.Fatalf("status = %s, want DELIVERED", f.delivery.Status)
	}
	if f.delivery.LastStatusCode == nil || *f.delivery.LastStatusCode != 200 {
		t.Fatalf("LastStatusCode = %v, want 200", f.delivery.LastStatusCode)
	}
	if f.deliv.updateCount() != 1 {
		t.Fatalf("update calls = %d, want 1", f.deliv.updateCount())
	}
}

func TestProcessDeliverySignsAndThrottles(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{StatusCode: 200}, 1)

	if err := f.uc.Invoke(context.Background(), f.delivery); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if len(f.limiter.hosts) != 1 || f.limiter.hosts[0] != "example.com" || f.limiter.rps[0] != f.sub.MaxRPS {
		t.Fatalf("limiter called with %v / %v", f.limiter.hosts, f.limiter.rps)
	}
	req := f.sender.requests[0]
	if req.URL != f.sub.URL {
		t.Fatalf("URL = %s, want %s", req.URL, f.sub.URL)
	}
	if !strings.HasPrefix(req.Signature, instruction.SignaturePrefix) {
		t.Fatalf("signature = %q", req.Signature)
	}
	want, err := instruction.NewSignPayload().Invoke(context.Background(), req.Body, f.sub.Secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if req.Signature != want {
		t.Fatal("signature does not match the delivered body")
	}
	var body map[string]any
	if err := json.Unmarshal(req.Body, &body); err != nil {
		t.Fatalf("body is not json: %v", err)
	}
	if body["event_id"] != f.event.ID.String() || body["type"] != "order.created" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestProcessDeliveryRetryableResponseSchedulesRetry(t *testing.T) {
	for _, code := range []int{429, 500, 503} {
		f := newProcessFixture(t, entity.AttemptResult{StatusCode: code}, 1)
		if err := f.uc.Invoke(context.Background(), f.delivery); err != nil {
			t.Fatalf("code %d Invoke: %v", code, err)
		}
		if f.delivery.Status != entity.StatusRetrying {
			t.Fatalf("code %d status = %s, want RETRYING", code, f.delivery.Status)
		}
		want := testNow.Add(entity.BackoffDelay(0, nil))
		if f.delivery.NextAttemptAt == nil || !f.delivery.NextAttemptAt.Equal(want) {
			t.Fatalf("code %d NextAttemptAt = %v, want %v", code, f.delivery.NextAttemptAt, want)
		}
	}
}

func TestProcessDeliveryTimeoutSchedulesRetry(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{TimedOut: true}, 1)
	if err := f.uc.Invoke(context.Background(), f.delivery); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if f.delivery.Status != entity.StatusRetrying {
		t.Fatalf("status = %s, want RETRYING", f.delivery.Status)
	}
}

func TestProcessDeliveryNonRetryableResponseDeadLetters(t *testing.T) {
	for _, code := range []int{301, 400, 404} {
		f := newProcessFixture(t, entity.AttemptResult{StatusCode: code}, 1)
		if err := f.uc.Invoke(context.Background(), f.delivery); err != nil {
			t.Fatalf("code %d Invoke: %v", code, err)
		}
		if f.delivery.Status != entity.StatusDeadLetter {
			t.Fatalf("code %d status = %s, want DEAD_LETTER", code, f.delivery.Status)
		}
		if f.delivery.NextAttemptAt != nil {
			t.Fatalf("code %d must not be rescheduled", code)
		}
	}
}

func TestProcessDeliveryLastAttemptDeadLetters(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{StatusCode: 500}, entity.MaxAttempts)
	if err := f.uc.Invoke(context.Background(), f.delivery); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if f.delivery.Status != entity.StatusDeadLetter {
		t.Fatalf("status = %s, want DEAD_LETTER", f.delivery.Status)
	}
	if f.delivery.AttemptCount != entity.MaxAttempts {
		t.Fatalf("AttemptCount = %d, want %d", f.delivery.AttemptCount, entity.MaxAttempts)
	}
}

func TestProcessDeliveryStopsWhenRateLimiterIsCanceled(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{StatusCode: 200}, 1)
	f.limiter.err = context.Canceled

	err := f.uc.Invoke(context.Background(), f.delivery)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if f.sender.calls() != 0 {
		t.Fatal("nothing may be sent once the limiter gives up")
	}
	if f.delivery.Status != entity.StatusSending {
		t.Fatalf("status = %s, want the delivery to stay claimed", f.delivery.Status)
	}
	if f.deliv.updateCount() != 0 {
		t.Fatal("no state may be persisted for an aborted attempt")
	}
}

func TestProcessDeliveryRejectsUnclaimedDelivery(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{StatusCode: 200}, 0)
	if err := f.uc.Invoke(context.Background(), f.delivery); err == nil {
		t.Fatal("expected a conflict for a delivery that is not SENDING")
	}
	if f.sender.calls() != 0 {
		t.Fatal("unclaimed delivery must not be sent")
	}
}

func TestProcessDeliveryDeadLettersWhenSubscriptionIsGone(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{StatusCode: 200}, 1)
	f.delivery.SubscriptionID = uuid.New()

	if err := f.uc.Invoke(context.Background(), f.delivery); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if f.delivery.Status != entity.StatusDeadLetter {
		t.Fatalf("status = %s, want DEAD_LETTER", f.delivery.Status)
	}
	if f.sender.calls() != 0 {
		t.Fatal("nothing may be sent without a subscription")
	}
}

func TestProcessDeliverySenderFailureLeavesDeliveryClaimed(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{}, 1)
	f.sender.err = errors.New("sender misconfigured")

	if err := f.uc.Invoke(context.Background(), f.delivery); err == nil {
		t.Fatal("expected error")
	}
	if f.delivery.Status != entity.StatusSending {
		t.Fatalf("status = %s, want SENDING", f.delivery.Status)
	}
}

func TestProcessDeliveryRespectsCanceledContext(t *testing.T) {
	f := newProcessFixture(t, entity.AttemptResult{StatusCode: 200}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.uc.Invoke(ctx, f.delivery); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if f.sender.calls() != 0 {
		t.Fatal("nothing may be sent with a canceled context")
	}
}
