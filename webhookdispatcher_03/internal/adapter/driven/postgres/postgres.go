// Package postgres — driven-адаптер хранилища на PostgreSQL (pgx).
// Реализует порты application.ports (SubscriptionRepo, EventRepo, DeliveryRepo)
// и ничего не знает о других адаптерах.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"webhookdispatcher/internal/application/ports"
)

// Compile-time проверки: структуры адаптера обязаны удовлетворять портам.
var (
	_ ports.SubscriptionRepo = (*SubscriptionRepo)(nil)
	_ ports.EventRepo        = (*EventRepo)(nil)
	_ ports.DeliveryRepo     = (*DeliveryRepo)(nil)
)

// NewPool создаёт пул подключений к PostgreSQL по строке соединения.
// pgxpool.New не выполняет сетевых операций, поэтому реальное подключение
// происходит лениво при первом запросе; проверка доступности остаётся на /ready.
func NewPool(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("storage.NewPool: %w", err)
	}
	return pool, nil
}
