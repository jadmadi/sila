package crew

import (
	"strings"
	"testing"
)

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"", "foo.go", false},
		{"*", "foo.go", true},
		{"**", "a/b/c/foo.go", true},
		{"internal/crew", "internal/crew/surface.go", true},
		{"internal/crew/**", "internal/crew/surface.go", true},
		{"internal/crew/**", "internal/crew/sub/deep/file.go", true},
		{"internal/crew/**", "internal/goals/validate.go", false},
		{"cmd/sila/*.go", "cmd/sila/crew.go", true},
		{"cmd/sila/*.go", "cmd/sila/sub/deep.go", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/README.md", true},
		{"*.md", "docs/README.go", false},
		{"internal/**/test/*.go", "internal/sub/test/unit.go", true},
		{"internal/**/test/*.go", "internal/test/unit.go", true},
		{"internal/**/test/*.go", "internal/sub/other/unit.go", false},
	}

	for _, tt := range tests {
		got := MatchGlob(tt.pattern, tt.path)
		if got != tt.want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestWriteSurface_CanWrite(t *testing.T) {
	// 1. Reviewer is structurally read-only
	rev := &WriteSurface{
		AgentID:   "auditor-1",
		RoleClass: RoleClassReviewer,
		Patterns:  []string{"**"},
	}
	if rev.CanWrite("any/file.go") {
		t.Errorf("reviewer should have zero write surface")
	}

	// 2. Builder with inclusions and exclusions
	builder := &WriteSurface{
		AgentID:   "builder-1",
		RoleClass: RoleClassProducer,
		Patterns: []string{
			"internal/crew/**",
			"cmd/sila/crew.go",
			"!internal/crew/legacy_*",
			"!*.md",
		},
	}

	if !builder.CanWrite("internal/crew/surface.go") {
		t.Errorf("builder should be allowed to write internal/crew/surface.go")
	}
	if !builder.CanWrite("cmd/sila/crew.go") {
		t.Errorf("builder should be allowed to write cmd/sila/crew.go")
	}
	if builder.CanWrite("internal/crew/legacy_old.go") {
		t.Errorf("builder should be blocked by !internal/crew/legacy_* exclusion")
	}
	if builder.CanWrite("internal/crew/notes.md") {
		t.Errorf("builder should be blocked by !*.md exclusion")
	}
	if builder.CanWrite("internal/goals/validate.go") {
		t.Errorf("builder should not write outside allowed patterns")
	}

	// 3. Shared ledger file exemption
	if !builder.CanWrite("CHANGELOG.md") {
		t.Errorf("CHANGELOG.md should be exempt as shared ledger")
	}
}

func TestSurfaceMap_RegisterAndCollision(t *testing.T) {
	sm := NewSurfaceMap()

	s1 := &WriteSurface{
		AgentID:   "agent-alpha",
		GoalSlug:  "goal-alpha",
		RoleClass: RoleClassProducer,
		Patterns:  []string{"internal/crew/**"},
	}
	if err := sm.Register(s1); err != nil {
		t.Fatalf("register s1 failed: %v", err)
	}

	// s2 collides with s1 on internal/crew/**
	s2 := &WriteSurface{
		AgentID:   "agent-beta",
		GoalSlug:  "goal-beta",
		RoleClass: RoleClassProducer,
		Patterns:  []string{"internal/crew/surface.go"},
	}
	err := sm.Register(s2)
	if err == nil {
		t.Fatalf("expected collision error registering overlapping surface s2")
	}
	if !strings.Contains(err.Error(), "write-surface collision") {
		t.Errorf("expected collision error text, got %v", err)
	}

	// s3 is a reviewer: no collision even with overlapping pattern
	rev := &WriteSurface{
		AgentID:   "agent-reviewer",
		GoalSlug:  "goal-alpha",
		RoleClass: RoleClassReviewer,
		Patterns:  []string{"internal/crew/**"},
	}
	if err := sm.Register(rev); err != nil {
		t.Fatalf("register reviewer should succeed without collision: %v", err)
	}

	// s4 is disjoint: internal/goals/**
	s4 := &WriteSurface{
		AgentID:   "agent-gamma",
		GoalSlug:  "goal-gamma",
		RoleClass: RoleClassProducer,
		Patterns:  []string{"internal/goals/**"},
	}
	if err := sm.Register(s4); err != nil {
		t.Fatalf("register disjoint s4 failed: %v", err)
	}
}

func TestSurfaceMap_FindOwner(t *testing.T) {
	sm := NewSurfaceMap()

	_ = sm.Register(&WriteSurface{
		AgentID:   "agent-crew",
		RoleClass: RoleClassProducer,
		Patterns:  []string{"internal/crew/**"},
	})
	_ = sm.Register(&WriteSurface{
		AgentID:   "agent-goals",
		RoleClass: RoleClassProducer,
		Patterns:  []string{"internal/goals/**"},
	})

	owner, err := sm.FindOwner("internal/crew/surface.go")
	if err != nil || owner != "agent-crew" {
		t.Errorf("expected owner agent-crew, got %q, err %v", owner, err)
	}

	owner, err = sm.FindOwner("internal/goals/validate.go")
	if err != nil || owner != "agent-goals" {
		t.Errorf("expected owner agent-goals, got %q, err %v", owner, err)
	}

	owner, err = sm.FindOwner("cmd/sila/main.go")
	if err != nil || owner != "" {
		t.Errorf("expected no owner for unmapped path, got %q, err %v", owner, err)
	}
}

func TestValidateStagedFilesAgainstSurface(t *testing.T) {
	surface := &WriteSurface{
		AgentID:   "builder-1",
		RoleClass: RoleClassProducer,
		Patterns:  []string{"internal/crew/**", "!internal/crew/legacy_*"},
	}

	// Valid staged files
	valid := []string{"internal/crew/surface.go", "CHANGELOG.md"}
	if err := ValidateStagedFilesAgainstSurface(surface, valid); err != nil {
		t.Errorf("expected valid staged files to pass, got: %v", err)
	}

	// Staged file outside boundary
	invalid1 := []string{"internal/crew/surface.go", "internal/goals/validate.go"}
	if err := ValidateStagedFilesAgainstSurface(surface, invalid1); err == nil {
		t.Errorf("expected failure when staging file outside pattern")
	}

	// Staged file in exclusion
	invalid2 := []string{"internal/crew/legacy_test.go"}
	if err := ValidateStagedFilesAgainstSurface(surface, invalid2); err == nil {
		t.Errorf("expected failure when staging excluded file")
	}

	// Reviewer agent staging files
	revSurface := &WriteSurface{
		AgentID:   "auditor-1",
		RoleClass: RoleClassReviewer,
		Patterns:  []string{"**"},
	}
	if err := ValidateStagedFilesAgainstSurface(revSurface, []string{"foo.go"}); err == nil {
		t.Errorf("expected reviewer to fail when files are staged")
	}
}
