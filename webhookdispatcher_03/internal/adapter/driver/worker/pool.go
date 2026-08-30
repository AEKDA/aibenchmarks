// Package worker — фоновый пул горутин (inbound adapter driver):
// опрашивает хранилище через ClaimNext и доставляет задачи через ProcessDelivery.
package worker

import (
	"context"
	"sync"
	"time"

	"webhookdispatcher/internal/application/usecase"
)

// Run запускает workers горутин, которые забирают готовые задачи и доставляют их.
// Блокирует до отмены ctx (graceful shutdown) и возвращается после того, как все
// горутины завершили текущую работу. Утечек горутин не оставляет.
func Run(ctx context.Context, workers int, claim *usecase.ClaimNext, process *usecase.ProcessDelivery, pollInterval time.Duration) error {
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deliveryLoop(ctx, claim, process, pollInterval)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

// deliveryLoop это цикл одной горутины пула: забирает пачку задач, доставляет
// каждую, при отсутствии задач или ошибке ждёт pollInterval.
func deliveryLoop(ctx context.Context, claim *usecase.ClaimNext, process *usecase.ProcessDelivery, pollInterval time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		tasks, err := claim.Invoke(ctx, time.Now(), 10)
		if err != nil {
			if ctx.Err() != nil {
				return // отмена во время забора — выходим сразу
			}
			sleepOrExit(ctx, pollInterval)
			continue
		}
		if len(tasks) == 0 {
			sleepOrExit(ctx, pollInterval)
			continue
		}
		for _, t := range tasks {
			_ = process.Invoke(deliveryCtx(ctx), t)
		}
	}
}

// deliveryCtx отвязывает контекст от отмены родителя, но сохраняет значения
// (trace id и пр.). При shutdown мы не прерываем уже начатую доставку:
// отмена могла бы оборвать HTTP-запрос подписчику на полуслове, а исход не
// сохранился бы — доставка осталась бы «зависшей» в SENDING. Поэтому текущие
// доставки доводятся до конца, а новые не забираются.
func deliveryCtx(ctx context.Context) context.Context {
	return context.WithoutCancel(ctx)
}

// sleepOrExit ждёт pollInterval, но немедленно прерывается по отмене ctx,
// чтобы shutdown не задерживался на время сна.
func sleepOrExit(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
