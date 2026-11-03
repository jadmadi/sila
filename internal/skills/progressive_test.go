package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestParseSkillSummaryFile(t *testing.T) {
	tempDir := t.TempDir()
	skillDir := filepath.Join(tempDir, ".agents", "skills", "test-perf-skill")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755); err != nil {
		t.Fatalf("mkdir scripts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "check.sh"), []byte("#!/bin/sh\necho ok"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	largeBody := strings.Repeat("Long multi-kilobyte instructions and runbook details for high performance testing.\n", 200)
	content := fmt.Sprintf(`---
name: Performance Analyzer
description: High-performance latency and memory allocation auditor. Inspects hot paths and reduces GC pressure.
tags:
  - performance
  - profiling
---
# Performance Analyzer Runbook
%s
`, largeBody)

	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}

	summary, err := ParseSkillSummaryFile("test-perf-skill", skillPath, "project")
	if err != nil {
		t.Fatalf("ParseSkillSummaryFile failed: %v", err)
	}

	if summary.Slug != "test-perf-skill" {
		t.Errorf("expected slug 'test-perf-skill', got %q", summary.Slug)
	}
	if summary.Name != "Performance Analyzer" {
		t.Errorf("expected name 'Performance Analyzer', got %q", summary.Name)
	}
	wantTrigger := "High-performance latency and memory allocation auditor."
	if summary.Trigger != wantTrigger {
		t.Errorf("expected trigger %q, got %q", wantTrigger, summary.Trigger)
	}
	if !summary.HasScripts {
		t.Error("expected HasScripts to be true")
	}
	if summary.Source != "project" {
		t.Errorf("expected source 'project', got %q", summary.Source)
	}
	if summary.EstimatedTokens < 1000 {
		t.Errorf("expected estimated tokens > 1000, got %d", summary.EstimatedTokens)
	}
}

func TestListSkillSummaries(t *testing.T) {
	tempDir := t.TempDir()
	emptyGlobal := filepath.Join(tempDir, "empty_global")
	_ = os.MkdirAll(emptyGlobal, 0755)
	t.Setenv("SILA_GLOBAL_SKILLS_DIR", emptyGlobal)

	pSkillsDir := filepath.Join(tempDir, ".agents", "skills")

	createDummySkill := func(slug, name, desc string) {
		dir := filepath.Join(pSkillsDir, slug)
		_ = os.MkdirAll(dir, 0755)
		content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n# Instructions\nRun instructions here.\n", name, desc)
		_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644)
	}

	createDummySkill("alpha-tool", "Alpha Tool", "First procedural tool for automation.")
	createDummySkill("beta-linter", "Beta Linter", "Fast deterministic linter for Go code.")
	createDummySkill("gamma-deploy", "Gamma Deployer", "Zero-downtime deployment runner.")

	// 1. List all summaries
	all, err := ListSkillSummaries(tempDir, "")
	if err != nil {
		t.Fatalf("ListSkillSummaries failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(all))
	}

	// 2. Filter by slug substring
	linterResults, err := ListSkillSummaries(tempDir, "linter")
	if err != nil {
		t.Fatalf("ListSkillSummaries filter failed: %v", err)
	}
	if len(linterResults) != 1 || linterResults[0].Slug != "beta-linter" {
		t.Fatalf("expected beta-linter, got %v", linterResults)
	}

	// 3. Filter by description substring
	deployResults, err := ListSkillSummaries(tempDir, "downtime")
	if err != nil {
		t.Fatalf("ListSkillSummaries filter failed: %v", err)
	}
	if len(deployResults) != 1 || deployResults[0].Slug != "gamma-deploy" {
		t.Fatalf("expected gamma-deploy, got %v", deployResults)
	}
}

func TestSearchCatalogWithLessons_RRF(t *testing.T) {
	tempDir := t.TempDir()
	pSkillsDir := filepath.Join(tempDir, ".agents", "skills")

	// Create authoritative Tier 1 skill
	dbSkillDir := filepath.Join(pSkillsDir, "sqlite-opt")
	_ = os.MkdirAll(dbSkillDir, 0755)
	_ = os.WriteFile(filepath.Join(dbSkillDir, "SKILL.md"), []byte(`---
name: SQLite Optimizer
description: Optimizes SQLite schema, PRAGMAs, and indexing for high concurrency workloads.
tags:
  - database
  - sqlite
---
# SQLite Optimizer Instructions
Instructions for SQLite optimization.
`), 0644)

	// Initialize test store
	dbPath := filepath.Join(tempDir, "test_sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	defer st.Close()

	// Save grounded lesson (Tier 2)
	_, err = st.SaveLesson("test-project", "database lock timeout under concurrent writes", "missing WAL mode", "execute PRAGMA journal_mode=WAL", "sqlite,concurrency,database", "antigravity", tempDir, []string{"internal/store/db.go"})
	if err != nil {
		t.Fatalf("SaveLesson failed: %v", err)
	}

	// Query fusing skills and lessons
	results, err := SearchCatalogWithLessons(tempDir, "sqlite", nil, 10, st)
	if err != nil {
		t.Fatalf("SearchCatalogWithLessons failed: %v", err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 fused results (skill + lesson), got %d: %+v", len(results), results)
	}

	// Verify Tier 1 Authoritative Skill is present
	foundSkill := false
	foundLesson := false
	for _, res := range results {
		if res.Tier == 1 && res.Slug == "sqlite-opt" {
			foundSkill = true
			if res.Source != "project" {
				t.Errorf("expected skill source 'project', got %q", res.Source)
			}
		}
		if res.Tier == 2 && strings.HasPrefix(res.Slug, "lesson-") {
			foundLesson = true
			if res.Source != "grounded_lesson" {
				t.Errorf("expected lesson source 'grounded_lesson', got %q", res.Source)
			}
			if !strings.Contains(res.Trigger, "journal_mode=WAL") {
				t.Errorf("expected trigger containing fix, got %q", res.Trigger)
			}
		}
	}

	if !foundSkill {
		t.Error("Tier 1 authoritative skill not found in fused results")
	}
	if !foundLesson {
		t.Error("Tier 2 grounded runbook lesson not found in fused results")
	}

	// Verify RRF scoring: Tier 1 weight (1.0) with rank 1 has higher score than Tier 2 weight (0.85) with rank 1
	if results[0].Tier != 1 {
		t.Errorf("expected top result to be Tier 1 Authoritative Skill, got Tier %d", results[0].Tier)
	}
}

func TestProgressiveDisclosure_TokenReduction(t *testing.T) {
	tempDir := t.TempDir()
	emptyGlobal := filepath.Join(tempDir, "empty_global")
	_ = os.MkdirAll(emptyGlobal, 0755)
	t.Setenv("SILA_GLOBAL_SKILLS_DIR", emptyGlobal)

	pSkillsDir := filepath.Join(tempDir, ".agents", "skills")

	totalRunbookTokens := 0
	totalSummaryTokens := 0

	for i := 1; i <= 50; i++ {
		slug := fmt.Sprintf("service-tool-%02d", i)
		dir := filepath.Join(pSkillsDir, slug)
		_ = os.MkdirAll(dir, 0755)

		// Each full skill runbook contains ~800 tokens of documentation, markdown, and instructions
		runbookBody := strings.Repeat(fmt.Sprintf("Runbook instructions and execution guidelines for service tool %02d.\n", i), 40)
		fullMarkdown := fmt.Sprintf(`---
name: Service Tool %02d
description: Automated operational assistant for microservice %02d. Provides deterministic remediation runbooks.
tags:
  - microservice
  - ops
---
# Service Tool %02d Runbook
%s
`, i, i, i, runbookBody)

		_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fullMarkdown), 0644)
		totalRunbookTokens += store.EstimateTokens(len(fullMarkdown))
	}

	summaries, err := ListSkillSummaries(tempDir, "")
	if err != nil {
		t.Fatalf("ListSkillSummaries failed: %v", err)
	}
	if len(summaries) != 50 {
		t.Fatalf("expected 50 summaries, got %d", len(summaries))
	}

	// Serialize summaries to compact JSON as would be served in active briefing / list_skills
	var sb strings.Builder
	for _, s := range summaries {
		fmt.Fprintf(&sb, "%s|%s|%s|%d|%t\n", s.Slug, s.Source, s.Trigger, s.EstimatedTokens, s.HasScripts)
	}
	totalSummaryTokens = store.EstimateTokens(sb.Len())

	t.Logf("50 Full Runbooks: ~%d tokens", totalRunbookTokens)
	t.Logf("50 Skill Summaries: ~%d tokens", totalSummaryTokens)

	reductionPct := float64(totalRunbookTokens-totalSummaryTokens) / float64(totalRunbookTokens) * 100.0
	t.Logf("Context Token Reduction: %.2f%%", reductionPct)

	if totalRunbookTokens < 30000 {
		t.Errorf("expected 50 full runbooks to be >30,000 tokens, got %d", totalRunbookTokens)
	}
	if totalSummaryTokens > 1500 {
		t.Errorf("expected 50 skill summaries to be <1,500 tokens, got %d", totalSummaryTokens)
	}
	if reductionPct < 90.0 {
		t.Errorf("expected >90%% token reduction, got %.2f%%", reductionPct)
	}
}

func BenchmarkListSkillSummaries_200Skills(b *testing.B) {
	tempDir := b.TempDir()
	emptyGlobal := filepath.Join(tempDir, "empty_global")
	_ = os.MkdirAll(emptyGlobal, 0755)
	b.Setenv("SILA_GLOBAL_SKILLS_DIR", emptyGlobal)

	pSkillsDir := filepath.Join(tempDir, ".agents", "skills")

	for i := 1; i <= 200; i++ {
		slug := fmt.Sprintf("benchmark-skill-%03d", i)
		dir := filepath.Join(pSkillsDir, slug)
		_ = os.MkdirAll(dir, 0755)

		content := fmt.Sprintf(`---
name: Benchmark Skill %03d
description: Benchmark testing skill number %03d. Evaluates sub-5ms catalog generation latency.
tags:
  - benchmark
  - latency
---
# Runbook
Multi-line runbook body for benchmark skill %03d.
`, i, i, i)
		_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644)
	}

	b.ResetTimer()
	start := time.Now()
	for i := 0; i < b.N; i++ {
		summaries, err := ListSkillSummaries(tempDir, "")
		if err != nil {
			b.Fatalf("ListSkillSummaries failed: %v", err)
		}
		if len(summaries) != 200 {
			b.Fatalf("expected 200 summaries, got %d", len(summaries))
		}
	}
	elapsed := time.Since(start)
	if b.N > 0 {
		avgPerOp := elapsed / time.Duration(b.N)
		b.Logf("Average time for 200 skills catalog generation: %v (<5ms limit)", avgPerOp)
		if avgPerOp > 50*time.Millisecond {
			b.Errorf("catalog generation exceeded 50ms: %v", avgPerOp)
		}
	}
}
