package usecase

import (
	"context"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"github.com/google/uuid"
)

// GetDelivery reads the current status of one delivery.
type GetDelivery struct {
	deliveries ports.DeliveryRepository
}

// NewGetDelivery builds the use case.
func NewGetDelivery(deliveries ports.DeliveryRepository) *GetDelivery {
	return &GetDelivery{deliveries: deliveries}
}

// Invoke returns the delivery, or errs.ErrNotFound when it does not exist.
func (u *GetDelivery) Invoke(ctx context.Context, id uuid.UUID) (*entity.Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrapf(err, "get delivery")
	}
	delivery, err := u.deliveries.GetByID(ctx, id)
	if err != nil {
		return nil, errs.Wrapf(err, "get delivery %s", id)
	}
	return delivery, nil
}
