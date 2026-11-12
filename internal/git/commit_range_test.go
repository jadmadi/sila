package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runGitRange(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if len(args) > 0 && (args[0] == "commit" || args[0] == "merge") {
		args = append([]string{"-c", "user.email=test@example.com", "-c", "user.name=test"}, args...)
	}
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func rangeFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	runGitRange(t, dir, "init", "-q")
	runGitRange(t, dir, "config", "user.email", "test@example.com")
	runGitRange(t, dir, "config", "user.name", "test")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "a")
	runGitRange(t, dir, "add", ".")
	runGitRange(t, dir, "commit", "-q", "-m", "first")
	write("b.txt", "b")
	runGitRange(t, dir, "add", ".")
	runGitRange(t, dir, "commit", "-q", "-m", "second")
	runGitRange(t, dir, "checkout", "-q", "-b", "feature")
	write("c.txt", "c")
	runGitRange(t, dir, "add", ".")
	runGitRange(t, dir, "commit", "-q", "-m", "feature")
	runGitRange(t, dir, "checkout", "-q", "-")
	write("d.txt", "d")
	runGitRange(t, dir, "add", ".")
	runGitRange(t, dir, "commit", "-q", "-m", "third")
	runGitRange(t, dir, "merge", "-q", "--no-ff", "feature", "-m", "merge")
	return dir
}

func TestCommitsInRange(t *testing.T) {
	dir := rangeFixture(t)
	commits, err := CommitsInRange(dir, "HEAD~2..HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("commits = %d", len(commits))
	}
	if commits[0].Email == "" || commits[0].SHA == "" || commits[0].Author == "" || commits[0].CommittedAt.IsZero() {
		t.Fatalf("metadata = %+v", commits[0])
	}
	single, err := CommitsInRange(dir, "HEAD")
	if err != nil || len(single) != 1 {
		t.Fatalf("single = %d err=%v", len(single), err)
	}
}

func TestChangedPathsMerge(t *testing.T) {
	dir := rangeFixture(t)
	paths, err := ChangedPaths(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range paths {
		if p == "c.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("merge paths = %v", paths)
	}
	root := runGitRange(t, dir, "rev-list", "--max-parents=0", "HEAD")
	rootPaths, err := ChangedPaths(dir, firstLine(root))
	if err != nil || len(rootPaths) == 0 {
		t.Fatalf("root paths = %v err=%v", rootPaths, err)
	}
}

func firstLine(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			return text[:i]
		}
	}
	return text
}

func TestCommitsInRangeErrors(t *testing.T) {
	dir := rangeFixture(t)
	if _, err := CommitsInRange(dir, "not-a-ref"); !errors.Is(err, ErrCommitRangeInvalid) {
		t.Fatalf("err = %v", err)
	}
	if _, err := CommitsInRange(dir, "a...b"); !errors.Is(err, ErrCommitRangeInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestCommitsInRangeBound(t *testing.T) {
	if testing.Short() {
		t.Skip("creates 201 commits")
	}
	dir := t.TempDir()
	runGitRange(t, dir, "init", "-q")
	runGitRange(t, dir, "config", "user.email", "test@example.com")
	runGitRange(t, dir, "config", "user.name", "test")
	for i := 0; i < 202; i++ {
		runGitRange(t, dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("commit %d", i))
	}
	root := firstLine(runGitRange(t, dir, "rev-list", "--max-parents=0", "HEAD"))
	if _, err := CommitsInRange(dir, root+"..HEAD"); !errors.Is(err, ErrCommitRangeTooLarge) {
		t.Fatalf("err = %v", err)
	}
}
