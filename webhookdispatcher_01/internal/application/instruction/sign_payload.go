// Package instruction holds behaviour shared by several use cases. Like the
// rest of the application layer it depends only on the standard library and
// github.com/google/uuid.
package instruction

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	"github.com/example/webhookdispatcher/internal/application/errs"
)

// SignaturePrefix is the algorithm marker of the X-Signature header value.
const SignaturePrefix = "sha256="

// SignPayload computes the HMAC-SHA256 signature of an outbound body.
type SignPayload struct{}

// NewSignPayload builds the instruction.
func NewSignPayload() *SignPayload { return &SignPayload{} }

// Invoke returns "sha256=<hex_digest>" where the digest is HMAC-SHA256 over
// body keyed with the subscription secret, in lowercase hex.
func (i *SignPayload) Invoke(ctx context.Context, body []byte, secret string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", errs.Wrapf(err, "sign payload")
	}
	if secret == "" {
		return "", errs.Invalidf("signing secret is required")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write(body); err != nil {
		return "", errs.Wrapf(err, "sign payload")
	}
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil)), nil
}
