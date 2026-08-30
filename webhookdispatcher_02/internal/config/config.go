// Package config читает и валидирует конфигурацию сервиса.
//
// Пакет вызывается только из composition root: ни адаптеры, ни usecase'ы не
// обращаются к окружению самостоятельно.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix — общий префикс всех переменных окружения сервиса.
const EnvPrefix = "WHD_"

// Config — полная конфигурация процесса.
type Config struct {
	// DatabaseURL — строка подключения к PostgreSQL. Обязательна.
	DatabaseURL string

	// HTTPAddr — адрес, на котором слушает HTTP-сервер.
	HTTPAddr string

	// SendTimeout — таймаут одной исходящей отправки вебхука.
	SendTimeout time.Duration

	// LeaseTimeout — предельная длительность аренды попытки доставки.
	// Строго больше SendTimeout, иначе реапер отберёт задачу у живого воркера.
	LeaseTimeout time.Duration

	// ShutdownTimeout — предельное время корректного завершения процесса.
	ShutdownTimeout time.Duration

	// WorkerCount — размер пула воркеров доставки.
	WorkerCount int
}

var defaults = Config{
	HTTPAddr:        ":8080",
	SendTimeout:     5 * time.Second,
	LeaseTimeout:    30 * time.Second,
	ShutdownTimeout: 15 * time.Second,
	WorkerCount:     8,
}

// Load читает конфигурацию из окружения и проверяет её целостность.
//
// Ошибки собираются целиком: подъём сервиса не должен превращаться в поиск
// следующей незаданной переменной по одной за запуск.
func Load(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}

	cfg := defaults
	var problems []string

	if v, ok := lookup(EnvPrefix + "DATABASE_URL"); ok && v != "" {
		cfg.DatabaseURL = v
	} else {
		problems = append(problems, EnvPrefix+"DATABASE_URL is required")
	}

	if v, ok := lookup(EnvPrefix + "HTTP_ADDR"); ok && v != "" {
		cfg.HTTPAddr = v
	}

	for _, d := range []struct {
		name   string
		target *time.Duration
	}{
		{"SEND_TIMEOUT", &cfg.SendTimeout},
		{"LEASE_TIMEOUT", &cfg.LeaseTimeout},
		{"SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout},
	} {
		raw, ok := lookup(EnvPrefix + d.name)
		if !ok || raw == "" {
			continue
		}
		parsed, err := time.ParseDuration(raw)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s%s: cannot parse duration %q", EnvPrefix, d.name, raw))
		case parsed <= 0:
			problems = append(problems, fmt.Sprintf("%s%s: must be positive, got %s", EnvPrefix, d.name, parsed))
		default:
			*d.target = parsed
		}
	}

	if raw, ok := lookup(EnvPrefix + "WORKER_COUNT"); ok && raw != "" {
		parsed, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%sWORKER_COUNT: cannot parse integer %q", EnvPrefix, raw))
		case parsed <= 0:
			problems = append(problems, fmt.Sprintf("%sWORKER_COUNT: must be positive, got %d", EnvPrefix, parsed))
		default:
			cfg.WorkerCount = parsed
		}
	}

	if cfg.LeaseTimeout <= cfg.SendTimeout {
		problems = append(problems, fmt.Sprintf(
			"%sLEASE_TIMEOUT (%s) must be strictly greater than %sSEND_TIMEOUT (%s)",
			EnvPrefix, cfg.LeaseTimeout, EnvPrefix, cfg.SendTimeout))
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return cfg, nil
}
