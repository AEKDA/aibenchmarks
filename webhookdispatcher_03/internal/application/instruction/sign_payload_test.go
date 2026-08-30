package instruction

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSignPayloadInvoke(t *testing.T) {
	secret := "s3cret"
	body := []byte(`{"type":"order.created"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	got := NewSignPayload().Invoke(secret, body)
	if got != want {
		t.Fatalf("NewSignPayload().Invoke()=%q want %q", got, want)
	}
}
