package dream

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestCompileRules(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "rules.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Insert a memo
	_, err = st.InsertMemo("test-proj", "Always use pure-Go SQLite modernc", "user", "", []string{"store/store.go"})
	if err != nil {
		t.Fatal(err)
	}

	// Insert an approved invariant directive (authorized by a human decision)
	err = st.UpsertFact(store.Fact{
		ID:            "inv-1",
		ProjectID:     "test-proj",
		Claim:         "Never write into another tool database files",
		Kind:          "invariant",
		Confidence:    "invariant",
		Corroboration: "invariant",
		FileRefsExist: true,
		Verified:      true,
		ReviewStatus:  store.ReviewApproved,
		SessionRef:    "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert an inferred directive candidate: must never reach the exported
	// hard constraints.
	err = st.UpsertFact(store.Fact{
		ID:            "cand-1",
		ProjectID:     "test-proj",
		Claim:         "Rule: Prefer purple teapots in every build pipeline",
		Kind:          "invariant",
		Confidence:    "candidate",
		Corroboration: "candidate",
		ReviewStatus:  store.ReviewCandidate,
		SessionRef:    "session-2",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert a claim that merely references a real file: approved and grounded,
	// but never corroborated or distilled. This is the case that used to be
	// exported under a "Verified Facts" heading, planting raw session text in a
	// rule file agents treat as a hard constraint.
	err = st.UpsertFact(store.Fact{
		ID:            "claim-1",
		ProjectID:     "test-proj",
		Claim:         "Both records are now in place, here is the action list",
		RepoFiles:     []string{"cmd/sila/main.go"},
		FileRefsExist: true,
		Verified:      true,
		ReviewStatus:  store.ReviewApproved,
		SessionRef:    "session-3",
		DistillStatus: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Insert a handoff
	err = st.InsertHandoff(store.Handoff{
		ProjectID: "test-proj",
		Summary:   "Refactored search indexing",
		OpenItems: []string{"Add benchmark tests"},
		Decisions: []string{"FTS5 BM25 ranking enabled"},
		Agent:     "claude-code",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test CLAUDE.md
	claudeDoc, err := CompileRules(st, "test-proj", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(claudeDoc, "CLAUDE.md") || !strings.Contains(claudeDoc, "Always use pure-Go SQLite") || !strings.Contains(claudeDoc, "Never write into another tool database files") || !strings.Contains(claudeDoc, "Refactored search indexing") {
		t.Fatalf("unexpected claude output: %s", claudeDoc)
	}
	if strings.Contains(claudeDoc, "purple teapots") {
		t.Fatalf("inferred candidate leaked into hard constraints: %s", claudeDoc)
	}

	// Test Cursor .cursorrules
	cursorDoc, err := CompileRules(st, "test-proj", "cursor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cursorDoc, "Cursor Project Rules") || !strings.Contains(cursorDoc, "Always use pure-Go SQLite") {
		t.Fatalf("unexpected cursor output: %s", cursorDoc)
	}

	// Test Windsurf
	windsurfDoc, err := CompileRules(st, "test-proj", "windsurf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(windsurfDoc, "Windsurf Project Rules") {
		t.Fatalf("unexpected windsurf output: %s", windsurfDoc)
	}

	// A file-referenced claim must ship under its own heading in every target,
	// never under a heading that calls it a fact.
	for _, tc := range []struct {
		name, doc string
	}{
		{"claude", claudeDoc},
		{"cursor", cursorDoc},
		{"windsurf", windsurfDoc},
	} {
		if !strings.Contains(tc.doc, "action list") {
			t.Fatalf("%s: the file-referenced claim vanished from the export:\n%s", tc.name, tc.doc)
		}
		if !strings.Contains(tc.doc, "File-Referenced Claims") {
			t.Fatalf("%s: claim is present without its honest heading:\n%s", tc.name, tc.doc)
		}
		factIdx := strings.Index(tc.doc, "action list")
		headingIdx := strings.Index(tc.doc, "File-Referenced Claims")
		if factIdx < headingIdx {
			t.Fatalf("%s: claim text appears before the claims heading — it landed in a facts section:\n%s", tc.name, tc.doc)
		}
	}
}
