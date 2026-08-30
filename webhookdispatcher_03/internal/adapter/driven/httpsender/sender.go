// Package httpsender реализует исходящий HTTP-клиент к подписчикам.
package httpsender

import (
	"bytes"
	"context"
	"net/http"
	"time"
)

// Sender отправляет вебхуки подписчикам через HTTP.
type Sender struct {
	cli *http.Client
}

// New создаёт Sender с таймаутом запроса 5 секунд.
func New() *Sender {
	return &Sender{cli: &http.Client{Timeout: 5 * time.Second}}
}

// Send POST-запрос payload по url с подписью X-Signature и кастомным User-Agent.
// Таймаут/сетевые ошибки поверх timeouts возвращаются как ошибка с кодом 0.
func (s *Sender) Send(ctx context.Context, url, userAgent, signature string, payload []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Signature", signature)
	resp, err := s.cli.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
