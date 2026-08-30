package errs

import "errors"

// domainError доменная ошибка с фиксированным видом.
type domainError struct {
	kind string
}

func (e domainError) Error() string { return e.kind }

// Is позволяет errors.Is находить типизированные доменные ошибки.
func (e domainError) Is(target error) bool {
	t, ok := target.(domainError)
	return ok && t.kind == e.kind
}

// Предопределённые доменные ошибки.
var (
	ErrNotFound = domainError{kind: "not found"}
	ErrConflict = domainError{kind: "conflict"}
	ErrInvalid  = domainError{kind: "invalid"}
)

// Is сообщает, является ли err искомой доменной ошибкой kind.
func Is(err, target error) bool {
	return errors.Is(err, target)
}