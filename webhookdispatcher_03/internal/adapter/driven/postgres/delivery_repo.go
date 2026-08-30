package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgErr "github.com/pkg/errors"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/errs"
)

// DeliveryRepo хранилище доставок с конкурентным забором задач.
type DeliveryRepo struct {
	pool *pgxpool.Pool
}

// NewDeliveryRepo собирает репозиторий доставок.
func NewDeliveryRepo(pool *pgxpool.Pool) *DeliveryRepo {
	return &DeliveryRepo{pool: pool}
}

// scanRow — интерфейс сканера строки (pgx.Row и pgx.Rows подходят).
type scanRow interface {
	Scan(dest ...any) error
}

// scanDelivery разбирает строку deliveries в сущность, корректно обрабатывая
// NULL в nullable-колонках (next_attempt_at, last_http_status).
func scanDelivery(row scanRow) (entity.Delivery, error) {
	var d entity.Delivery
	var nextAttempt *time.Time
	var lastStatus *int
	err := row.Scan(
		&d.ID, &d.EventID, &d.SubscriptionID,
		(*string)(&d.Status),
		&d.Attempt,
		&nextAttempt,
		&d.Payload,
		&lastStatus,
	)
	if err != nil {
		return entity.Delivery{}, err
	}
	if nextAttempt != nil {
		d.NextAttemptAt = *nextAttempt
	}
	if lastStatus != nil {
		d.LastHTTPStatus = *lastStatus
	}
	return d, nil
}

// claimQuery массово захватывает задачи и переводит их в SENDING:
// CTE выбирает до limit кандидатов FOR UPDATE SKIP LOCKED, UPDATE возвращает их.
const claimQuery = `
WITH claimed AS (
    SELECT id FROM deliveries
    WHERE status IN ('PENDING','RETRYING')
      AND (next_attempt_at IS NULL OR next_attempt_at <= $2)
    ORDER BY id
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE deliveries d SET status = 'SENDING'
FROM claimed c
WHERE d.id = c.id
RETURNING d.id, d.event_id, d.subscription_id, d.status, d.attempt,
          d.next_attempt_at, d.payload, d.last_http_status`

// ClaimNext захватывает до limit готовых задач (PENDING или RETRYING со сроком
// <= now) и переводит их в SENDING. Захват конкурентобезопасен (SKIP LOCKED).
func (r *DeliveryRepo) ClaimNext(ctx context.Context, limit int, now time.Time) ([]entity.Delivery, error) {
	rows, err := r.pool.Query(ctx, claimQuery, limit, now)
	if err != nil {
		return nil, pkgErr.Wrapf(err, "storage.DeliveryRepo.ClaimNext")
	}
	defer rows.Close()

	var out []entity.Delivery
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, pkgErr.Wrapf(err, "storage.DeliveryRepo.ClaimNext.scan")
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, pkgErr.Wrapf(err, "storage.DeliveryRepo.ClaimNext.iterate")
	}
	return out, nil
}

// MarkOutcome применяет исход доставки по id: статус, счётчик попыток,
// время следующей попытки и последний HTTP-статус.
func (r *DeliveryRepo) MarkOutcome(ctx context.Context, d entity.Delivery) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE deliveries
		 SET status=$2, attempt=$3, next_attempt_at=$4, last_http_status=$5
		 WHERE id=$1`,
		d.ID, string(d.Status), d.Attempt, d.NextAttemptAt, d.LastHTTPStatus)
	if err != nil {
		return pkgErr.Wrapf(err, "storage.DeliveryRepo.MarkOutcome")
	}
	return nil
}

// GetByID возвращает доставку по id; errs.ErrNotFound — если нет.
func (r *DeliveryRepo) GetByID(ctx context.Context, id uuid.UUID) (entity.Delivery, error) {
	row := r.pool.QueryRow(ctx,
		`SELECT id,event_id,subscription_id,status,attempt,next_attempt_at,payload,last_http_status
		 FROM deliveries WHERE id=$1`, id)
	d, err := scanDelivery(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Delivery{}, errs.ErrNotFound
	}
	if err != nil {
		return entity.Delivery{}, pkgErr.Wrapf(err, "storage.DeliveryRepo.GetByID")
	}
	return d, nil
}
