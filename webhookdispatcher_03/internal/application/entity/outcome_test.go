package entity

import (
	"testing"
)

func TestOutcomeFromStatus(t *testing.T) {
	cases := []struct {
		code int
		want Outcome
	}{
		{200, OutcomeDelivered},
		{204, OutcomeDelivered},
		{201, OutcomeDelivered},
		{429, OutcomeRetry}, // 429 трактуется как retry (перегрузка)
		{400, OutcomeRetry},
		{404, OutcomeRetry},
		{500, OutcomeRetry},
		{503, OutcomeRetry},
	}
	for _, c := range cases {
		if got := OutcomeFromStatus(c.code); got != c.want {
			t.Errorf("OutcomeFromStatus(%d)=%v want %v", c.code, got, c.want)
		}
	}
}
