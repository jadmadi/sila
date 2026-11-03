package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestHealAppliesAndIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	dbPath := filepath.Join(tmp, "heal_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	// Dry run writes nothing and returns the plan.
	planOut := captureStdout(t, func() {
		runHeal(ui, dbPath, repo, true, true, nil)
	})
	var plan struct {
		Automatic int `json:"automatic"`
		Judgment  int `json:"judgment"`
		Steps     []struct {
			ID        string `json:"id"`
			Automatic bool   `json:"automatic"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(planOut), &plan); err != nil {
		t.Fatalf("dry-run output is not JSON: %q: %v", planOut, err)
	}
	if plan.Automatic == 0 {
		t.Fatalf("plan = %+v, want at least one automatic step before healing", plan)
	}

	// Heal applies the automatic steps.
	applyOut := captureStdout(t, func() {
		runHeal(ui, dbPath, repo, false, true, nil)
	})
	var report struct {
		Automatic int      `json:"automatic_applied"`
		Judgment  int      `json:"remaining_judgment"`
		Errors    []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(applyOut), &report); err != nil {
		t.Fatalf("apply output is not JSON: %q: %v", applyOut, err)
	}
	if report.Automatic == 0 {
		t.Fatalf("apply report = %+v, want at least one applied automatic step", report)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("apply errors: %v", report.Errors)
	}

	// The repository is now current; only judgment steps remain.
	afterOut := captureStdout(t, func() {
		runHeal(ui, dbPath, repo, true, true, nil)
	})
	var after struct {
		Automatic int `json:"automatic"`
		Judgment  int `json:"judgment"`
	}
	if err := json.Unmarshal([]byte(afterOut), &after); err != nil {
		t.Fatalf("after output is not JSON: %q: %v", afterOut, err)
	}
	if after.Automatic != 0 {
		detail := ""
		if st, err := store.Open(dbPath); err == nil {
			rep := onboard.Check(st, repo, "", false)
			for _, item := range rep.Items {
				if item.Status != "pass" {
					detail += "\n  " + item.Name + ": " + item.Status + " · " + item.Detail + " fix=" + item.Fix
				}
			}
			st.Close()
		}
		t.Fatalf("after heal, automatic steps = %d, want 0; non-pass items:%s", after.Automatic, detail)
	}
	if after.Judgment == 0 {
		t.Fatal("judgment steps should remain after heal (handoff, memos, lessons)")
	}
}
