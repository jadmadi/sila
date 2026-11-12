package loaders

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type outcomeCase struct {
	Name       string `json:"name"`
	Prompt     string `json:"prompt"`
	GapSeconds int    `json:"gap_seconds"`
	Want       string `json:"want"`
}

func TestOutcomeFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "outcome", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []outcomeCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 5 {
		t.Fatalf("cases = %d", len(fixture.Cases))
	}
	for _, tc := range fixture.Cases {
		if got := ClassifyTurnOutcome(tc.Prompt, time.Duration(tc.GapSeconds)*time.Second); got != tc.Want {
			t.Fatalf("%s = %q, want %q", tc.Name, got, tc.Want)
		}
	}
}
