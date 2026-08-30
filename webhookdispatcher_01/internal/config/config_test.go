package config_test

import (
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/config"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{"WD_POSTGRES_DSN": "postgres://x"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	def := config.Default()
	if cfg.HTTPAddr != def.HTTPAddr || cfg.WorkerPoolSize != def.WorkerPoolSize ||
		cfg.PollInterval != def.PollInterval || cfg.BatchSize != def.BatchSize ||
		cfg.StaleThreshold != def.StaleThreshold || cfg.SendTimeout != def.SendTimeout ||
		cfg.UserAgent != def.UserAgent {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := config.Load(envFrom(map[string]string{
		"WD_POSTGRES_DSN":     "postgres://db",
		"WD_HTTP_ADDR":        ":9999",
		"WD_WORKER_POOL_SIZE": "3",
		"WD_BATCH_SIZE":       "7",
		"WD_POLL_INTERVAL":    "250ms",
		"WD_STALE_THRESHOLD":  "2m",
		"WD_SEND_TIMEOUT":     "3s",
		"WD_USER_AGENT":       "custom/9",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PostgresDSN != "postgres://db" || cfg.HTTPAddr != ":9999" ||
		cfg.WorkerPoolSize != 3 || cfg.BatchSize != 7 ||
		cfg.PollInterval != 250*time.Millisecond || cfg.StaleThreshold != 2*time.Minute ||
		cfg.SendTimeout != 3*time.Second || cfg.UserAgent != "custom/9" {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	cases := []map[string]string{
		{},
		{"WD_POSTGRES_DSN": "d", "WD_WORKER_POOL_SIZE": "0"},
		{"WD_POSTGRES_DSN": "d", "WD_WORKER_POOL_SIZE": "many"},
		{"WD_POSTGRES_DSN": "d", "WD_POLL_INTERVAL": "-1s"},
		{"WD_POSTGRES_DSN": "d", "WD_SEND_TIMEOUT": "soon"},
	}
	for i, env := range cases {
		if _, err := config.Load(envFrom(env)); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}
