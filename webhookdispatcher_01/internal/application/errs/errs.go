// Package errs defines the typed domain errors shared by the application layer.
//
// Adapters translate their infrastructure errors into these sentinels, and the
// inbound HTTP adapter maps them back to status codes. Wrapping is done with
// fmt.Errorf and %w so errors.Is keeps working through any number of layers.
package errs

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound reports that a requested aggregate does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict reports a state conflict, such as a forbidden state transition.
	ErrConflict = errors.New("conflict")
	// ErrInvalidInput reports that the caller supplied invalid data.
	ErrInvalidInput = errors.New("invalid input")
	// ErrAlreadyExists reports that a uniqueness constraint is already taken.
	ErrAlreadyExists = errors.New("already exists")
)

// Wrapf annotates err with a formatted message while preserving the error
// chain, so errors.Is against the sentinels above still matches.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(fmt.Sprintf(format, args...)+": %w", err)
}

// Invalidf builds an ErrInvalidInput carrying a caller-facing reason.
func Invalidf(format string, args ...any) error {
	return Wrapf(ErrInvalidInput, format, args...)
}

// Conflictf builds an ErrConflict carrying a caller-facing reason.
func Conflictf(format string, args ...any) error {
	return Wrapf(ErrConflict, format, args...)
}

// NotFoundf builds an ErrNotFound carrying a caller-facing reason.
func NotFoundf(format string, args ...any) error {
	return Wrapf(ErrNotFound, format, args...)
}
