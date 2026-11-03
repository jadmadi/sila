package goals

import (
	"path/filepath"
	"strings"
	"testing"
)

func mustCreateTestGoal(t *testing.T, root, slug string) {
	t.Helper()
	indep := true
	if _, err := CreateGoal(CreateGoalParams{
		RootDir:     root,
		Slug:        slug,
		Title:       slug,
		Description: "session binding test goal",
		Independent: &indep,
	}); err != nil {
		t.Fatalf("create goal %s: %v", slug, err)
	}
}

// A focused session must be discoverable by lookup and by audit filter, must
// refuse an implicit move, and must move deliberately with force while clearing
// the old binding. Feedback fb_705b62fdffa32b24.
func TestFocusGoalBindsSessionDeterministically(t *testing.T) {
	root := t.TempDir()
	mustCreateTestGoal(t, root, "focus-target")
	mustCreateTestGoal(t, root, "focus-other")

	claim, err := FocusGoal(root, "focus-target", "agent-a", "working on it", "ses-1", false)
	if err != nil {
		t.Fatalf("focus target: %v", err)
	}
	if claim.SessionID != "ses-1" {
		t.Fatalf("session_id = %q, want ses-1", claim.SessionID)
	}
	if claim.ClaimedBy != "agent-a" {
		t.Fatalf("claimed_by = %q, want agent-a", claim.ClaimedBy)
	}

	if got := SessionBoundGoal(root, "ses-1"); got != "focus-target" {
		t.Fatalf("SessionBoundGoal = %q, want focus-target", got)
	}
	if got := SessionBoundGoal(root, "ses-unknown"); got != "" {
		t.Fatalf("SessionBoundGoal for unknown session = %q, want empty", got)
	}

	res, err := ScanGoals(root)
	if err != nil {
		t.Fatalf("scan goals: %v", err)
	}
	matched := GoalsBySession(res, "ses-1")
	if len(matched) != 1 || matched[0].Slug != "focus-target" {
		t.Fatalf("GoalsBySession = %+v, want only focus-target", matched)
	}
	if got := GoalsBySession(res, "ses-unknown"); len(got) != 0 {
		t.Fatalf("GoalsBySession for unknown session = %d goals, want 0", len(got))
	}

	// An idempotent re-claim without --session preserves the binding.
	refreshed, err := ClaimGoal(root, "focus-target", "agent-a", "", false)
	if err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if refreshed.SessionID != "ses-1" {
		t.Fatalf("re-claim session_id = %q, want ses-1 preserved", refreshed.SessionID)
	}

	// Moving the session to another goal without force is refused and names the holder.
	_, err = FocusGoal(root, "focus-other", "agent-a", "", "ses-1", false)
	if err == nil {
		t.Fatal("expected refusal when the session is bound to another goal")
	}
	if !strings.Contains(err.Error(), "already focused on goal") || !strings.Contains(err.Error(), "focus-target") {
		t.Fatalf("refusal error = %q, want it to name focus-target", err)
	}

	// Force moves the binding and clears the old claim's session.
	movedClaim, err := FocusGoal(root, "focus-other", "agent-a", "", "ses-1", true)
	if err != nil {
		t.Fatalf("force focus: %v", err)
	}
	if movedClaim.SessionID != "ses-1" {
		t.Fatalf("moved session_id = %q, want ses-1", movedClaim.SessionID)
	}
	if got := SessionBoundGoal(root, "ses-1"); got != "focus-other" {
		t.Fatalf("SessionBoundGoal after move = %q, want focus-other", got)
	}
	old, err := readClaim(filepath.Join(root, "goals", "focus-target", "claim.json"))
	if err != nil {
		t.Fatalf("read old claim: %v", err)
	}
	if old.SessionID != "" {
		t.Fatalf("old claim session_id = %q, want cleared", old.SessionID)
	}

	// A foreign agent cannot take over the claim carrying the binding.
	if _, err := FocusGoal(root, "focus-other", "agent-b", "", "ses-1", false); err == nil {
		t.Fatal("expected refusal when another agent holds the claim")
	} else if !strings.Contains(err.Error(), "already claimed by") {
		t.Fatalf("foreign agent error = %q, want it to name the holder", err)
	}

	// Focus requires a session id.
	if _, err := FocusGoal(root, "focus-other", "agent-a", "", "", false); err == nil {
		t.Fatal("expected error when session id is empty")
	}
}

func TestClaimThenFocusOrderBindsSessionAndRefusesForeignAgent(t *testing.T) {
	root := t.TempDir()
	mustCreateTestGoal(t, root, "sila-onboarding")

	if _, err := ClaimGoal(root, "sila-onboarding", "opencode", "", false); err != nil {
		t.Fatalf("claim goal: %v", err)
	}

	// AC-F3-a: claim then focus with the same --agent (and when focus omits --agent) binds the session.
	claim, err := FocusGoal(root, "sila-onboarding", "", "", "sess-db2a7f53c5ac1dc6a1d4a34e33963505", false)
	if err != nil {
		t.Fatalf("focus after claim without explicit agent failed: %v", err)
	}
	if claim.SessionID != "sess-db2a7f53c5ac1dc6a1d4a34e33963505" || claim.ClaimedBy != "opencode" {
		t.Fatalf("unexpected focused claim: %+v", claim)
	}

	claimSame, err := FocusGoal(root, "sila-onboarding", "opencode", "", "sess-db2a7f53c5ac1dc6a1d4a34e33963505", false)
	if err != nil {
		t.Fatalf("focus after claim with same agent failed: %v", err)
	}
	if claimSame.SessionID != "sess-db2a7f53c5ac1dc6a1d4a34e33963505" {
		t.Fatalf("unexpected focused session: %+v", claimSame)
	}

	// AC-F3-b: a different agent's focus is still refused without --force.
	if _, err := FocusGoal(root, "sila-onboarding", "other-agent", "", "sess-other", false); err == nil || !strings.Contains(err.Error(), "already claimed by") {
		t.Fatalf("expected foreign agent focus to be refused without --force, got: %v", err)
	}
}
