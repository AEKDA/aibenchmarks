// Package errs содержит типизированные доменные ошибки.
//
// Значения объявлены здесь и только здесь: адаптеры транслируют ошибки своих
// технологий в эти значения, а HTTP-адаптер отображает их в коды состояния.
// Ни домен, ни driven-адаптеры не знают о транспортных кодах.
package errs

import "errors"

var (
	// ErrNotFound — запрошенный объект отсутствует.
	ErrNotFound = errors.New("not found")

	// ErrConflict — операция противоречит текущему состоянию объекта.
	ErrConflict = errors.New("conflict")

	// ErrValidation — входные данные не удовлетворяют доменным правилам.
	ErrValidation = errors.New("validation failed")

	// ErrInvalidTransition — запрошенный переход состояния недопустим.
	ErrInvalidTransition = errors.New("invalid state transition")

	// ErrBlockedTarget — целевой адрес запрещён политикой исходящих соединений.
	ErrBlockedTarget = errors.New("target blocked")
)
