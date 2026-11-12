package loaders

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyTurnOutcomeUnder1ms(t *testing.T) {
	if raceEnabled {
		t.Skip("latency budget excludes race instrumentation")
	}
	prompt := strings.Repeat("deployment ", 40) + " no wait, that broke it"
	start := time.Now()
	const runs = 1000
	for i := 0; i < runs; i++ {
		_ = ClassifyTurnOutcome(prompt, time.Minute)
	}
	perMessage := time.Since(start) / runs
	t.Logf("classifier cost = %s per message", perMessage)
	if perMessage > time.Millisecond {
		t.Fatalf("classifier cost %s exceeds 1ms", perMessage)
	}
}
