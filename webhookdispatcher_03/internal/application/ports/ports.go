// Package ports объявляет все порты гексагональной архитектуры —
// интерфейсы, которые домен требует от адаптеров.
package ports

import (
	"context"
	"time"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
)

// SubscriptionRepo хранилище подписчиков.
type SubscriptionRepo interface {
	Save(ctx context.Context, s entity.Subscription) error
	GetByID(ctx context.Context, id uuid.UUID) (entity.Subscription, error)
	GetByEventType(ctx context.Context, eventType string) ([]entity.Subscription, error)
}

// OutboxResult результат атомарного сохранения события и доставок.
type OutboxResult struct {
	EventID     uuid.UUID
	DeliveryIDs []uuid.UUID
	Duplicate   bool // true, если idempotency-ключ уже был применён
}

// EventRepo хранилище событий с идемпотентностью по ключу.
type EventRepo interface {
	// SaveWithin откладывает запись события и доставок в открытую транзакцию;
	// возвращает Duplicate, если ключ уже использован (атомарно).
	SaveWithin(ctx context.Context, idempotencyKey string, ev entity.Event, del []entity.Delivery) (OutboxResult, error)
}

// DeliveryRepo хранилище доставок с конкурентным забором задач.
type DeliveryRepo interface {
	// ClaimNext захватывает до n задач PENDING или готовых RETRYING (SKIP LOCKED),
	// переводит их в SENDING. Возвращает захваченные задачи.
	ClaimNext(ctx context.Context, limit int, now time.Time) ([]entity.Delivery, error)
	// MarkOutcome применяет исход доставки (DELIVERED/RETRYING/DEAD_LETTER).
	MarkOutcome(ctx context.Context, d entity.Delivery) error
	GetByID(ctx context.Context, id uuid.UUID) (entity.Delivery, error)
}

// Sender исходящий HTTP-клиент к подписчику.
type Sender interface {
	// Send доставляет payload по URL с подписью. Возвращает HTTP-статус.
	Send(ctx context.Context, url, userAgent, signature string, payload []byte) (int, error)
}

// RateLimiter ограничивает RPS на хост.
type RateLimiter interface {
	// Allow блокирует до появления слота для хоста. Ошибка — если хоста нет в конфиге.
	Allow(ctx context.Context, host string) error
}

// Clock абстракция времени для тестов.
type Clock interface {
	Now() time.Time
}