package entity

import (
	"github.com/google/uuid"
	"time"
)

// Event публикуемое событие.
type Event struct {
	ID        uuid.UUID
	Type      string
	Payload   []byte
	CreatedAt time.Time
}
