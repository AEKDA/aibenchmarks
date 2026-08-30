package usecase

import (
	"context"
	"time"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports"
)

// ClaimNext забирает до limit готовых задач на доставку из хранилища.
type ClaimNext struct {
	repo ports.DeliveryRepo
}

// NewClaimNext собирает сценарий забора задач.
func NewClaimNext(repo ports.DeliveryRepo) *ClaimNext {
	return &ClaimNext{repo: repo}
}

// Invoke захватывает до limit задач (PENDING/готовых RETRYING) и переводит их в SENDING.
func (c *ClaimNext) Invoke(ctx context.Context, now time.Time, limit int) ([]entity.Delivery, error) {
	return c.repo.ClaimNext(ctx, limit, now)
}
