package postgres

import (
	"errors"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/errs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestTranslate(t *testing.T) {
	other := errors.New("connection reset")
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"nil stays nil", nil, nil},
		{"no rows", pgx.ErrNoRows, errs.ErrNotFound},
		{"wrapped no rows", errs.Wrapf(pgx.ErrNoRows, "select"), errs.ErrNotFound},
		{"unique violation", &pgconn.PgError{Code: codeUniqueViolation}, errs.ErrAlreadyExists},
		{"foreign key violation", &pgconn.PgError{Code: codeForeignKeyViolation}, errs.ErrInvalidInput},
		{"check violation", &pgconn.PgError{Code: codeCheckViolation}, errs.ErrInvalidInput},
		{"unknown pg error", &pgconn.PgError{Code: "08006"}, nil},
		{"unrelated error", other, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := translate(tc.in)
			if tc.want != nil {
				if !errors.Is(got, tc.want) {
					t.Fatalf("translate(%v) = %v, want %v", tc.in, got, tc.want)
				}
				return
			}
			if tc.in == nil {
				if got != nil {
					t.Fatalf("translate(nil) = %v, want nil", got)
				}
				return
			}
			// Errors we do not recognise must pass through untouched.
			if !errors.Is(got, tc.in) {
				t.Fatalf("translate(%v) = %v, want the original error", tc.in, got)
			}
			for _, sentinel := range []error{errs.ErrNotFound, errs.ErrAlreadyExists, errs.ErrInvalidInput, errs.ErrConflict} {
				if errors.Is(got, sentinel) {
					t.Fatalf("translate(%v) must not map onto %v", tc.in, sentinel)
				}
			}
		})
	}
}
