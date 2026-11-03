package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestProjectsMergeDryRunCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "merge_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	keep, err := st.EnsureProject(filepath.Join(tmp, "keep"))
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	drop, err := st.EnsureProject(filepath.Join(tmp, "drop"))
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("", "opencode", "1", []store.LoadMessage{{
		ProjectID:   drop.ID,
		ContentHash: "merge-cli-msg",
		TS:          "2026-09-20T08:00:00Z",
		Role:        "assistant",
		Content:     "dropped project data",
		Tool:        "opencode",
	}}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runProjects(ui, dbPath, false, true, true, false, []string{"merge", keep.ID, drop.ID})
	})

	var res struct {
		DryRun   bool   `json:"dry_run"`
		Keep     string `json:"keep"`
		Drop     string `json:"drop"`
		Messages int    `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("merge dry-run output is not JSON: %q: %v", out, err)
	}
	if !res.DryRun || res.Keep != keep.ID || res.Drop != drop.ID {
		t.Fatalf("payload = %+v, want dry run of %s into %s", res, drop.ID, keep.ID)
	}
	if res.Messages != 1 {
		t.Fatalf("messages = %d, want 1", res.Messages)
	}
}
