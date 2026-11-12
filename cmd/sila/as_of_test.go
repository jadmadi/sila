package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func asOfCLIStore(t *testing.T) (string, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "asof-cli.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB.Exec(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status)
		VALUES ('cli-fact','cli-p','historical claim','grounded','[]','[]','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','approved')`); err != nil {
		t.Fatal(err)
	}
	return dbPath, "cli-p"
}

func TestCLIAsOfJSON(t *testing.T) {
	dbPath, project := asOfCLIStore(t)
	asOfRequest = "2026-03-01"
	t.Cleanup(func() { asOfRequest = "" })
	out := captureStdout(t, func() {
		runAsOfOutput(render.New(nil, true), dbPath, project, true, true)
	})
	var snapshot struct {
		AsOf       string `json:"as_of"`
		Historical bool   `json:"historical"`
		Facts      []struct {
			ID string `json:"id"`
		} `json:"facts"`
	}
	if err := json.Unmarshal([]byte(out), &snapshot); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !snapshot.Historical || snapshot.AsOf != "2026-03-01T00:00:00Z" || len(snapshot.Facts) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestCLIAsOfInvalidAndDefault(t *testing.T) {
	dbPath, project := asOfCLIStore(t)
	if _, err := historicalSnapshot(dbPath, project, "garbage"); err == nil || !strings.Contains(err.Error(), "as_of must be") {
		t.Fatalf("err = %v", err)
	}
	asOfRequest = ""
	t.Cleanup(func() { asOfRequest = "" })
	if asOfEnabled() {
		t.Fatal("empty as-of reported enabled")
	}
	future, clamped, err := store.ParseAsOf("2027-01-01", time.Now().UTC())
	if err != nil || !clamped || future.After(time.Now().UTC()) {
		t.Fatalf("future = %s clamped=%v err=%v", future, clamped, err)
	}
}
