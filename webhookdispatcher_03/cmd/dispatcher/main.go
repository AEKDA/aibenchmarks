// Команда dispatcher собирает все слои и запускает http-сервер и воркеры.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"webhookdispatcher/internal/adapter/driven/httpsender"
	"webhookdispatcher/internal/adapter/driven/postgres"
	"webhookdispatcher/internal/adapter/driven/ratelimiter"
	handlerHTTP "webhookdispatcher/internal/adapter/driver/http"
	"webhookdispatcher/internal/adapter/driver/worker"
	"webhookdispatcher/internal/application/usecase"
	"webhookdispatcher/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config.Load: %v", err)
	}

	// Пул подключений создаётся лениво: реальный контакт с БД — при первом запросе.
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres.NewPool: %v", err)
	}
	defer pool.Close()

	// Репозитории (driven) → usecase (application) → handler/worker (driver).
	subs := postgres.NewSubscriptionRepo(pool)
	events := postgres.NewEventRepo(pool)
	deliveries := postgres.NewDeliveryRepo(pool)

	createSubscription := usecase.NewCreateSubscription(subs)
	publishEvent := usecase.NewPublishEvent(events, subs)
	getDelivery := usecase.NewGetDelivery(deliveries)

	claimNext := usecase.NewClaimNext(deliveries)
	processDelivery := usecase.NewProcessDelivery(
		deliveries, subs, httpsender.New(), ratelimiter.New(cfg.RateLimit),
	)

	// HTTP-сервер слушает в отдельной горутине.
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handlerHTTP.NewHandler(createSubscription, publishEvent, getDelivery)}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("http: ListenAndServe: %v", err)
		}
	}()

	// worker.Run блокирует main до отмены ctx (сигнал SIGINT/SIGTERM).
	_ = worker.Run(ctx, cfg.Workers, claimNext, processDelivery, cfg.PollInterval)

	// После остановки воркеров гасим http-сервер с таймаутом (graceful shutdown).
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http: Shutdown: %v", err)
	}
}
