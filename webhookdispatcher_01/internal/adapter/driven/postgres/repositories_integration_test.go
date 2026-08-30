package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/adapter/driven/postgres"
	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testNow = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func mustSubscription(t *testing.T, url string, events ...string) *entity.Subscription {
	t.Helper()
	sub, err := entity.NewSubscription(url, "secret", events, 10, testNow)
	if err != nil {
		t.Fatalf("NewSubscription: %v", err)
	}
	return sub
}

func mustEvent(t *testing.T, key string) *entity.Event {
	t.Helper()
	ev, err := entity.NewEvent(key, "order.created", json.RawMessage(`{"order_id":42}`), testNow)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	return ev
}

func TestMigrateIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	// newTestPool already migrated once; a second run must be a no-op.
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	for _, table := range []string{"subscriptions", "events", "deliveries", "schema_migrations"} {
		var exists bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s was not created", table)
		}
	}
}

func TestSubscriptionRepository(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := postgres.NewSubscriptionRepository(pool)

	wanted := mustSubscription(t, "https://a.example.com/hook", "order.created")
	other := mustSubscription(t, "https://b.example.com/hook", "order.paid")
	inactive := mustSubscription(t, "https://c.example.com/hook", "order.created")
	inactive.Active = false
	for _, sub := range []*entity.Subscription{wanted, other, inactive} {
		if err := repo.Save(ctx, sub); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	got, err := repo.FindByEventType(ctx, "order.created")
	if err != nil {
		t.Fatalf("FindByEventType: %v", err)
	}
	if len(got) != 1 || got[0].ID != wanted.ID {
		t.Fatalf("FindByEventType returned %d rows, want only %s", len(got), wanted.ID)
	}
	if got[0].Secret != "secret" || got[0].MaxRPS != 10 || len(got[0].Events) != 1 {
		t.Fatalf("subscription did not round-trip: %+v", got[0])
	}

	byID, err := repo.GetByID(ctx, wanted.ID)
	if err != nil || byID.ID != wanted.ID {
		t.Fatalf("GetByID = %v, %v", byID, err)
	}
	if _, err := repo.GetByID(ctx, uuid.New()); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("GetByID(missing) = %v, want ErrNotFound", err)
	}
}

func TestEventRepositorySavesEventAndDeliveriesAtomically(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	subs := postgres.NewSubscriptionRepository(pool)
	events := postgres.NewEventRepository(pool)

	subA := mustSubscription(t, "https://a.example.com/hook", "order.created")
	subB := mustSubscription(t, "https://b.example.com/hook", "order.created")
	for _, s := range []*entity.Subscription{subA, subB} {
		if err := subs.Save(ctx, s); err != nil {
			t.Fatalf("Save subscription: %v", err)
		}
	}

	ev := mustEvent(t, "key-1")
	deliveries := []*entity.Delivery{
		entity.NewDelivery(ev.ID, subA.ID, testNow),
		entity.NewDelivery(ev.ID, subB.ID, testNow),
	}
	if err := events.SaveEventWithDeliveries(ctx, ev, deliveries); err != nil {
		t.Fatalf("SaveEventWithDeliveries: %v", err)
	}
	if countRows(t, pool, `SELECT count(*) FROM deliveries WHERE event_id = $1`, ev.ID) != 2 {
		t.Fatal("expected two deliveries")
	}

	stored, err := events.GetByID(ctx, ev.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Type != ev.Type || string(stored.Payload) == "" {
		t.Fatalf("event did not round-trip: %+v", stored)
	}
}

func TestEventRepositoryRollsBackWhenADeliveryFails(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	events := postgres.NewEventRepository(pool)

	ev := mustEvent(t, "key-1")
	// The subscription does not exist, so the second insert violates the
	// foreign key and the whole transaction must roll back.
	bad := entity.NewDelivery(ev.ID, uuid.New(), testNow)
	if err := events.SaveEventWithDeliveries(ctx, ev, []*entity.Delivery{bad}); err == nil {
		t.Fatal("expected the transaction to fail")
	}
	if countRows(t, pool, `SELECT count(*) FROM events WHERE id = $1`, ev.ID) != 0 {
		t.Fatal("event must not survive a failed transaction")
	}
	if countRows(t, pool, `SELECT count(*) FROM deliveries WHERE event_id = $1`, ev.ID) != 0 {
		t.Fatal("deliveries must not survive a failed transaction")
	}
}

func TestEventRepositoryIdempotencyKeyIsUnique(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	events := postgres.NewEventRepository(pool)

	first := mustEvent(t, "key-1")
	if err := events.SaveEventWithDeliveries(ctx, first, nil); err != nil {
		t.Fatalf("first save: %v", err)
	}
	second := mustEvent(t, "key-1")
	err := events.SaveEventWithDeliveries(ctx, second, nil)
	if !errors.Is(err, errs.ErrAlreadyExists) {
		t.Fatalf("second save = %v, want ErrAlreadyExists", err)
	}
	if countRows(t, pool, `SELECT count(*) FROM events WHERE idempotency_key = $1`, "key-1") != 1 {
		t.Fatal("duplicate key must not create a second event")
	}

	found, err := events.FindByIdempotencyKey(ctx, "key-1")
	if err != nil || found.ID != first.ID {
		t.Fatalf("FindByIdempotencyKey = %v, %v, want %s", found, err, first.ID)
	}
	if _, err := events.FindByIdempotencyKey(ctx, "missing"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("FindByIdempotencyKey(missing) = %v, want ErrNotFound", err)
	}
}

func TestEventRepositoryConcurrentSameKeyStoresOneEvent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	subs := postgres.NewSubscriptionRepository(pool)
	events := postgres.NewEventRepository(pool)

	sub := mustSubscription(t, "https://a.example.com/hook", "order.created")
	if err := subs.Save(ctx, sub); err != nil {
		t.Fatalf("Save subscription: %v", err)
	}

	const writers = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev := mustEvent(t, "key-race")
			d := entity.NewDelivery(ev.ID, sub.ID, testNow)
			err := events.SaveEventWithDeliveries(ctx, ev, []*entity.Delivery{d})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded++
			} else if !errors.Is(err, errs.ErrAlreadyExists) {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("%d writers succeeded, want exactly 1", succeeded)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM events WHERE idempotency_key = $1`, "key-race"); n != 1 {
		t.Fatalf("events stored = %d, want 1", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM deliveries`); n != 1 {
		t.Fatalf("deliveries stored = %d, want 1", n)
	}
}

func TestDeliveryRepositoryClaimReady(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	deliveries := postgres.NewDeliveryRepository(pool)
	sub, ev := seedEvent(t, pool)

	pending := entity.NewDelivery(ev.ID, sub.ID, testNow)
	due := newRetrying(t, ev, sub, testNow.Add(-time.Second))
	notDue := newRetrying(t, ev, sub, testNow.Add(time.Hour))
	dead := newTerminal(t, ev, sub, entity.StatusDeadLetter)
	delivered := newTerminal(t, ev, sub, entity.StatusDelivered)
	insertDeliveries(t, pool, pending, due, notDue, dead, delivered)

	claimed, err := deliveries.ClaimReady(ctx, 10, testNow)
	if err != nil {
		t.Fatalf("ClaimReady: %v", err)
	}
	got := map[uuid.UUID]*entity.Delivery{}
	for _, d := range claimed {
		got[d.ID] = d
	}
	if len(got) != 2 || got[pending.ID] == nil || got[due.ID] == nil {
		t.Fatalf("claimed %d deliveries, want the PENDING one and the due RETRYING one", len(got))
	}
	for _, d := range claimed {
		if d.Status != entity.StatusSending {
			t.Fatalf("claimed delivery status = %s, want SENDING", d.Status)
		}
		if d.LockedAt == nil {
			t.Fatal("claimed delivery must be locked")
		}
	}
	if got[pending.ID].AttemptCount != 1 {
		t.Fatalf("AttemptCount = %d, want 1", got[pending.ID].AttemptCount)
	}
	if got[due.ID].AttemptCount != due.AttemptCount+1 {
		t.Fatalf("AttemptCount = %d, want %d", got[due.ID].AttemptCount, due.AttemptCount+1)
	}

	// A second claim finds nothing left: the rows are already SENDING.
	again, err := deliveries.ClaimReady(ctx, 10, testNow)
	if err != nil {
		t.Fatalf("second ClaimReady: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second claim returned %d deliveries, want 0", len(again))
	}
}

func TestDeliveryRepositoryConcurrentClaimsDoNotOverlap(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := postgres.NewDeliveryRepository(pool)
	sub, ev := seedEvent(t, pool)

	const total = 20
	all := make([]*entity.Delivery, 0, total)
	for i := 0; i < total; i++ {
		all = append(all, entity.NewDelivery(ev.ID, sub.ID, testNow))
	}
	insertDeliveries(t, pool, all...)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		claimed = map[uuid.UUID]int{}
	)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				batch, err := repo.ClaimReady(ctx, 3, testNow)
				if err != nil {
					t.Errorf("ClaimReady: %v", err)
					return
				}
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, d := range batch {
					claimed[d.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claimed) != total {
		t.Fatalf("claimed %d distinct deliveries, want %d", len(claimed), total)
	}
	for id, times := range claimed {
		if times != 1 {
			t.Fatalf("delivery %s was claimed %d times, want exactly 1", id, times)
		}
	}
}

func TestDeliveryRepositoryUpdateAndGet(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := postgres.NewDeliveryRepository(pool)
	sub, ev := seedEvent(t, pool)

	d := entity.NewDelivery(ev.ID, sub.ID, testNow)
	insertDeliveries(t, pool, d)

	claimed, err := repo.ClaimReady(ctx, 1, testNow)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimReady = %v, %v", claimed, err)
	}
	live := claimed[0]
	next := testNow.Add(2 * time.Second)
	code := 503
	if err := live.MarkRetrying(next, &code, "unexpected response status", testNow); err != nil {
		t.Fatalf("MarkRetrying: %v", err)
	}
	if err := repo.Update(ctx, live); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != entity.StatusRetrying || got.AttemptCount != 1 {
		t.Fatalf("delivery = %+v, want RETRYING with 1 attempt", got)
	}
	if got.NextAttemptAt == nil || !got.NextAttemptAt.Equal(next) {
		t.Fatalf("NextAttemptAt = %v, want %v", got.NextAttemptAt, next)
	}
	if got.LastStatusCode == nil || *got.LastStatusCode != 503 || got.LastError == "" {
		t.Fatalf("failure details lost: %+v", got)
	}
	if got.LockedAt != nil {
		t.Fatal("a retrying delivery must not stay locked")
	}

	if _, err := repo.GetByID(ctx, uuid.New()); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("GetByID(missing) = %v, want ErrNotFound", err)
	}
	missing := entity.NewDelivery(ev.ID, sub.ID, testNow)
	if err := repo.Update(ctx, missing); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("Update(missing) = %v, want ErrNotFound", err)
	}
}

func TestDeliveryRepositoryReleaseStale(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := postgres.NewDeliveryRepository(pool)
	sub, ev := seedEvent(t, pool)

	stuck := entity.NewDelivery(ev.ID, sub.ID, testNow)
	fresh := entity.NewDelivery(ev.ID, sub.ID, testNow)
	insertDeliveries(t, pool, stuck, fresh)
	if _, err := repo.ClaimReady(ctx, 10, testNow); err != nil {
		t.Fatalf("ClaimReady: %v", err)
	}
	// Only `stuck` looks abandoned: its lock is older than the threshold.
	if _, err := pool.Exec(ctx, `UPDATE deliveries SET locked_at = $2 WHERE id = $1`, stuck.ID, testNow.Add(-time.Hour)); err != nil {
		t.Fatalf("age the lock: %v", err)
	}

	released, err := repo.ReleaseStale(ctx, testNow.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ReleaseStale: %v", err)
	}
	if released != 1 {
		t.Fatalf("released %d deliveries, want 1", released)
	}

	back, err := repo.GetByID(ctx, stuck.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if back.Status != entity.StatusRetrying || back.LockedAt != nil {
		t.Fatalf("released delivery = %+v, want RETRYING and unlocked", back)
	}
	still, err := repo.GetByID(ctx, fresh.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if still.Status != entity.StatusSending {
		t.Fatalf("recently locked delivery = %s, want SENDING", still.Status)
	}

	// The released delivery is due again and can be claimed.
	claimed, err := repo.ClaimReady(ctx, 10, testNow)
	if err != nil {
		t.Fatalf("ClaimReady: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != stuck.ID {
		t.Fatalf("claimed %v, want the released delivery %s", claimed, stuck.ID)
	}
}

// --- helpers -------------------------------------------------------------

func seedEvent(t *testing.T, pool *pgxpool.Pool) (*entity.Subscription, *entity.Event) {
	t.Helper()
	ctx := context.Background()
	sub := mustSubscription(t, "https://a.example.com/hook", "order.created")
	if err := postgres.NewSubscriptionRepository(pool).Save(ctx, sub); err != nil {
		t.Fatalf("Save subscription: %v", err)
	}
	ev := mustEvent(t, fmt.Sprintf("key-%s", uuid.NewString()))
	if err := postgres.NewEventRepository(pool).SaveEventWithDeliveries(ctx, ev, nil); err != nil {
		t.Fatalf("Save event: %v", err)
	}
	return sub, ev
}

func newRetrying(t *testing.T, ev *entity.Event, sub *entity.Subscription, next time.Time) *entity.Delivery {
	t.Helper()
	d := entity.NewDelivery(ev.ID, sub.ID, testNow)
	if err := d.MarkSending(testNow); err != nil {
		t.Fatalf("MarkSending: %v", err)
	}
	if err := d.MarkRetrying(next, nil, "retry", testNow); err != nil {
		t.Fatalf("MarkRetrying: %v", err)
	}
	return d
}

func newTerminal(t *testing.T, ev *entity.Event, sub *entity.Subscription, status entity.DeliveryStatus) *entity.Delivery {
	t.Helper()
	d := entity.NewDelivery(ev.ID, sub.ID, testNow)
	if err := d.MarkSending(testNow); err != nil {
		t.Fatalf("MarkSending: %v", err)
	}
	var err error
	if status == entity.StatusDelivered {
		err = d.MarkDelivered(200, testNow)
	} else {
		err = d.MarkDeadLetter(nil, "gave up", testNow)
	}
	if err != nil {
		t.Fatalf("terminal transition: %v", err)
	}
	return d
}

func insertDeliveries(t *testing.T, pool *pgxpool.Pool, deliveries ...*entity.Delivery) {
	t.Helper()
	const q = `INSERT INTO deliveries
		(id, event_id, subscription_id, status, attempt_count, next_attempt_at,
		 last_status_code, last_error, locked_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	for _, d := range deliveries {
		if _, err := pool.Exec(context.Background(), q, d.ID, d.EventID, d.SubscriptionID, string(d.Status),
			d.AttemptCount, d.NextAttemptAt, d.LastStatusCode, d.LastError, d.LockedAt, d.CreatedAt, d.UpdatedAt); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
