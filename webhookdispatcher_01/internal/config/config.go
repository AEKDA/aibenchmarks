// Package config reads the service configuration from the process environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds every knob the composition root needs to wire the service.
type Config struct {
	// PostgresDSN is the PostgreSQL connection string. Required.
	PostgresDSN string
	// HTTPAddr is the listen address of the inbound REST API.
	HTTPAddr string
	// WorkerPoolSize is the number of concurrent delivery workers.
	WorkerPoolSize int
	// PollInterval is how often workers look for ready deliveries.
	PollInterval time.Duration
	// BatchSize is how many deliveries a single poll claims at most.
	BatchSize int
	// StaleThreshold is how long a delivery may sit in SENDING before it is
	// released back to RETRYING by the reaper.
	StaleThreshold time.Duration
	// ReleaseInterval is how often the stale-delivery reaper runs.
	ReleaseInterval time.Duration
	// SendTimeout bounds a single outbound delivery attempt.
	SendTimeout time.Duration
	// ShutdownTimeout bounds graceful shutdown of the HTTP server and workers.
	ShutdownTimeout time.Duration
	// UserAgent identifies this service to subscribers.
	UserAgent string
}

// Default returns the configuration used when the environment overrides nothing.
func Default() Config {
	return Config{
		HTTPAddr:        ":8080",
		WorkerPoolSize:  8,
		PollInterval:    time.Second,
		BatchSize:       50,
		StaleThreshold:  time.Minute,
		ReleaseInterval: 30 * time.Second,
		SendTimeout:     5 * time.Second,
		ShutdownTimeout: 15 * time.Second,
		UserAgent:       "webhookdispatcher/1.0",
	}
}

// Load builds a Config from the environment, falling back to Default for every
// key that is unset. It returns an error when a value is present but unusable.
func Load(getenv func(string) string) (Config, error) {
	cfg := Default()
	var err error

	cfg.PostgresDSN = str(getenv, "WD_POSTGRES_DSN", cfg.PostgresDSN)
	cfg.HTTPAddr = str(getenv, "WD_HTTP_ADDR", cfg.HTTPAddr)
	if cfg.WorkerPoolSize, err = positiveInt(getenv, "WD_WORKER_POOL_SIZE", cfg.WorkerPoolSize); err != nil {
		return Config{}, err
	}
	if cfg.BatchSize, err = positiveInt(getenv, "WD_BATCH_SIZE", cfg.BatchSize); err != nil {
		return Config{}, err
	}
	for _, d := range []struct {
		key string
		dst *time.Duration
	}{
		{"WD_POLL_INTERVAL", &cfg.PollInterval},
		{"WD_STALE_THRESHOLD", &cfg.StaleThreshold},
		{"WD_RELEASE_INTERVAL", &cfg.ReleaseInterval},
		{"WD_SEND_TIMEOUT", &cfg.SendTimeout},
		{"WD_SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
	} {
		if *d.dst, err = positiveDuration(getenv, d.key, *d.dst); err != nil {
			return Config{}, err
		}
	}
	cfg.UserAgent = str(getenv, "WD_USER_AGENT", cfg.UserAgent)

	if cfg.PostgresDSN == "" {
		return Config{}, fmt.Errorf("config: WD_POSTGRES_DSN is required")
	}
	return cfg, nil
}

// LoadFromOS reads the configuration from the real process environment.
func LoadFromOS() (Config, error) { return Load(os.Getenv) }

func str(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

func positiveInt(getenv func(string) string, key string, fallback int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s is not an integer: %w", key, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("config: %s must be positive, got %d", key, v)
	}
	return v, nil
}

func positiveDuration(getenv func(string) string, key string, fallback time.Duration) (time.Duration, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s is not a duration: %w", key, err)
	}
	if v <= 0 {
		return 0, fmt.Errorf("config: %s must be positive, got %s", key, v)
	}
	return v, nil
}
