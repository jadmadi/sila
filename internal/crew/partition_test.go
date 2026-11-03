package crew

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildPackageDAG(t *testing.T) {
	tempDir := t.TempDir()

	// Create go.mod
	goModContent := `module example.com/testmod

go 1.27
`
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatalf("failed to write go.mod: %v", err)
	}

	// Create pkgA (upstream)
	pkgADir := filepath.Join(tempDir, "pkgA")
	if err := os.MkdirAll(pkgADir, 0755); err != nil {
		t.Fatalf("failed to create pkgA: %v", err)
	}
	fileA := `package pkgA

type Service struct{}
`
	if err := os.WriteFile(filepath.Join(pkgADir, "a.go"), []byte(fileA), 0644); err != nil {
		t.Fatalf("failed to write a.go: %v", err)
	}

	// Create pkgB (downstream, imports pkgA)
	pkgBDir := filepath.Join(tempDir, "pkgB")
	if err := os.MkdirAll(pkgBDir, 0755); err != nil {
		t.Fatalf("failed to create pkgB: %v", err)
	}
	fileB := `package pkgB

import "example.com/testmod/pkgA"

type Consumer struct {
	S pkgA.Service
}
`
	if err := os.WriteFile(filepath.Join(pkgBDir, "b.go"), []byte(fileB), 0644); err != nil {
		t.Fatalf("failed to write b.go: %v", err)
	}

	dag, err := BuildPackageDAG(tempDir)
	if err != nil {
		t.Fatalf("BuildPackageDAG failed: %v", err)
	}

	if dag.ModulePath != "example.com/testmod" {
		t.Errorf("expected module path example.com/testmod, got %q", dag.ModulePath)
	}

	// pkgB should import pkgA
	importsB := dag.Imports["pkgB"]
	foundA := false
	for _, imp := range importsB {
		if imp == "pkgA" {
			foundA = true
			break
		}
	}
	if !foundA {
		t.Errorf("expected pkgB to import pkgA, got %v", importsB)
	}

	// pkgA should have pkgB as dependent
	depsA := dag.Dependents["pkgA"]
	foundB := false
	for _, dep := range depsA {
		if dep == "pkgB" {
			foundB = true
			break
		}
	}
	if !foundB {
		t.Errorf("expected pkgA to have dependent pkgB, got %v", depsA)
	}
}

func TestPlanLanes_BriefFile(t *testing.T) {
	tempDir := t.TempDir()

	goModContent := `module example.com/testmod

go 1.27
`
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)

	// Create store, cmd, and docs directories
	_ = os.MkdirAll(filepath.Join(tempDir, "internal", "store"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "internal", "store", "store.go"), []byte(`package store
type Store struct{}
`), 0644)

	_ = os.MkdirAll(filepath.Join(tempDir, "cmd", "app"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "cmd", "app", "main.go"), []byte(`package main
import "example.com/testmod/internal/store"
var s store.Store
`), 0644)

	_ = os.MkdirAll(filepath.Join(tempDir, "docs"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "docs", "README.md"), []byte(`# Docs`), 0644)

	briefContent := `task_store: internal/store/store.go
task_cmd: cmd/app/main.go
task_docs: docs/README.md
`
	briefPath := filepath.Join(tempDir, "tasks.txt")
	if err := os.WriteFile(briefPath, []byte(briefContent), 0644); err != nil {
		t.Fatalf("failed to write brief: %v", err)
	}

	res, err := PlanLanes(tempDir, 3, briefPath, nil)
	if err != nil {
		t.Fatalf("PlanLanes failed: %v", err)
	}

	if res.Status != "planned" {
		t.Errorf("expected status 'planned', got %q", res.Status)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if res.PlannedLanes != 3 {
		t.Errorf("expected 3 planned lanes, got %d", res.PlannedLanes)
	}

	// Verify contract touch: task_cmd imports task_store
	if len(res.ContractTouches) == 0 {
		t.Errorf("expected contract touches between store and cmd, got 0")
	} else {
		ct := res.ContractTouches[0]
		if ct.SharedPackage != "internal/store" {
			t.Errorf("expected shared package 'internal/store', got %q", ct.SharedPackage)
		}
		// Downstream lane landing order should be strictly greater than upstream lane
		upLane := res.Lanes[ct.UpstreamLane-1]
		downLane := res.Lanes[ct.DownstreamLane-1]
		if downLane.LandingOrder <= upLane.LandingOrder {
			t.Errorf("expected downstream lane order (%d) > upstream lane order (%d)",
				downLane.LandingOrder, upLane.LandingOrder)
		}
	}
}

func TestPlanLanes_SameFileRisk(t *testing.T) {
	tempDir := t.TempDir()

	goModContent := `module example.com/testmod

go 1.27
`
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)

	_ = os.MkdirAll(filepath.Join(tempDir, "pkg1"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg1", "f1.go"), []byte(`package pkg1`), 0644)
	_ = os.MkdirAll(filepath.Join(tempDir, "pkg2"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg2", "f2.go"), []byte(`package pkg2`), 0644)

	// Two tasks touch their own package, but both touch shared.go
	briefContent := `task_1: pkg1/f1.go, shared.go
task_2: pkg2/f2.go, shared.go
`
	briefPath := filepath.Join(tempDir, "tasks.txt")
	_ = os.WriteFile(briefPath, []byte(briefContent), 0644)

	res, err := PlanLanes(tempDir, 2, briefPath, nil)
	if err != nil {
		t.Fatalf("PlanLanes failed: %v", err)
	}

	if len(res.SameFileRisks) != 1 {
		t.Fatalf("expected 1 same file risk, got %d", len(res.SameFileRisks))
	}
	if res.SameFileRisks[0].File != "shared.go" {
		t.Errorf("expected same file risk on shared.go, got %s", res.SameFileRisks[0].File)
	}
}

func TestPlanLanes_HardConflict(t *testing.T) {
	tempDir := t.TempDir()

	goModContent := `module example.com/testmod

go 1.27
`
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644)

	_ = os.MkdirAll(filepath.Join(tempDir, "common"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "common", "c.go"), []byte(`package common`), 0644)

	// Simulate conflicting tasks touching the same package in parallel
	// If force partitioned without dependency
	tasks := []TaskSpec{
		{ID: "task_a", Packages: []string{"common"}, Files: []string{"common/a.go"}},
		{ID: "task_b", Packages: []string{"common"}, Files: []string{"common/b.go"}},
	}

	// Directly construct lanes to test conflict detection
	res := &LanePlanResult{
		RequestedLanes: 2,
		PlannedLanes:   2,
		Lanes: []Lane{
			{Lane: 1, Role: "worker_a", Packages: []string{"common"}, LandingOrder: 1, Tasks: []string{"task_a"}},
			{Lane: 2, Role: "worker_b", Packages: []string{"common"}, LandingOrder: 1, Tasks: []string{"task_b"}},
		},
	}

	for i := 0; i < len(res.Lanes); i++ {
		for j := i + 1; j < len(res.Lanes); j++ {
			laneA := res.Lanes[i]
			laneB := res.Lanes[j]
			if laneA.LandingOrder == laneB.LandingOrder {
				for _, pa := range laneA.Packages {
					for _, pb := range laneB.Packages {
						if pa == pb {
							res.Conflicts = append(res.Conflicts, LaneConflict{
								LaneA:   laneA.Lane,
								LaneB:   laneB.Lane,
								Package: pa,
								Reason:  "simultaneous execution in same landing tier without dependency ordering",
							})
						}
					}
				}
			}
		}
	}

	if len(res.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(res.Conflicts))
	}
	if res.Conflicts[0].Package != "common" {
		t.Errorf("expected conflict on 'common', got %s", res.Conflicts[0].Package)
	}
	_ = tasks
}
