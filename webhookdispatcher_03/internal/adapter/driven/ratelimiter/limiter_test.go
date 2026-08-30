package ratelimiter

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestAllow_Burst затем: пока есть токены (maxRPS всего) — Allow проходит мгновенно;
// следующий вынужден ждать и уважает отмену короткого контекста.
func TestAllow_Burst(t *testing.T) {
	const maxRPS = 3
	l := New(maxRPS)

	ctx := context.Background()
	// Пока токены не исчерпаны — все проходят синхронно.
	for i := 0; i < maxRPS; i++ {
		if err := l.Allow(ctx, "host-a"); err != nil {
			t.Fatalf("Allow #%d: %v", i, err)
		}
	}

	// Следующий запрос токена уже нет — должен заблокироваться.
	// Короткий отменяющий контекст: Allow обязан вернуться с ошибкой, а не висеть.
	cctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if err := l.Allow(cctx, "host-a"); err == nil {
		t.Fatal("Allow: want error (blocked), got nil")
	}
}

// TestAllow_PerHost изолирует лимиты по хостам: исчерпание одного хоста
// не влияет на другой.
func TestAllow_PerHost(t *testing.T) {
	const maxRPS = 2
	l := New(maxRPS)

	ctx := context.Background()
	// Исчерпаем лимит хоста A.
	if err := l.Allow(ctx, "host-a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Allow(ctx, "host-a"); err != nil {
		t.Fatal(err)
	}

	// Хост B имеет свой свежий bucket — должен пройти.
	if err := l.Allow(ctx, "host-b"); err != nil {
		t.Fatalf("host-b should have own bucket, got: %v", err)
	}
}

// TestAllow_Concurrent запускает конкурентные Allow на один хост;
// под -race проверяет отсутствие гонки на map и счётчиках токенов.
func TestAllow_Concurrent(t *testing.T) {
	const maxRPS = 1
	const goroutines = 50
	l := New(maxRPS)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_ = l.Allow(ctx, "host-a") // ошибка допустима, важна отсутствие гонки
		}()
	}
	wg.Wait()
}
