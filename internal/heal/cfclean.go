package heal

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/assess"
)

// GitattributesCFCleanHeader is the comment header placed above cfclean git attributes.
const GitattributesCFCleanHeader = "# Cloudflare credential sanitization filter (cfclean)"

// TargetCFCleanAttributes contains the default git filter attributes configured by cfclean.
var TargetCFCleanAttributes = []string{
	"wrangler.jsonc filter=cfclean",
	"wrangler.json filter=cfclean",
	"wrangler.toml filter=cfclean",
	"*wrangler*.jsonc filter=cfclean",
	"*wrangler*.json filter=cfclean",
	"*wrangler*.toml filter=cfclean",
}

// NeedsCFGitFilter checks if the repo is a Cloudflare Worker project that lacks
// the cfclean filter in .gitattributes or local git configuration.
func NeedsCFGitFilter(repoRoot string) bool {
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	if !assess.IsCFWorkerProject(repoRoot) && !assess.IsCloudflareWorkerProject(repoRoot) {
		return false
	}

	// 1. Check .gitattributes
	gitAttrPath := filepath.Join(repoRoot, ".gitattributes")
	data, err := os.ReadFile(gitAttrPath)
	if err != nil {
		return true
	}
	content := string(data)
	if !strings.Contains(content, "filter=cfclean") {
		return true
	}

	// 2. Check local git config if in a git repository
	cmdClean := exec.Command("git", "config", "filter.cfclean.clean")
	cmdClean.Dir = repoRoot
	outClean, errClean := cmdClean.Output()
	if errClean != nil || strings.TrimSpace(string(outClean)) != "cfclean" {
		return true
	}

	cmdSmudge := exec.Command("git", "config", "filter.cfclean.smudge")
	cmdSmudge.Dir = repoRoot
	outSmudge, errSmudge := cmdSmudge.Output()
	smudgeVal := strings.TrimSpace(string(outSmudge))

	if _, err := exec.LookPath("cfclean"); err == nil {
		if errSmudge != nil || smudgeVal != "cfclean smudge" {
			return true
		}
	} else {
		if errSmudge != nil || smudgeVal == "" {
			return true
		}
	}

	return false
}

// EnsureCFGitFilter idempotently configures the cfclean git filter in .gitattributes
// and git config for a Cloudflare Worker project.
func EnsureCFGitFilter(repoRoot string) (bool, error) {
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}
	if !assess.IsCFWorkerProject(repoRoot) && !assess.IsCloudflareWorkerProject(repoRoot) {
		return false, nil
	}

	modified := false

	// Try running cfclean setup if cfclean is installed on PATH
	if _, err := exec.LookPath("cfclean"); err == nil {
		cmdSetup := exec.Command("cfclean", "setup", "--smudge")
		cmdSetup.Dir = repoRoot
		if out, err := cmdSetup.CombinedOutput(); err == nil {
			_ = out
			return true, nil
		}

		// Fallback to cfclean setup without --smudge if an older version is installed
		cmdSetupOld := exec.Command("cfclean", "setup")
		cmdSetupOld.Dir = repoRoot
		if out, err := cmdSetupOld.CombinedOutput(); err == nil {
			_ = out
			cmdSmudge := exec.Command("git", "config", "filter.cfclean.smudge", "cfclean smudge")
			cmdSmudge.Dir = repoRoot
			_ = cmdSmudge.Run()
			return true, nil
		}
	}

	// Fallback to direct git config and .gitattributes modification
	cmdClean := exec.Command("git", "config", "filter.cfclean.clean", "cfclean")
	cmdClean.Dir = repoRoot
	if err := cmdClean.Run(); err == nil {
		modified = true
	}

	smudgeVal := "cat"
	if _, err := exec.LookPath("cfclean"); err == nil {
		smudgeVal = "cfclean smudge"
	}
	cmdSmudge := exec.Command("git", "config", "filter.cfclean.smudge", smudgeVal)
	cmdSmudge.Dir = repoRoot
	if err := cmdSmudge.Run(); err == nil {
		modified = true
	}

	gitAttrPath := filepath.Join(repoRoot, ".gitattributes")
	var existingContent string
	if data, err := os.ReadFile(gitAttrPath); err == nil {
		existingContent = string(data)
	}

	var toAppend []string
	for _, attr := range TargetCFCleanAttributes {
		if !strings.Contains(existingContent, attr) {
			toAppend = append(toAppend, attr)
		}
	}

	if len(toAppend) > 0 {
		f, err := os.OpenFile(gitAttrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return modified, fmt.Errorf("failed to open .gitattributes: %w", err)
		}
		defer f.Close()

		if !strings.Contains(existingContent, "filter=cfclean") {
			if len(existingContent) > 0 && !strings.HasSuffix(existingContent, "\n") {
				if _, err := f.WriteString("\n"); err != nil {
					return modified, err
				}
			}
			if _, err := f.WriteString(GitattributesCFCleanHeader + "\n"); err != nil {
				return modified, err
			}
		}

		for _, item := range toAppend {
			if _, err := f.WriteString(item + "\n"); err != nil {
				return modified, fmt.Errorf("failed to write to .gitattributes: %w", err)
			}
		}
		modified = true
	}

	return modified, nil
}
