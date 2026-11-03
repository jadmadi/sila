package gate

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/store"
)

// ArchSentryOptions configures execution of the deterministic architectural pre-commit sentry.
type ArchSentryOptions struct {
	RepoRoot      string       // Repository root directory
	Store         *store.Store // Optional knowledge store for fact grounding check
	SkipArch      bool         // Explicit skip flag
	CommitMessage string       // Optional commit message to check for Sila-Skip-Arch trailer
	Diff          string       // Staged git diff (if provided, avoids spawning git)
	NameStatus    string       // Staged git diff --name-status (if provided, avoids spawning git)
}

// ArchSentryResult summarizes the outcome of all architectural sentry checks.
type ArchSentryResult struct {
	Clean        bool                   `json:"clean"`
	Skipped      bool                   `json:"skipped"`
	SkipReason   string                 `json:"skip_reason,omitempty"`
	SRP          SRPResult              `json:"srp"`
	AntiPatterns ArchScanResult         `json:"anti_patterns"`
	FactWarnings []FactGroundingWarning `json:"fact_warnings"`
	Errors       []string               `json:"errors"`
	Warnings     []string               `json:"warnings"`
	Duration     time.Duration          `json:"duration"`
}

// ParseStagedFiles parses git diff --cached --name-status output into structured StagedFile entries.
func ParseStagedFiles(nameStatusOutput string) []StagedFile {
	var staged []StagedFile
	lines := strings.Split(nameStatusOutput, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		status := fields[0]
		// Handle rename status like R100 old new -> new file is fields[2]
		path := ""
		if strings.HasPrefix(status, "R") && len(fields) >= 3 {
			path = fields[2]
			status = "R"
		} else if len(fields) >= 2 {
			path = fields[1]
			if strings.HasPrefix(status, "A") {
				status = "A"
			} else if strings.HasPrefix(status, "M") {
				status = "M"
			} else if strings.HasPrefix(status, "D") {
				status = "D"
			}
		}
		if path != "" {
			staged = append(staged, StagedFile{
				Path:   path,
				Status: status,
			})
		}
	}
	return staged
}

// RunArchSentry runs all pre-commit architectural checks in-memory with sub-50ms latency budget.
func RunArchSentry(opts ArchSentryOptions) ArchSentryResult {
	start := time.Now()
	res := ArchSentryResult{
		Clean:        true,
		Errors:       make([]string, 0),
		Warnings:     make([]string, 0),
		FactWarnings: make([]FactGroundingWarning, 0),
	}

	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot, _ = os.Getwd()
	}

	// 1. Check escape hatches
	if opts.SkipArch {
		res.Skipped = true
		res.SkipReason = "bypassed via --skip-arch CLI flag"
		res.Duration = time.Since(start)
		return res
	}

	if os.Getenv("SILA_SKIP_ARCH") == "1" {
		res.Skipped = true
		res.SkipReason = "bypassed via SILA_SKIP_ARCH environment variable"
		res.Duration = time.Since(start)
		return res
	}

	// Check commit message trailer
	commitMsg := opts.CommitMessage
	if commitMsg == "" {
		commitMsg = git.ReadCommitEditMsg(repoRoot)
	}
	if reason, ok := git.HasSkipArchTrailer(commitMsg); ok {
		res.Skipped = true
		res.SkipReason = fmt.Sprintf("bypassed via Sila-Skip-Arch commit trailer: %s", reason)
		res.Duration = time.Since(start)
		return res
	}

	// 2. Fetch git staged info if not pre-supplied
	nameStatus := opts.NameStatus
	if nameStatus == "" {
		cmd := exec.Command("git", "diff", "--cached", "--name-status")
		cmd.Dir = repoRoot
		out, err := cmd.Output()
		if err == nil {
			nameStatus = string(out)
		}
	}

	diffText := opts.Diff
	if diffText == "" {
		cmd := exec.Command("git", "diff", "--cached")
		cmd.Dir = repoRoot
		out, err := cmd.Output()
		if err == nil {
			diffText = string(out)
		}
	}

	// If no changes staged, sentry passes cleanly
	if strings.TrimSpace(nameStatus) == "" && strings.TrimSpace(diffText) == "" {
		res.Duration = time.Since(start)
		return res
	}

	// 3. Phase 1: SRP File Weight Sentry
	stagedFiles := ParseStagedFiles(nameStatus)
	res.SRP = CheckSRP(repoRoot, stagedFiles)
	for _, issue := range res.SRP.Issues {
		if issue.Severity == "reject" {
			res.Clean = false
			res.Errors = append(res.Errors, issue.Message)
		} else {
			res.Warnings = append(res.Warnings, issue.Message)
		}
	}

	// 4. Phase 2: Anti-Pattern Diff Regex Scanners
	res.AntiPatterns = ScanArchDiff(diffText)
	if !res.AntiPatterns.Clean {
		res.Clean = false
		for _, v := range res.AntiPatterns.Violations {
			res.Errors = append(res.Errors, v.Format())
		}
	}

	// 5. Phase 3: Epistemic Fact Grounding Sentry
	deletedPaths := ExtractDeletedPaths(nameStatus)
	if len(deletedPaths) > 0 && opts.Store != nil {
		fgRes := CheckFactGrounding(opts.Store, deletedPaths)
		res.FactWarnings = fgRes.Warnings
		for _, w := range fgRes.Warnings {
			res.Warnings = append(res.Warnings, w.Format())
		}
	}

	res.Duration = time.Since(start)
	return res
}
