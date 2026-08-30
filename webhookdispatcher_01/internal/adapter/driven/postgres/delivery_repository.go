package postgres

import (
	"context"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DeliveryRepository stores delivery tasks and hands ready ones to workers.
type DeliveryRepository struct {
	pool *pgxpool.Pool
}

// NewDeliveryRepository builds the adapter.
func NewDeliveryRepository(pool *pgxpool.Pool) *DeliveryRepository {
	return &DeliveryRepository{pool: pool}
}

var _ ports.DeliveryRepository = (*DeliveryRepository)(nil)

const deliveryColumns = `id, event_id, subscription_id, status, attempt_count, next_attempt_at,
	last_status_code, last_error, locked_at, created_at, updated_at`

// GetByID returns one delivery.
func (r *DeliveryRepository) GetByID(ctx context.Context, id uuid.UUID) (*entity.Delivery, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+deliveryColumns+` FROM deliveries WHERE id = $1`, id)
	d, err := scanDelivery(row)
	if err != nil {
		return nil, errs.Wrapf(translate(err), "select delivery %s", id)
	}
	return d, nil
}

// ClaimReady takes up to limit ready deliveries into SENDING in one statement.
// FOR UPDATE SKIP LOCKED guarantees that concurrent workers never claim the
// same row, and the attempt counter is incremented as the attempt starts.
func (r *DeliveryRepository) ClaimReady(ctx context.Context, limit int, now time.Time) ([]*entity.Delivery, error) {
	const q = `
		UPDATE deliveries d
		SET status = 'SENDING',
		    attempt_count = d.attempt_count + 1,
		    next_attempt_at = NULL,
		    locked_at = $2,
		    updated_at = $2
		WHERE d.id IN (
			SELECT c.id FROM deliveries c
			WHERE c.status = 'PENDING'
			   OR (c.status = 'RETRYING' AND c.next_attempt_at <= $2)
			ORDER BY c.next_attempt_at NULLS FIRST, c.created_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		RETURNING ` + deliveryColumns

	rows, err := r.pool.Query(ctx, q, limit, now.UTC())
	if err != nil {
		return nil, errs.Wrapf(translate(err), "claim ready deliveries")
	}
	defer rows.Close()

	var claimed []*entity.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, errs.Wrapf(translate(err), "scan claimed delivery")
		}
		claimed = append(claimed, d)
	}
	if err := rows.Err(); err != nil {
		return nil, errs.Wrapf(translate(err), "iterate claimed deliveries")
	}
	return claimed, nil
}

// Update persists the current state of a delivery.
func (r *DeliveryRepository) Update(ctx context.Context, d *entity.Delivery) error {
	const q = `
		UPDATE deliveries
		SET status = $2, attempt_count = $3, next_attempt_at = $4, last_status_code = $5,
		    last_error = $6, locked_at = $7, updated_at = $8
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, d.ID, string(d.Status), d.AttemptCount, d.NextAttemptAt,
		d.LastStatusCode, d.LastError, d.LockedAt, d.UpdatedAt)
	if err != nil {
		return errs.Wrapf(translate(err), "update delivery %s", d.ID)
	}
	if tag.RowsAffected() == 0 {
		return errs.NotFoundf("delivery %s", d.ID)
	}
	return nil
}

// ReleaseStale returns deliveries abandoned in SENDING to the queue, so a crash
// mid-attempt cannot strand a delivery forever.
func (r *DeliveryRepository) ReleaseStale(ctx context.Context, lockedBefore time.Time) (int, error) {
	const q = `
		UPDATE deliveries
		SET status = 'RETRYING', next_attempt_at = $1, locked_at = NULL, updated_at = $1
		WHERE status = 'SENDING' AND locked_at IS NOT NULL AND locked_at < $1`
	tag, err := r.pool.Exec(ctx, q, lockedBefore.UTC())
	if err != nil {
		return 0, errs.Wrapf(translate(err), "release stale deliveries")
	}
	return int(tag.RowsAffected()), nil
}

// scanner is satisfied by both pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanDelivery(s scanner) (*entity.Delivery, error) {
	var (
		d      entity.Delivery
		status string
	)
	if err := s.Scan(&d.ID, &d.EventID, &d.SubscriptionID, &status, &d.AttemptCount, &d.NextAttemptAt,
		&d.LastStatusCode, &d.LastError, &d.LockedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	d.Status = entity.DeliveryStatus(status)
	return &d, nil
}
