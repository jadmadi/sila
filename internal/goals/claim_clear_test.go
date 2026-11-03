package goals

import (
	"os"
	"path/filepath"
	"testing"
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
