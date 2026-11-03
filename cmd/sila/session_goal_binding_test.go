package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

// sila goals focus records a session binding and sila goals active --session
// reads exactly that goal back. Feedback fb_705b62fdffa32b24.
func TestGoalsFocusAndSessionQueryCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "session_binding.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(tmp); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)

	runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "Session Target", "Description", "", "", 1,
		true, false, false, false, false, false, false, false, false, false, false, false, true,
		"", "", "", false, []string{"create", "session-target"})

	focusOut := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false, true, false, false, false, true,
			"", "", "", false, []string{"focus", "session-target"}, goalsRunOptions{session: "ses-cli-1"})
	})

	var focused struct {
		Slug    string           `json:"slug"`
		Focused bool             `json:"focused"`
		Claim   *goals.GoalClaim `json:"claim"`
	}
	if err := json.Unmarshal([]byte(focusOut), &focused); err != nil {
		t.Fatalf("focus output is not JSON: %q: %v", focusOut, err)
	}
	if !focused.Focused || focused.Slug != "session-target" {
		t.Fatalf("focus payload = %+v, want focused session-target", focused)
	}
	if focused.Claim == nil || focused.Claim.SessionID != "ses-cli-1" {
		t.Fatalf("focus claim = %+v, want session_id ses-cli-1", focused.Claim)
	}

	sessionOut := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false, true, false, false, false, true,
			"", "", "", false, []string{"active"}, goalsRunOptions{session: "ses-cli-1"})
	})

	var payload struct {
		Project string       `json:"project"`
		Filter  string       `json:"filter"`
		Session string       `json:"session"`
		Count   int          `json:"count"`
		Goals   []goals.Goal `json:"goals"`
	}
	if err := json.Unmarshal([]byte(sessionOut), &payload); err != nil {
		t.Fatalf("session output is not JSON: %q: %v", sessionOut, err)
	}
	if payload.Session != "ses-cli-1" || payload.Filter != "claimed" {
		t.Fatalf("payload = %+v, want session ses-cli-1 and claimed filter", payload)
	}
	if payload.Count != 1 || len(payload.Goals) != 1 || payload.Goals[0].Slug != "session-target" {
		t.Fatalf("payload goals = %+v, want only session-target", payload.Goals)
	}
	if payload.Goals[0].Claim == nil || payload.Goals[0].Claim.SessionID != "ses-cli-1" {
		t.Fatalf("goal claim = %+v, want session_id ses-cli-1", payload.Goals[0].Claim)
	}

	unknownOut := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false, true, false, false, false, true,
			"", "", "", false, []string{"active"}, goalsRunOptions{session: "ses-unknown"})
	})
	var unknown struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(unknownOut), &unknown); err != nil {
		t.Fatalf("unknown session output is not JSON: %q: %v", unknownOut, err)
	}
	if unknown.Count != 0 {
		t.Fatalf("unknown session count = %d, want 0", unknown.Count)
	}
}

func TestClaimThenFocusCLIAndSalamLoop(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "claim_focus.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(tmp); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	runGoals(ui, dbPath, tmp, "opencode", "", "", "", false, false, "", "Onboarding", "Description", "", "", 1,
		true, false, false, false, false, false, false, false, false, false, false, false, true,
		"", "", "", false, []string{"create", "sila-onboarding"})

	// Step 1: claim with --agent=opencode
	runGoals(ui, dbPath, tmp, "opencode", "", "", "", false, false, "", "", "", "", "", 0,
		false, false, false, false, false, false, false, false, false, false, false, false, true,
		"", "", "", false, []string{"claim", "sila-onboarding"})

	// Step 2: focus without --agent (and with --agent=opencode) succeeds and binds session
	focusOut := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false, true, false, false, false, true,
			"", "", "", false, []string{"focus", "sila-onboarding"}, goalsRunOptions{session: "sess-db2a7f53c5ac1dc6a1d4a34e33963505"})
	})
	if !strings.Contains(focusOut, `"focused": true`) || !strings.Contains(focusOut, `"session_id": "sess-db2a7f53c5ac1dc6a1d4a34e33963505"`) {
		t.Fatalf("expected focus after claim to succeed, got: %s", focusOut)
	}

	// Step 3: foreign agent focus is refused without --force
	foreignOut := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "foreign-agent", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false, true, false, false, false, true,
			"", "", "", false, []string{"focus", "sila-onboarding"}, goalsRunOptions{session: "sess-foreign"})
	})
	if !strings.Contains(foreignOut, `"focused": false`) || !strings.Contains(foreignOut, "already claimed by") {
		t.Fatalf("expected foreign agent focus to be refused, got: %s", foreignOut)
	}

	// Step 4: SalamLoop step 2 matches the accepted order
	if len(salam.SalamLoop) < 2 || !strings.Contains(salam.SalamLoop[1], "sila goals claim <slug> --agent=<name>") || !strings.Contains(salam.SalamLoop[1], "sila goals focus <slug> --session=<id> --agent=<name>") {
		t.Fatalf("SalamLoop[1] does not match accepted claim-then-focus order: %q", salam.SalamLoop[1])
	}
}
