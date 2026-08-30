package usecase

import (
	"context"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
)

// ClaimDeliveries hands the worker pool the deliveries that are ready to send:
// PENDING ones and RETRYING ones whose next attempt time has arrived.
type ClaimDeliveries struct {
	deliveries ports.DeliveryRepository
	clock      ports.Clock
}

// NewClaimDeliveries builds the use case.
func NewClaimDeliveries(deliveries ports.DeliveryRepository, clock ports.Clock) *ClaimDeliveries {
	return &ClaimDeliveries{deliveries: deliveries, clock: clock}
}

// Invoke claims at most limit ready deliveries and moves them into SENDING.
func (u *ClaimDeliveries) Invoke(ctx context.Context, limit int) ([]*entity.Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, errs.Wrapf(err, "claim deliveries")
	}
	if limit <= 0 {
		return nil, errs.Invalidf("claim limit must be positive, got %d", limit)
	}
	claimed, err := u.deliveries.ClaimReady(ctx, limit, u.clock.Now(ctx))
	if err != nil {
		return nil, errs.Wrapf(err, "claim ready deliveries")
	}
	return claimed, nil
}

// ReleaseStaleDeliveries returns deliveries that were left in SENDING by a
// crashed or interrupted worker back to RETRYING.
type ReleaseStaleDeliveries struct {
	deliveries ports.DeliveryRepository
	clock      ports.Clock
}

// NewReleaseStaleDeliveries builds the use case.
func NewReleaseStaleDeliveries(deliveries ports.DeliveryRepository, clock ports.Clock) *ReleaseStaleDeliveries {
	return &ReleaseStaleDeliveries{deliveries: deliveries, clock: clock}
}

// Invoke releases every delivery locked longer ago than staleAfter.
func (u *ReleaseStaleDeliveries) Invoke(ctx context.Context, staleAfter time.Duration) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, errs.Wrapf(err, "release stale deliveries")
	}
	if staleAfter <= 0 {
		return 0, errs.Invalidf("stale threshold must be positive, got %s", staleAfter)
	}
	released, err := u.deliveries.ReleaseStale(ctx, u.clock.Now(ctx).Add(-staleAfter))
	if err != nil {
		return 0, errs.Wrapf(err, "release stale deliveries")
	}
	return released, nil
}
