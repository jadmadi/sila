package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanAndRepairStaleReferences(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/store", "a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"internal/store/kept.go", "a/dup.go", "b/dup.go"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	s, err := Open(filepath.Join(t.TempDir(), "hygiene.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	proj, err := s.EnsureProject(root)
	if err != nil {
		t.Fatal(err)
	}

	facts := []Fact{
		{ID: "fact_moved", ProjectID: proj.ID, Claim: "moved reference", RepoFiles: []string{"old/kept.go"}, FirstSeen: "2026-01-01T00:00:00Z", ReviewStatus: ReviewApproved},
		{ID: "fact_deleted", ProjectID: proj.ID, Claim: "deleted reference", RepoFiles: []string{"gone/none.go"}, FirstSeen: "2026-01-02T00:00:00Z", ReviewStatus: ReviewApproved},
		{ID: "fact_generated", ProjectID: proj.ID, Claim: "generated reference", RepoFiles: []string{"node_modules/pkg/missing.js"}, FirstSeen: "2026-01-03T00:00:00Z", ReviewStatus: ReviewApproved},
		{ID: "fact_wrong", ProjectID: proj.ID, Claim: "ambiguous reference", RepoFiles: []string{"old/dup.go"}, FirstSeen: "2026-01-04T00:00:00Z", ReviewStatus: ReviewApproved},
	}
	for _, f := range facts {
		if err := s.SaveFact(f); err != nil {
			t.Fatal(err)
		}
	}

	items, err := s.ScanStaleReferences(proj.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 stale facts, got %d: %+v", len(items), items)
	}
	classes := map[string]string{}
	for _, item := range items {
		classes[item.FactID] = item.Classification
	}
	if classes["fact_moved"] != StaleMoved {
		t.Errorf("moved class = %q", classes["fact_moved"])
	}
	if classes["fact_deleted"] != StaleDeleted {
		t.Errorf("deleted class = %q", classes["fact_deleted"])
	}
	if classes["fact_generated"] != StaleGenerated {
		t.Errorf("generated class = %q", classes["fact_generated"])
	}
	if classes["fact_wrong"] != StaleWrong {
		t.Errorf("wrong class = %q", classes["fact_wrong"])
	}

	dry, err := s.RepairStaleReferences(proj.ID, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.Repaired != 0 || dry.Archived != 0 || dry.Before != 4 {
		t.Fatalf("dry-run mutated: %+v", dry)
	}

	res, err := s.RepairStaleReferences(proj.ID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Repaired != 1 || res.Archived != 3 || res.After != 0 {
		t.Fatalf("repair result = %+v", res)
	}
	moved, err := s.FactByID("fact_moved")
	if err != nil || moved == nil {
		t.Fatal(err)
	}
	if !moved.FileRefsExist || len(moved.RepoFiles) != 1 || moved.RepoFiles[0] != "internal/store/kept.go" {
		t.Fatalf("moved fact not re-pointed: %+v", moved)
	}
	for _, id := range []string{"fact_deleted", "fact_generated", "fact_wrong"} {
		f, err := s.FactByID(id)
		if err != nil || f == nil {
			t.Fatalf("%s was deleted: %v", id, err)
		}
		if !f.IsStaleFlag {
			t.Errorf("%s was not archived", id)
		}
	}
	if audit, err := s.VerifyChain(""); err != nil || !audit.Intact {
		t.Fatalf("hygiene broke the chain: %v, %+v", err, audit)
	}
	remaining, err := s.StaleFacts(proj.ID, 0)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("archived facts still count as active stale: %v, %v", remaining, err)
	}
}
