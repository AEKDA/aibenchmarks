// Package httpsender implements the WebhookSender driven port over net/http.
package httpsender

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/example/webhookdispatcher/internal/application/entity"
	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/ports"
)

// SignatureHeader carries the HMAC-SHA256 signature of the body.
const SignatureHeader = "X-Signature"

// maxDrainedBody bounds how much of a response body we read before closing, so
// the connection can be reused without letting a subscriber flood us.
const maxDrainedBody = 4 << 10

// Sender performs one outbound webhook call per attempt.
type Sender struct {
	client    *http.Client
	userAgent string
	timeout   time.Duration
}

// New builds the adapter. timeout bounds a single attempt.
func New(userAgent string, timeout time.Duration) *Sender {
	return &Sender{
		// No client-level timeout: the per-attempt deadline comes from the
		// context, so cancellation propagates from the caller.
		client:    &http.Client{},
		userAgent: userAgent,
		timeout:   timeout,
	}
}

// NewWithClient builds the adapter around a caller-supplied client. Tests use
// it to point the sender at an httptest server's transport.
func NewWithClient(client *http.Client, userAgent string, timeout time.Duration) *Sender {
	return &Sender{client: client, userAgent: userAgent, timeout: timeout}
}

var _ ports.WebhookSender = (*Sender)(nil)

// Send POSTs the signed body to the subscriber. Transport failures and
// timeouts are reported inside the AttemptResult; the error return is reserved
// for problems that are not attempt outcomes, such as a cancelled caller.
func (s *Sender) Send(ctx context.Context, req ports.SendRequest) (entity.AttemptResult, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return entity.AttemptResult{}, errs.Wrapf(err, "build request for %s", req.URL)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", s.userAgent)
	httpReq.Header.Set(SignatureHeader, req.Signature)
	httpReq.Header.Set("X-Event-Id", req.EventID.String())
	httpReq.Header.Set("X-Event-Type", req.EventType)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		// A cancelled caller is a shutdown, not a failed attempt.
		if ctx.Err() != nil {
			return entity.AttemptResult{}, errs.Wrapf(ctx.Err(), "send to %s", req.URL)
		}
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return entity.AttemptResult{TimedOut: true}, nil
		}
		return entity.AttemptResult{TransportError: "transport failure"}, nil
	}
	defer resp.Body.Close()
	// Drain a bounded prefix so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainedBody))

	return entity.AttemptResult{StatusCode: resp.StatusCode}, nil
}

func isTimeout(err error) bool {
	var timeoutErr interface{ Timeout() bool }
	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}
