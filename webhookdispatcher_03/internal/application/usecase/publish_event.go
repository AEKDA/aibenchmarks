package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/errs"
	"webhookdispatcher/internal/application/ports"
)

// PublishEventIn входные данные публикации события.
type PublishEventIn struct {
	Type           string
	Payload        []byte
	IdempotencyKey string
	Now            time.Time
}

// PublishEventOut результат публикации.
type PublishEventOut struct {
	EventID   uuid.UUID
	Duplicate bool
}

// PublishEvent публикует событие: в единой транзакции сохраняет событие
// и создаёт доставки для подписанных подписчиков (transactional outbox).
type PublishEvent struct {
	events ports.EventRepo
	subs   ports.SubscriptionRepo
}

// NewPublishEvent собирает сценарий публикации.
func NewPublishEvent(events ports.EventRepo, subs ports.SubscriptionRepo) *PublishEvent {
	return &PublishEvent{events: events, subs: subs}
}

// Invoke публикует событие с гарантией идемпотентности по IdempotencyKey.
func (p *PublishEvent) Invoke(ctx context.Context, in PublishEventIn) (PublishEventOut, error) {
	if in.Type == "" || in.IdempotencyKey == "" {
		return PublishEventOut{}, errs.ErrInvalid
	}
	subs, err := p.subs.GetByEventType(ctx, in.Type)
	if err != nil {
		return PublishEventOut{}, err
	}
	ev := entity.Event{ID: uuid.New(), Type: in.Type, Payload: in.Payload, CreatedAt: in.Now}
	dels := make([]entity.Delivery, 0, len(subs))
	for _, s := range subs {
		dels = append(dels, entity.Delivery{
			ID: uuid.New(), EventID: ev.ID, SubscriptionID: s.ID,
			Status: entity.StatusPending, Payload: in.Payload,
		})
	}
	res, err := p.events.SaveWithin(ctx, in.IdempotencyKey, ev, dels)
	if err != nil {
		return PublishEventOut{}, err
	}
	return PublishEventOut{EventID: res.EventID, Duplicate: res.Duplicate}, nil
}