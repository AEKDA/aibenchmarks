package entity

import (
	"math/rand"
	"time"
)

// MaxAttempts лимит попыток доставки (включая первую).
const MaxAttempts = 5

const (
	backoffBase   = time.Second
	backoffJitter = 0.25
)

// BackoffDelay рассчитывает экспоненциальную задержку с джиттером для попытки attempt.
// T = base × 2^attempt ± jitter. attempt индексируется с 0 (первая попытка → base).
func BackoffDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := backoffBase * time.Duration(1<<attempt)
	delta := time.Duration(float64(base) * backoffJitter * rand.Float64())
	if rand.Intn(2) == 0 {
		return base - delta
	}
	return base + delta
}