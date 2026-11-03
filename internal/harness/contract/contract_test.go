package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func fixtureProject(t *testing.T) (string, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("goals/test-goal/goal.md", "# Goal: Test\n\n## Done Condition\n\n1. Done.\n")
	write("goals/test-goal/plan.md", "# Plan\n\n- [x] **Task one**\n")
	write("goals/test-goal/facts.md", "# Facts\n\n- grounded\n")
	write("goals/test-goal/evidence.md", "# Evidence\n\nVerdict: PASS\n")
	now := time.Now().UTC().Format(time.RFC3339)
	write("goals/test-goal/claim.json", `{"goal_slug":"test-goal","claimed_by":"tester","agent":"tester","session_id":"sess-1","status":"in_progress","claimed_at":"`+now+`","updated_at":"`+now+`"}`)
	st, err := store.Open(filepath.Join(root, "sila.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return root, st, store.ProjectID(root)
}

func baseSignal(root, projectID string) Signal {
	return NormalizeSignal(Signal{
		Harness: "test", SessionID: "sess-1", ProjectID: projectID,
		Cwd: root, Phase: PhaseSessionEnd, SilaGoalSlug: "test-goal",
	})
}

func seedAllowState(t *testing.T, st *store.Store, root, projectID string) {
	t.Helper()
	if err := st.InsertHandoff(store.Handoff{
		ID: "h1", ProjectID: projectID, Agent: "tester", Summary: "done",
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Status: "pending", SessionID: "sess-1",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemoWithProvenance(projectID, "contract memo", "tester", root, []string{"goals/test-goal/plan.md"}, "memo", store.Provenance{SessionRef: "sess-1"}); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateAllowsCompleteGoal(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	seedAllowState(t, st, root, projectID)
	if err := SaveBinding(root, Binding{Harness: "test", SessionID: "sess-1", SilaGoalSlug: "test-goal", Source: "explicit", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d := NewChecker(st, root).Evaluate(baseSignal(root, projectID), EffectivePolicy{Policy: PolicyEnforce})
	if d.Decision != DecisionAllow {
		t.Fatalf("expected allow, got %s: %+v", d.Decision, d.Checks)
	}
}

func TestEvaluateDeniesMissingEvidence(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	seedAllowState(t, st, root, projectID)
	if err := os.Remove(filepath.Join(root, "goals", "test-goal", "evidence.md")); err != nil {
		t.Fatal(err)
	}
	d := NewChecker(st, root).Evaluate(baseSignal(root, projectID), EffectivePolicy{Policy: PolicyEnforce})
	if d.Decision != DecisionDeny || d.ReasonCode != ReasonEvidenceMissing {
		t.Fatalf("expected evidence deny, got %s/%s: %+v", d.Decision, d.ReasonCode, d.Checks)
	}
}

func TestWaiverCoversEvidence(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	seedAllowState(t, st, root, projectID)
	if err := os.Remove(filepath.Join(root, "goals", "test-goal", "evidence.md")); err != nil {
		t.Fatal(err)
	}
	w := &Waiver{Reason: "legacy goal", ExpiresAt: time.Now().Add(time.Hour), Checks: []string{"evidence"}}
	d := NewChecker(st, root).EvaluateWith(baseSignal(root, projectID), EffectivePolicy{Policy: PolicyEnforce}, nil, w)
	if d.Decision != DecisionAllow {
		t.Fatalf("expected waiver allow, got %s: %+v", d.Decision, d.Checks)
	}
}

func TestExpiredBindingDenies(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	seedAllowState(t, st, root, projectID)
	if err := SaveBinding(root, Binding{Harness: "test", SessionID: "sess-1", SilaGoalSlug: "test-goal", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d := NewChecker(st, root).Evaluate(baseSignal(root, projectID), EffectivePolicy{Policy: PolicyEnforce})
	if d.Decision != DecisionDeny || d.ReasonCode != ReasonBindingExpired {
		t.Fatalf("expected expired binding, got %s/%s", d.Decision, d.ReasonCode)
	}
}

func TestGoalMismatchDenies(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	seedAllowState(t, st, root, projectID)
	if err := SaveBinding(root, Binding{Harness: "test", SessionID: "sess-1", SilaGoalSlug: "test-goal", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	sig := baseSignal(root, projectID)
	sig.SilaGoalSlug = "other-goal"
	d := NewChecker(st, root).Evaluate(sig, EffectivePolicy{Policy: PolicyEnforce})
	if d.Decision != DecisionDeny || d.ReasonCode != ReasonGoalMismatch {
		t.Fatalf("expected goal mismatch, got %s/%s", d.Decision, d.ReasonCode)
	}
}

func TestPolicyOffIsSilent(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	d := NewChecker(st, root).Evaluate(baseSignal(root, projectID), EffectivePolicy{Policy: PolicyOff})
	if d.Decision != DecisionAllow || d.ReasonCode != ReasonPolicyOff {
		t.Fatalf("expected policy off allow, got %s/%s", d.Decision, d.ReasonCode)
	}
}

func TestBypassIsVisible(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	d := NewChecker(st, root).Evaluate(baseSignal(root, projectID), EffectivePolicy{Policy: PolicyEnforce, Bypass: true})
	if d.Decision != DecisionAllow || !d.Bypassed || d.ReasonCode != ReasonBypassed {
		t.Fatalf("expected visible bypass, got %+v", d)
	}
}

func TestReceiptIsPrivateAndPersisted(t *testing.T) {
	root, _, projectID := fixtureProject(t)
	sig := baseSignal(root, projectID)
	d := Decision{Decision: DecisionDeny, ReasonCode: ReasonEvidenceMissing, Checks: map[string]CheckResult{"evidence": {Status: CheckFail, Detail: "evidence missing"}}}
	d = RecordDecision(root, sig, EffectivePolicy{Policy: PolicyEnforce}, d, time.Millisecond)
	if d.ReceiptPath == "" {
		t.Fatal("expected receipt path")
	}
	data, err := os.ReadFile(d.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tool_value") || strings.Contains(string(data), root) {
		t.Fatalf("receipt leaked private data: %s", data)
	}
	info, err := os.Stat(d.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
}

func TestSignalValidation(t *testing.T) {
	if err := (Signal{}).Validate(); err == nil {
		t.Fatal("expected missing session error")
	}
	s := baseSignal("/tmp", "proj")
	if err := s.Validate(); err != nil {
		t.Fatalf("valid signal rejected: %v", err)
	}
	s.Phase = "bogus"
	if err := s.Validate(); err == nil {
		t.Fatal("expected unknown phase error")
	}
}

func TestCheckerUsesInjectedClock(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	seedAllowState(t, st, root, projectID)
	if err := SaveBinding(root, Binding{Harness: "test", SessionID: "sess-1", SilaGoalSlug: "test-goal", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	c := NewChecker(st, root)
	c.Now = time.Now().Add(2 * time.Hour)
	d := c.Evaluate(baseSignal(root, projectID), EffectivePolicy{Policy: PolicyEnforce})
	if d.Decision != DecisionDeny || d.ReasonCode != ReasonBindingExpired {
		t.Fatalf("expected clock-driven expiry, got %s/%s", d.Decision, d.ReasonCode)
	}
}

func TestChildSessionCannotHoldClaim(t *testing.T) {
	root, st, projectID := fixtureProject(t)
	seedAllowState(t, st, root, projectID)
	// Point the claim at the child, then present it as a child of another session.
	now := time.Now().UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(root, "goals", "test-goal", "claim.json"), []byte(`{"goal_slug":"test-goal","claimed_by":"tester","agent":"tester","session_id":"sess-child","status":"in_progress","claimed_at":"`+now+`","updated_at":"`+now+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := baseSignal(root, projectID)
	sig.SessionID = "sess-child"
	sig.ParentSessionID = "sess-parent"
	sig.SilaGoalSlug = ""
	d := NewChecker(st, root).Evaluate(sig, EffectivePolicy{Policy: PolicyEnforce})
	if d.ReasonCode != ReasonClaimDuplicate {
		t.Fatalf("expected duplicate claim, got %s: %+v", d.ReasonCode, d.Checks["claim"])
	}
}

func TestBindingPathIsTraversalSafe(t *testing.T) {
	p := BindingPath(t.TempDir(), "../../evil", "../../session")
	if strings.Contains(p, "/../") || strings.HasSuffix(p, "/..") {
		t.Fatalf("path traversal survived: %s", p)
	}
}
