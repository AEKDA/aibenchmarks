package postgres

import (
	"context"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EventRepository stores events together with their delivery outbox rows.
type EventRepository struct {
	pool *pgxpool.Pool
}

// NewEventRepository builds the adapter.
func NewEventRepository(pool *pgxpool.Pool) *EventRepository {
	return &EventRepository{pool: pool}
}

var _ ports.EventRepository = (*EventRepository)(nil)

// SaveEventWithDeliveries writes the event and all its deliveries in a single
// transaction (transactional outbox). A duplicate idempotency key aborts the
// whole transaction and is reported as errs.ErrAlreadyExists, so a losing
// concurrent writer never leaves partial rows behind.
func (r *EventRepository) SaveEventWithDeliveries(ctx context.Context, event *entity.Event, deliveries []*entity.Delivery) error {
	err := withTx(ctx, r.pool, func(tx pgx.Tx) error {
		const insertEvent = `INSERT INTO events (id, idempotency_key, type, payload, created_at)
		                     VALUES ($1, $2, $3, $4, $5)`
		if _, err := tx.Exec(ctx, insertEvent, event.ID, event.IdempotencyKey, event.Type, []byte(event.Payload), event.CreatedAt); err != nil {
			return err
		}
		const insertDelivery = `INSERT INTO deliveries
			(id, event_id, subscription_id, status, attempt_count, next_attempt_at,
			 last_status_code, last_error, locked_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
		for _, d := range deliveries {
			if _, err := tx.Exec(ctx, insertDelivery,
				d.ID, d.EventID, d.SubscriptionID, string(d.Status), d.AttemptCount, d.NextAttemptAt,
				d.LastStatusCode, d.LastError, d.LockedAt, d.CreatedAt, d.UpdatedAt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return errs.Wrapf(translate(err), "save event %s with %d deliveries", event.ID, len(deliveries))
	}
	return nil
}

// FindByIdempotencyKey returns the event stored under key.
func (r *EventRepository) FindByIdempotencyKey(ctx context.Context, key string) (*entity.Event, error) {
	const q = `SELECT id, idempotency_key, type, payload, created_at FROM events WHERE idempotency_key = $1`
	return r.queryEvent(ctx, q, key, "select event by idempotency key")
}

// GetByID returns one event.
func (r *EventRepository) GetByID(ctx context.Context, id uuid.UUID) (*entity.Event, error) {
	const q = `SELECT id, idempotency_key, type, payload, created_at FROM events WHERE id = $1`
	return r.queryEvent(ctx, q, id, "select event")
}

func (r *EventRepository) queryEvent(ctx context.Context, query string, arg any, what string) (*entity.Event, error) {
	var (
		ev      entity.Event
		payload []byte
	)
	err := r.pool.QueryRow(ctx, query, arg).Scan(&ev.ID, &ev.IdempotencyKey, &ev.Type, &payload, &ev.CreatedAt)
	if err != nil {
		return nil, errs.Wrapf(translate(err), "%s", what)
	}
	ev.Payload = payload
	return &ev, nil
}
