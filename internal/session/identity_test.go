package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePointer(t *testing.T, path, sessionID, projectID string, written time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := `{"session_id":"` + sessionID + `","project_id":"` + projectID + `","harness":"codex","written_at":"` + written.UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveIdentityPrecedence(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("SILA_SESSION_ID", "")
	t.Setenv("SILA_ACTIVE_GOAL", "")
	t.Setenv("SILA_CLAIM_ID", "")
	id, err := ResolveIdentity("flag-id", "demo", cwd)
	if err != nil || id.SessionID != "flag-id" || id.Source != "flag" {
		t.Fatalf("flag precedence: %+v err=%v", id, err)
	}
	t.Setenv("SILA_SESSION_ID", "env-id")
	id, err = ResolveIdentity("", "demo", cwd)
	if err != nil || id.SessionID != "env-id" || id.Source != "env" {
		t.Fatalf("env precedence: %+v err=%v", id, err)
	}
	t.Setenv("SILA_SESSION_ID", "")
	writePointer(t, ProjectCurrentPath(cwd), "project-id", "demo", time.Now())
	id, err = ResolveIdentity("", "demo", cwd)
	if err != nil || id.SessionID != "project-id" || id.Source != "current-file" {
		t.Fatalf("project current precedence: %+v err=%v", id, err)
	}
	// A matching global pointer is the fallback when the project file is stale.
	writePointer(t, ProjectCurrentPath(cwd), "stale-id", "demo", time.Now().Add(-48*time.Hour))
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	writePointer(t, GlobalCurrentPath("demo"), "global-id", "demo", time.Now())
	id, err = ResolveIdentity("", "demo", cwd)
	if err != nil || id.SessionID != "global-id" || id.Source != "global-current" {
		t.Fatalf("global current precedence: %+v err=%v", id, err)
	}
	if err := os.Remove(GlobalCurrentPath("demo")); err != nil {
		t.Fatal(err)
	}
	id, err = ResolveIdentity("", "demo", cwd)
	if err != nil || id.SessionID != "" || id.Source != "none" {
		t.Fatalf("none precedence: %+v err=%v", id, err)
	}
}

func TestResolveIdentityIgnoresInvalidPointers(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("SILA_SESSION_ID", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	writePointer(t, ProjectCurrentPath(cwd), "wrong-project", "other", time.Now())
	if id, _ := ResolveIdentity("", "demo", cwd); id.SessionID != "" {
		t.Fatalf("wrong-project pointer was accepted: %+v", id)
	}
	writePointer(t, ProjectCurrentPath(cwd), "stale", "demo", time.Now().Add(-25*time.Hour))
	if id, _ := ResolveIdentity("", "demo", cwd); id.SessionID != "" {
		t.Fatalf("stale pointer was accepted: %+v", id)
	}
	if err := os.WriteFile(ProjectCurrentPath(cwd), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if id, _ := ResolveIdentity("", "demo", cwd); id.SessionID != "" {
		t.Fatalf("malformed pointer was accepted: %+v", id)
	}
}

func TestWriteAndClearCurrent(t *testing.T) {
	cwd := t.TempDir()
	id := Identity{SessionID: "s1", ProjectID: "demo", Harness: "test"}
	if err := WriteCurrent(cwd, id); err != nil {
		t.Fatal(err)
	}
	path := ProjectCurrentPath(cwd)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("current mode = %o, want 600", fi.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("session dir mode = %o, want 700", dir.Mode().Perm())
	}
	if err := ClearCurrent(cwd, "demo", "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a foreign session removed the pointer")
	}
	if err := ClearCurrent(cwd, "demo", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("matching session did not clear the pointer")
	}
}

func gitSessionFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "one")
	mainHead := run("rev-parse", "HEAD")
	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "two")
	featureHead := run("rev-parse", "HEAD")
	return root, mainHead, featureHead
}

func TestSnapshotGitAndResumeContext(t *testing.T) {
	root, mainHead, featureHead := gitSessionFixture(t)
	current := SnapshotGit(root)
	if current.Branch != "feature" || current.Head != featureHead || current.Detached {
		t.Fatalf("snapshot = %+v", current)
	}
	own := GitContext{Branch: "main", Head: mainHead}
	kept, drift := ResumeContext(own, root)
	if kept.Branch != "main" || kept.Head != mainHead {
		t.Fatalf("resume adopted sibling branch: %+v", kept)
	}
	if !strings.Contains(drift, "main@") || !strings.Contains(drift, "feature@") {
		t.Fatalf("drift not recorded: %q", drift)
	}
	legacy, legacyDrift := ResumeContext(GitContext{}, root)
	if legacy.Branch != "feature" || !strings.Contains(legacyDrift, "no previous snapshot") {
		t.Fatalf("legacy resume = %+v drift=%q", legacy, legacyDrift)
	}
	stale, staleDrift := ResumeContext(GitContext{Branch: "main", Head: strings.Repeat("0", 40)}, root)
	if stale.Branch != "feature" || !strings.Contains(staleDrift, "is gone") {
		t.Fatalf("stale resume = %+v drift=%q", stale, staleDrift)
	}
	if noGit := SnapshotGit(t.TempDir()); !noGit.IsZero() {
		t.Fatalf("non-repository snapshot = %+v", noGit)
	}
}

func TestSnapshotGitDetached(t *testing.T) {
	root, mainHead, _ := gitSessionFixture(t)
	if out, err := exec.Command("git", "-C", root, "checkout", "--detach", mainHead).CombinedOutput(); err != nil {
		t.Fatalf("detach: %v\n%s", err, out)
	}
	ctx := SnapshotGit(root)
	if !ctx.Detached || ctx.Branch != "" || ctx.Head != mainHead {
		t.Fatalf("detached snapshot = %+v", ctx)
	}
}
