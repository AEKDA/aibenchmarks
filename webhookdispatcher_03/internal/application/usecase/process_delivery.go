package usecase

import (
	"context"
	"net/url"

	"webhookdispatcher/internal/application/entity"
	"webhookdispatcher/internal/application/instruction"
	"webhookdispatcher/internal/application/ports"
)

// ProcessDelivery исполняет одну доставку: рейт-лимит, подпись, отправка,
// применение исхода (DELIVERED/RETRYING/DEAD_LETTER).
type ProcessDelivery struct {
	deliveries ports.DeliveryRepo
	subs       ports.SubscriptionRepo
	sender     ports.Sender
	rl         ports.RateLimiter
}

// NewProcessDelivery собирает сценарий доставки.
func NewProcessDelivery(deliveries ports.DeliveryRepo, subs ports.SubscriptionRepo, sender ports.Sender, rl ports.RateLimiter) *ProcessDelivery {
	return &ProcessDelivery{deliveries: deliveries, subs: subs, sender: sender, rl: rl}
}

// Invoke исполняет доставку задачи d.
func (p *ProcessDelivery) Invoke(ctx context.Context, d entity.Delivery) error {
	d.Start() // SENDING + attempt++
	sub, err := p.subs.GetByID(ctx, d.SubscriptionID)
	if err != nil {
		return err
	}
	sig := instruction.NewSignPayload().Invoke(sub.Secret, d.Payload)
	ua := "webhook-dispatcher/1.0"
	if err := p.rl.Allow(ctx, hostFromURL(sub.URL)); err != nil {
		return err
	}
	status, err := p.sender.Send(ctx, sub.URL, ua, sig, d.Payload)
	if err != nil {
		// сетевой сбой / timeout → retry
		status = 500
	}
	if _, err = instruction.NewScheduleRetry().Invoke(&d, status); err != nil {
		return err
	}
	return p.deliveries.MarkOutcome(ctx, d)
}

// hostFromURL извлекает хост из URL подписчика (для рейт-лимита).
func hostFromURL(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		return u.Host
	}
	return rawURL
}
