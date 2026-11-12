package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/dream"
)

func TestCompactEnvelopeGolden(t *testing.T) {
	fixture := make([]dream.RecallHit, 0, 30)
	for i := 0; i < 30; i++ {
		fixture = append(fixture, dream.RecallHit{
			Kind: "lesson", ID: "hit-" + string(rune('a'+i%26)), Score: float64(100 - i),
			Symptom: "symptom title for compact envelope", Timestamp: "2026-11-06T10:00:00Z",
		})
	}
	compact := dream.CompactHits(fixture, 0, 0)
	envelope := map[string]any{
		"query": "fixture", "total": len(fixture),
		"hits": compact.Hits, "truncated": compact.Truncated, "tokens": compact.Tokens,
		"hint": "expand with id=<id>",
	}
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "compact", "envelope.golden")
	if os.Getenv("SILA_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden (run with SILA_UPDATE_GOLDEN=1): %v", err)
	}
	if string(raw)+"\n" != string(want) {
		t.Fatal("compact envelope golden drifted; run with SILA_UPDATE_GOLDEN=1")
	}
}
