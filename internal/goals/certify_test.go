package goals

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCertifyGoal(t *testing.T) {
	dir := t.TempDir()
	goalDir := filepath.Join(dir, "goals", "verified-feature")
	_ = os.MkdirAll(goalDir, 0755)
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Verified Feature\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [ ] Step 1\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	// Create an evidence JSON file
	evFile := filepath.Join(dir, "facts-result.json")
	evContent := `{
		"facts": [
			{"id": "fact-1", "fact": "Auth validates tokens", "automatedVerification": true, "passed": true},
			{"id": "fact-2", "fact": "Rate limiter works", "automatedVerification": true, "passed": true}
		]
	}`
	_ = os.WriteFile(evFile, []byte(evContent), 0644)

	cert, err := CertifyGoal(dir, "verified-feature", evFile, "@agent", "all automated verification facts passed")
	if err != nil {
		t.Fatalf("CertifyGoal failed: %v", err)
	}
	if !cert.Verified || cert.FactsPassed != 2 || cert.FactsTotal != 2 {
		t.Fatalf("unexpected certification: %+v", cert)
	}

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if len(res.Goals) != 1 || res.Goals[0].Status != "implemented" {
		t.Fatalf("expected goal status implemented via evidence certification, got %q", res.Goals[0].Status)
	}
	if !res.Goals[0].Certified {
		t.Fatalf("expected goal.Certified true")
	}
}

func TestCertifyGoalMarkdownAndSelfAudit(t *testing.T) {
	setupGoal := func(t *testing.T, slug, goalContent string, withClaim bool) string {
		t.Helper()
		dir := t.TempDir()
		goalDir := filepath.Join(dir, "goals", slug)
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644)
		_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Complete\n"), 0644)
		_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)
		_ = os.WriteFile(filepath.Join(goalDir, "evidence.md"), []byte("# Evidence\n\nVerdict: PASS\n"), 0644)
		if withClaim {
			_ = os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(`{"goal_slug":"`+slug+`","agent":"claimer","claimed_at":"2026-01-01T00:00:00Z"}`), 0644)
			_ = os.WriteFile(filepath.Join(goalDir, ".claim.lock"), []byte("lock"), 0644)
		}
		return dir
	}

	prev := markdownEvidenceVerifier
	t.Cleanup(func() { SetMarkdownEvidenceVerifier(prev) })
	SetMarkdownEvidenceVerifier(func(rootDir, slug, evidencePath string, data []byte) error {
		if !strings.Contains(string(data), "Verdict: PASS") {
			return errors.New("EVIDENCE_VERDICT_REQUIRED: missing verdict declaration in markdown evidence")
		}
		return nil
	})

	t.Run("BindsCommitAndReleasesVerifiedClaim", func(t *testing.T) {
		slug := "markdown-commit-goal"
		dir := setupGoal(t, slug, "# Goal: Markdown Commit\n", true)
		git(t, dir, "init", "-q")
		git(t, dir, "config", "user.email", "test@test.com")
		git(t, dir, "config", "user.name", "Tester")
		git(t, dir, "add", ".")
		git(t, dir, "commit", "-qm", "feat: implement markdown goal (goals/"+slug+"/goal.md)")

		headOut, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		head := strings.TrimSpace(string(headOut))

		cert, err := CertifyGoal(dir, slug, filepath.Join("goals", slug, "evidence.md"), "auditor", "")
		if err != nil {
			t.Fatalf("CertifyGoal markdown failed: %v", err)
		}
		if !cert.Verified || cert.EvidenceKind != "review_document" {
			t.Fatalf("unexpected certification: %+v", cert)
		}
		if cert.CommitSHA != head {
			t.Errorf("expected CommitSHA %s, got %q", head, cert.CommitSHA)
		}
		if fileExists(filepath.Join(dir, "goals", slug, "claim.json")) {
			t.Error("expected verified claim.json to be released")
		}
		if fileExists(filepath.Join(dir, "goals", slug, ".claim.lock")) {
			t.Error("expected .claim.lock to be released")
		}
	})

	t.Run("IsnadSelfAuditProhibited", func(t *testing.T) {
		slug := "markdown-isnad-self"
		dir := setupGoal(t, slug, "# Goal: Markdown Self\n\n## Chain of Custody (Isnad)\n- **Implemented By**: @producer\n", false)
		_, err := CertifyGoal(dir, slug, filepath.Join("goals", slug, "evidence.md"), "producer", "")
		if err == nil || !strings.Contains(err.Error(), "self-audit prohibited") {
			t.Fatalf("expected self-audit prohibition, got %v", err)
		}
	})

	t.Run("ClaimSelfAuditProhibited", func(t *testing.T) {
		slug := "markdown-claim-self"
		dir := setupGoal(t, slug, "# Goal: Claim Self\n", true)
		_, err := CertifyGoal(dir, slug, filepath.Join("goals", slug, "evidence.md"), "claimer", "")
		if err == nil || !strings.Contains(err.Error(), "self-audit prohibited") {
			t.Fatalf("expected claim-based self-audit prohibition, got %v", err)
		}
	})

	t.Run("VerifierRejectionPreservesState", func(t *testing.T) {
		slug := "markdown-rejected"
		dir := setupGoal(t, slug, "# Goal: Rejected\n", true)
		SetMarkdownEvidenceVerifier(func(rootDir, slug, evidencePath string, data []byte) error {
			return errors.New("EVIDENCE_AUDIT_BLOCKED: rejected by test")
		})
		_, err := CertifyGoal(dir, slug, filepath.Join("goals", slug, "evidence.md"), "auditor", "")
		if err == nil || !strings.Contains(err.Error(), "EVIDENCE_AUDIT_BLOCKED") {
			t.Fatalf("expected EVIDENCE_AUDIT_BLOCKED, got %v", err)
		}
		if fileExists(filepath.Join(dir, "goals", slug, "certification.json")) {
			t.Error("rejected evidence must not write certification.json")
		}
		if !fileExists(filepath.Join(dir, "goals", slug, "claim.json")) {
			t.Error("rejected evidence must preserve claim.json")
		}
	})
}

func TestImportPlannotatorPackage(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "plannotator-output")
	_ = os.MkdirAll(sourceDir, 0755)

	interviewContent := `{
		"project": "sila",
		"title": "Plannotator Importer",
		"description": "Seamless ingestion of plannotator metadata",
		"scope": "Import facts, review, and plan",
		"non_goals": ["External daemon"]
	}`
	_ = os.WriteFile(filepath.Join(sourceDir, "interview.json"), []byte(interviewContent), 0644)

	factsContent := `[
		{"id": "f1", "fact": "Database uses SQLite", "automatedVerification": true, "passed": true, "source": "store.go"}
	]`
	_ = os.WriteFile(filepath.Join(sourceDir, "facts.meta.json"), []byte(factsContent), 0644)

	planContent := `# Plan: Plannotator Importer
- [ ] Phase 1 — Ingestion
- [ ] Phase 2 — Verification
`
	_ = os.WriteFile(filepath.Join(sourceDir, "plan.md"), []byte(planContent), 0644)

	err := ImportPlannotatorPackage(dir, "plannotator-importer", sourceDir)
	if err != nil {
		t.Fatalf("ImportPlannotatorPackage failed: %v", err)
	}

	// Verify files were generated in goals/plannotator-importer
	gDir := filepath.Join(dir, "goals", "plannotator-importer")
	if !fileExists(filepath.Join(gDir, "goal.md")) {
		t.Errorf("expected goal.md created")
	}
	if !fileExists(filepath.Join(gDir, "facts.md")) {
		t.Errorf("expected facts.md created")
	}
	if !fileExists(filepath.Join(gDir, "plan.md")) {
		t.Errorf("expected plan.md created")
	}

	res, err := ScanGoals(dir)
	if err != nil {
		t.Fatalf("ScanGoals failed: %v", err)
	}
	if len(res.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(res.Goals))
	}
	if res.Goals[0].Title != "Plannotator Importer" {
		t.Errorf("expected title 'Plannotator Importer', got %q", res.Goals[0].Title)
	}
}

func TestCertifyGoal_RetainsClaimLockUntilCommitClosure(t *testing.T) {
	dir := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v, out: %s", err, string(out))
	}
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@example.com").Run()

	goalDir := filepath.Join(dir, "goals", "claim-retention")
	_ = os.MkdirAll(goalDir, 0755)
	_ = os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Claim Retention\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [ ] Step 1\n"), 0644)
	_ = os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644)

	_ = exec.Command("git", "-C", dir, "add", ".").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "chore: initial commit").Run()

	claimPath := filepath.Join(goalDir, "claim.json")
	lockPath := filepath.Join(goalDir, ".claim.lock")
	_ = os.WriteFile(claimPath, []byte(`{"agent":"@antigravity","claimed_at":"2026-10-18T12:00:00Z"}`), 0644)
	_ = os.WriteFile(lockPath, []byte("lock"), 0644)
	_ = SaveCharter(dir, &GoalCharter{GoalSlug: "claim-retention", Agent: "antigravity"})

	evFile := filepath.Join(dir, "evidence.json")
	_ = os.WriteFile(evFile, []byte(`{"facts":[{"id":"f1","passed":true}]}`), 0644)

	// Certify goal before commit closure
	_, err := CertifyGoal(dir, "claim-retention", evFile, "@agent", "all passed")
	if err != nil {
		t.Fatalf("CertifyGoal failed: %v", err)
	}

	// Verify claim.json and charter are retained, while .claim.lock is removed
	if !fileExists(claimPath) {
		t.Errorf("expected claim.json to be retained when no closing commit exists")
	}
	if fileExists(lockPath) {
		t.Errorf("expected .claim.lock to be released")
	}
	if !fileExists(CharterPath(dir, "claim-retention")) {
		t.Errorf("expected charter to be retained when no closing commit exists")
	}

	// Now make closing commit
	_ = os.WriteFile(filepath.Join(dir, "dummy.txt"), []byte("done"), 0644)
	_ = exec.Command("git", "-C", dir, "add", ".").Run()
	_ = exec.Command("git", "-C", dir, "commit", "-m", "fix: implement claim retention [goal: claim-retention]").Run()

	if !HasVerifiedCommitClosure(dir, "claim-retention") {
		t.Fatalf("expected HasVerifiedCommitClosure to be true after commit")
	}

	// Now releaseClaimLockIfClosed should clear claim.json and charter
	releaseClaimLockIfClosed(dir, goalDir, "claim-retention")
	if fileExists(claimPath) {
		t.Errorf("expected claim.json to be removed after commit closure")
	}
	if fileExists(CharterPath(dir, "claim-retention")) {
		t.Errorf("expected charter to be removed after commit closure")
	}
}

func TestCertifyGoalScrubsAbsoluteEvidencePath(t *testing.T) {
	dir := t.TempDir()
	slug := "scrub-ev-path"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Scrub Evidence Path\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}
	absEvPath := filepath.Join(goalDir, "evidence.json")
	if err := os.WriteFile(absEvPath, []byte(`{"facts":[{"id":"f1","passed":true,"verified":true}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	cert, err := CertifyGoal(dir, slug, absEvPath, "reviewer", "scrub check")
	if err != nil {
		t.Fatalf("CertifyGoal failed: %v", err)
	}
	wantRel := filepath.ToSlash(filepath.Join("goals", slug, "evidence.json"))
	if cert.EvidenceFile != wantRel {
		t.Fatalf("cert.EvidenceFile = %q, want relative %q", cert.EvidenceFile, wantRel)
	}
	if filepath.IsAbs(cert.EvidenceFile) || strings.Contains(cert.EvidenceFile, dir) {
		t.Fatalf("cert.EvidenceFile leaked absolute host path: %q", cert.EvidenceFile)
	}
}

func TestCertifyGoalRejectsSameSessionAgentSpoofing(t *testing.T) {
	dir := t.TempDir()
	slug := "session-spoof-check"
	goalDir := filepath.Join(dir, "goals", slug)
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal: Session Spoof Check\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte("# Plan\n- [x] Done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte("# Facts\n"), 0644); err != nil {
		t.Fatal(err)
	}
	claimJSON := `{"agent":"implementer","claimed_by":"implementer","session_id":"sess-abc","claimed_at":"2026-11-03T08:00:00Z"}`
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(claimJSON), 0644); err != nil {
		t.Fatal(err)
	}
	evPath := filepath.Join(goalDir, "evidence.json")
	if err := os.WriteFile(evPath, []byte(`{"facts":[{"id":"f1","passed":true,"verified":true}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Same session ("sess-abc") spoofing a different --agent="reviewer" must be rejected
	_, err := CertifyGoalWithOptions(dir, slug, evPath, "reviewer", "", CertifyOptions{SessionID: "sess-abc"})
	if err == nil || !strings.Contains(err.Error(), "self-audit prohibited: session bleed detected: reviewer (reviewer) shares session (sess-abc) with producer (implementer)") {
		t.Fatalf("expected session bleed rejection when reviewer shares session with producer, got: %v", err)
	}

	// 2. Distinct reviewer session ("sess-xyz") with --agent="reviewer" must succeed
	cert, err := CertifyGoalWithOptions(dir, slug, evPath, "reviewer", "", CertifyOptions{SessionID: "sess-xyz"})
	if err != nil {
		t.Fatalf("expected distinct reviewer session to succeed, got: %v", err)
	}
	if !cert.Verified {
		t.Fatalf("expected cert.Verified=true")
	}
}
