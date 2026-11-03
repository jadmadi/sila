package loaders

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitLoader(t *testing.T) {
	tmp := t.TempDir()

	// Initialize git repo in tmp
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v, output: %s", args, err, out)
		}
	}

	run("init")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")

	// Create a commit
	testFile := filepath.Join(tmp, "hello.txt")
	_ = os.WriteFile(testFile, []byte("hello git loader"), 0644)
	run("add", "hello.txt")
	run("commit", "-m", "Initial test commit for git loader")

	loader := NewGitLoader(tmp)
	if loader.Name() != "git" {
		t.Fatalf("expected loader name git, got %s", loader.Name())
	}

	cnt, err := loader.Count(&tmp)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected count 1, got %d", cnt)
	}

	msgs, wm, err := loader.Load("", &tmp)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "commit" || msgs[0].Tool != "git" {
		t.Fatalf("unexpected message format: %+v", msgs[0])
	}
	if wm == "" {
		t.Fatalf("expected non-empty watermark")
	}

	// Incremental load with watermark should return 0 new messages
	msgs2, _, err := loader.Load(wm, &tmp)
	if err != nil {
		t.Fatalf("incremental Load failed: %v", err)
	}
	if len(msgs2) != 0 {
		t.Fatalf("expected 0 new messages after watermark, got %d", len(msgs2))
	}
}
