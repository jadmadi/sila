package dream

import (
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestExtractSessionLessonCandidates(t *testing.T) {
	messages := []store.StoredMessage{
		{
			Role: "assistant",
			Content: `We investigated the issue with SQLite lock contention:
Problem: database locked error during concurrent cloud mailbox drain
Cause: concurrent goroutines opening read-write connections without WAL mode
Fix: set PRAGMA journal_mode=WAL and serialize writes with a mutex in internal/cloud/client.go
`,
			TS: time.Now().UTC().Format(time.RFC3339),
		},
		{
			Role: "assistant",
			Content: `The failure was caused by missing cgo flags in build.sh. Fixed by adding CGO_ENABLED=0 to build environment.
`,
			TS: time.Now().UTC().Format(time.RFC3339),
		},
		{
			Role: "assistant",
			Content: `Fixed memory leak on long-running watch mode by closing sqlite transaction rows.
`,
			TS: time.Now().UTC().Format(time.RFC3339),
		},
	}

	existing := []store.Fact{
		{
			Claim: store.FormatLesson("already known issue", "some cause", "some fix", "sqlite"),
		},
	}

	candidates := ExtractSessionLessonCandidates(messages, existing)
	if len(candidates) == 0 {
		t.Fatalf("expected extracted candidates, got none")
	}

	foundWAL := false
	for _, c := range candidates {
		if c.Symptom != "" && c.Fix != "" {
			if len(c.Files) > 0 {
				for _, f := range c.Files {
					if f == "internal/cloud/client.go" {
						foundWAL = true
					}
				}
			}
		}
	}

	if !foundWAL {
		t.Errorf("expected candidate with file internal/cloud/client.go, got %+v", candidates)
	}
}

func TestExtractSessionLessonCandidates_DuplicateFiltering(t *testing.T) {
	sym := "database locked error during concurrent drain"
	cause := "concurrent goroutines opening read-write connections"
	fix := "set PRAGMA journal_mode=WAL"

	messages := []store.StoredMessage{
		{
			Role: "assistant",
			Content: `Symptom: ` + sym + `
Cause: ` + cause + `
Fix: ` + fix + `
`,
		},
	}

	existing := []store.Fact{
		{
			Claim: store.FormatLesson(sym, cause, fix, "sqlite"),
		},
	}

	candidates := ExtractSessionLessonCandidates(messages, existing)
	if len(candidates) != 0 {
		t.Fatalf("expected duplicates to be filtered out, got %d candidates: %+v", len(candidates), candidates)
	}
}
