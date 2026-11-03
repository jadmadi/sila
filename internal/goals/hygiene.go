package goals

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// StaleRelease describes one claim released (or previewed) by the stale sweep.
type StaleRelease struct {
	Goal    string `json:"goal"`
	Project string `json:"project,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Age     string `json:"age"`
	Reason  string `json:"reason,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
}

// ReleaseStaleClaims releases claims that CheckStale marks stale and that are at
// least olderThan old. A live process is never released: CheckStale already
// refuses to mark one stale, so the sweep inherits that guard.
func ReleaseStaleClaims(rootDir string, olderThan time.Duration, dryRun bool) ([]StaleRelease, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	entries, err := os.ReadDir(filepath.Join(rootDir, "goals"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read goals directory: %w", err)
	}
	project := store.ProjectID(rootDir)
	var out []StaleRelease
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		slug := e.Name()
		claimPath := filepath.Join(rootDir, "goals", slug, "claim.json")
		if !fileExists(claimPath) {
			continue
		}
		claim, err := readClaim(claimPath)
		if err != nil || claim == nil || !claim.Stale {
			continue
		}
		age := time.Since(claim.UpdatedAt)
		if claim.UpdatedAt.IsZero() {
			age = time.Since(claim.ClaimedAt)
		}
		if olderThan > 0 && age < olderThan {
			continue
		}
		agent := claim.ClaimedBy
		if agent == "" {
			agent = claim.Agent
		}
		out = append(out, StaleRelease{
			Goal:    slug,
			Project: project,
			Agent:   agent,
			Age:     formatClaimAge(age),
			Reason:  claim.StaleReason,
			DryRun:  dryRun,
		})
		if dryRun {
			continue
		}
		if err := ReleaseGoal(rootDir, slug); err != nil {
			return out, fmt.Errorf("release stale claim %s: %w", slug, err)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Goal < out[j].Goal })
	return out, nil
}

// PruneClosedClaims lists or removes claim records left on implemented or
// superseded goals. A dry run returns the paths without touching them.
func PruneClosedClaims(rootDir string, dryRun bool) ([]string, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	res, err := ScanGoals(rootDir)
	if err != nil {
		return nil, err
	}
	return PruneClosedClaimsFromScan(rootDir, res, dryRun)
}

// PruneClosedClaimsFromScan lists or removes claim records left on implemented or
// superseded goals using a pre-computed AuditResult when non-nil.
func PruneClosedClaimsFromScan(rootDir string, res *AuditResult, dryRun bool) ([]string, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	if res == nil {
		var err error
		res, err = ScanGoals(rootDir)
		if err != nil {
			return nil, err
		}
	}
	var paths []string
	for _, g := range res.Goals {
		if g.Claim == nil || (g.Status != "implemented" && g.Status != "superseded") {
			continue
		}
		paths = append(paths, filepath.Join("goals", g.Slug, "claim.json"))
	}
	sort.Strings(paths)
	if dryRun || len(paths) == 0 {
		return paths, nil
	}
	return ClearClosedClaims(rootDir, res)
}

// ClaimIgnorePattern is the canonical gitignore pattern for ephemeral goal claims.
const ClaimIgnorePattern = "goals/*/claim.json"

// StateIgnorePattern is the gitignore pattern for sila runtime state (goal contract bindings).
const StateIgnorePattern = ".sila/state/"

// NeedsGitIgnoreClaim reports whether the repository at rootDir has a goals/ directory
// or git tracking, but does not yet ignore ephemeral claim.json files in .gitignore.
func NeedsGitIgnoreClaim(rootDir string) (bool, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	gitPath := filepath.Join(rootDir, ".git")
	goalsPath := filepath.Join(rootDir, "goals")

	// Only relevant if this is a git repository with a goals/ directory
	if _, gitErr := os.Stat(gitPath); gitErr != nil {
		return false, nil
	}
	if _, goalsErr := os.Stat(goalsPath); goalsErr != nil {
		return false, nil
	}

	ignoreFile := filepath.Join(rootDir, ".gitignore")
	data, err := os.ReadFile(ignoreFile)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	claimIgnored, stateIgnored := false, false
	for _, l := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Match any pattern that ignores claim.json under goals
		if trimmed == "goals/*/claim.json" || trimmed == "/goals/*/claim.json" ||
			trimmed == "goals/**/claim.json" || trimmed == "/goals/**/claim.json" ||
			trimmed == "**/claim.json" || trimmed == "claim.json" ||
			trimmed == "*.json" {
			claimIgnored = true
		}
		if trimmed == StateIgnorePattern || trimmed == "/"+StateIgnorePattern ||
			trimmed == ".sila/state" || trimmed == ".sila/" || trimmed == ".sila" {
			stateIgnored = true
		}
	}
	return !claimIgnored || !stateIgnored, nil
}

// EnsureGitIgnoreClaim ensures that goals/*/claim.json is present in .gitignore
// at rootDir. If already ignored, it returns false, nil.
func EnsureGitIgnoreClaim(rootDir string) (bool, error) {
	needs, err := NeedsGitIgnoreClaim(rootDir)
	if err != nil || !needs {
		return false, err
	}

	rootDir = resolveGoalsRootDir(rootDir)
	ignoreFile := filepath.Join(rootDir, ".gitignore")
	data, _ := os.ReadFile(ignoreFile)
	content := string(data)

	var rule string
	if !gitignoreHasLine(content, ClaimIgnorePattern) {
		rule += "\n# Ephemeral goal claim locks (managed by sila)\ngoals/*/claim.json\n"
	}
	if !gitignoreHasLine(content, StateIgnorePattern) {
		rule += "\n# Sila runtime state (managed by sila)\n.sila/state/\n.sila/charters/\n.sila/locks/\n"
	}
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		rule = "\n" + rule
	}

	newContent := content + rule
	if err := os.WriteFile(ignoreFile, []byte(newContent), 0o644); err != nil {
		return false, fmt.Errorf("write .gitignore: %w", err)
	}
	return true, nil
}

// NeedsGitIgnoreWorkspaceShim checks if .bin/ or .bin/sila needs to be added to .gitignore.
func NeedsGitIgnoreWorkspaceShim(rootDir string) (bool, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	ignoreFile := filepath.Join(rootDir, ".gitignore")
	data, err := os.ReadFile(ignoreFile)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("read .gitignore: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == ".bin/" || trimmed == "/.bin/" || trimmed == ".bin" || trimmed == "/.bin" ||
			trimmed == ".bin/*" || trimmed == ".bin/sila" || trimmed == "/.bin/sila" {
			return false, nil
		}
	}
	return true, nil
}

// EnsureGitIgnoreWorkspaceShim ensures that .bin/ is present in .gitignore at rootDir.
func EnsureGitIgnoreWorkspaceShim(rootDir string) (bool, error) {
	needs, err := NeedsGitIgnoreWorkspaceShim(rootDir)
	if err != nil || !needs {
		return false, err
	}

	rootDir = resolveGoalsRootDir(rootDir)
	ignoreFile := filepath.Join(rootDir, ".gitignore")
	data, _ := os.ReadFile(ignoreFile)
	content := string(data)

	rule := "\n# Standalone workspace runner for sandboxed AI agents (managed by sila)\n.bin/\n"
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		rule = "\n" + rule
	}

	newContent := content + rule
	if err := os.WriteFile(ignoreFile, []byte(newContent), 0o644); err != nil {
		return false, fmt.Errorf("write .gitignore: %w", err)
	}
	return true, nil
}

func gitignoreHasLine(content, pattern string) bool {
	for _, l := range strings.Split(content, "\n") {
		if strings.TrimSpace(l) == pattern {
			return true
		}
	}
	return false
}
