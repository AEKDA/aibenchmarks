package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/aenigmma/webhookdispatcher/internal/config"
)

func env(pairs map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := pairs[key]
		return v, ok
	}
}

func base() map[string]string {
	return map[string]string{"WHD_DATABASE_URL": "postgres://localhost/whd"}
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(env(base()))
	if err != nil {
		t.Fatalf("ожидалась валидная конфигурация: %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, ожидалось \":8080\"", cfg.HTTPAddr)
	}
	if cfg.SendTimeout != 5*time.Second {
		t.Errorf("SendTimeout = %s, ожидалось 5s", cfg.SendTimeout)
	}
	if cfg.LeaseTimeout <= cfg.SendTimeout {
		t.Errorf("значения по умолчанию нарушают инвариант аренды: lease=%s send=%s",
			cfg.LeaseTimeout, cfg.SendTimeout)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %s, ожидалось 15s", cfg.ShutdownTimeout)
	}
	if cfg.WorkerCount != 8 {
		t.Errorf("WorkerCount = %d, ожидалось 8", cfg.WorkerCount)
	}
}

func TestLoadRejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(map[string]string)
		wantHas string
	}{
		{
			name:    "отсутствует обязательная переменная",
			mutate:  func(e map[string]string) { delete(e, "WHD_DATABASE_URL") },
			wantHas: "WHD_DATABASE_URL is required",
		},
		{
			name:    "пустая обязательная переменная",
			mutate:  func(e map[string]string) { e["WHD_DATABASE_URL"] = "" },
			wantHas: "WHD_DATABASE_URL is required",
		},
		{
			name: "нарушен инвариант аренды",
			mutate: func(e map[string]string) {
				e["WHD_LEASE_TIMEOUT"] = "3s"
				e["WHD_SEND_TIMEOUT"] = "5s"
			},
			wantHas: "must be strictly greater than",
		},
		{
			name:    "неразбираемая длительность",
			mutate:  func(e map[string]string) { e["WHD_SEND_TIMEOUT"] = "5" },
			wantHas: "cannot parse duration",
		},
		{
			name:    "неположительная длительность",
			mutate:  func(e map[string]string) { e["WHD_SHUTDOWN_TIMEOUT"] = "0s" },
			wantHas: "must be positive",
		},
		{
			name:    "неразбираемое число воркеров",
			mutate:  func(e map[string]string) { e["WHD_WORKER_COUNT"] = "many" },
			wantHas: "cannot parse integer",
		},
		{
			name:    "неположительное число воркеров",
			mutate:  func(e map[string]string) { e["WHD_WORKER_COUNT"] = "0" },
			wantHas: "must be positive",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := base()
			tc.mutate(e)

			_, err := config.Load(env(e))
			if err == nil {
				t.Fatal("ожидалась ошибка, конфигурация принята")
			}
			if !strings.Contains(err.Error(), tc.wantHas) {
				t.Errorf("сообщение не содержит %q: %s", tc.wantHas, err)
			}
		})
	}
}

// TestLoadReportsAllProblems фиксирует, что подъём сервиса не превращается в
// поиск следующей незаданной переменной по одной за запуск.
func TestLoadReportsAllProblems(t *testing.T) {
	t.Parallel()

	e := base()
	delete(e, "WHD_DATABASE_URL")
	e["WHD_SEND_TIMEOUT"] = "nope"

	_, err := config.Load(env(e))
	if err == nil {
		t.Fatal("ожидалась ошибка, конфигурация принята")
	}
	for _, want := range []string{"WHD_DATABASE_URL is required", "cannot parse duration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("сообщение не содержит %q: %s", want, err)
		}
	}
}

func TestLoadAcceptsOverrides(t *testing.T) {
	t.Parallel()

	e := base()
	e["WHD_HTTP_ADDR"] = "127.0.0.1:9090"
	e["WHD_SEND_TIMEOUT"] = "2s"
	e["WHD_LEASE_TIMEOUT"] = "10s"
	e["WHD_SHUTDOWN_TIMEOUT"] = "7s"
	e["WHD_WORKER_COUNT"] = "4"

	cfg, err := config.Load(env(e))
	if err != nil {
		t.Fatalf("ожидалась валидная конфигурация: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9090" || cfg.SendTimeout != 2*time.Second ||
		cfg.LeaseTimeout != 10*time.Second || cfg.ShutdownTimeout != 7*time.Second ||
		cfg.WorkerCount != 4 {
		t.Errorf("переопределения не применены: %+v", cfg)
	}
}
