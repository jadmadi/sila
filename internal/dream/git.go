package dream

import (
	"os/exec"
	"strings"
)

// GitContext holds current git repository metadata.
type GitContext struct {
	IsGitRepo bool   `json:"is_git_repo"`
	Branch    string `json:"branch,omitempty"`
	Commit    string `json:"commit,omitempty"`
	RootPath  string `json:"root_path,omitempty"`
}

// DetectGitContext inspects the given directory for git metadata.
func DetectGitContext(dir string) GitContext {
	ctx := GitContext{}
	if dir == "" {
		dir = "."
	}

	// 1. Check toplevel
	cmdTop := exec.Command("git", "rev-parse", "--show-toplevel")
	cmdTop.Dir = dir
	outTop, err := cmdTop.Output()
	if err != nil {
		return ctx
	}
	ctx.IsGitRepo = true
	ctx.RootPath = strings.TrimSpace(string(outTop))

	// 2. Branch name
	cmdBranch := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmdBranch.Dir = dir
	if outBranch, err := cmdBranch.Output(); err == nil {
		branch := strings.TrimSpace(string(outBranch))
		if branch != "HEAD" {
			ctx.Branch = branch
		}
	}

	// 3. Commit SHA
	cmdCommit := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmdCommit.Dir = dir
	if outCommit, err := cmdCommit.Output(); err == nil {
		ctx.Commit = strings.TrimSpace(string(outCommit))
	}

	return ctx
}
