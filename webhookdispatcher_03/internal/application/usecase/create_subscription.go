// Package usecase содержит сценарии применения доменной логики.
package usecase

import (
	"context"
	"net/url"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/errs"
	"webhookdispatcher/internal/application/ports"
)

// CreateSubscriptionIn входные данные регистрации подписчика.
type CreateSubscriptionIn struct {
	URL    string
	Secret string
	Events []string
	MaxRPS int
}

// CreateSubscription регистрирует подписчика.
type CreateSubscription struct {
	repo ports.SubscriptionRepo
}

// NewCreateSubscription собирает сценарий с хранилищем подписчиков.
func NewCreateSubscription(repo ports.SubscriptionRepo) *CreateSubscription {
	return &CreateSubscription{repo: repo}
}

// Invoke валидирует и сохраняет нового подписчика.
func (c *CreateSubscription) Invoke(ctx context.Context, in CreateSubscriptionIn) (entity.Subscription, error) {
	if _, err := url.ParseRequestURI(in.URL); err != nil || in.URL == "" {
		return entity.Subscription{}, errs.ErrInvalid
	}
	if in.Secret == "" {
		return entity.Subscription{}, errs.ErrInvalid
	}
	s := entity.Subscription{
		ID: uuid.New(), URL: in.URL, Secret: in.Secret,
		Events: in.Events, MaxRPS: in.MaxRPS,
	}
	if err := c.repo.Save(ctx, s); err != nil {
		return entity.Subscription{}, err
	}
	return s, nil
}
