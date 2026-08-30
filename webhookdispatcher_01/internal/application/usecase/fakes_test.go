package usecase_test

import (
	"context"
	"sync"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
)

var testNow = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now(context.Context) time.Time { return c.t }

type fakeSubscriptions struct {
	mu      sync.Mutex
	saved   []*entity.Subscription
	byID    map[uuid.UUID]*entity.Subscription
	byType  map[string][]*entity.Subscription
	saveErr error
	findErr error
	getErr  error
}

func newFakeSubscriptions() *fakeSubscriptions {
	return &fakeSubscriptions{
		byID:   map[uuid.UUID]*entity.Subscription{},
		byType: map[string][]*entity.Subscription{},
	}
}

func (f *fakeSubscriptions) add(sub *entity.Subscription) {
	f.byID[sub.ID] = sub
	for _, e := range sub.Events {
		f.byType[e] = append(f.byType[e], sub)
	}
}

func (f *fakeSubscriptions) Save(_ context.Context, sub *entity.Subscription) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, sub)
	f.add(sub)
	return nil
}

func (f *fakeSubscriptions) GetByID(_ context.Context, id uuid.UUID) (*entity.Subscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	sub, ok := f.byID[id]
	if !ok {
		return nil, errs.NotFoundf("subscription %s", id)
	}
	return sub, nil
}

func (f *fakeSubscriptions) FindByEventType(_ context.Context, eventType string) ([]*entity.Subscription, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byType[eventType], nil
}

type fakeEvents struct {
	mu          sync.Mutex
	byKey       map[string]*entity.Event
	byID        map[uuid.UUID]*entity.Event
	deliveries  []*entity.Delivery
	saveErr     error
	lookupErr   error
	getErr      error
	saveCalls   int
	forceExists bool
	preexisting *entity.Event
}

func newFakeEvents() *fakeEvents {
	return &fakeEvents{byKey: map[string]*entity.Event{}, byID: map[uuid.UUID]*entity.Event{}}
}

func (f *fakeEvents) add(ev *entity.Event) {
	f.byKey[ev.IdempotencyKey] = ev
	f.byID[ev.ID] = ev
}

func (f *fakeEvents) SaveEventWithDeliveries(_ context.Context, event *entity.Event, deliveries []*entity.Delivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.forceExists {
		// Simulate a concurrent writer that won the unique-key race: this write
		// is rejected whole, so no partial event or deliveries are stored.
		if f.preexisting != nil {
			f.add(f.preexisting)
		}
		return errs.Wrapf(errs.ErrAlreadyExists, "idempotency key %s", event.IdempotencyKey)
	}
	if _, ok := f.byKey[event.IdempotencyKey]; ok {
		return errs.Wrapf(errs.ErrAlreadyExists, "idempotency key %s", event.IdempotencyKey)
	}
	f.add(event)
	f.deliveries = append(f.deliveries, deliveries...)
	return nil
}

func (f *fakeEvents) FindByIdempotencyKey(_ context.Context, key string) (*entity.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	ev, ok := f.byKey[key]
	if !ok {
		return nil, errs.NotFoundf("event with key %s", key)
	}
	return ev, nil
}

func (f *fakeEvents) GetByID(_ context.Context, id uuid.UUID) (*entity.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	ev, ok := f.byID[id]
	if !ok {
		return nil, errs.NotFoundf("event %s", id)
	}
	return ev, nil
}

func (f *fakeEvents) deliveryCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deliveries)
}

type fakeDeliveries struct {
	mu          sync.Mutex
	byID        map[uuid.UUID]*entity.Delivery
	claimed     []*entity.Delivery
	updated     []*entity.Delivery
	claimErr    error
	updateErr   error
	getErr      error
	releaseErr  error
	released    int
	claimLimit  int
	releaseArgs []time.Time
}

func newFakeDeliveries() *fakeDeliveries {
	return &fakeDeliveries{byID: map[uuid.UUID]*entity.Delivery{}}
}

func (f *fakeDeliveries) GetByID(_ context.Context, id uuid.UUID) (*entity.Delivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	d, ok := f.byID[id]
	if !ok {
		return nil, errs.NotFoundf("delivery %s", id)
	}
	return d, nil
}

func (f *fakeDeliveries) ClaimReady(_ context.Context, limit int, _ time.Time) ([]*entity.Delivery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimLimit = limit
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	return f.claimed, nil
}

func (f *fakeDeliveries) Update(_ context.Context, delivery *entity.Delivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = append(f.updated, delivery)
	f.byID[delivery.ID] = delivery
	return nil
}

func (f *fakeDeliveries) ReleaseStale(_ context.Context, lockedBefore time.Time) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseArgs = append(f.releaseArgs, lockedBefore)
	if f.releaseErr != nil {
		return 0, f.releaseErr
	}
	return f.released, nil
}

func (f *fakeDeliveries) updateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.updated)
}

type fakeSender struct {
	mu       sync.Mutex
	requests []ports.SendRequest
	result   entity.AttemptResult
	err      error
}

func (f *fakeSender) Send(_ context.Context, req ports.SendRequest) (entity.AttemptResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	return f.result, f.err
}

func (f *fakeSender) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

type fakeLimiter struct {
	mu    sync.Mutex
	hosts []string
	rps   []int
	err   error
}

func (f *fakeLimiter) Wait(_ context.Context, host string, rps int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hosts = append(f.hosts, host)
	f.rps = append(f.rps, rps)
	return f.err
}

// Compile-time checks that the fakes satisfy the driven ports.
var (
	_ ports.SubscriptionRepository = (*fakeSubscriptions)(nil)
	_ ports.EventRepository        = (*fakeEvents)(nil)
	_ ports.DeliveryRepository     = (*fakeDeliveries)(nil)
	_ ports.WebhookSender          = (*fakeSender)(nil)
	_ ports.RateLimiter            = (*fakeLimiter)(nil)
	_ ports.Clock                  = fixedClock{}
)
