package http_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/aenigmma/webhookdispatcher/internal/adapter/driver/http"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// start поднимает сервер на свободном порту и возвращает его адрес и функцию
// остановки, дожидающуюся полного завершения Run.
func start(t *testing.T, handler http.Handler, shutdownTimeout time.Duration) (string, func() error) {
	t.Helper()

	srv := httpadapter.NewServer("127.0.0.1:0", shutdownTimeout, discardLogger(), handler)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	select {
	case <-srv.Ready():
	case err := <-done:
		cancel()
		t.Fatalf("сервер не поднялся: %v", err)
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("сервер не поднялся за 2s")
	}

	return "http://" + srv.Addr(), func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("Run не вернулся за 5s после отмены контекста")
			return nil
		}
	}
}

func TestHealthzRespondsOK(t *testing.T) {
	base, stop := start(t, httpadapter.Routes(), time.Second)

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("запрос healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("статус = %d, ожидалось 200", resp.StatusCode)
	}

	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, ожидалось application/json", got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("чтение тела healthz: %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("тело = %q, ожидалось {\"status\":\"ok\"}", body)
	}

	if err := stop(); err != nil {
		t.Errorf("Run вернул ошибку: %v", err)
	}
}

// TestRunStopsOnContextCancel проверяет требование graceful shutdown: Run
// возвращается только после полной остановки и не оставляет горутин.
func TestRunStopsOnContextCancel(t *testing.T) {
	before := runtime.NumGoroutine()

	_, stop := start(t, httpadapter.Routes(), time.Second)
	if err := stop(); err != nil {
		t.Fatalf("Run вернул ошибку: %v", err)
	}

	if leaked := waitGoroutines(before); leaked {
		t.Errorf("горутины не завершились: было %d, стало %d", before, runtime.NumGoroutine())
	}
}

// TestRunFinishesInFlightRequest проверяет, что начатый запрос дообрабатывается,
// а не обрывается отменой контекста.
func TestRunFinishesInFlightRequest(t *testing.T) {
	entered := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})

	base, stop := start(t, handler, 5*time.Second)

	type result struct {
		code int
		err  error
	}
	res := make(chan result, 1)
	go func() {
		resp, err := http.Get(base + "/slow")
		if err != nil {
			res <- result{err: err}
			return
		}
		defer resp.Body.Close()
		res <- result{code: resp.StatusCode}
	}()

	<-entered
	stopErr := make(chan error, 1)
	go func() { stopErr <- stop() }()

	got := <-res
	if got.err != nil {
		t.Fatalf("запрос оборван вместо дообработки: %v", got.err)
	}
	if got.code != http.StatusNoContent {
		t.Errorf("статус = %d, ожидалось 204", got.code)
	}
	if err := <-stopErr; err != nil {
		t.Errorf("Run вернул ошибку: %v", err)
	}
}

// TestRunReportsShutdownTimeout фиксирует, что превышение таймаута завершения
// не проходит молча: Run возвращает ошибку, а процесс — ненулевой код.
func TestRunReportsShutdownTimeout(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	base, stop := start(t, handler, 100*time.Millisecond)

	go func() {
		resp, err := http.Get(base + "/blocking")
		if err == nil {
			resp.Body.Close()
		}
	}()

	<-entered
	err := stop()
	close(release)

	if err == nil {
		t.Fatal("ожидалась ошибка превышенного таймаута завершения, получен nil")
	}
	if !strings.Contains(err.Error(), "shutdown") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
}

func waitGoroutines(before int) (leaked bool) {
	for range 50 {
		if runtime.NumGoroutine() <= before {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}
