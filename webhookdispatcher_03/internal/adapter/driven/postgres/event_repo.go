package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pkgErr "github.com/pkg/errors"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports"
)

// EventRepo хранилище событий с идемпотентностью по ключу (outbox).
type EventRepo struct {
	pool *pgxpool.Pool
}

// NewEventRepo собирает репозиторий событий.
func NewEventRepo(pool *pgxpool.Pool) *EventRepo {
	return &EventRepo{pool: pool}
}

// SaveWithin атомарно сохраняет событие и его доставки в одной транзакции.
// Если idem_key уже применён (в т.ч. из параллельной транзакции), возвращает
// существующий EventID с Duplicate=true, ничего не записывая повторно.
func (r *EventRepo) SaveWithin(ctx context.Context, key string, ev entity.Event, del []entity.Delivery) (ports.OutboxResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.begin")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Был ли ключ применён ранее?
	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM events WHERE idem_key=$1`, key).Scan(&existingID)
	if err == nil {
		return ports.OutboxResult{EventID: existingID, Duplicate: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.select-key")
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO events(id,type,payload,created_at,idem_key) VALUES($1,$2,$3,$4,$5)`,
		ev.ID, ev.Type, ev.Payload, ev.CreatedAt, key); err != nil {
		// Гонка: параллельная транзакция успела вставить тот же ключ —
		// возвращаем уже существующее событие вместо конфликта.
		if isUniqueViolation(err) {
			err = tx.QueryRow(ctx, `SELECT id FROM events WHERE idem_key=$1`, key).Scan(&existingID)
			if err == nil {
				return ports.OutboxResult{EventID: existingID, Duplicate: true}, nil
			}
			return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.re-read-key")
		}
		return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.insert-event")
	}

	for _, d := range del {
		if _, err := tx.Exec(ctx,
			`INSERT INTO deliveries(id,event_id,subscription_id,status,attempt,payload)
			 VALUES($1,$2,$3,$4,$5,$6)`,
			d.ID, d.EventID, d.SubscriptionID, string(d.Status), d.Attempt, d.Payload); err != nil {
			return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.insert-delivery")
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.commit")
	}
	return ports.OutboxResult{EventID: ev.ID}, nil
}

// isUniqueViolation определяет, является ли ошибка нарушением unique-ограничения.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
