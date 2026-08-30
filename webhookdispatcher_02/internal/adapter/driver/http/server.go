// Package http содержит входящий HTTP-адаптер.
package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Ограничения на чтение и запись защищают процесс от соединений, которые
// открыты, но не двигаются: без них медленный клиент удерживает сокет и
// горутину сколько угодно и задерживает завершение.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 1 << 20
)

// Routes собирает маршруты, доступные на текущем этапе.
func Routes() http.Handler {
	mux := http.NewServeMux()

	// Liveness намеренно не проверяет внешние зависимости: он отвечает на
	// вопрос «процесс жив», а не «процесс готов обслуживать».
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	return mux
}

// Server обслуживает HTTP-запросы и живёт ровно столько, сколько его контекст.
//
// Экземпляр одноразовый: повторный вызов Run возвращает ошибку.
type Server struct {
	srv             *http.Server
	log             *slog.Logger
	shutdownTimeout time.Duration

	ready     chan struct{}
	readyOnce sync.Once
	started   atomic.Bool

	mu   sync.Mutex
	addr string
}

// NewServer собирает сервер поверх переданного набора маршрутов.
func NewServer(addr string, shutdownTimeout time.Duration, log *slog.Logger, handler http.Handler) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           handler,
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    maxHeaderBytes,
			// Ошибки уровня соединения иначе уходят в stderr незаструктурированным
			// текстом мимо общего формата логов.
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError),
		},
		log:             log,
		shutdownTimeout: shutdownTimeout,
		ready:           make(chan struct{}),
		addr:            addr,
	}
}

// Ready закрывается, когда сервер принял слушающий сокет либо отказался
// стартовать: ожидающий не должен зависнуть на неудачном старте.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr возвращает фактический адрес прослушивания; до старта — сконфигурированный.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// ShutdownTimeout возвращает бюджет корректного завершения.
func (s *Server) ShutdownTimeout() time.Duration { return s.shutdownTimeout }

func (s *Server) setAddr(addr string) {
	s.mu.Lock()
	s.addr = addr
	s.mu.Unlock()
}

// Run слушает до отмены контекста и возвращается только после того, как сервер
// полностью остановлен.
func (s *Server) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("http server: Run called more than once")
	}
	defer s.readyOnce.Do(func() { close(s.ready) })

	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("http listen on %s: %w", s.srv.Addr, err)
	}
	s.setAddr(ln.Addr().String())
	s.readyOnce.Do(func() { close(s.ready) })

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", "addr", s.Addr())
		err := s.srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	// Контекст завершения отвязан от отменённого родителя: иначе Shutdown
	// прервётся мгновенно и оборвёт запросы, которые обязан дообработать.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	s.log.Info("http server shutting down")
	shutdownErr := s.srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		// Бюджет исчерпан: закрываем принудительно, иначе соединения и их
		// горутины переживут возврат из Run.
		s.log.Warn("graceful shutdown budget exceeded, closing connections",
			"timeout", s.shutdownTimeout)
		_ = s.srv.Close()
	}
	serveErr := <-errCh

	if shutdownErr != nil {
		return fmt.Errorf("http server shutdown: %w", shutdownErr)
	}
	if serveErr != nil {
		return fmt.Errorf("http server: %w", serveErr)
	}
	return nil
}
