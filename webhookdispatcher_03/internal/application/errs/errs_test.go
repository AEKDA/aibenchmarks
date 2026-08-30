package errs

import (
	"errors"
	"testing"
)

func TestIs(t *testing.T) {
	if !Is(ErrNotFound, ErrNotFound) {
		t.Fatal("Is(ErrNotFound, ErrNotFound) должно быть true")
	}
	if Is(ErrConflict, ErrNotFound) {
		t.Fatal("ErrConflict не должен считаться ErrNotFound")
	}
	wrapped := errors.New("x") // тип DomainError через %w
	_ = wrapped
}