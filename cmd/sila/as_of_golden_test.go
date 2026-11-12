package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func cliAsOfFixture(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "asofg.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	insertFact := func(id, firstSeen, status, supersedes string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status, supersedes_id)
			VALUES (?, 'g', ?, 'grounded', '[]', '[]', '', ?, ?, ?, ?)`, id, "claim "+id, firstSeen, firstSeen, status, supersedes); err != nil {
			t.Fatal(err)
		}
	}
	insertFact("f-old", "2025-12-01T00:00:00Z", "approved", "")
	insertFact("f-new", "2026-07-01T00:00:00Z", "approved", "f-old")
	if _, err := st.DB.Exec(`INSERT INTO memory_records(id, project_id, fact_id, kind, title, summary, digest, envelope_json, state, created_at, updated_at)
		VALUES ('m1','g','f1','memo','t','memo summary','d','{}','current','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO memory_lifecycle(event_id, memory_id, project_id, action, from_state, to_state, authority, reason, actor, created_at, event_hash)
		VALUES ('e1','m1','g','archive','current','archived','user_request','other','test','2026-05-01T00:00:00Z','hash')`); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func TestCLIAsOfGoldens(t *testing.T) {
	dbPath := cliAsOfFixture(t)
	for _, date := range []string{"2025-12-15", "2026-03-01", "2026-06-01", "2026-08-01"} {
		asOfRequest = date
		out := captureStdout(t, func() {
			runAsOfOutput(render.New(nil, true), dbPath, "g", true, true)
		})
		path := filepath.Join("testdata", "as_of", date+".golden")
		if os.Getenv("SILA_UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(path, []byte(out+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("missing golden: %v", err)
		}
		if strings.TrimSpace(out) != strings.TrimSpace(string(want)) {
			t.Fatalf("%s golden drifted", date)
		}
	}
	asOfRequest = ""
}
