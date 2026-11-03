package goals

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// writeStaleClaim writes a claim.json whose process is dead and whose last
// update is older than the stale grace period.
func writeStaleClaim(t *testing.T, root, slug, agent string, age time.Duration) {
	t.Helper()
	goalDir := filepath.Join(root, "goals", slug)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	claim := GoalClaim{
		GoalSlug:  slug,
		ClaimedBy: agent,
		Agent:     agent,
		ClaimedAt: time.Now().UTC().Add(-age),
		UpdatedAt: time.Now().UTC().Add(-age),
		Hostname:  host,
		PID:       99999999, // never a live process
		Status:    "in_progress",
	}
	data, err := json.MarshalIndent(claim, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(filepath.Join(goalDir, "claim.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseStaleClaimsRespectsAgeAndLiveProcesses(t *testing.T) {
	root := t.TempDir()
	writeStaleClaim(t, root, "old-stale", "opencode", 5*24*time.Hour)
	writeStaleClaim(t, root, "fresh-stale", "opencode", 2*time.Hour)

	// Older-than 72h releases only the old claim.
	preview, err := ReleaseStaleClaims(root, 72*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 1 || preview[0].Goal != "old-stale" {
		t.Fatalf("preview = %+v, want only old-stale", preview)
	}
	if preview[0].Reason == "" {
		t.Fatal("stale preview is missing a reason")
	}
	if _, err := os.Stat(filepath.Join(root, "goals", "old-stale", "claim.json")); err != nil {
		t.Fatal("dry run must not remove the claim file")
	}

	released, err := ReleaseStaleClaims(root, 72*time.Hour, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(released) != 1 || released[0].Goal != "old-stale" {
		t.Fatalf("released = %+v, want only old-stale", released)
	}
	if _, err := os.Stat(filepath.Join(root, "goals", "old-stale", "claim.json")); !os.IsNotExist(err) {
		t.Fatal("released claim file still exists")
	}
	if _, err := os.Stat(filepath.Join(root, "goals", "fresh-stale", "claim.json")); err != nil {
		t.Fatal("claim under the age threshold was removed")
	}
}

func TestPruneClosedClaimsRemovesOnlyClosed(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("config", "commit.gpgsign", "false")

	indep := true
	for _, slug := range []string{"closed-goal", "open-goal"} {
		if _, err := CreateGoal(CreateGoalParams{RootDir: root, Slug: slug, Title: slug, Description: "d", Independent: &indep}); err != nil {
			t.Fatal(err)
		}
		writeStaleClaim(t, root, slug, "opencode", time.Hour)
	}
	// One commit closes closed-goal; open-goal stays pending.
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "feat: close it (goals/closed-goal/goal.md)")

	preview, err := PruneClosedClaims(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 1 || preview[0] != filepath.Join("goals", "closed-goal", "claim.json") {
		t.Fatalf("preview = %v, want closed-goal only", preview)
	}
	if _, err := os.Stat(filepath.Join(root, "goals", "closed-goal", "claim.json")); err != nil {
		t.Fatal("dry run must not remove the claim")
	}

	removed, err := PruneClosedClaims(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v, want one path", removed)
	}
	if _, err := os.Stat(filepath.Join(root, "goals", "closed-goal", "claim.json")); !os.IsNotExist(err) {
		t.Fatal("closed-goal claim still exists after prune")
	}
	if _, err := os.Stat(filepath.Join(root, "goals", "open-goal", "claim.json")); err != nil {
		t.Fatal("open goal claim must survive prune")
	}
}
