package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

func TestContinuityProjectIdentityParity(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dbPath := filepath.Join(tmp, "continuity.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmp, "workspace-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Register with ID that differs from directory basename "workspace-repo"
	canonicalID := "canonical-proj-id"
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, canonicalID, repoDir); err != nil {
		t.Fatal(err)
	}

	// Change working directory to repoDir
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chdir(origWD)
	}()

	ui := render.New(os.Stdout, true)

	// 1. Record memo using empty project (resolves current directory)
	outMemo := captureStdout(t, func() {
		runMemo(ui, dbPath, "", "architect", "", "", memoOptions{jsonOut: true}, []string{"Core architecture invariant: parity verified"})
	})
	var memoFact store.Fact
	if err := json.Unmarshal([]byte(outMemo), &memoFact); err != nil {
		t.Fatalf("runMemo output not JSON: %s: %v", outMemo, err)
	}
	if memoFact.ProjectID != canonicalID {
		t.Fatalf("runMemo recorded under project %q, want %q", memoFact.ProjectID, canonicalID)
	}

	// 2. Record handoff using empty project
	outHandoff := captureStdout(t, func() {
		runHandoff(ui, dbPath, "", "main", "antigravity", "Open item 1", "Decision 1", "", true, false, []string{"Session handoff for canonical project"})
	})
	var handoffObj store.Handoff
	if err := json.Unmarshal([]byte(outHandoff), &handoffObj); err != nil {
		t.Fatalf("runHandoff output not JSON: %s: %v", outHandoff, err)
	}
	if handoffObj.ProjectID != canonicalID {
		t.Fatalf("runHandoff recorded under project %q, want %q", handoffObj.ProjectID, canonicalID)
	}

	// 3. View handoff using empty project
	outViewHandoff := captureStdout(t, func() {
		runHandoff(ui, dbPath, "", "", "", "", "", "", true, false, nil)
	})
	var viewHandoff store.Handoff
	if err := json.Unmarshal([]byte(outViewHandoff), &viewHandoff); err != nil {
		t.Fatalf("runHandoff view output not JSON: %s: %v", outViewHandoff, err)
	}
	if viewHandoff.ProjectID != canonicalID {
		t.Fatalf("view handoff project %q, want %q", viewHandoff.ProjectID, canonicalID)
	}
	if viewHandoff.Summary != "Session handoff for canonical project" {
		t.Fatalf("view handoff summary %q, want %q", viewHandoff.Summary, "Session handoff for canonical project")
	}

	// 4. Run salam: must use canonicalID and see the recorded handoff
	outSalam := captureStdout(t, func() {
		runSalam(ui, dbPath, "", false, true, nil)
	})
	var brief salam.SalamBrief
	if err := json.Unmarshal([]byte(outSalam), &brief); err != nil {
		t.Fatalf("salam output not JSON: %s: %v", outSalam, err)
	}
	if brief.Project != canonicalID {
		t.Fatalf("salam project %q, want %q", brief.Project, canonicalID)
	}
	if brief.LastHandoff == nil || brief.LastHandoff.Summary != "Session handoff for canonical project" {
		t.Fatalf("salam did not see expected handoff: %+v", brief.LastHandoff)
	}

	// 5. Run prime: must use canonicalID and see both handoff and memo fact
	outPrime := captureStdout(t, func() {
		runPrime(ui, dbPath, "", true, false, 5, "")
	})
	var primeSummary dream.PrimeSummary
	if err := json.Unmarshal([]byte(outPrime), &primeSummary); err != nil {
		t.Fatalf("prime output not JSON: %s: %v", outPrime, err)
	}
	if primeSummary.ProjectID != canonicalID {
		t.Fatalf("prime project %q, want %q", primeSummary.ProjectID, canonicalID)
	}
	if primeSummary.ActiveHandoff == nil || primeSummary.ActiveHandoff.Summary != "Session handoff for canonical project" {
		t.Fatalf("prime did not see expected active handoff: %+v", primeSummary.ActiveHandoff)
	}
	if len(primeSummary.TopFacts) == 0 || !strings.Contains(primeSummary.TopFacts[0].Claim, "Core architecture invariant: parity verified") {
		t.Fatalf("prime did not see expected fact: %+v", primeSummary.TopFacts)
	}

	// 6. Test subdirectory inside repo: moving into a sub-folder still sees canonicalID
	subDir := filepath.Join(repoDir, "cli")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(subDir); err != nil {
		t.Fatal(err)
	}

	outSubPrime := captureStdout(t, func() {
		runPrime(ui, dbPath, "", true, false, 5, "")
	})
	var subSummary dream.PrimeSummary
	if err := json.Unmarshal([]byte(outSubPrime), &subSummary); err != nil {
		t.Fatalf("sub prime output not JSON: %s: %v", outSubPrime, err)
	}
	if subSummary.ProjectID != canonicalID {
		t.Fatalf("subdirectory prime project %q, want %q", subSummary.ProjectID, canonicalID)
	}

	// 7. Colliding basename in a completely different outside directory: must NOT adopt canonicalID
	outsideDir := filepath.Join(tmp, "other", "workspace-repo")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(outsideDir); err != nil {
		t.Fatal(err)
	}

	outOutsidePrime := captureStdout(t, func() {
		runPrime(ui, dbPath, "", true, false, 5, "")
	})
	var outsideSummary dream.PrimeSummary
	if err := json.Unmarshal([]byte(outOutsidePrime), &outsideSummary); err != nil {
		t.Fatalf("outside prime output not JSON: %s: %v", outOutsidePrime, err)
	}
	if outsideSummary.ProjectID == canonicalID {
		t.Fatalf("outside colliding basename dir must not adopt %q", canonicalID)
	}
	if outsideSummary.ProjectID != "workspace-repo" {
		t.Fatalf("outside project %q, want derived %q", outsideSummary.ProjectID, "workspace-repo")
	}
	if outsideSummary.ActiveHandoff != nil {
		t.Fatalf("outside project must not see inside handoff: %+v", outsideSummary.ActiveHandoff)
	}
}

func TestContinuityExplicitSelectorRejection(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "rejection.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	repoDir1 := filepath.Join(tmp, "dir1", "common-app")
	repoDir2 := filepath.Join(tmp, "dir2", "common-app")
	if err := os.MkdirAll(repoDir1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoDir2, 0755); err != nil {
		t.Fatal(err)
	}

	// Register two projects with distinct IDs but same directory basename "common-app"
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "app-one", repoDir1); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO projects (id, path, created_at) VALUES (?, ?, datetime('now'))`, "app-two", repoDir2); err != nil {
		t.Fatal(err)
	}

	// 1. Unknown selector must fail in salam and prime
	_, err = salam.BuildBrief(st, "", "completely-unknown")
	if err == nil || !strings.Contains(err.Error(), "no registered project matches") {
		t.Fatalf("expected 'no registered project matches' error in salam, got %v", err)
	}

	_, err = dream.BuildPrimeBrief(st, "completely-unknown", "", 5, 0)
	if err == nil || !strings.Contains(err.Error(), "no registered project matches") {
		t.Fatalf("expected 'no registered project matches' error in prime, got %v", err)
	}

	// 2. Ambiguous selector "common-app" must fail in salam and prime
	_, err = salam.BuildBrief(st, "", "common-app")
	if err == nil || !strings.Contains(err.Error(), "is ambiguous") {
		t.Fatalf("expected 'is ambiguous' error in salam, got %v", err)
	}

	_, err = dream.BuildPrimeBrief(st, "common-app", "", 5, 0)
	if err == nil || !strings.Contains(err.Error(), "is ambiguous") {
		t.Fatalf("expected 'is ambiguous' error in prime, got %v", err)
	}
}
