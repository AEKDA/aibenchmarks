package entity

import "github.com/google/uuid"

// Subscription подписчик на события: точки доставки и правила.
type Subscription struct {
	ID     uuid.UUID
	URL    string
	Secret string
	Events []string
	MaxRPS int
}

// Matches сообщает, подписан ли подписчик на событие данного типа.
func (s Subscription) Matches(eventType string) bool {
	for _, e := range s.Events {
		if e == eventType {
			return true
		}
	}
	return false
}
