package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
//
// Идемпотентность реализована одним атомарным INSERT ... ON CONFLICT DO NOTHING:
// в отличие от предпроверки SELECT-затем-INSERT не абортит транзакцию при гонке
// (unique violation перевёл бы tx в aborted-состояние, сделав re-read бессмысленным).
func (r *EventRepo) SaveWithin(ctx context.Context, key string, ev entity.Event, del []entity.Delivery) (ports.OutboxResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.begin")
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var insertedID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO events(id,type,payload,created_at,idem_key) VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (idem_key) DO NOTHING RETURNING id`,
		ev.ID, ev.Type, ev.Payload, ev.CreatedAt, key).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Ключ уже существует: ON CONFLICT DO NOTHING не абортит транзакцию,
		// поэтому повторный SELECT валиден. Возвращаем ранее созданное событие.
		var existingID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id FROM events WHERE idem_key=$1`, key).Scan(&existingID); err != nil {
			return ports.OutboxResult{}, pkgErr.Wrapf(err, "storage.EventRepo.SaveWithin.re-read-key")
		}
		return ports.OutboxResult{EventID: existingID, Duplicate: true}, nil
	}
	if err != nil {
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
