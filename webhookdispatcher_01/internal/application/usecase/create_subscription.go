// Package usecase implements the application's driver ports. Each use case is
// named after the action it performs and is invoked through Invoke.
package usecase

import (
	"context"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
)

// CreateSubscription registers a webhook subscriber.
type CreateSubscription struct {
	subscriptions ports.SubscriptionRepository
	clock         ports.Clock
}

// NewCreateSubscription builds the use case.
func NewCreateSubscription(subscriptions ports.SubscriptionRepository, clock ports.Clock) *CreateSubscription {
	return &CreateSubscription{subscriptions: subscriptions, clock: clock}
}

// Invoke validates the input and stores the subscription. The returned output
// never carries the subscription secret.
func (u *CreateSubscription) Invoke(ctx context.Context, in ports.CreateSubscriptionInput) (ports.CreateSubscriptionOutput, error) {
	if err := ctx.Err(); err != nil {
		return ports.CreateSubscriptionOutput{}, errs.Wrapf(err, "create subscription")
	}
	sub, err := entity.NewSubscription(in.URL, in.Secret, in.Events, in.MaxRPS, u.clock.Now(ctx))
	if err != nil {
		return ports.CreateSubscriptionOutput{}, errs.Wrapf(err, "create subscription")
	}
	if err := u.subscriptions.Save(ctx, sub); err != nil {
		return ports.CreateSubscriptionOutput{}, errs.Wrapf(err, "save subscription")
	}
	return ports.CreateSubscriptionOutput{
		ID:     sub.ID,
		URL:    sub.URL,
		Events: sub.Events,
		MaxRPS: sub.MaxRPS,
		Active: sub.Active,
	}, nil
}
