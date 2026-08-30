// Package ratelimiter implements the RateLimiter driven port with one token
// bucket per target host, so a burst of deliveries never floods a subscriber.
package ratelimiter

import (
	"context"
	"sync"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
	"golang.org/x/time/rate"
)

// PerHost throttles outbound calls per target host. Limiters are created on
// demand and shared by every subscription pointing at the same host.
type PerHost struct {
	mu       sync.Mutex
	limiters map[string]*hostLimiter
}

type hostLimiter struct {
	limiter *rate.Limiter
	rps     int
}

// New builds the adapter.
func New() *PerHost {
	return &PerHost{limiters: map[string]*hostLimiter{}}
}

var _ ports.RateLimiter = (*PerHost)(nil)

// Wait blocks until the call to host is allowed by its rps budget, or returns
// the context error when the context ends first, so shutdown is never delayed
// by a queued token.
func (p *PerHost) Wait(ctx context.Context, host string, rps int) error {
	if rps <= 0 {
		return errs.Invalidf("rate limit for host %s must be positive, got %d", host, rps)
	}
	if err := ctx.Err(); err != nil {
		return errs.Wrapf(err, "rate limit host %s", host)
	}
	if err := p.limiterFor(host, rps).Wait(ctx); err != nil {
		return errs.Wrapf(err, "rate limit host %s", host)
	}
	return nil
}

// limiterFor returns the limiter of a host, creating or retuning it when the
// subscription's rps changed.
func (p *PerHost) limiterFor(host string, rps int) *rate.Limiter {
	p.mu.Lock()
	defer p.mu.Unlock()

	existing, ok := p.limiters[host]
	if !ok {
		created := &hostLimiter{limiter: rate.NewLimiter(rate.Limit(rps), rps), rps: rps}
		p.limiters[host] = created
		return created.limiter
	}
	if existing.rps != rps {
		existing.limiter.SetLimit(rate.Limit(rps))
		existing.limiter.SetBurst(rps)
		existing.rps = rps
	}
	return existing.limiter
}
