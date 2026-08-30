package errs_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/example/webhookdispatcher/internal/application/errs"
)

func TestWrapfPreservesChain(t *testing.T) {
	wrapped := errs.Wrapf(errs.ErrNotFound, "load delivery %s", "abc")
	if !errors.Is(wrapped, errs.ErrNotFound) {
		t.Fatalf("errors.Is(ErrNotFound) = false for %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "load delivery abc") {
		t.Fatalf("message lost context: %q", wrapped.Error())
	}
}

func TestWrapfNestsRepeatedly(t *testing.T) {
	inner := errs.Wrapf(errs.ErrConflict, "transition")
	outer := errs.Wrapf(inner, "process delivery")
	if !errors.Is(outer, errs.ErrConflict) {
		t.Fatalf("errors.Is(ErrConflict) = false for %v", outer)
	}
	if errors.Is(outer, errs.ErrNotFound) {
		t.Fatal("unrelated sentinel matched")
	}
}

func TestWrapfNilStaysNil(t *testing.T) {
	if got := errs.Wrapf(nil, "noop"); got != nil {
		t.Fatalf("Wrapf(nil) = %v, want nil", got)
	}
}

func TestConstructors(t *testing.T) {
	cases := []struct {
		err      error
		sentinel error
	}{
		{errs.Invalidf("bad %s", "url"), errs.ErrInvalidInput},
		{errs.Conflictf("bad transition"), errs.ErrConflict},
		{errs.NotFoundf("no delivery"), errs.ErrNotFound},
	}
	for _, tc := range cases {
		if !errors.Is(tc.err, tc.sentinel) {
			t.Fatalf("%v does not match sentinel %v", tc.err, tc.sentinel)
		}
	}
}
