package entity_test

import (
	"testing"

	"github.com/example/webhookdispatcher/internal/application/entity"
)

func TestAttemptResultOutcome(t *testing.T) {
	cases := []struct {
		name   string
		result entity.AttemptResult
		want   entity.AttemptOutcome
	}{
		{"200", entity.AttemptResult{StatusCode: 200}, entity.OutcomeSuccess},
		{"204", entity.AttemptResult{StatusCode: 204}, entity.OutcomeSuccess},
		{"299", entity.AttemptResult{StatusCode: 299}, entity.OutcomeSuccess},
		{"301", entity.AttemptResult{StatusCode: 301}, entity.OutcomeFatal},
		{"400", entity.AttemptResult{StatusCode: 400}, entity.OutcomeFatal},
		{"404", entity.AttemptResult{StatusCode: 404}, entity.OutcomeFatal},
		{"410", entity.AttemptResult{StatusCode: 410}, entity.OutcomeFatal},
		{"429", entity.AttemptResult{StatusCode: 429}, entity.OutcomeRetryable},
		{"500", entity.AttemptResult{StatusCode: 500}, entity.OutcomeRetryable},
		{"503", entity.AttemptResult{StatusCode: 503}, entity.OutcomeRetryable},
		{"timeout", entity.AttemptResult{TimedOut: true}, entity.OutcomeRetryable},
		{"transport error", entity.AttemptResult{TransportError: "connection refused"}, entity.OutcomeRetryable},
		{"timeout after 400", entity.AttemptResult{StatusCode: 400, TimedOut: true}, entity.OutcomeRetryable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.result.Outcome(); got != tc.want {
				t.Fatalf("Outcome() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAttemptResultReason(t *testing.T) {
	if got := (entity.AttemptResult{TimedOut: true}).Reason(); got != "request timed out" {
		t.Fatalf("Reason() = %q", got)
	}
	if got := (entity.AttemptResult{TransportError: "dial error"}).Reason(); got != "dial error" {
		t.Fatalf("Reason() = %q", got)
	}
	if got := (entity.AttemptResult{StatusCode: 500}).Reason(); got == "" {
		t.Fatal("Reason() must not be empty")
	}
}
