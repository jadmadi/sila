package sentry

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCalculateACIFromGit_AbsolutePathAndNormalization(t *testing.T) {
	tmp := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Configure git user for commits
	_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@test.local").Run()
	_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()

	subDir := filepath.Join(tmp, "pkg", "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	trackedFile := filepath.Join(subDir, "code.go")
	src := "package sub\n\nfunc MyFunc() int {\n\treturn 42\n}\n"
	if err := os.WriteFile(trackedFile, []byte(src), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Commit file
	_ = exec.Command("git", "-C", tmp, "add", ".").Run()
	_ = exec.Command("git", "-C", tmp, "commit", "-m", "init").Run()

	t.Run("Unchanged file with absolute path returns 0.0", func(t *testing.T) {
		aci, err := CalculateACIFromGit(tmp, trackedFile, "MyFunc")
		if err != nil {
			t.Fatalf("CalculateACIFromGit error: %v", err)
		}
		if aci != 0.0 {
			t.Fatalf("expected ACI 0.0 for unchanged file, got %f", aci)
		}
	})

	t.Run("Modified symbol returns positive ACI", func(t *testing.T) {
		modifiedSrc := "package sub\n\nfunc MyFunc() int {\n\t// modified\n\tx := 10\n\treturn 42 + x\n}\n"
		if err := os.WriteFile(trackedFile, []byte(modifiedSrc), 0644); err != nil {
			t.Fatalf("write modified: %v", err)
		}
		defer func() {
			_ = os.WriteFile(trackedFile, []byte(src), 0644)
		}()

		aci, err := CalculateACIFromGit(tmp, trackedFile, "MyFunc")
		if err != nil {
			t.Fatalf("CalculateACIFromGit error: %v", err)
		}
		if aci < 0.20 {
			t.Fatalf("expected ACI >= 0.20 for modified symbol, got %f", aci)
		}
	})

	t.Run("Newly created untracked file returns 1.0", func(t *testing.T) {
		newFile := filepath.Join(subDir, "new.go")
		if err := os.WriteFile(newFile, []byte("package sub\n\nfunc NewFunc() {}\n"), 0644); err != nil {
			t.Fatalf("write new file: %v", err)
		}

		aci, err := CalculateACIFromGit(tmp, newFile, "NewFunc")
		if err != nil {
			t.Fatalf("CalculateACIFromGit error: %v", err)
		}
		if aci != 1.0 {
			t.Fatalf("expected ACI 1.0 for new file, got %f", aci)
		}
	})

	t.Run("Nonexistent file returns error", func(t *testing.T) {
		_, err := CalculateACIFromGit(tmp, filepath.Join(tmp, "nonexistent.go"), "MyFunc")
		if err == nil {
			t.Fatal("expected error for nonexistent file, got nil")
		}
	})

	t.Run("File outside repo returns error", func(t *testing.T) {
		otherTmp := t.TempDir()
		otherFile := filepath.Join(otherTmp, "other.go")
		_ = os.WriteFile(otherFile, []byte("package other"), 0644)

		_, err := CalculateACIFromGit(tmp, otherFile, "MyFunc")
		if err == nil {
			t.Fatal("expected error for file outside repo, got nil")
		}
	})
}

// TestCalculateACI_AbsolutePathUnchanged tests that an unchanged tracked file passed
// via absolute path returns <= 0.10 (specifically 0.0, not 1.0) (AC-002).
func TestCalculateACI_AbsolutePathUnchanged(t *testing.T) {
	tmp := t.TempDir()

	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@test.local").Run()
	_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()

	subDir := filepath.Join(tmp, "pkg", "sub")
	_ = os.MkdirAll(subDir, 0755)

	trackedFile := filepath.Join(subDir, "code.go")
	src := "package sub\n\nfunc MyFunc() int {\n\treturn 42\n}\n"
	if err := os.WriteFile(trackedFile, []byte(src), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_ = exec.Command("git", "-C", tmp, "add", ".").Run()
	_ = exec.Command("git", "-C", tmp, "commit", "-m", "init").Run()

	aci, err := CalculateACIFromGit(tmp, trackedFile, "MyFunc")
	if err != nil {
		t.Fatalf("CalculateACIFromGit failed: %v", err)
	}

	if aci > 0.10 {
		t.Fatalf("expected ACI <= 0.10 for unchanged file with absolute path, got %f", aci)
	}
}
