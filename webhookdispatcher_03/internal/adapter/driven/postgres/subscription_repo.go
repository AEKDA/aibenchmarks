package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgErr "github.com/pkg/errors"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/errs"
)

// SubscriptionRepo хранилище подписчиков.
type SubscriptionRepo struct {
	pool *pgxpool.Pool
}

// NewSubscriptionRepo собирает репозиторий подписчиков.
func NewSubscriptionRepo(pool *pgxpool.Pool) *SubscriptionRepo {
	return &SubscriptionRepo{pool: pool}
}

// Save создаёт или обновляет подписчика (upsert по id).
func (r *SubscriptionRepo) Save(ctx context.Context, s entity.Subscription) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO subscriptions(id,url,secret,events,max_rps)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (id) DO UPDATE SET
		   url=$2, secret=$3, events=$4, max_rps=$5`,
		s.ID, s.URL, s.Secret, s.Events, s.MaxRPS)
	if err != nil {
		return pkgErr.Wrapf(err, "storage.SubscriptionRepo.Save")
	}
	return nil
}

// GetByID возвращает подписчика по id; errs.ErrNotFound — если нет.
func (r *SubscriptionRepo) GetByID(ctx context.Context, id uuid.UUID) (entity.Subscription, error) {
	var s entity.Subscription
	err := r.pool.QueryRow(ctx,
		`SELECT id,url,secret,events,max_rps FROM subscriptions WHERE id=$1`, id).
		Scan(&s.ID, &s.URL, &s.Secret, &s.Events, &s.MaxRPS)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Subscription{}, errs.ErrNotFound
	}
	if err != nil {
		return entity.Subscription{}, pkgErr.Wrapf(err, "storage.SubscriptionRepo.GetByID")
	}
	return s, nil
}

// GetByEventType возвращает подписчиков, подписанных на тип события.
func (r *SubscriptionRepo) GetByEventType(ctx context.Context, eventType string) ([]entity.Subscription, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id,url,secret,events,max_rps FROM subscriptions WHERE $1 = ANY(events)`, eventType)
	if err != nil {
		return nil, pkgErr.Wrapf(err, "storage.SubscriptionRepo.GetByEventType")
	}
	defer rows.Close()

	var subs []entity.Subscription
	for rows.Next() {
		var s entity.Subscription
		if err := rows.Scan(&s.ID, &s.URL, &s.Secret, &s.Events, &s.MaxRPS); err != nil {
			return nil, pkgErr.Wrapf(err, "storage.SubscriptionRepo.GetByEventType.scan")
		}
		subs = append(subs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, pkgErr.Wrapf(err, "storage.SubscriptionRepo.GetByEventType.iterate")
	}
	return subs, nil
}
