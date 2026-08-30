// Package clock implements the Clock driven port with the system clock.
package clock

import (
	"context"
	"time"

	"github.com/example/webhookdispatcher/internal/application/ports"
)

// System reads the wall clock in UTC.
type System struct{}

// New builds the adapter.
func New() System { return System{} }

var _ ports.Clock = System{}

// Now returns the current UTC time.
func (System) Now(context.Context) time.Time { return time.Now().UTC() }
