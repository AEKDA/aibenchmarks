package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/example/webhookdispatcher/internal/adapter/driven/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// dsnEnv points at a throwaway PostgreSQL used by the integration tests.
const dsnEnv = "WD_TEST_POSTGRES_DSN"

// newTestPool connects to the test database, applies the migrations and
// truncates every table so each test starts from a clean slate. Tests are
// skipped when the database is unavailable or when -short is set, so that
// `go test -short ./...` needs no infrastructure.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping postgres integration test in -short mode")
	}
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("skipping postgres integration test: %s is not set", dsnEnv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("apply migrations: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE deliveries, events, subscriptions RESTART IDENTITY CASCADE`); err != nil {
		pool.Close()
		t.Fatalf("truncate tables: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
