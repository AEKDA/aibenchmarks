package usecase

import (
	"context"

	"github.com/google/uuid"
	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/ports"
)

// GetDelivery возвращает статус доставки по ID.
type GetDelivery struct {
	repo ports.DeliveryRepo
}

// NewGetDelivery собирает сценарий чтения доставки.
func NewGetDelivery(repo ports.DeliveryRepo) *GetDelivery {
	return &GetDelivery{repo: repo}
}

// Invoke возвращает доставку по ID (errs.ErrNotFound, если нет).
func (g *GetDelivery) Invoke(ctx context.Context, id uuid.UUID) (entity.Delivery, error) {
	return g.repo.GetByID(ctx, id)
}
