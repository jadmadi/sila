package store

import (
	"path/filepath"
	"testing"
)

func TestPruneProjectsKeepsChainAndOtherProjects(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "prune.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, f := range []Fact{
		{ID: "k1", ProjectID: "keep", Claim: "keep one", SessionRef: "s1"},
		{ID: "d1", ProjectID: "dead", Claim: "dead one", SessionRef: "s2"},
		{ID: "k2", ProjectID: "keep", Claim: "keep two", SessionRef: "s1"},
	} {
		if err := s.SaveFact(f); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PruneProjects([]string{"no-project"}); err == nil {
		t.Fatal("expected refusal to prune no-project")
	}
	if err := s.PruneProjects([]string{"dead"}); err != nil {
		t.Fatalf("PruneProjects: %v", err)
	}
	audit, err := s.VerifyChain("")
	if err != nil || !audit.Intact || audit.TotalBlocks != 2 {
		t.Fatalf("chain broken after prune: err=%v audit=%+v", err, audit)
	}
	if f, _ := s.FactByID("d1"); f != nil {
		t.Fatalf("dead project fact survived: %+v", f)
	}
	if f, _ := s.FactByID("k2"); f == nil {
		t.Fatal("kept project fact was removed")
	}
}

func TestIsTempPath(t *testing.T) {
	for path, want := range map[string]bool{
		"/tmp/x":       true,
		"/tmp":         false,
		"/var/tmp/a/b": true,
		"/home/u/proj": false,
		"/tmpfoo/x":    false,
	} {
		if got := isTempPath(path); got != want {
			t.Errorf("isTempPath(%q) = %v, want %v", path, got, want)
		}
	}
}
