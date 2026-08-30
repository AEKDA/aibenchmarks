package entity

import (
	"testing"
)

func TestSubscriptionMatches(t *testing.T) {
	s := Subscription{Events: []string{"order.created", "order.cancelled"}}
	cases := []struct {
		eventType string
		want      bool
	}{
		{"order.created", true},
		{"order.cancelled", true},
		{"user.created", false},
	}
	for _, c := range cases {
		if got := s.Matches(c.eventType); got != c.want {
			t.Errorf("Matches(%q)=%v want %v", c.eventType, got, c.want)
		}
	}
}