package postgres

import (
	"context"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SubscriptionRepository stores subscribers in PostgreSQL.
type SubscriptionRepository struct {
	pool *pgxpool.Pool
}

// NewSubscriptionRepository builds the adapter.
func NewSubscriptionRepository(pool *pgxpool.Pool) *SubscriptionRepository {
	return &SubscriptionRepository{pool: pool}
}

var _ ports.SubscriptionRepository = (*SubscriptionRepository)(nil)

// Save persists a new subscription.
func (r *SubscriptionRepository) Save(ctx context.Context, sub *entity.Subscription) error {
	const q = `INSERT INTO subscriptions (id, url, secret, events, max_rps, active, created_at)
	           VALUES ($1, $2, $3, $4, $5, $6, $7)`
	if _, err := r.pool.Exec(ctx, q, sub.ID, sub.URL, sub.Secret, sub.Events, sub.MaxRPS, sub.Active, sub.CreatedAt); err != nil {
		return errs.Wrapf(translate(err), "insert subscription %s", sub.ID)
	}
	return nil
}

// GetByID returns one subscription regardless of its active flag.
func (r *SubscriptionRepository) GetByID(ctx context.Context, id uuid.UUID) (*entity.Subscription, error) {
	const q = `SELECT id, url, secret, events, max_rps, active, created_at
	           FROM subscriptions WHERE id = $1`
	var sub entity.Subscription
	err := r.pool.QueryRow(ctx, q, id).
		Scan(&sub.ID, &sub.URL, &sub.Secret, &sub.Events, &sub.MaxRPS, &sub.Active, &sub.CreatedAt)
	if err != nil {
		return nil, errs.Wrapf(translate(err), "select subscription %s", id)
	}
	return &sub, nil
}

// FindByEventType returns every active subscription listening to eventType.
func (r *SubscriptionRepository) FindByEventType(ctx context.Context, eventType string) ([]*entity.Subscription, error) {
	const q = `SELECT id, url, secret, events, max_rps, active, created_at
	           FROM subscriptions
	           WHERE active AND $1 = ANY (events)
	           ORDER BY created_at`
	rows, err := r.pool.Query(ctx, q, eventType)
	if err != nil {
		return nil, errs.Wrapf(translate(err), "select subscriptions for %s", eventType)
	}
	defer rows.Close()

	var subs []*entity.Subscription
	for rows.Next() {
		var sub entity.Subscription
		if err := rows.Scan(&sub.ID, &sub.URL, &sub.Secret, &sub.Events, &sub.MaxRPS, &sub.Active, &sub.CreatedAt); err != nil {
			return nil, errs.Wrapf(translate(err), "scan subscription")
		}
		subs = append(subs, &sub)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrapf(translate(err), "iterate subscriptions")
	}
	return subs, nil
}
