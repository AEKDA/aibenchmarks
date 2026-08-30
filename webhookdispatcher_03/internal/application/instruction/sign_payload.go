// Package instruction содержит переиспользуемые шаги бизнес-логики.
package instruction

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// signPayload — инструкция подписи полезной нагрузки (HMAC-SHA256).
type signPayload struct{}

// NewSignPayload возвращает инструкцию подписи.
func NewSignPayload() *signPayload { return &signPayload{} }

// Invoke возвращает значение заголовка X-Signature: sha256=<hex>.
func (signPayload) Invoke(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
