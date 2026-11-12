package git

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrForeignHook reports an existing pre-commit hook that sila did not write.
// sila must never overwrite another tool's hook.
var ErrForeignHook = errors.New("existing pre-commit hook is not sila-managed")

// GatePipelineResult encapsulates the outcome of a pre-push or remote gate run.
type GatePipelineResult struct {
	Branch      string        `json:"branch"`
	TargetRef   string        `json:"target_ref"`
	Forwarded   bool          `json:"forwarded"`
	Clean       bool          `json:"clean"`
	Duration    time.Duration `json:"duration"`
	Worktree    string        `json:"worktree"`
	Checks      []string      `json:"checks"`
	Output      string        `json:"output"`
	ErrorString string        `json:"error,omitempty"`
}

// CurrentBranch returns the currently active branch name or "HEAD" if detached.
func (m *Manager) CurrentBranch() string {
	cmd := exec.Command("git", "-C", m.repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "HEAD"
	}
	b := strings.TrimSpace(string(out))
	if b == "" {
		return "HEAD"
	}
	return b
}

// RunGatePipeline executes an isolated quality gate in a disposable worktree,
// and if 100% clean, optionally forwards the push to the target remote.
func (m *Manager) RunGatePipeline(branch, forwardTarget string, autoForward bool) (*GatePipelineResult, error) {
	if branch == "" {
		branch = m.CurrentBranch()
	}
	if forwardTarget == "" {
		forwardTarget = "origin"
	}

	res := &GatePipelineResult{
		Branch:    branch,
		TargetRef: forwardTarget,
		Checks:    []string{},
	}

	start := time.Now()
	name := fmt.Sprintf("gate-%d", time.Now().UnixNano()%1000000)

	// Create disposable worktree on the given branch/ref
	wt, err := m.Create(name, branch)
	if err != nil {
		return nil, fmt.Errorf("create gate worktree: %w", err)
	}
	res.Worktree = wt.Path

	defer func() {
		_ = m.Remove(name, true)
	}()

	// 1. Run sila gate in the worktree
	silaBin := os.Getenv("SILA_BIN")
	if silaBin == "" {
		silaBin = "sila"
		if exe, err := os.Executable(); err == nil && exe != "" && strings.Contains(filepath.Base(exe), "sila") && !strings.Contains(exe, ".test") {
			silaBin = exe
		}
	}
	gateCmd := exec.Command(silaBin, "gate")
	gateCmd.Dir = wt.Path
	outBuf := &strings.Builder{}
	gateCmd.Stdout = outBuf
	gateCmd.Stderr = outBuf

	err = gateCmd.Run()
	res.Output = outBuf.String()
	res.Duration = time.Since(start)

	if err != nil {
		res.Clean = false
		res.ErrorString = fmt.Sprintf("gate checks failed in sandbox: %v", err)
		return res, nil
	}

	res.Clean = true
	res.Checks = append(res.Checks, "sila gate (tests, changelog, binary, readme)")

	// 2. Forward push if requested and green
	if autoForward {
		pushCmd := exec.Command("git", "push", forwardTarget, branch)
		pushCmd.Dir = m.repoRoot
		pushOut, pushErr := pushCmd.CombinedOutput()
		if pushErr != nil {
			res.Forwarded = false
			res.ErrorString = fmt.Sprintf("forward push failed: %s: %v", strings.TrimSpace(string(pushOut)), pushErr)
			return res, nil
		}
		res.Forwarded = true
		res.Checks = append(res.Checks, fmt.Sprintf("forwarded to %s %s", forwardTarget, branch))
	}

	return res, nil
}

// InstallPrePushHook installs or updates a .git/hooks/pre-push script that runs sila gate.
func (m *Manager) InstallPrePushHook() (string, error) {
	hooksDir := filepath.Join(m.repoRoot, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir hooks: %w", err)
	}
	hookPath := filepath.Join(hooksDir, "pre-push")
	if data, err := os.ReadFile(hookPath); err == nil && !strings.Contains(string(data), "Installed by sila") {
		return "", ErrForeignHook
	}
	script := `#!/bin/sh
# Installed by sila gate install --hook
echo "· [sila] running pre-push quality gate in disposable worktree..."
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX
sila gate pipeline || {
    echo "✖ [sila] gate checks failed — push aborted." >&2
    exit 1
}
`
	if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("write pre-push hook: %w", err)
	}
	return hookPath, nil
}

// InstallPreCommitHook installs or updates a .git/hooks/pre-commit script that runs sila gate.
func (m *Manager) InstallPreCommitHook() (string, error) {
	hooksDir := filepath.Join(m.repoRoot, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir hooks: %w", err)
	}
	hookPath := filepath.Join(hooksDir, "pre-commit")
	if data, err := os.ReadFile(hookPath); err == nil && !strings.Contains(string(data), "Installed by sila") {
		return "", ErrForeignHook
	}
	script := `#!/bin/sh
# Installed by sila install / sila hook install
echo "· [sila] running pre-commit engineering discipline check..."

# Multi-repo CWD resilience: resolve git top-level before clearing environment variables
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
if [ -n "$REPO_ROOT" ] && [ -d "$REPO_ROOT" ]; then
    cd "$REPO_ROOT"
fi

unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX
export PATH="$HOME/.local/bin:/usr/local/bin:$PATH"

if command -v sila >/dev/null 2>&1; then
    SILABIN="sila"
elif [ -x "./.bin/sila" ]; then
    SILABIN="./.bin/sila"
elif [ -x "./sila" ]; then
    SILABIN="./sila"
elif command -v go >/dev/null 2>&1 && [ -f "./cmd/sila/main.go" ]; then
    SILABIN="go run ./cmd/sila"
else
    SILABIN="sila"
fi

# Run pre-commit quality gate (sila gate)
$SILABIN gate --pre-commit || {
    echo "✖ [sila] pre-commit checks failed — resolve before committing." >&2
    exit 1
}
`
	if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("write pre-commit hook: %w", err)
	}
	return hookPath, nil
}

// InstallGateRemote configures a local git remote (e.g. 'sila-gate') pointing to a local pipeline runner.
func (m *Manager) InstallGateRemote(remoteName string) (string, error) {
	if remoteName == "" {
		remoteName = "sila-gate"
	}
	// Check if remote already exists
	checkCmd := exec.Command("git", "-C", m.repoRoot, "remote", "get-url", remoteName)
	if err := checkCmd.Run(); err == nil {
		// Remote already exists
		return remoteName, nil
	}

	// Add local gate remote pointing to repo directory
	addCmd := exec.Command("git", "-C", m.repoRoot, "remote", "add", remoteName, m.repoRoot)
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git remote add: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return remoteName, nil
}

// InstallPostCommitHook installs a managed post-commit hook that records a
// commit receipt. A foreign hook is preserved and reported as ErrForeignHook.
func (m *Manager) InstallPostCommitHook() (string, error) {
	hooksDir := filepath.Join(m.repoRoot, ".git", "hooks")
	if err := os.MkdirAll(hooksDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir hooks: %w", err)
	}
	hookPath := filepath.Join(hooksDir, "post-commit")
	if data, err := os.ReadFile(hookPath); err == nil && !strings.Contains(string(data), "Installed by sila") {
		return "", ErrForeignHook
	}
	script := `#!/bin/sh
# Installed by sila gate install --post-commit
# Records a local commit-to-session receipt; never mutates the commit.
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
if [ -n "$REPO_ROOT" ] && [ -d "$REPO_ROOT" ]; then
    cd "$REPO_ROOT"
fi
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_PREFIX
export PATH="$HOME/.local/bin:/usr/local/bin:$PATH"
if command -v sila >/dev/null 2>&1; then
    SILABIN="sila"
elif [ -x "./.bin/sila" ]; then
    SILABIN="./.bin/sila"
elif [ -x "./sila" ]; then
    SILABIN="./sila"
elif command -v go >/dev/null 2>&1 && [ -f "./cmd/sila/main.go" ]; then
    SILABIN="go run ./cmd/sila"
else
    SILABIN="sila"
fi
$SILABIN hook commit >/dev/null 2>&1 || true
`
	if err := os.WriteFile(hookPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("write post-commit hook: %w", err)
	}
	return hookPath, nil
}

// HookStatus describes one managed hook.
type HookStatus struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Installed bool   `json:"installed"`
	Current   bool   `json:"current"`
	Owned     bool   `json:"owned"`
}

// ManagedHookStatus inspects a hook without changing it.
func (m *Manager) ManagedHookStatus(name string) HookStatus {
	status := HookStatus{Name: name, Path: filepath.Join(m.repoRoot, ".git", "hooks", name)}
	data, err := os.ReadFile(status.Path)
	if err != nil {
		return status
	}
	status.Installed = true
	text := string(data)
	status.Owned = strings.Contains(text, "Installed by sila")
	switch name {
	case "post-commit":
		status.Current = status.Owned && strings.Contains(text, "hook commit")
	case "pre-commit":
		status.Current = status.Owned && strings.Contains(text, "gate --pre-commit")
	case "pre-push":
		status.Current = status.Owned && strings.Contains(text, "gate pipeline")
	}
	return status
}
