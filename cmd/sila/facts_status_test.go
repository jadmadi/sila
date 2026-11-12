package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestFactsStatusJSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "s.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, file_refs_exist, repo_files, tools, session_ref,
		first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash)
		VALUES ('v1','p','claim-v1','grounded',1,'["a.go"]','[]','s',?,?,?,'hash-v1',?), ('u1','p','claim-u1','grounded',0,'[]','[]','s',?,?,?,'hash-u1',?)`,
		now, now, now, store.GenesisHash, now, now, now, store.GenesisHash); err != nil {
		t.Fatal(err)
	}
	st.Close()
	out := captureStdout(t, func() {
		runFacts(render.New(os.Stdout, true), dbPath, "p", true, false, false, "", []string{"status"})
	})
	var counts store.FactCounts
	if err := json.Unmarshal([]byte(out), &counts); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if counts.Verified != 1 || counts.Unverified != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}
