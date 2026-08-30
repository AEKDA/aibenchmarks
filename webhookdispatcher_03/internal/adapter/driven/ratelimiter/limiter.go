// Package ratelimiter ограничивает RPS доставки на хост подписчика.
package ratelimiter

import (
	"context"
	"sync"
	"time"
)

// Limiter хранит по одному token bucket'у на хост и ограничивает RPS на хост.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	maxRPS  int
}

// bucket — token bucket одного хоста. Вместимость и скорость пополнения
// равны maxRPS токенов в секунду, bucket наполняется полностью за 1 секунду.
type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// New создаёт Limiter с maxRPS токенов в секунду на хост.
func New(maxRPS int) *Limiter {
	return &Limiter{
		buckets: make(map[string]*bucket),
		maxRPS:  maxRPS,
	}
}

// bucketFor возвращает bucket для host, создавая его при первом обращении.
func (l *Limiter) bucketFor(host string) *bucket {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[host]
	if !ok {
		// Bucket стартует полным, чтобы допустимый burst из maxRPS запросов
		// проходил мгновенно, а не ждал первичного пополнения.
		b = &bucket{tokens: float64(l.maxRPS), last: time.Now()}
		l.buckets[host] = b
	}
	return b
}

// Allow блокирует до появления токена для host (или мгновенно, если токены есть).
// Уважает отмену ctx: при отмене возвращает ctx.Err().
func (l *Limiter) Allow(ctx context.Context, host string) error {
	b := l.bucketFor(host)

	rate := float64(l.maxRPS)
	capacity := float64(l.maxRPS)

	b.mu.Lock()
	for {
		// Пополняем токены по прошедшему времени до вместимости.
		now := time.Now()
		elapsed := now.Sub(b.last).Seconds()
		b.tokens += elapsed * rate
		if b.tokens > capacity {
			b.tokens = capacity
		}
		b.last = now

		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		}

		// Токена нет — ждём накопления, отпуская лок, чтобы не блокировать
		// другие горутины хоста на время ожидания.
		wait := time.Duration((1 - b.tokens) / rate * float64(time.Second))
		b.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		b.mu.Lock()
	}
}
