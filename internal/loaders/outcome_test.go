package loaders

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyTurnOutcome(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		gap    time.Duration
		want   string
	}{
		{"correction", "no wait, that broke it", time.Minute, OutcomeCorrected},
		{"success", "Perfect, ship it", time.Minute, OutcomeSuccess},
		{"neutral", "Now do the next task", time.Minute, OutcomeNeutral},
		{"long gap", "thanks", 25 * time.Hour, OutcomeNeutral},
		{"empty", "", time.Minute, OutcomeNeutral},
		{"uppercase", "NO WAIT THAT BROKE IT", time.Minute, OutcomeCorrected},
		{"corrected wins", "thanks but that broke it", time.Minute, OutcomeCorrected},
	}
	for _, tc := range cases {
		if got := ClassifyTurnOutcome(tc.prompt, tc.gap); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClassifyIgnoresAfter200Runes(t *testing.T) {
	prompt := strings.Repeat("x", 250) + " no wait that broke it"
	if got := ClassifyTurnOutcome(prompt, time.Minute); got != OutcomeNeutral {
		t.Fatalf("got %q", got)
	}
}
