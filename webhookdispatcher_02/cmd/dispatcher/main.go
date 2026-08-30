// Command dispatcher — точка входа сервиса доставки вебхуков.
//
// Это единственное место, где читается окружение и связываются между собой
// адаптеры: ниже по стеку компоненты получают готовые значения аргументами.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	httpadapter "github.com/aenigmma/webhookdispatcher/internal/adapter/driver/http"
	"github.com/aenigmma/webhookdispatcher/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(log); err != nil {
		log.Error("dispatcher stopped with error", "error", err)
		os.Exit(1)
	}
	log.Info("dispatcher stopped")
}

// runnable — общий контракт всего, что живёт дольше одного вызова: возвращается
// только после полной остановки.
type runnable interface {
	Run(ctx context.Context) error
}

func run(log *slog.Logger) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	components := []runnable{
		httpadapter.NewServer(cfg.HTTPAddr, cfg.ShutdownTimeout, log, httpadapter.Routes(log)),
	}

	group, groupCtx := errgroup.WithContext(ctx)
	for _, c := range components {
		group.Go(func() error { return c.Run(groupCtx) })
	}

	if err := group.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("component failed: %w", err)
	}
	return nil
}
