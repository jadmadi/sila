package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestClearClosedClaims pins the reconciliation promise: a claim record on an
// implemented or superseded goal is removed, while an active claim and the goal
// packages themselves are untouched.
func TestClearClosedClaims(t *testing.T) {
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
	for _, slug := range []string{"done", "closed", "live"} {
		write("goals/"+slug+"/claim.json", `{"goal_slug":"`+slug+`"}`)
		write("goals/"+slug+"/goal.md", "# Goal: "+slug)
	}
	write("goals/finished/goal.md", "# Goal: finished")

	res := &AuditResult{Goals: []Goal{
		{Slug: "done", Status: "implemented", Claim: &GoalClaim{GoalSlug: "done"}},
		{Slug: "closed", Status: "superseded", Claim: &GoalClaim{GoalSlug: "closed"}},
		{Slug: "live", Status: "claimed", Claim: &GoalClaim{GoalSlug: "live"}},
		{Slug: "finished", Status: "implemented"},
		{Slug: "pending-no-claim", Status: "pending"},
	}}

	cleared, err := ClearClosedClaims(root, res)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 2 {
		t.Fatalf("cleared = %v, want the two closed claim records", cleared)
	}
	for _, rel := range cleared {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", rel)
		}
	}
	for _, kept := range []string{"goals/live/claim.json", "goals/done/goal.md", "goals/live/goal.md"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Errorf("%s should have survived: %v", kept, err)
		}
	}

	if cleared, err := ClearClosedClaims(root, nil); err != nil || cleared != nil {
		t.Errorf("nil audit = %v, %v; want a no-op", cleared, err)
	}
}

func TestGoalStopAtTaskSetAndClear(t *testing.T) {
	root := t.TempDir()
	slug := "stop-goal"
	goalDir := filepath.Join(root, "goals", slug)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claim := GoalClaim{GoalSlug: slug, ClaimedBy: "agent", Agent: "agent", ClaimedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	data, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	set, err := SetGoalStopAtTask(root, slug, "T-002")
	if err != nil {
		t.Fatal(err)
	}
	if set.StopAtTaskID != "T-002" {
		t.Fatalf("stop_at_task_id = %q, want T-002", set.StopAtTaskID)
	}
	cleared, err := SetGoalStopAtTask(root, slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if cleared.StopAtTaskID != "" {
		t.Fatalf("stop_at_task_id = %q after clear, want empty", cleared.StopAtTaskID)
	}
}
