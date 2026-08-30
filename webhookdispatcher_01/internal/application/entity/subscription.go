// Package entity holds the domain model of the webhook dispatcher. It depends
// only on the Go standard library and github.com/google/uuid.
package entity

import (
	"net/url"
	"strings"
	"time"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/google/uuid"
)

// Subscription is a registered webhook receiver.
type Subscription struct {
	ID        uuid.UUID
	URL       string
	Secret    string
	Events    []string
	MaxRPS    int
	Active    bool
	CreatedAt time.Time
}

// NewSubscription validates the caller-supplied fields and builds an active
// subscription with a freshly generated identifier.
func NewSubscription(rawURL, secret string, events []string, maxRPS int, now time.Time) (*Subscription, error) {
	if err := validateTargetURL(rawURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(secret) == "" {
		return nil, errs.Invalidf("subscription secret is required")
	}
	cleaned := make([]string, 0, len(events))
	for _, e := range events {
		e = strings.TrimSpace(e)
		if e == "" {
			return nil, errs.Invalidf("subscription event type must not be empty")
		}
		cleaned = append(cleaned, e)
	}
	if len(cleaned) == 0 {
		return nil, errs.Invalidf("subscription must list at least one event type")
	}
	if maxRPS <= 0 {
		return nil, errs.Invalidf("subscription max_rps must be positive, got %d", maxRPS)
	}
	return &Subscription{
		ID:        uuid.New(),
		URL:       rawURL,
		Secret:    secret,
		Events:    cleaned,
		MaxRPS:    maxRPS,
		Active:    true,
		CreatedAt: now.UTC(),
	}, nil
}

// SubscribedTo reports whether this subscription wants the given event type.
func (s *Subscription) SubscribedTo(eventType string) bool {
	for _, e := range s.Events {
		if e == eventType {
			return true
		}
	}
	return false
}

// Host returns the target host used as the rate-limiting key.
func (s *Subscription) Host() string {
	u, err := url.Parse(s.URL)
	if err != nil {
		return s.URL
	}
	return u.Host
}

func validateTargetURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return errs.Invalidf("subscription url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return errs.Invalidf("subscription url is not a valid url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errs.Invalidf("subscription url must use http or https scheme")
	}
	if u.Host == "" {
		return errs.Invalidf("subscription url must contain a host")
	}
	return nil
}
