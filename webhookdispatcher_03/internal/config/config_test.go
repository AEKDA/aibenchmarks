package config

import (
	"testing"
	"time"
)

// TestLoadDefaults проверяет дефолты при пустых (не заданных) ENV-параметрах.
func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://host/db")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("WORKERS", "")
	t.Setenv("RATE_LIMIT", "")
	t.Setenv("POLL_INTERVAL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DatabaseURL != "postgres://host/db" {
		t.Errorf("DatabaseURL = %q, want построку подключения", cfg.DatabaseURL)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.Workers != 5 {
		t.Errorf("Workers = %d, want 5", cfg.Workers)
	}
	if cfg.RateLimit != 10 {
		t.Errorf("RateLimit = %d, want 10", cfg.RateLimit)
	}
	if cfg.PollInterval != time.Second {
		t.Errorf("PollInterval = %v, want 1s", cfg.PollInterval)
	}
}

// TestLoadCustom проверяет парсинг явно заданных значений.
func TestLoadCustom(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://prod/db")
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("WORKERS", "12")
	t.Setenv("RATE_LIMIT", "50")
	t.Setenv("POLL_INTERVAL", "250ms")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if cfg.Workers != 12 {
		t.Errorf("Workers = %d, want 12", cfg.Workers)
	}
	if cfg.RateLimit != 50 {
		t.Errorf("RateLimit = %d, want 50", cfg.RateLimit)
	}
	if cfg.PollInterval != 250*time.Millisecond {
		t.Errorf("PollInterval = %v, want 250ms", cfg.PollInterval)
	}
}

// TestLoadRequiresDatabaseURL проверяет обязательность DATABASE_URL.
func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load: ожидалась ошибка при пустом DATABASE_URL")
	}
}

// TestLoadInvalidValues проверяет ошибки на некорректных/неположительных значениях.
func TestLoadInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		key  string
		val  string
	}{
		{"workers zero", "WORKERS", "0"},
		{"workers non-numeric", "WORKERS", "abc"},
		{"rate limit zero", "RATE_LIMIT", "0"},
		{"rate limit negative", "RATE_LIMIT", "-1"},
		{"rate limit non-numeric", "RATE_LIMIT", "x"},
		{"bad poll interval", "POLL_INTERVAL", "notaduration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://host/db")
			t.Setenv(tc.key, tc.val)
			if _, err := Load(); err == nil {
				t.Errorf("%s=%q: ожидалась ошибка", tc.key, tc.val)
			}
		})
	}
}
