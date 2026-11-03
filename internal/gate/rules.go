package gate

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/agents"
)

const (
	// MaxRuleFileBytes is Antigravity's hard 24 KB file ceiling (24,000 bytes).
	// Files exceeding this limit are silently truncated at line boundaries during prompt injection.
	MaxRuleFileBytes = 24000

	// WarnRuleFileBytes is the warning threshold (20,000 bytes, ~83% of budget)
	// when rule files are approaching truncation limits.
	WarnRuleFileBytes = 20000
)

// RuleFileAudit contains diagnostic details for a single audited rule file.
type RuleFileAudit struct {
	Path       string `json:"path"`
	RelPath    string `json:"rel_path"`
	Exists     bool   `json:"exists"`
	IsSymlink  bool   `json:"is_symlink"`
	Target     string `json:"target,omitempty"`
	BrokenLink bool   `json:"broken_link,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	ExceedsCap bool   `json:"exceeds_cap"`
	NearCap    bool   `json:"near_cap"`
	Error      string `json:"error,omitempty"`
}

// RuleBudgetResult summarizes rule file budget, symlink parity, and include integrity.
type RuleBudgetResult struct {
	Clean    bool            `json:"clean"`
	Files    []RuleFileAudit `json:"files"`
	Errors   []string        `json:"errors"`
	Warnings []string        `json:"warnings"`
	Repairs  []string        `json:"repairs,omitempty"`
}

var includeRegex = regexp.MustCompile(`@\[.*?\]\((.*?)\)`)

// AuditRuleBudget inspects repository rule files against Antigravity's 24 KB ceiling,
// validates harness symlink integrity, and checks include directive resolution.
func AuditRuleBudget(repoRoot string) RuleBudgetResult {
	res := RuleBudgetResult{
		Clean:    true,
		Files:    make([]RuleFileAudit, 0, 4),
		Errors:   make([]string, 0),
		Warnings: make([]string, 0),
	}

	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			res.Clean = false
			res.Errors = append(res.Errors, fmt.Sprintf("cannot determine repository root: %v", err))
			return res
		}
		repoRoot = cwd
	}

	// 1. Audit AGENTS.md (canonical root standard)
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
	agentsAudit := auditSingleFile(agentsPath, "AGENTS.md", repoRoot)
	res.Files = append(res.Files, agentsAudit)

	if !agentsAudit.Exists {
		res.Warnings = append(res.Warnings, "AGENTS.md not found in repository root; consider creating one for AI agent guidance")
	} else {
		if agentsAudit.ExceedsCap {
			res.Clean = false
			res.Errors = append(res.Errors, fmt.Sprintf("AGENTS.md (%d bytes) exceeds Antigravity 24 KB ceiling (%d bytes); prompt will be truncated", agentsAudit.SizeBytes, MaxRuleFileBytes))
		} else if agentsAudit.NearCap {
			res.Warnings = append(res.Warnings, fmt.Sprintf("AGENTS.md (%d bytes) is approaching 24 KB ceiling (%d bytes); %d bytes headroom remaining", agentsAudit.SizeBytes, MaxRuleFileBytes, MaxRuleFileBytes-agentsAudit.SizeBytes))
		}
		auditIncludes(agentsPath, repoRoot, &res)
	}

	// 2. Audit GEMINI.md (Antigravity harness file)
	geminiPath := filepath.Join(repoRoot, "GEMINI.md")
	geminiAudit := auditSingleFile(geminiPath, "GEMINI.md", repoRoot)
	res.Files = append(res.Files, geminiAudit)

	if !geminiAudit.Exists {
		res.Warnings = append(res.Warnings, "GEMINI.md not found; run `sila install` or `sila agents sync` to provision GEMINI.md -> AGENTS.md symlink")
	} else if geminiAudit.BrokenLink {
		res.Clean = false
		res.Errors = append(res.Errors, fmt.Sprintf("GEMINI.md is a broken symlink pointing to %q", geminiAudit.Target))
	} else {
		if geminiAudit.ExceedsCap {
			res.Clean = false
			res.Errors = append(res.Errors, fmt.Sprintf("GEMINI.md (%d bytes) exceeds Antigravity 24 KB ceiling (%d bytes); prompt will be truncated", geminiAudit.SizeBytes, MaxRuleFileBytes))
		} else if geminiAudit.NearCap {
			res.Warnings = append(res.Warnings, fmt.Sprintf("GEMINI.md (%d bytes) is approaching 24 KB ceiling (%d bytes); %d bytes headroom remaining", geminiAudit.SizeBytes, MaxRuleFileBytes, MaxRuleFileBytes-geminiAudit.SizeBytes))
		}
		if !geminiAudit.IsSymlink && agentsAudit.Exists {
			res.Warnings = append(res.Warnings, "GEMINI.md is a regular file rather than a symlink to AGENTS.md; run `sila agents sync --replace` to unify harness rules")
		}
		if !geminiAudit.IsSymlink {
			auditIncludes(geminiPath, repoRoot, &res)
		}
	}

	// 3. Audit CLAUDE.md symlink integrity if present
	claudePath := filepath.Join(repoRoot, "CLAUDE.md")
	claudeAudit := auditSingleFile(claudePath, "CLAUDE.md", repoRoot)
	if claudeAudit.Exists {
		res.Files = append(res.Files, claudeAudit)
		if claudeAudit.BrokenLink {
			res.Clean = false
			res.Errors = append(res.Errors, fmt.Sprintf("CLAUDE.md is a broken symlink pointing to %q", claudeAudit.Target))
		}
	}

	// 4. Audit global Gemini rules if present in ~/.gemini
	if homeDir, err := os.UserHomeDir(); err == nil {
		globalGemini := filepath.Join(homeDir, ".gemini", "AGENTS.md")
		if fi, err := os.Stat(globalGemini); err == nil {
			if fi.Size() > MaxRuleFileBytes {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Global rule file ~/.gemini/AGENTS.md (%d bytes) exceeds 24 KB budget; Antigravity will exclude it from inline injection", fi.Size()))
			}
		}
	}

	return res
}

func auditSingleFile(absPath, relPath, repoRoot string) RuleFileAudit {
	audit := RuleFileAudit{
		Path:    absPath,
		RelPath: relPath,
	}

	fi, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			audit.Exists = false
			return audit
		}
		audit.Exists = false
		audit.Error = err.Error()
		return audit
	}

	audit.Exists = true
	audit.IsSymlink = (fi.Mode() & os.ModeSymlink) != 0

	if audit.IsSymlink {
		target, err := os.Readlink(absPath)
		if err != nil {
			audit.BrokenLink = true
			audit.Error = err.Error()
			return audit
		}
		audit.Target = target

		// Stat resolved target
		resolvedFi, err := os.Stat(absPath)
		if err != nil {
			audit.BrokenLink = true
			audit.Error = err.Error()
			return audit
		}
		audit.SizeBytes = resolvedFi.Size()
	} else {
		audit.SizeBytes = fi.Size()
	}

	if audit.SizeBytes > MaxRuleFileBytes {
		audit.ExceedsCap = true
	} else if audit.SizeBytes > WarnRuleFileBytes {
		audit.NearCap = true
	}

	return audit
}

func auditIncludes(path, repoRoot string, res *RuleBudgetResult) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		matches := includeRegex.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) < 2 {
				continue
			}
			incPath := strings.TrimSpace(m[1])
			if incPath == "" || strings.HasPrefix(incPath, "http://") || strings.HasPrefix(incPath, "https://") {
				continue
			}

			// Expand ~ prefix
			resolvedInc := incPath
			if strings.HasPrefix(resolvedInc, "~/") {
				if home, err := os.UserHomeDir(); err == nil {
					resolvedInc = filepath.Join(home, resolvedInc[2:])
				}
			} else if !filepath.IsAbs(resolvedInc) {
				resolvedInc = filepath.Join(repoRoot, resolvedInc)
			}

			if _, err := os.Stat(resolvedInc); os.IsNotExist(err) {
				res.Clean = false
				res.Errors = append(res.Errors, fmt.Sprintf("%s:%d: included file %q does not exist", filepath.Base(path), lineNum, incPath))
			}
		}
	}
}

// RepairRuleBudget repairs broken or missing GEMINI.md / CLAUDE.md symlinks
// pointing to canonical AGENTS.md in repoRoot.
func RepairRuleBudget(repoRoot string) ([]string, error) {
	var repaired []string

	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot repair symlinks: AGENTS.md not found in %s", repoRoot)
	}

	targets := []string{"GEMINI.md", "CLAUDE.md"}
	for _, target := range targets {
		p := filepath.Join(repoRoot, target)
		fi, err := os.Lstat(p)
		if os.IsNotExist(err) {
			if target == "GEMINI.md" && !agents.HasGeminiIntegration(repoRoot) {
				continue
			}
			if target == "CLAUDE.md" && !agents.HasClaudeIntegration(repoRoot) {
				continue
			}
			if err := os.Symlink("AGENTS.md", p); err == nil {
				repaired = append(repaired, fmt.Sprintf("created %s -> AGENTS.md symlink", target))
			}
			continue
		}

		if (fi.Mode() & os.ModeSymlink) != 0 {
			// Check if broken
			if _, err := os.Stat(p); os.IsNotExist(err) {
				_ = os.Remove(p)
				if err := os.Symlink("AGENTS.md", p); err == nil {
					repaired = append(repaired, fmt.Sprintf("repaired broken %s -> AGENTS.md symlink", target))
				}
			}
		}
	}

	return repaired, nil
}
