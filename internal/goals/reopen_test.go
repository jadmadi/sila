package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeClaimHygieneGoal(t *testing.T, dir, slug string) {
	t.Helper()
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: "+slug+"\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Complete\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)
}

func TestReportGoalRequiresClaimAndRenewsCharter(t *testing.T) {
	dir := t.TempDir()
	slug := "claim-hygiene-goal"
	writeClaimHygieneGoal(t, dir, slug)

	if _, err := ReportGoal(dir, slug, "progress", ""); err == nil || !strings.Contains(err.Error(), "not currently claimed") {
		t.Fatalf("expected fail-closed report on unclaimed goal, got %v", err)
	}

	if _, err := ClaimGoal(dir, slug, "agent-a", "working", false); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	before, err := LoadCharter(dir, slug)
	if err != nil {
		t.Fatalf("LoadCharter: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	if _, err := ReportGoal(dir, slug, "progress", "in_progress"); err != nil {
		t.Fatalf("ReportGoal: %v", err)
	}
	afterReport, err := LoadCharter(dir, slug)
	if err != nil {
		t.Fatalf("LoadCharter after report: %v", err)
	}
	if !afterReport.CreatedAt.After(before.CreatedAt) {
		t.Errorf("expected ReportGoal to renew charter lease")
	}

	time.Sleep(2 * time.Millisecond)
	if _, err := TouchGoal(dir, slug, "agent-a"); err != nil {
		t.Fatalf("TouchGoal: %v", err)
	}
	afterTouch, err := LoadCharter(dir, slug)
	if err != nil {
		t.Fatalf("LoadCharter after touch: %v", err)
	}
	if !afterTouch.CreatedAt.After(afterReport.CreatedAt) {
		t.Errorf("expected TouchGoal to renew charter lease")
	}
}

func TestCircuitBreakerRemovesCharter(t *testing.T) {
	dir := t.TempDir()
	slug := "circuit-breaker-goal"
	writeClaimHygieneGoal(t, dir, slug)
	if _, err := ClaimGoal(dir, slug, "agent-a", "working", false); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}

	for i := 1; i <= 3; i++ {
		_, tripped, err := RecordClaimIteration(dir, slug, false, "identical failure")
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if i < 3 && tripped {
			t.Fatalf("iteration %d tripped early", i)
		}
		if i == 3 && !tripped {
			t.Fatalf("expected circuit breaker to trip on iteration 3")
		}
	}
	if _, err := os.Stat(CharterPath(dir, slug)); !os.IsNotExist(err) {
		t.Errorf("expected charter removed after circuit-breaker trip, stat err: %v", err)
	}
}

func TestSupersedeRemovesCharter(t *testing.T) {
	dir := t.TempDir()
	slug := "supersede-hygiene-goal"
	writeClaimHygieneGoal(t, dir, slug)
	if _, err := ClaimGoal(dir, slug, "agent-a", "working", false); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	if _, err := SupersedeGoal(dir, slug, "successor-goal", "agent-a", "replaced"); err != nil {
		t.Fatalf("SupersedeGoal: %v", err)
	}
	if _, err := os.Stat(CharterPath(dir, slug)); !os.IsNotExist(err) {
		t.Errorf("expected charter removed after supersede, stat err: %v", err)
	}
	if fileExists(filepath.Join(dir, "goals", slug, "claim.json")) {
		t.Error("expected claim.json removed after supersede")
	}
}

func TestReopenArchivesVerificationArtifacts(t *testing.T) {
	dir := t.TempDir()
	slug := "reopen-hygiene-goal"
	writeClaimHygieneGoal(t, dir, slug)

	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Tester")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "feat: implement ("+filepath.ToSlash(filepath.Join("goals", slug, "goal.md"))+")")

	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.WriteFile(filepath.Join(goalDir, "certification.json"), []byte(`{"goal_slug":"`+slug+`","verified":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "audit-receipt.json"), []byte(`{"goal_slug":"`+slug+`","version":"sila/audit-receipt/v1"}`), 0644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := ReopenGoal(dir, slug, "remediator", "regression detected", false); err != nil {
		t.Fatalf("ReopenGoal: %v", err)
	}

	for _, name := range []string{"certification.json", "audit-receipt.json"} {
		if fileExists(filepath.Join(goalDir, name)) {
			t.Errorf("expected active %s to be archived on reopen", name)
		}
	}
	entries, err := os.ReadDir(filepath.Join(goalDir, "amendments"))
	if err != nil {
		t.Fatalf("read amendments: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 archived verification artifacts, got %d", len(entries))
	}
}
