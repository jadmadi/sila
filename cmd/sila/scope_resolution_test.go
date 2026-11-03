package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestResolveCommandProjectScope(t *testing.T) {
	tmp := t.TempDir()
	repoRoot := filepath.Join(tmp, "oc-plugins")
	subDir := filepath.Join(repoRoot, "packages", "opencode-sila")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "goals"), 0755); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", repoRoot, "init").Run()

	dbPath := filepath.Join(tmp, "scope.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rootProj, err := st.EnsureProject(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	gotFromRoot, err := resolveCommandProjectScope(st, "", repoRoot)
	if err != nil {
		t.Fatalf("resolveCommandProjectScope from root failed: %v", err)
	}
	gotFromSub, err := resolveCommandProjectScope(st, "", subDir)
	if err != nil {
		t.Fatalf("resolveCommandProjectScope from subdir failed: %v", err)
	}
	if gotFromRoot != rootProj.ID || gotFromSub != rootProj.ID {
		t.Fatalf("expected identical project_id %q across root (%q) and subdir (%q)", rootProj.ID, gotFromRoot, gotFromSub)
	}

	// Explicit --project override must be respected
	gotWildcard, err := resolveCommandProjectScope(st, "*", subDir)
	if err != nil || gotWildcard != "*" {
		t.Fatalf("expected '*' override, got %q (err=%v)", gotWildcard, err)
	}
}

func TestFormatFactsVerifyIncidents(t *testing.T) {
	ui := render.New(os.Stdout, true)
	res := &store.ChainAuditResult{
		Intact:        true,
		IncidentCount: 15,
		Incidents: []store.FactIncident{
			{
				IncidentType: "digest_mismatch",
				FactID:       "fact-123",
				StoredHash:   "1111111111111111",
				ComputedHash: "2222222222222222",
				CreatedAt:    "2026-10-01T00:00:00Z",
				Details:      "historical test incident",
			},
		},
	}

	// 1. Default intact run (verbose=false, limit=0) prints concise single-line summary
	var conciseBuf bytes.Buffer
	formatFactsVerifyIncidents(&conciseBuf, ui, res, false, 0)
	conciseOut := conciseBuf.String()
	if !strings.Contains(conciseOut, "15 historical record(s) archived (active ledger intact; pass --verbose or --limit=N to inspect)") {
		t.Fatalf("expected concise incident summary on intact default run, got:\n%s", conciseOut)
	}
	if strings.Contains(conciseOut, "fact-123") {
		t.Fatalf("expected individual incident rows to be omitted on intact default run, got:\n%s", conciseOut)
	}

	// 2. Verbose run prints full incident row details
	var verboseBuf bytes.Buffer
	formatFactsVerifyIncidents(&verboseBuf, ui, res, true, 0)
	verboseOut := verboseBuf.String()
	if !strings.Contains(verboseOut, "fact-123") || !strings.Contains(verboseOut, "historical test incident") {
		t.Fatalf("expected full incident row in --verbose output, got:\n%s", verboseOut)
	}

	// 3. Non-intact ledger prints full incident row details even when verbose=false
	brokenRes := *res
	brokenRes.Intact = false
	var brokenBuf bytes.Buffer
	formatFactsVerifyIncidents(&brokenBuf, ui, &brokenRes, false, 0)
	if !strings.Contains(brokenBuf.String(), "fact-123") {
		t.Fatalf("expected full incident row when Intact=false, got:\n%s", brokenBuf.String())
	}
}
