package dream

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

// compactGoldenFixture builds a deterministic 30-hit ranked list.
func compactGoldenFixture() []RecallHit {
	kinds := []string{"lesson", "fact", "transcript"}
	hits := make([]RecallHit, 0, 30)
	for i := 0; i < 30; i++ {
		hits = append(hits, RecallHit{
			Kind:       kinds[i%len(kinds)],
			ID:         "hit-" + twoDigits(i),
			ProjectID:  "sila-cli",
			Timestamp:  "2026-11-06T10:00:00Z",
			Symptom:    strings.Repeat("symptom-"+twoDigits(i)+" ", 8),
			Cause:      strings.Repeat("cause detail "+twoDigits(i)+" ", 6),
			Fix:        strings.Repeat("fix detail "+twoDigits(i)+" ", 6),
			Resolution: strings.Repeat("full resolution body for testing token weight "+twoDigits(i)+" ", 4),
			SourceRef:  "sila lesson show hit-" + twoDigits(i),
			Tool:       "claude",
			RepoFiles:  []string{"internal/store/facts.go", "internal/dream/recall.go"},
			Grounded:   i%2 == 0,
			Score:      float64(100 - i),
		})
	}
	return hits
}

func updateGolden(t *testing.T) bool {
	t.Helper()
	return os.Getenv("SILA_UPDATE_GOLDEN") == "1"
}

func TestCompactGolden(t *testing.T) {
	fixture := compactGoldenFixture()
	compact := CompactHits(fixture, 0, 0)
	raw, err := json.MarshalIndent(compact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "compact", "compact.golden")
	if updateGolden(t) {
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden (run with SILA_UPDATE_GOLDEN=1): %v", err)
	}
	if string(raw)+"\n" != string(want) {
		t.Fatalf("compact golden drifted; run with SILA_UPDATE_GOLDEN=1")
	}

	full, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fullPath := filepath.Join("testdata", "compact", "full.golden")
	if updateGolden(t) {
		if err := os.WriteFile(fullPath, append(full, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	wantFull, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("missing full golden: %v", err)
	}
	if string(full)+"\n" != string(wantFull) {
		t.Fatalf("full golden drifted; run with SILA_UPDATE_GOLDEN=1")
	}
}

func TestCompactTokenMeasurement(t *testing.T) {
	fixture := compactGoldenFixture()
	compact := CompactHits(fixture, 0, 0)
	fullJSON, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fullTokens := store.EstimateTokens(len(fullJSON))
	if fullTokens == 0 || compact.Tokens == 0 {
		t.Fatalf("tokens full=%d compact=%d", fullTokens, compact.Tokens)
	}
	ratio := float64(compact.Tokens) / float64(fullTokens)
	t.Logf("compact=%d tokens full=%d tokens ratio=%.3f", compact.Tokens, fullTokens, ratio)
	if ratio > 0.25 {
		t.Fatalf("compact uses %.1f%% of full tokens, want at most 25%%", ratio*100)
	}
}
