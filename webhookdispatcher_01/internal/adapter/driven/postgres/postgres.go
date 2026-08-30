// Package postgres implements the storage driven ports on top of PostgreSQL.
// It is the only package that knows about pgx, and it translates every driver
// error into a domain error before returning.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed all:migrations
var migrationFS embed.FS

// SQLSTATE codes we translate into domain errors.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeCheckViolation      = "23514"
)

// NewPool opens a connection pool and verifies it can reach the database.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errs.Wrapf(err, "parse postgres dsn")
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errs.Wrapf(err, "open postgres pool")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, errs.Wrapf(err, "ping postgres")
	}
	return pool, nil
}

// Migrate applies every embedded migration that has not run yet. It is safe to
// call on every start: applied files are recorded in schema_migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return errs.Wrapf(translate(err), "create schema_migrations")
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return errs.Wrapf(err, "read migrations")
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // lexical order is the migration order

	for _, name := range names {
		var applied bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE name = $1)`, name,
		).Scan(&applied); err != nil {
			return errs.Wrapf(translate(err), "check migration %s", name)
		}
		if applied {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return errs.Wrapf(err, "read migration %s", name)
		}
		if err := withTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name)
			return err
		}); err != nil {
			return errs.Wrapf(translate(err), "apply migration %s", name)
		}
	}
	return nil
}

// withTx runs fn inside a transaction, rolling back on any error so a failed
// write never leaves a partial state behind.
func withTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		// Rollback after a successful Commit is a no-op.
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// translate maps a pgx/pgconn error onto the domain error vocabulary so that
// driver types never leak out of this package.
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errs.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case codeUniqueViolation:
			return errs.ErrAlreadyExists
		case codeForeignKeyViolation, codeCheckViolation:
			return errs.ErrInvalidInput
		}
	}
	return err
}
