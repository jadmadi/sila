package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

// TestWorkspaceParityAcrossSurfaces reproduces the live defect: from the
// workspace root, prime/salam resolved the umbrella project while goals
// resolved the cli child. All four surfaces must agree.
func TestWorkspaceParityAcrossSurfaces(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(root, "cli")
	if err := os.MkdirAll(filepath.Join(cli, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	goalDir := filepath.Join(cli, "goals", "demo")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goalBody := "# Goal: Demo\n\n## Dependencies & Execution Order\n- **Sequence**: 1\n- **Tier**: roadmap\n"
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sila.code-workspace"), []byte(`{"folders":[{"path":"cli"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "parity.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	identity, err := store.ResolveWorkspaceIdentity(root)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}

	oldWD, _ := os.Getwd()
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWD)

	brief, err := dream.BuildPrimeBriefWithOptions(st, dream.PrimeOptions{
		ProjectDir:         root,
		MaxFacts:           1,
		DisableCodebaseMap: true,
	})
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	salamBrief, err := salam.BuildBrief(st, root, "")
	if err != nil {
		t.Fatalf("salam: %v", err)
	}
	goalsDir, err := goals.ResolveProjectDir(st, "")
	if err != nil {
		t.Fatalf("goals: %v", err)
	}

	if identity.ProjectID != "sila-cli" {
		t.Fatalf("resolver project = %q, want sila-cli", identity.ProjectID)
	}
	if brief.ProjectID != identity.ProjectID {
		t.Fatalf("prime project = %q, want %q", brief.ProjectID, identity.ProjectID)
	}
	if salamBrief.Project != identity.ProjectID {
		t.Fatalf("salam project = %q, want %q", salamBrief.Project, identity.ProjectID)
	}
	if goalsDir != cli {
		t.Fatalf("goals dir = %q, want %q", goalsDir, cli)
	}

	explicit, err := dream.BuildPrimeBriefWithOptions(st, dream.PrimeOptions{
		ProjectSel:         cli,
		ProjectDir:         root,
		MaxFacts:           1,
		DisableCodebaseMap: true,
	})
	if err != nil {
		t.Fatalf("explicit prime: %v", err)
	}
	if explicit.ProjectID != "sila-cli" {
		t.Fatalf("explicit --project = %q, want sila-cli", explicit.ProjectID)
	}
}
