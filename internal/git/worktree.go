// Package git provides low-level Git worktree management for sila's disposable
// agent sandbox. All Git operations are executed via exec.Command("git", ...)
// with zero CGO dependencies (libgit2 is strictly prohibited).
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Worktree represents one Git worktree entry.
type Worktree struct {
	Path      string // filesystem path to the worktree
	Branch    string // branch name checked out in this worktree
	Head      string // commit hash at HEAD
	Bare      bool
	Locked    string // lock reason, or "" if not locked
	Prunable  bool
	IsPrimary bool // true for the main working tree
}

// Manager handles worktree lifecycle operations against a repository.
type Manager struct {
	repoRoot string
}

// NewManager creates a worktree Manager for the repository rooted at repoRoot.
// If repoRoot is empty, the current working directory is used.
func NewManager(repoRoot string) (*Manager, error) {
	if repoRoot == "" {
		var err error
		repoRoot, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
	}
	root, err := gitRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	return &Manager{repoRoot: root}, nil
}

// gitRoot returns the top-level of the git repository containing dir.
func gitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s", dir)
	}
	return strings.TrimSpace(string(out)), nil
}

// RepoRoot returns the absolute path to the repository root.
func (m *Manager) RepoRoot() string { return m.repoRoot }

// WorktreeDir returns the directory under which disposable worktrees are stored.
// This is <repoRoot>/.git/sila-worktrees/ for in-repo worktrees.
func (m *Manager) WorktreeDir() string {
	gitDir := m.gitDir()
	return filepath.Join(gitDir, "sila-worktrees")
}

// gitDir returns the .git directory path (handles worktrees correctly).
func (m *Manager) gitDir() string {
	cmd := exec.Command("git", "-C", m.repoRoot, "rev-parse", "--git-dir")
	out, err := cmd.Output()
	if err != nil {
		// Fallback: assume .git in repo root.
		return filepath.Join(m.repoRoot, ".git")
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(m.repoRoot, dir)
	}
	return dir
}

// Create creates a new worktree at <worktreeDir>/<name> on a new branch
// named sila/<name>, based on the given base ref (default: HEAD).
// If the branch already exists, it is reused.
func (m *Manager) Create(name, baseRef string) (*Worktree, error) {
	if name == "" {
		return nil, fmt.Errorf("worktree name is required")
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}

	wtDir := m.WorktreeDir()
	wtPath := filepath.Join(wtDir, name)
	branchName := "sila/" + name

	// Ensure the parent directory exists.
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		return nil, fmt.Errorf("create worktree dir: %w", err)
	}

	// Check if branch already exists. If so, use it; otherwise create it.
	checkCmd := exec.Command("git", "-C", m.repoRoot, "rev-parse", "--verify", branchName)
	if err := checkCmd.Run(); err != nil {
		// Branch doesn't exist — create it with the worktree.
		cmd := exec.Command("git", "-C", m.repoRoot, "worktree", "add",
			"-b", branchName, wtPath, baseRef)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git worktree add: %s: %w", strings.TrimSpace(string(out)), err)
		}
	} else {
		// Branch exists — checkout it in a new worktree.
		cmd := exec.Command("git", "-C", m.repoRoot, "worktree", "add", wtPath, branchName)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git worktree add (existing branch): %s: %w", strings.TrimSpace(string(out)), err)
		}
	}

	return &Worktree{
		Path:   wtPath,
		Branch: branchName,
	}, nil
}

// CreateDetached creates a new detached worktree at <worktreeDir>/<name>
// checked out directly at baseRef without creating or clobbering a branch.
func (m *Manager) CreateDetached(name, baseRef string) (*Worktree, error) {
	if name == "" {
		return nil, fmt.Errorf("worktree name is required")
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}
	wtDir := m.WorktreeDir()
	wtPath := filepath.Join(wtDir, name)
	if err := os.MkdirAll(wtDir, 0755); err != nil {
		return nil, fmt.Errorf("create worktree dir: %w", err)
	}
	cmd := exec.Command("git", "-C", m.repoRoot, "worktree", "add", "--detach", wtPath, baseRef)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add --detach: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return &Worktree{
		Path:   wtPath,
		Branch: "HEAD",
	}, nil
}

// List returns all worktrees in the repository, including the primary tree.
func (m *Manager) List() ([]Worktree, error) {
	cmd := exec.Command("git", "-C", m.repoRoot, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	return parseWorktreeList(string(out)), nil
}

// parseWorktreeList parses `git worktree list --porcelain` output.
func parseWorktreeList(raw string) []Worktree {
	var wts []Worktree
	var current *Worktree
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if current != nil {
				wts = append(wts, *current)
				current = nil
			}
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			if current != nil {
				wts = append(wts, *current)
			}
			path := strings.TrimPrefix(line, "worktree ")
			current = &Worktree{Path: path, IsPrimary: len(wts) == 0}
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			current.Bare = true
		case strings.HasPrefix(line, "locked "):
			current.Locked = strings.TrimPrefix(line, "locked ")
		case line == "locked":
			current.Locked = "(no reason)"
		case line == "prunable":
			current.Prunable = true
		}
	}
	if current != nil {
		wts = append(wts, *current)
	}
	return wts
}

// Remove removes a worktree by name or path. If force is true, removal
// proceeds even if the worktree has uncommitted changes.
func (m *Manager) Remove(nameOrPath string, force bool) error {
	if nameOrPath == "" {
		return fmt.Errorf("worktree name or path is required")
	}
	wtPath := m.resolvePath(nameOrPath)
	args := []string{"-C", m.repoRoot, "worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, wtPath)
	cmd := exec.Command("git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", strings.TrimSpace(string(out)), err)
	}
	// Also delete the sila/<name> branch if it exists and is not checked out elsewhere.
	branchName := "sila/" + filepath.Base(wtPath)
	delCmd := exec.Command("git", "-C", m.repoRoot, "branch", "-D", branchName)
	delCmd.Run() // best-effort; ignore errors
	return nil
}

// Prune removes stale worktree administrative entries (worktrees that no
// longer exist on disk).
func (m *Manager) Prune() error {
	cmd := exec.Command("git", "-C", m.repoRoot, "worktree", "prune")
	return cmd.Run()
}

// resolvePath resolves a worktree name to its full path under WorktreeDir(),
// or returns the path as-is if it's already absolute.
func (m *Manager) resolvePath(nameOrPath string) string {
	if filepath.IsAbs(nameOrPath) {
		return nameOrPath
	}
	// Check if it's a relative path that exists.
	if _, err := os.Stat(nameOrPath); err == nil {
		abs, _ := filepath.Abs(nameOrPath)
		return abs
	}
	// Treat as a name under WorktreeDir().
	return filepath.Join(m.WorktreeDir(), nameOrPath)
}

// Run executes a command in the worktree identified by nameOrPath.
// It returns the command's stdout, stderr, and exit error (if any).
func (m *Manager) Run(nameOrPath string, cmdName string, args ...string) (stdout, stderr string, exitErr error) {
	wtPath := m.resolvePath(nameOrPath)
	cmd := exec.Command(cmdName, args...)
	cmd.Dir = wtPath
	outBuf, errBuf := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	err := cmd.Run()
	return outBuf.String(), errBuf.String(), err
}

// RunStreamed executes a command in the worktree with stdout/stderr connected
// to the parent process, so output is streamed live.
func (m *Manager) RunStreamed(nameOrPath string, cmdName string, args ...string) error {
	wtPath := m.resolvePath(nameOrPath)
	cmd := exec.Command(cmdName, args...)
	cmd.Dir = wtPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// SeedContext writes a brief context briefing into the worktree for the agent
// to read on startup. This is a lightweight alternative to running `sila prime`
// inside the worktree — it writes a SILA_CONTEXT.md file with the project ID,
// branch, and a note about the sandboxed environment.
func (m *Manager) SeedContext(nameOrPath, projectID, handoffSummary string) error {
	wtPath := m.resolvePath(nameOrPath)
	ctxPath := filepath.Join(wtPath, "SILA_CONTEXT.md")
	var b strings.Builder
	b.WriteString("# sila · worktree context\n\n")
	b.WriteString("> This worktree is a disposable agent sandbox managed by `sila worktree`.\n")
	b.WriteString("> Run `sila worktree clean <name>` when done to remove it.\n\n")
	fmt.Fprintf(&b, "**Project**: %s\n", projectID)
	fmt.Fprintf(&b, "**Repo root**: %s\n", m.repoRoot)
	fmt.Fprintf(&b, "**Worktree**: %s\n", wtPath)
	if handoffSummary != "" {
		fmt.Fprintf(&b, "\n## Handoff\n\n%s\n", handoffSummary)
	}
	b.WriteString("\n## Next steps\n\n")
	b.WriteString("1. Read this file for context.\n")
	b.WriteString("2. Run `sila prime` for full project briefing.\n")
	b.WriteString("3. Execute your task.\n")
	b.WriteString("4. Run `sila gate` before reporting completion.\n")
	b.WriteString("5. Run `sila worktree clean <name>` to dispose.\n")
	return os.WriteFile(ctxPath, []byte(b.String()), 0644)
}

// PilotResult encapsulates the outcome of a sandboxed pilot execution trial.
type PilotResult struct {
	Name        string        `json:"name"`
	Worktree    string        `json:"worktree"`
	Command     string        `json:"command"`
	Stdout      string        `json:"stdout"`
	Stderr      string        `json:"stderr"`
	ExitCode    int           `json:"exit_code"`
	Success     bool          `json:"success"`
	Duration    time.Duration `json:"duration"`
	CleanedUp   bool          `json:"cleaned_up"`
	ErrorString string        `json:"error,omitempty"`
}

// PilotRun executes a time-bounded trial in a disposable, detached worktree sandbox.
// It creates the worktree, executes the test/build command within timeout, records telemetry,
// and automatically disposes of the worktree when finished.
func (m *Manager) PilotRun(name, baseRef string, timeout time.Duration, cmdName string, args ...string) (*PilotResult, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 60*time.Second {
		timeout = 60 * time.Second
	}
	if name == "" {
		name = fmt.Sprintf("pilot-%d", time.Now().UnixNano()%1000000)
	}

	wt, err := m.Create(name, baseRef)
	if err != nil {
		return nil, fmt.Errorf("create pilot worktree: %w", err)
	}

	res := &PilotResult{
		Name:     name,
		Worktree: wt.Path,
		Command:  cmdName + " " + strings.Join(args, " "),
	}

	defer func() {
		// Guaranteed cleanup of disposable pilot sandbox
		_ = m.Remove(name, true)
		res.CleanedUp = true
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, cmdName, args...)
	cmd.Dir = wt.Path
	outBuf, errBuf := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf

	runErr := cmd.Run()
	res.Duration = time.Since(start)
	res.Stdout = outBuf.String()
	res.Stderr = errBuf.String()

	if runErr != nil {
		res.Success = false
		res.ErrorString = runErr.Error()
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = 1
		}
	} else {
		res.Success = true
		res.ExitCode = 0
	}

	return res, nil
}
