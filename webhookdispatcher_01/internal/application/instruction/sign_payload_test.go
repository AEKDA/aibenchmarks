package instruction_test

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/example/webhookdispatcher/internal/application/instruction"
)

var signatureFormat = regexp.MustCompile(`^sha256=[0-9a-f]{64}$`)

func TestSignPayloadFormat(t *testing.T) {
	got, err := instruction.NewSignPayload().Invoke(context.Background(), []byte(`{"a":1}`), "s3cr3t")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !signatureFormat.MatchString(got) {
		t.Fatalf("signature = %q, want sha256=<64 lowercase hex>", got)
	}
}

func TestSignPayloadKnownVector(t *testing.T) {
	// RFC-style reference vector for HMAC-SHA256.
	const (
		key  = "key"
		body = "The quick brown fox jumps over the lazy dog"
		want = "sha256=f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	)
	got, err := instruction.NewSignPayload().Invoke(context.Background(), []byte(body), key)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}

func TestSignPayloadIsDeterministic(t *testing.T) {
	sign := instruction.NewSignPayload()
	first, err := sign.Invoke(context.Background(), []byte("body"), "secret")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	second, err := sign.Invoke(context.Background(), []byte("body"), "secret")
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if first != second {
		t.Fatalf("signatures differ across calls: %q vs %q", first, second)
	}
}

func TestSignPayloadDependsOnBodyAndSecret(t *testing.T) {
	sign := instruction.NewSignPayload()
	ctx := context.Background()
	a, _ := sign.Invoke(ctx, []byte("body-a"), "secret")
	b, _ := sign.Invoke(ctx, []byte("body-b"), "secret")
	if a == b {
		t.Fatal("different bodies produced the same signature")
	}
	c, _ := sign.Invoke(ctx, []byte("body-a"), "other-secret")
	if a == c {
		t.Fatal("different secrets produced the same signature")
	}
}

func TestSignPayloadRejectsEmptySecret(t *testing.T) {
	_, err := instruction.NewSignPayload().Invoke(context.Background(), []byte("body"), "")
	if !errors.Is(err, errs.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestSignPayloadRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := instruction.NewSignPayload().Invoke(ctx, []byte("body"), "secret"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
