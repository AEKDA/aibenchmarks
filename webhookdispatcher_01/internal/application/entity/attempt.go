package entity

// AttemptOutcome is the domain-level verdict on one delivery attempt.
type AttemptOutcome int

const (
	// OutcomeSuccess means the subscriber accepted the delivery.
	OutcomeSuccess AttemptOutcome = iota
	// OutcomeRetryable means the attempt may succeed later.
	OutcomeRetryable
	// OutcomeFatal means retrying cannot help; the delivery is dead-lettered.
	OutcomeFatal
)

// String renders the outcome for logs and test failures.
func (o AttemptOutcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "SUCCESS"
	case OutcomeRetryable:
		return "RETRYABLE"
	case OutcomeFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// AttemptResult is what the outbound sender reports back to the domain. It
// deliberately carries no transport types so the domain stays free of net/http.
type AttemptResult struct {
	// StatusCode is the subscriber's HTTP status code, or 0 when no response
	// was received.
	StatusCode int
	// TimedOut reports that the attempt exceeded its deadline.
	TimedOut bool
	// TransportError describes a connection-level failure, empty when none.
	TransportError string
}

// Outcome classifies the attempt: 2xx succeeds; 429, 5xx, timeouts and
// transport failures are retryable; every other response is fatal.
func (r AttemptResult) Outcome() AttemptOutcome {
	if r.TimedOut || r.TransportError != "" {
		return OutcomeRetryable
	}
	switch {
	case r.StatusCode >= 200 && r.StatusCode <= 299:
		return OutcomeSuccess
	case r.StatusCode == 429:
		return OutcomeRetryable
	case r.StatusCode >= 500 && r.StatusCode <= 599:
		return OutcomeRetryable
	default:
		return OutcomeFatal
	}
}

// Reason renders a short, log-safe description of a non-successful attempt.
func (r AttemptResult) Reason() string {
	switch {
	case r.TimedOut:
		return "request timed out"
	case r.TransportError != "":
		return r.TransportError
	default:
		return "unexpected response status"
	}
}
