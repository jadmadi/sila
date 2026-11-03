package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/jadmadi/sila/internal/render"
)

// GoalCharter defines the formal boundary enforcement model for a claimed goal,
// establishing directory blast-radius scoping, capability intersection, and POSIX
// flock anti-collision protection across concurrent agent workers.
type GoalCharter struct {
	GoalSlug       string        `json:"goal_slug"`
	Agent          string        `json:"agent"`
	SessionID      string        `json:"session_id,omitempty"`
	AllowPaths     []string      `json:"allow_paths"`     // Canonical repo-relative paths authorized for mutation
	DenyPaths      []string      `json:"deny_paths"`      // Inviolable boundary paths (e.g. "research/*", "worker/*")
	RequiredChecks []string      `json:"required_checks"` // Deterministic pre-completion validation commands
	MaxDuration    time.Duration `json:"max_duration"`    // Auto-release deadline
	LockToken      string        `json:"lock_token"`      // POSIX flock identifier
	CreatedAt      time.Time     `json:"created_at"`
}

// CollisionError reports a concurrency collision where a candidate goal's charter
// allows paths overlapping with an already active charter held by another agent.
type CollisionError struct {
	GoalSlug         string   `json:"goal_slug"`
	ConflictingGoal  string   `json:"conflicting_goal"`
	HolderAgent      string   `json:"holder_agent"`
	OverlappingPaths []string `json:"overlapping_paths"`
}

func (e *CollisionError) Error() string {
	return fmt.Sprintf("cannot claim goal %q: path collision on %v with active charter for goal %q (held by @%s). Use --force to override",
		e.GoalSlug, e.OverlappingPaths, e.ConflictingGoal, e.HolderAgent)
}

// DefaultInviolableDenyPaths defines root workspace boundaries that can never be
// overridden or broadened by a subagent charter.
var DefaultInviolableDenyPaths = []string{
	"research/*",
	".git/*",
}

// CharterDir returns the filesystem directory storing goal charters.
func CharterDir(rootDir string) string {
	return filepath.Join(resolveGoalsRootDir(rootDir), ".sila", "charters")
}

// LockDir returns the filesystem directory storing POSIX flock files.
func LockDir(rootDir string) string {
	return filepath.Join(resolveGoalsRootDir(rootDir), ".sila", "locks")
}

// CharterPath returns the canonical JSON path for a goal charter.
func CharterPath(rootDir, slug string) string {
	return filepath.Join(CharterDir(rootDir), slug+".json")
}

// LockFilePath returns the canonical flock file path for a goal.
func LockFilePath(rootDir, slug string) string {
	return filepath.Join(LockDir(rootDir), slug+".flock")
}

// ensureEphemeralDirGitignore writes a local .gitignore ("*\n") inside an ephemeral
// .sila subdirectory (such as .sila/charters or .sila/locks) so runtime files never
// dirty git status even before root .gitignore is updated.
func ensureEphemeralDirGitignore(dir string) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	gi := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(gi); os.IsNotExist(err) {
		_ = os.WriteFile(gi, []byte("*\n"), 0644)
	}
}

// AcquireCharterLock acquires a POSIX advisory flock with inode verification on .sila/locks/<slug>.flock.
func AcquireCharterLock(rootDir, slug string, timeout time.Duration) (func(), string, error) {
	lockDir := LockDir(rootDir)
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create lock dir: %w", err)
	}
	ensureEphemeralDirGitignore(lockDir)
	lockFile := LockFilePath(rootDir, slug)
	return acquireFlockFile(lockFile, timeout)
}

// GlobalCharterLockFilePath returns the path to the repository-wide charter synchronization lock.
func GlobalCharterLockFilePath(rootDir string) string {
	return filepath.Join(LockDir(rootDir), ".charters.flock")
}

// AcquireGlobalCharterLock acquires an exclusive advisory lock across all charter modifications in rootDir.
func AcquireGlobalCharterLock(rootDir string, timeout time.Duration) (func(), error) {
	lockDir := LockDir(rootDir)
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	ensureEphemeralDirGitignore(lockDir)
	lockFile := GlobalCharterLockFilePath(rootDir)
	unlock, _, err := acquireFlockFile(lockFile, timeout)
	return unlock, err
}

// MatchCharterPath evaluates if a repository-relative path matches a charter pattern.
// Supports exact paths, directory prefix matches ("dir/*", "dir/"), and glob wildcards.
// It normalizes path prefixes across multi-repo root vs sub-repo contexts (e.g. "cli/internal" matches "internal").
func MatchCharterPath(path, pattern string) bool {
	p := NormalizeGoalPath(path)
	pat := NormalizeGoalPath(pattern)
	if p == "" || pat == "" {
		return false
	}
	if pat == "*" || pat == "." {
		return true
	}
	if matchCharterPathDirect(p, pat) {
		return true
	}
	pNoCli := strings.TrimPrefix(p, "cli/")
	patNoCli := strings.TrimPrefix(pat, "cli/")
	if pNoCli != p || patNoCli != pat {
		if matchCharterPathDirect(pNoCli, patNoCli) {
			return true
		}
	}
	return false
}

func matchCharterPathDirect(p, pat string) bool {
	if p == pat {
		return true
	}
	patClean := strings.TrimSuffix(pat, "/*")
	patClean = strings.TrimSuffix(patClean, "*")
	if patClean != "" {
		if p == patClean || strings.HasPrefix(p, patClean+"/") {
			return true
		}
	}
	if m, err := filepath.Match(pat, p); err == nil && m {
		return true
	}
	return PathsOverlap(p, pat)
}

// IsSharedLedgerFile returns true for shared documentation, changelog, and metadata files
// that are globally maintained across tasks and exempt from single-owner allow_paths restrictions.
func IsSharedLedgerFile(path string) bool {
	norm := NormalizeGoalPath(path)
	normNoCli := strings.TrimPrefix(norm, "cli/")
	if normNoCli == "CHANGELOG.md" || normNoCli == "goals/README.md" {
		return true
	}
	if strings.HasPrefix(normNoCli, ".sila/") || strings.HasPrefix(normNoCli, ".git/") {
		return true
	}
	return false
}

// ChartersOverlap returns all normalized paths in c1 that intersect with c2.
func ChartersOverlap(c1, c2 *GoalCharter) []string {
	if c1 == nil || c2 == nil {
		return nil
	}
	var overlaps []string
	seen := make(map[string]bool)
	for _, p1 := range c1.AllowPaths {
		if IsSharedLedgerFile(p1) {
			continue
		}
		for _, p2 := range c2.AllowPaths {
			if IsSharedLedgerFile(p2) {
				continue
			}
			if MatchCharterPath(p1, p2) || MatchCharterPath(p2, p1) || PathsOverlap(p1, p2) {
				norm := NormalizeGoalPath(p1)
				if !seen[norm] {
					seen[norm] = true
					overlaps = append(overlaps, norm)
				}
			}
		}
	}
	return overlaps
}

// CreateCharter constructs a new GoalCharter enforcing the Capability Intersection Principle:
// A charter can only narrow permissions, never broaden them. If a workspace forbids editing
// a path (e.g. research/*), a goal charter cannot allow it.
func CreateCharter(rootDir, slug, agent, sessionID string, allowPaths, denyPaths, requiredChecks []string, duration time.Duration) (*GoalCharter, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}

	// 1. Merge default inviolable boundaries with explicit deny paths
	denySet := make(map[string]bool)
	var finalDeny []string
	for _, d := range DefaultInviolableDenyPaths {
		norm := NormalizeGoalPath(d)
		if norm != "" && !denySet[norm] {
			denySet[norm] = true
			finalDeny = append(finalDeny, norm)
		}
	}
	for _, d := range denyPaths {
		norm := NormalizeGoalPath(d)
		if norm != "" && !denySet[norm] {
			denySet[norm] = true
			finalDeny = append(finalDeny, norm)
		}
	}

	// 2. Normalize and check allow paths against inviolable deny paths
	var finalAllow []string
	allowSet := make(map[string]bool)
	for _, a := range allowPaths {
		norm := NormalizeGoalPath(a)
		if norm == "" {
			continue
		}
		// Strict capability intersection check: cannot allow denied paths
		for _, d := range finalDeny {
			if MatchCharterPath(norm, d) {
				return nil, fmt.Errorf("capability intersection violation: allow path %q overlaps with inviolable deny path %q", a, d)
			}
		}
		if !allowSet[norm] {
			allowSet[norm] = true
			finalAllow = append(finalAllow, norm)
		}
	}

	// 3. Fallback: if no allow paths provided, inspect facts.md contracts and goal.md files_to_touch
	if len(finalAllow) == 0 {
		goalDir := filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug)

		// 3a. Extract contract paths from facts.md
		if factsData, err := os.ReadFile(filepath.Join(goalDir, "facts.md")); err == nil {
			factsPaths := ExtractFactsContractPaths(string(factsData))
			for _, fp := range factsPaths {
				norm := NormalizeGoalPath(fp)
				if norm != "" && !allowSet[norm] {
					denied := false
					for _, d := range finalDeny {
						if MatchCharterPath(norm, d) {
							denied = true
							break
						}
					}
					if !denied {
						allowSet[norm] = true
						finalAllow = append(finalAllow, norm)
					}
				}
			}
		}

		// 3b. Extract candidate files from goal.md files_to_touch
		if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
			_, _, _, _, _, candidateFiles, _ := parseGoalMetadata(string(data))
			for _, cf := range candidateFiles {
				norm := NormalizeGoalPath(cf)
				if norm != "" && !allowSet[norm] {
					// Check against deny paths
					denied := false
					for _, d := range finalDeny {
						if MatchCharterPath(norm, d) {
							denied = true
							break
						}
					}
					if !denied {
						allowSet[norm] = true
						finalAllow = append(finalAllow, norm)
					}
				}
			}
		}

		// 3c. Extract edit/create paths from work-package.json
		if wp, err := LoadWorkPackage(resolveGoalsRootDir(rootDir), slug); err == nil && wp != nil {
			for _, t := range wp.Tasks {
				for _, p := range append(t.Files.Edit, t.Files.Create...) {
					norm := NormalizeGoalPath(p)
					if norm != "" && !allowSet[norm] {
						denied := false
						for _, d := range finalDeny {
							if MatchCharterPath(norm, d) {
								denied = true
								break
							}
						}
						if !denied {
							allowSet[norm] = true
							finalAllow = append(finalAllow, norm)
						}
					}
				}
			}
		}

		// Always allow goal self-package directory
		goalSelf := filepath.Join("goals", slug) + "/*"
		if !allowSet[goalSelf] {
			allowSet[goalSelf] = true
			finalAllow = append(finalAllow, goalSelf)
		}
		if slug == "sila-onboarding" {
			for _, p := range []string{"AGENTS.md", ".gitignore", ".gitattributes", "goals/*", "tests/*", ".cursor/rules/*", ".agents/skills/*"} {
				if !allowSet[p] {
					allowSet[p] = true
					finalAllow = append(finalAllow, p)
				}
			}
		}
	}

	if duration <= 0 {
		duration = DefaultClaimInactivityTimeout
	}

	if len(requiredChecks) == 0 {
		requiredChecks = []string{"git diff --check", "sila gate"}
	}

	charter := &GoalCharter{
		GoalSlug:       slug,
		Agent:          strings.TrimPrefix(agent, "@"),
		SessionID:      sessionID,
		AllowPaths:     finalAllow,
		DenyPaths:      finalDeny,
		RequiredChecks: requiredChecks,
		MaxDuration:    duration,
		LockToken:      uuid.New().String(),
		CreatedAt:      time.Now().UTC(),
	}
	return charter, nil
}

// SaveCharter atomically persists a charter to .sila/charters/<slug>.json.
func SaveCharter(rootDir string, c *GoalCharter) error {
	if c == nil || c.GoalSlug == "" {
		return fmt.Errorf("invalid charter: missing goal slug")
	}
	ensureEphemeralDirGitignore(CharterDir(rootDir))
	path := CharterPath(rootDir, c.GoalSlug)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal charter: %w", err)
	}
	return AtomicWriteFile(path, data, 0644)
}

// LoadCharter loads a charter from .sila/charters/<slug>.json.
func LoadCharter(rootDir, slug string) (*GoalCharter, error) {
	if slug == "" {
		return nil, fmt.Errorf("missing goal slug")
	}
	path := CharterPath(rootDir, slug)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c GoalCharter
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse charter %s: %w", path, err)
	}
	return &c, nil
}

// RenewCharter refreshes the active charter lease by resetting its creation
// timestamp. Missing charters are treated as a no-op for legacy claims.
func RenewCharter(rootDir, slug string) error {
	if slug == "" {
		return nil
	}
	charter, err := LoadCharter(rootDir, slug)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if charter == nil {
		return nil
	}
	charter.CreatedAt = time.Now().UTC()
	return SaveCharter(rootDir, charter)
}

// RemoveCharter removes the charter JSON and flock file for a goal.
func RemoveCharter(rootDir, slug string) error {
	if slug == "" {
		return nil
	}
	_ = os.Remove(CharterPath(rootDir, slug))
	_ = os.Remove(LockFilePath(rootDir, slug))
	return nil
}

// ListActiveCharters returns all active, non-expired charters in .sila/charters/.
func ListActiveCharters(rootDir string) ([]*GoalCharter, error) {
	dir := CharterDir(rootDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []*GoalCharter
	now := time.Now().UTC()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".json")
		c, err := LoadCharter(rootDir, slug)
		if err != nil || c == nil {
			continue
		}
		// Skip expired charters
		if c.MaxDuration > 0 && !c.CreatedAt.IsZero() && now.Sub(c.CreatedAt) > c.MaxDuration {
			continue
		}
		list = append(list, c)
	}
	return list, nil
}

// EvaluateCharterCollisions verifies if candidate's allow paths collide with any active charter.
func EvaluateCharterCollisions(rootDir string, candidate *GoalCharter, force bool) error {
	if force || candidate == nil {
		return nil
	}
	activeCharters, err := ListActiveCharters(rootDir)
	if err != nil {
		return nil
	}
	for _, active := range activeCharters {
		if active.GoalSlug == candidate.GoalSlug {
			continue
		}
		overlaps := ChartersOverlap(candidate, active)
		if len(overlaps) > 0 {
			return &CollisionError{
				GoalSlug:         candidate.GoalSlug,
				ConflictingGoal:  active.GoalSlug,
				HolderAgent:      active.Agent,
				OverlappingPaths: overlaps,
			}
		}
	}
	return nil
}

// ValidateStagedFilesAgainstCharter evaluates staged git files against charter allow/deny boundaries.
func ValidateStagedFilesAgainstCharter(charter *GoalCharter, stagedFiles []string) error {
	if charter == nil || len(stagedFiles) == 0 {
		return nil
	}
	for _, rawFile := range stagedFiles {
		file := NormalizeGoalPath(rawFile)
		if file == "" {
			continue
		}
		// 1. Check DenyPaths (inviolable deny paths always apply)
		for _, deny := range charter.DenyPaths {
			if MatchCharterPath(file, deny) {
				return fmt.Errorf("Gate rejected: file %q violates GoalCharter for goal %q (denied path)", rawFile, charter.GoalSlug)
			}
		}
		// Shared ledger files (CHANGELOG.md, goals/README.md, .sila/*) are exempt from allow_paths restriction
		if IsSharedLedgerFile(file) {
			continue
		}
		// 2. Check AllowPaths if defined
		if len(charter.AllowPaths) > 0 {
			allowed := false
			for _, allow := range charter.AllowPaths {
				if MatchCharterPath(file, allow) {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("Gate rejected: file %q violates GoalCharter for goal %q (outside authorized allow_paths)", rawFile, charter.GoalSlug)
			}
		}
	}
	return nil
}

// ValidateStagedFilesNoCollisionWithCharter evaluates whether staged files collide with another active charter's AllowPaths.
func ValidateStagedFilesNoCollisionWithCharter(charter *GoalCharter, stagedFiles []string) error {
	if charter == nil || len(stagedFiles) == 0 {
		return nil
	}
	for _, rawFile := range stagedFiles {
		file := NormalizeGoalPath(rawFile)
		if file == "" || IsSharedLedgerFile(file) {
			continue
		}
		for _, allow := range charter.AllowPaths {
			if IsSharedLedgerFile(allow) {
				continue
			}
			if MatchCharterPath(file, allow) {
				return fmt.Errorf("Gate rejected: file %q collides with active charter for goal %q (held by @%s)", rawFile, charter.GoalSlug, charter.Agent)
			}
		}
	}
	return nil
}

// PrintCharter renders a human-readable summary of a GoalCharter.
func PrintCharter(ui render.UI, c *GoalCharter) {
	fmt.Fprintf(os.Stdout, "%s GoalCharter: %s\n", ui.Cyan("📜"), ui.Bold(c.GoalSlug))
	fmt.Fprintf(os.Stdout, "  Agent:       %s\n", ui.Cyan("@"+c.Agent))
	if c.SessionID != "" {
		fmt.Fprintf(os.Stdout, "  Session:     %s\n", ui.Dim(c.SessionID))
	}
	remaining := time.Duration(0)
	if c.MaxDuration > 0 && !c.CreatedAt.IsZero() {
		elapsed := time.Since(c.CreatedAt)
		if elapsed < c.MaxDuration {
			remaining = (c.MaxDuration - elapsed).Round(time.Second)
		}
	}
	fmt.Fprintf(os.Stdout, "  Lock Token:  %s (lease remaining: %s)\n", ui.Dim(c.LockToken), ui.Yellow(remaining.String()))
	fmt.Fprintf(os.Stdout, "  Allow Paths: %s\n", ui.Green(strings.Join(c.AllowPaths, ", ")))
	fmt.Fprintf(os.Stdout, "  Deny Paths:  %s\n", ui.Red(strings.Join(c.DenyPaths, ", ")))
	if len(c.RequiredChecks) > 0 {
		fmt.Fprintf(os.Stdout, "  Checks:      %s\n", ui.Dim(strings.Join(c.RequiredChecks, ", ")))
	}
}

// RenderCharterTOON formats a GoalCharter using TOON syntax.
func RenderCharterTOON(c *GoalCharter) string {
	fields := [][2]string{
		{"slug", c.GoalSlug},
		{"agent", c.Agent},
		{"session_id", c.SessionID},
		{"allow_paths", strings.Join(c.AllowPaths, ",")},
		{"deny_paths", strings.Join(c.DenyPaths, ",")},
		{"lock_token", c.LockToken},
		{"created_at", c.CreatedAt.Format(time.RFC3339)},
		{"max_duration", c.MaxDuration.String()},
	}
	return render.FormatTOONObject("goal_charter", fields)
}

// RunCharterPreCommitAudit is a standalone check helper for gate sentry.
func RunCharterPreCommitAudit(cwd string) (clean bool, err error) {
	activeCharters, err := ListActiveCharters(cwd)
	if err != nil || len(activeCharters) == 0 {
		return true, nil
	}
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return true, nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var staged []string
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			staged = append(staged, s)
		}
	}
	if len(staged) == 0 {
		return true, nil
	}
	for _, c := range activeCharters {
		if err := ValidateStagedFilesAgainstCharter(c, staged); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ExtractFactsContractPaths parses the contents of a facts.md document and extracts
// file paths declared under the "File & Interface Contracts" or "File Contracts" section.
// It recognizes bullet lists, backticked file tokens, and comma-separated path items.
func ExtractFactsContractPaths(factsContent string) []string {
	var paths []string
	if strings.TrimSpace(factsContent) == "" {
		return paths
	}

	seen := make(map[string]bool)
	addPath := func(raw string) {
		clean := strings.TrimSpace(raw)
		clean = strings.Trim(clean, "`'\",;:()")
		if clean == "" {
			return
		}
		// Skip URLs
		if strings.HasPrefix(clean, "http://") || strings.HasPrefix(clean, "https://") {
			return
		}
		// Must look like a path: has directory separator or known extension
		ext := filepath.Ext(clean)
		hasSep := strings.Contains(clean, "/")
		if !hasSep && ext == "" {
			return
		}
		// Skip natural language fragments or lines containing whitespace
		if strings.ContainsAny(clean, " \t\n") {
			return
		}
		norm := NormalizeGoalPath(clean)
		if norm != "" && !seen[norm] {
			seen[norm] = true
			paths = append(paths, norm)
		}
	}

	lines := strings.Split(factsContent, "\n")
	inContractsSection := false
	reSectionHeader := regexp.MustCompile(`(?i)^##\s+.*(?:file|interface).*contract`)
	reNextSection := regexp.MustCompile(`^##\s+`)
	reBacktick := regexp.MustCompile("`([^`]+)`")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if reSectionHeader.MatchString(trimmed) {
			inContractsSection = true
			continue
		} else if inContractsSection && reNextSection.MatchString(trimmed) {
			inContractsSection = false
			break
		}

		if inContractsSection {
			matches := reBacktick.FindAllStringSubmatch(line, -1)
			if len(matches) > 0 {
				for _, m := range matches {
					if len(m) >= 2 {
						parts := strings.Split(m[1], ",")
						for _, p := range parts {
							addPath(p)
						}
					}
				}
			} else {
				bullet := strings.TrimLeft(trimmed, "-*+ ")
				bullet = strings.TrimSpace(bullet)
				if bullet != "" && !strings.HasPrefix(bullet, "Relevant files:") && !strings.HasPrefix(bullet, "Contracts:") {
					parts := strings.Fields(bullet)
					for _, p := range parts {
						addPath(p)
					}
				}
			}
		}
	}

	// Fallback: If no paths found in dedicated section, look across document
	if len(paths) == 0 {
		for _, line := range lines {
			low := strings.ToLower(line)
			if strings.Contains(low, "contract") || strings.Contains(low, "files") {
				matches := reBacktick.FindAllStringSubmatch(line, -1)
				for _, m := range matches {
					if len(m) >= 2 {
						addPath(m[1])
					}
				}
			}
		}
	}

	return paths
}
