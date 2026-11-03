package assess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScrubEightChannelsSyntheticCommitIsolation(t *testing.T) {
	tmp := t.TempDir()
	sandboxDir := filepath.Join(tmp, "sandbox")
	homeDir := filepath.Join(tmp, "home")
	cacheDir := filepath.Join(tmp, "cache")
	_ = os.MkdirAll(sandboxDir, 0755)
	_ = os.MkdirAll(homeDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	// 1. Legitimate files that SHOULD be committed into synthetic git baseline
	legitSource := filepath.Join(sandboxDir, "main.go")
	_ = os.WriteFile(legitSource, []byte("package main\n\nfunc main() {}\n"), 0644)
	legitPkg := filepath.Join(sandboxDir, "pkg", "core.go")
	_ = os.MkdirAll(filepath.Dir(legitPkg), 0755)
	_ = os.WriteFile(legitPkg, []byte("package pkg\n"), 0644)

	// 2. Channel 2: Scratchpads
	scratchDir := filepath.Join(sandboxDir, "scratch")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.WriteFile(filepath.Join(scratchDir, "scratchpad.txt"), []byte("scratch note"), 0644)
	tmpDir := filepath.Join(sandboxDir, ".tmp")
	_ = os.MkdirAll(tmpDir, 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "temp.log"), []byte("temp"), 0644)

	// 3. Channel 4: Test mocks
	mockFile := filepath.Join(sandboxDir, "service.mock.go")
	_ = os.WriteFile(mockFile, []byte("package main"), 0644)
	mockFile2 := filepath.Join(sandboxDir, "store_mock.go")
	_ = os.WriteFile(mockFile2, []byte("package main"), 0644)

	// 4. Channel 5: Shell history
	histFile := filepath.Join(homeDir, ".bash_history")
	_ = os.WriteFile(histFile, []byte("history command"), 0644)

	// 5. Channel 6: Transcripts
	transcriptDir := filepath.Join(sandboxDir, ".system_generated")
	_ = os.MkdirAll(transcriptDir, 0755)
	_ = os.WriteFile(filepath.Join(transcriptDir, "transcript.jsonl"), []byte("{}"), 0644)
	agentDir := filepath.Join(sandboxDir, ".agents")
	_ = os.MkdirAll(agentDir, 0755)
	_ = os.WriteFile(filepath.Join(agentDir, "agent.log"), []byte("log"), 0644)

	// 6. Channel 7: Defect trails
	defectFile := filepath.Join(sandboxDir, "defect-dossier.json")
	_ = os.WriteFile(defectFile, []byte("{}"), 0644)
	gateLog := filepath.Join(sandboxDir, "gate-test.log")
	_ = os.WriteFile(gateLog, []byte("failure log"), 0644)

	fixedEpoch := time.Unix(1700000000, 0).UTC()
	err := ScrubEightChannels(sandboxDir, homeDir, cacheDir, fixedEpoch)
	if err != nil {
		t.Fatalf("ScrubEightChannels failed: %v", err)
	}

	// Assert files are removed from the filesystem
	if _, err := os.Stat(scratchDir); !os.IsNotExist(err) {
		t.Errorf("scratch directory still exists on disk")
	}
	if _, err := os.Stat(mockFile); !os.IsNotExist(err) {
		t.Errorf("mock file still exists on disk")
	}
	if _, err := os.Stat(transcriptDir); !os.IsNotExist(err) {
		t.Errorf("transcript directory still exists on disk")
	}
	if _, err := os.Stat(defectFile); !os.IsNotExist(err) {
		t.Errorf("defect dossier still exists on disk")
	}

	// Inspect synthetic git commit tree: must contain ZERO scrubbed files
	gitCmd := exec.Command("git", "ls-tree", "-r", "--name-only", "HEAD")
	gitCmd.Dir = sandboxDir
	out, err := gitCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-tree failed: %v, out: %s", err, string(out))
	}

	committedFiles := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, f := range committedFiles {
		if strings.Contains(f, "scratch") ||
			strings.Contains(f, "mock") ||
			strings.Contains(f, ".system_generated") ||
			strings.Contains(f, ".agents") ||
			strings.Contains(f, "defect-dossier") ||
			strings.Contains(f, "gate-test.log") ||
			strings.Contains(f, ".tmp") {
			t.Errorf("synthetic git commit contains scrubbed file: %s", f)
		}
	}

	// Verify legitimate files are in the git commit
	hasMain := false
	for _, f := range committedFiles {
		if f == "main.go" {
			hasMain = true
			break
		}
	}
	if !hasMain {
		t.Errorf("synthetic git commit missing legitimate source main.go; committed files: %v", committedFiles)
	}
}
