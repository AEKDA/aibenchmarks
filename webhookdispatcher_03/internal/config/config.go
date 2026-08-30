// Package config читает конфигурацию сервиса webhook-диспетчера из переменных окружения.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config конфигурация сервиса.
type Config struct {
	HTTPAddr     string        // адрес http-сервера
	DatabaseURL  string        // строка подключения к PostgreSQL
	Workers      int           // размер пула воркеров доставки
	RateLimit    int           // максимальное RPS на хост
	PollInterval time.Duration // пауза воркера при отсутствии задач
}

// Load собирает конфигурацию из ENV, применяя дефолты и валидируя значения.
// DATABASE_URL обязателен; WORKERS и RATE_LIMIT должны быть строго положительными.
func Load() (Config, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return Config{}, fmt.Errorf("config.Load: переменная DATABASE_URL обязательна")
	}

	workers, err := intEnv("WORKERS", 5)
	if err != nil {
		return Config{}, fmt.Errorf("config.Load: %w", err)
	}
	if workers <= 0 {
		return Config{}, fmt.Errorf("config.Load: WORKERS должен быть > 0, got %d", workers)
	}

	rateLimit, err := intEnv("RATE_LIMIT", 10)
	if err != nil {
		return Config{}, fmt.Errorf("config.Load: %w", err)
	}
	if rateLimit <= 0 {
		return Config{}, fmt.Errorf("config.Load: RATE_LIMIT должен быть > 0, got %d", rateLimit)
	}

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	poll := os.Getenv("POLL_INTERVAL")
	interval := time.Second
	if poll != "" {
		interval, err = time.ParseDuration(poll)
		if err != nil {
			return Config{}, fmt.Errorf("config.Load: POLL_INTERVAL %q: %w", poll, err)
		}
	}

	return Config{
		HTTPAddr:     addr,
		DatabaseURL:  dbURL,
		Workers:      workers,
		RateLimit:    rateLimit,
		PollInterval: interval,
	}, nil
}

// intEnv читает целочисленную переменную окружения; пустое значение — дефолт.
func intEnv(key string, def int) (int, error) {
	s := os.Getenv(key)
	if s == "" {
		return def, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s=%q: %w", key, s, err)
	}
	return v, nil
}
