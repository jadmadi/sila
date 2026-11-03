package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeTier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"immediate", "immediate"},
		{"IMMEDIATE", "immediate"},
		{"tier-1", "immediate"},
		{"Tier 1", "immediate"},
		{"urgent", "immediate"},
		{"p0", "immediate"},
		{"p1", "immediate"},
		{"roadmap", "roadmap"},
		{"ROADMAP", "roadmap"},
		{"tier-2", "roadmap"},
		{"Tier 2", "roadmap"},
		{"p2", "roadmap"},
		{"default", "roadmap"},
		{"", "roadmap"},
		{"unknown-tier", "roadmap"},
		{"nice-to-have", "nice-to-have"},
		{"nice_to_have", "nice-to-have"},
		{"nice to have", "nice-to-have"},
		{"tier-3", "nice-to-have"},
		{"p3", "nice-to-have"},
		{"optional", "nice-to-have"},
		{"wont-fix", "wont-fix"},
		{"wont_fix", "wont-fix"},
		{"won't fix", "wont-fix"},
		{"tier-4", "wont-fix"},
		{"p4", "wont-fix"},
		{"defer", "wont-fix"},
		{"deferred", "wont-fix"},
	}

	for _, tt := range tests {
		got := NormalizeTier(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeTier(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseGoalMetadataWithTier(t *testing.T) {
	t.Run("frontmatter tier", func(t *testing.T) {
		content := `---
title: Test Tier Frontmatter
description: Description here
tier: immediate
---

## Overview
Content...
`
		_, _, _, _, _, _, _, tier := parseGoalMetadataWithTier(content)
		if tier != "immediate" {
			t.Errorf("parseGoalMetadataWithTier() tier = %q; want immediate", tier)
		}
	})

	t.Run("frontmatter priority alias", func(t *testing.T) {
		content := `---
title: Test Priority Frontmatter
description: Description here
priority: urgent
---

## Overview
Content...
`
		_, _, _, _, _, _, _, tier := parseGoalMetadataWithTier(content)
		if tier != "immediate" {
			t.Errorf("parseGoalMetadataWithTier() tier = %q; want immediate", tier)
		}
	})

	t.Run("markdown body tier", func(t *testing.T) {
		content := `# Goal: Test Body Tier

- **Title**: Body Tier Goal
- **Description**: Body description
- **Tier**: nice-to-have
- **Execution Mode**: Independent

## Scope
`
		_, _, _, _, _, _, _, tier := parseGoalMetadataWithTier(content)
		if tier != "nice-to-have" {
			t.Errorf("parseGoalMetadataWithTier() tier = %q; want nice-to-have", tier)
		}
	})

	t.Run("markdown body priority alias", func(t *testing.T) {
		content := `# Goal: Test Body Priority

- **Title**: Body Priority Goal
- **Description**: Body description
- **Priority**: defer
- **Execution Mode**: Independent

## Scope
`
		_, _, _, _, _, _, _, tier := parseGoalMetadataWithTier(content)
		if tier != "wont-fix" {
			t.Errorf("parseGoalMetadataWithTier() tier = %q; want wont-fix", tier)
		}
	})

	t.Run("explicit tier wins over earlier prose priority", func(t *testing.T) {
		content := `# Goal: Test Explicit Tier Over Priority

Priority: **P2**. Status: pending implementation.

- **Title**: Explicit Tier Goal
- **Description**: Body description
- **Tier**: immediate
- **Execution Mode**: Independent

## Scope
`
		_, _, _, _, _, _, _, tier := parseGoalMetadataWithTier(content)
		if tier != "immediate" {
			t.Errorf("parseGoalMetadataWithTier() tier = %q; want immediate", tier)
		}
	})

	t.Run("default tier when omitted", func(t *testing.T) {
		content := `# Goal: Omitted Tier

- **Title**: Omitted Tier Goal
- **Description**: Body description
- **Execution Mode**: Independent
`
		_, _, _, _, _, _, _, tier := parseGoalMetadataWithTier(content)
		if tier != "roadmap" {
			t.Errorf("parseGoalMetadataWithTier() tier = %q; want roadmap", tier)
		}
	})
}

func TestFilterByTier(t *testing.T) {
	audit := &AuditResult{
		Project: "test-proj",
		Goals: []Goal{
			{Slug: "g1", Status: "pending", Tier: "immediate", Independent: true},
			{Slug: "g2", Status: "claimed", Tier: "immediate"},
			{Slug: "g3", Status: "implemented", Tier: "immediate"},
			{Slug: "g4", Status: "pending", Tier: "roadmap"},
			{Slug: "g5", Status: "pending", Tier: "nice-to-have"},
			{Slug: "g6", Status: "pending", Tier: "wont-fix"},
		},
		Total:            6,
		PendingCount:     4,
		ClaimedCount:     1,
		ImplementedCount: 1,
		ImmediateCount:   3,
		RoadmapCount:     1,
		NiceToHaveCount:  1,
		WontFixCount:     1,
	}

	filtered := audit.FilterByTier("immediate")
	if filtered.Total != 3 {
		t.Fatalf("expected filtered.Total = 3, got %d", filtered.Total)
	}
	if filtered.PendingCount != 1 {
		t.Errorf("expected filtered.PendingCount = 1, got %d", filtered.PendingCount)
	}
	if filtered.ClaimedCount != 1 {
		t.Errorf("expected filtered.ClaimedCount = 1, got %d", filtered.ClaimedCount)
	}
	if filtered.ImplementedCount != 1 {
		t.Errorf("expected filtered.ImplementedCount = 1, got %d", filtered.ImplementedCount)
	}
	for _, g := range filtered.Goals {
		if g.Tier != "immediate" {
			t.Errorf("unexpected goal tier %q in filtered immediate results", g.Tier)
		}
	}

	filteredRoadmap := audit.FilterByTier("tier-2")
	if filteredRoadmap.Total != 1 || filteredRoadmap.Goals[0].Slug != "g4" {
		t.Errorf("unexpected filtered roadmap result: %+v", filteredRoadmap)
	}
}

func TestCreateAndUpdateGoalTier(t *testing.T) {
	tmp := t.TempDir()

	// Create goal with immediate tier
	g, err := CreateGoal(CreateGoalParams{
		RootDir:     tmp,
		Slug:        "tier-goal",
		Title:       "Tier Test Goal",
		Description: "Testing tier creation and update",
		Tier:        "immediate",
	})
	if err != nil {
		t.Fatalf("CreateGoal failed: %v", err)
	}
	if g.Tier != "immediate" {
		t.Errorf("expected g.Tier = immediate, got %q", g.Tier)
	}

	goalFile := filepath.Join(tmp, "goals", "tier-goal", "goal.md")
	content, err := os.ReadFile(goalFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(content), "- **Tier**: immediate") {
		t.Errorf("expected goal.md to contain '- **Tier**: immediate', got:\n%s", string(content))
	}

	// Update goal to nice-to-have
	newTier := "nice-to-have"
	gUpdated, err := UpdateGoal(UpdateGoalParams{
		RootDir: tmp,
		Slug:    "tier-goal",
		Tier:    &newTier,
	})
	if err != nil {
		t.Fatalf("UpdateGoal failed: %v", err)
	}
	if gUpdated.Tier != "nice-to-have" {
		t.Errorf("expected gUpdated.Tier = nice-to-have, got %q", gUpdated.Tier)
	}

	contentUpdated, err := os.ReadFile(goalFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(contentUpdated), "- **Tier**: nice-to-have") {
		t.Errorf("expected updated goal.md to contain '- **Tier**: nice-to-have', got:\n%s", string(contentUpdated))
	}
}

func TestSyncReadmeTierTable(t *testing.T) {
	tmp := t.TempDir()
	goalsDir := filepath.Join(tmp, "goals")
	if err := os.MkdirAll(goalsDir, 0755); err != nil {
		t.Fatal(err)
	}

	res := &AuditResult{
		Project: "tier-test-proj",
		Goals: []Goal{
			{
				Slug:        "goal-imm",
				Title:       "Immediate Goal",
				Description: "Immediate task",
				Status:      "pending",
				Independent: true,
				Tier:        "immediate",
				LaunchCmd:   "/goal goals/goal-imm/goal.md",
			},
			{
				Slug:        "goal-road",
				Title:       "Roadmap Goal",
				Description: "Roadmap task",
				Status:      "pending",
				Independent: true,
				Tier:        "roadmap",
				LaunchCmd:   "/goal goals/goal-road/goal.md",
			},
		},
		Total:          2,
		PendingCount:   2,
		ImmediateCount: 1,
		RoadmapCount:   1,
	}

	err := SyncReadmeWithOpts(tmp, res, SyncReadmeOptions{})
	if err != nil {
		t.Fatalf("SyncReadmeWithOpts failed: %v", err)
	}

	readmeBytes, err := os.ReadFile(filepath.Join(goalsDir, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile goals/README.md failed: %v", err)
	}
	readme := string(readmeBytes)

	// Verify By Tier summary line
	if !strings.Contains(readme, "🎯 **By Tier**: 1 immediate (Tier 1), 1 roadmap (Tier 2), 0 nice-to-have (Tier 3), 0 wont-fix (Tier 4)") {
		t.Errorf("goals/README.md missing expected tier summary line. Got:\n%s", readme)
	}

	// Verify Tier column in table
	if !strings.Contains(readme, "| Goal Package | Tier | Mode & Sequence | Dependencies | Focus & Description | Launch Command |") {
		t.Errorf("goals/README.md table header missing Tier column. Got:\n%s", readme)
	}
	if !strings.Contains(readme, "🎯 Immediate") || !strings.Contains(readme, "🗺️ Roadmap") {
		t.Errorf("goals/README.md table rows missing tier badges. Got:\n%s", readme)
	}
}
