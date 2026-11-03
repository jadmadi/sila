package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// CommitMemoryWriteResult is the pre-commit memory-write sentry outcome.
type CommitMemoryWriteResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // pass | fail | skip
	Detail string `json:"detail"`
	Code   string `json:"code,omitempty"`
	Memo   int    `json:"memo,omitempty"`
	Lesson int    `json:"lesson,omitempty"`
	Case   int    `json:"case,omitempty"`
	Window string `json:"window,omitempty"`
	Waived bool   `json:"waived,omitempty"`
}

// MemoryWriteOptions configures the sentry.
type MemoryWriteOptions struct {
	RepoRoot string
	Subject  string
	Staged   []string
	DBPath   string
	Now      time.Time
}

var (
	parenAttributionRE   = regexp.MustCompile(`\(goals/([a-z0-9][a-z0-9-]*)/goal\.md\)`)
	bracketAttributionRE = regexp.MustCompile(`\[goal:\s*([a-z0-9][a-z0-9-]*)\]`)
	closurePrefixRE      = regexp.MustCompile(`^(feat|fix|refactor|perf|chore)(\([^)]*\))?!?:`)
	docsTestPrefixRE     = regexp.MustCompile(`^(docs|test)(\([^)]*\))?!?:`)
)

// ParseAttribution returns one goal slug, a closure flag, and docs/test flags.
func ParseAttribution(subject string) (slug string, closure, docsOnly, ambiguous bool) {
	slugSet := map[string]bool{}
	for _, m := range parenAttributionRE.FindAllStringSubmatch(subject, -1) {
		slugSet[m[1]] = true
	}
	for _, m := range bracketAttributionRE.FindAllStringSubmatch(subject, -1) {
		slugSet[m[1]] = true
	}
	if len(slugSet) == 1 {
		for s := range slugSet {
			slug = s
		}
	}
	if len(slugSet) > 1 {
		ambiguous = true
	}
	closure = closurePrefixRE.MatchString(strings.TrimSpace(subject))
	docsOnly = docsTestPrefixRE.MatchString(strings.TrimSpace(subject)) || strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "chore(goals)")
	if docsOnly {
		closure = false
	}
	return slug, closure, docsOnly, ambiguous
}

// CheckMemoryWrite evaluates the current commit subject against recent grounded
// memory writes. It performs no network access and never mutates evidence.
func CheckMemoryWrite(opts MemoryWriteOptions) CommitMemoryWriteResult {
	res := CommitMemoryWriteResult{Name: "memory-write"}
	if opts.RepoRoot == "" {
		opts.RepoRoot = "."
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if _, err := os.Stat(filepath.Join(opts.RepoRoot, "goals")); err != nil {
		res.Status, res.Detail = "skip", "no goals directory"
		return res
	}

	slug, closure, docsOnly, ambiguous := ParseAttribution(opts.Subject)
	if ambiguous {
		res.Status, res.Code = "fail", "MEMORY_ATTRIBUTION_AMBIGUOUS"
		res.Detail = "commit subject names more than one goal slug"
		return res
	}
	if slug == "" && !docsOnly {
		slug, closure = inferStagedClosureGoal(opts.RepoRoot, opts.Staged)
	}
	if slug == "" {
		res.Status, res.Detail = "skip", "unattributed commit"
		return res
	}
	if docsOnly || !closure {
		res.Status, res.Detail = "skip", "docs/test or non-closure commit"
		return res
	}

	audit, err := goals.ScanGoals(opts.RepoRoot)
	if err != nil || audit == nil {
		res.Status, res.Detail = "skip", "goal registry unavailable"
		return res
	}
	var goal *goals.Goal
	for i := range audit.Goals {
		if audit.Goals[i].Slug == slug {
			goal = &audit.Goals[i]
			break
		}
	}
	if goal == nil {
		res.Status, res.Detail = "skip", "attributed goal package not found"
		return res
	}

	// Waiver is checked before the store so a documented waiver still works in
	// an environment with a missing store.
	if waived, code, detail := validMemoryWaiver(opts.RepoRoot, slug); waived {
		res.Status, res.Detail, res.Waived = "pass", detail, true
		return res
	} else if code != "" {
		res.Status, res.Code, res.Detail = "fail", code, detail
		return res
	}

	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = os.Getenv("SILA_DB")
	}
	if dbPath == "" {
		dbPath = store.DefaultDBPath()
	}
	if _, err := os.Stat(dbPath); err != nil {
		res.Status = "skip"
		res.Detail = "no knowledge store in " + dbPath
		return res
	}
	st, err := store.OpenRO(dbPath)
	if err != nil {
		res.Status, res.Code = "fail", "MEMORY_STORE_UNAVAILABLE"
		res.Detail = err.Error()
		return res
	}
	defer st.Close()

	project, err := st.ResolveScope("", opts.RepoRoot)
	if err != nil || project.ID == "" {
		project.ID = store.ProjectID(opts.RepoRoot)
	}
	windowStart := memoryWindowStart(opts.RepoRoot, slug, opts.Now)
	files := append(append([]string{}, opts.Staged...), goal.FilesToTouch...)
	eval, err := EvaluateMemoryWrites(context.Background(), st, MemoryWriteScope{
		ProjectID:   project.ID,
		SessionID:   strings.TrimSpace(os.Getenv("SILA_SESSION_ID")),
		GoalSlug:    slug,
		ActiveGoal:  strings.TrimSpace(os.Getenv("SILA_ACTIVE_GOAL")),
		WindowStart: windowStart,
		Files:       files,
	})
	if err != nil {
		res.Status = "fail"
		res.Code = "MEMORY_STORE_UNAVAILABLE"
		if strings.Contains(err.Error(), "MEMORY_STORE_SLOW") {
			res.Code = "MEMORY_STORE_SLOW"
		}
		res.Detail = err.Error()
		return res
	}
	res.Memo, res.Lesson, res.Case = eval.Memos, eval.Lessons, eval.Cases
	res.Window = eval.Window
	if eval.Verdict != "pass" {
		res.Status = "fail"
		res.Code = "MEMORY_WRITE_MISSING"
		hint := "<file>"
		if len(files) > 0 {
			hint = files[0]
		}
		ruleNote := "none of the staged files is referenced by a memory write in the window"
		if eval.InWindow > 0 && len(eval.UnmatchedFiles) > 0 {
			ruleNote = fmt.Sprintf("%s; %d write(s) in window referenced %s", ruleNote, eval.InWindow, strings.Join(eval.UnmatchedFiles, ", "))
		}
		res.Detail = fmt.Sprintf("no grounded memory write in window %s (%s); run sila memo \"<decision>\" --files=%s / sila lesson \"<symptom>\" --cause=... --fix=... --files=%s or add .sila/memory-write-waiver.json (%s)", res.Window, ruleNote, hint, hint, MemoryWaiverSchemaHint(slug))
		return res
	}
	res.Status = "pass"
	res.Detail = fmt.Sprintf("%d writes grounded (%d memo, %d lesson, %d case; window %s)", eval.Writes, eval.Memos, eval.Lessons, eval.Cases, res.Window)
	return res
}

// PreClaimMemoryGraceWindow allows grounded memos, lessons, or cases recorded
// earlier in the same session (up to 2 hours before claiming/re-claiming a goal)
// to satisfy the goal memory-write contract.
const PreClaimMemoryGraceWindow = 2 * time.Hour

// MemoryWaiverSchemaHint returns the exact JSON template required for .sila/memory-write-waiver.json.
func MemoryWaiverSchemaHint(slug string) string {
	if strings.TrimSpace(slug) == "" {
		slug = "<slug>"
	}
	return fmt.Sprintf(`{"schema":"sila/memory-write-waiver/v1","goal":%q,"reason":"<>=20 chars>","approved_by":"<operator>","expires_commit":"<HEAD>"}`, slug)
}

func memoryWindowStart(repoRoot, slug string, now time.Time) time.Time {
	if data, err := os.ReadFile(filepath.Join(repoRoot, "goals", slug, "claim.json")); err == nil {
		var claim struct {
			ClaimedAt time.Time `json:"claimed_at"`
		}
		if json.Unmarshal(data, &claim) == nil && !claim.ClaimedAt.IsZero() {
			return claim.ClaimedAt.UTC().Add(-PreClaimMemoryGraceWindow)
		}
	}
	cmd := exec.Command("git", "-C", repoRoot, "log", "--format=%cI", "--grep="+slug)
	if out, err := cmd.Output(); err == nil {
		var earliest time.Time
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(line)); err == nil {
				if earliest.IsZero() || t.Before(earliest) {
					earliest = t
				}
			}
		}
		if !earliest.IsZero() {
			// Memos recorded just before the first attributed commit belong to the goal.
			return earliest.Add(-PreClaimMemoryGraceWindow)
		}
	}
	return now.Add(-48 * time.Hour)
}

type memoryWaiver struct {
	Schema        string `json:"schema"`
	Goal          string `json:"goal"`
	Reason        string `json:"reason"`
	ApprovedBy    string `json:"approved_by"`
	ExpiresCommit string `json:"expires_commit"`
}

func validMemoryWaiver(repoRoot, slug string) (bool, string, string) {
	path := filepath.Join(repoRoot, ".sila", "memory-write-waiver.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", ""
	}
	var waiver memoryWaiver
	if err := json.Unmarshal(data, &waiver); err != nil {
		return false, "MEMORY_WAIVER_INVALID", "waiver is not valid JSON; expected " + MemoryWaiverSchemaHint(slug)
	}
	if waiver.Schema != "sila/memory-write-waiver/v1" || waiver.Goal != slug || len(strings.TrimSpace(waiver.Reason)) < 20 || waiver.ExpiresCommit == "" {
		return false, "MEMORY_WAIVER_INVALID", "waiver schema, goal, reason (>=20 chars), or expires_commit is invalid; expected " + MemoryWaiverSchemaHint(slug)
	}
	cmd := exec.Command("git", "-C", repoRoot, "merge-base", "--is-ancestor", "HEAD", waiver.ExpiresCommit)
	if err := cmd.Run(); err != nil {
		return false, "MEMORY_WAIVER_EXPIRED", "waiver expires_commit is not HEAD or a descendant of HEAD"
	}
	return true, "", "waived: " + waiver.Reason
}

func factGrounded(fact store.Fact, slug string, staged, goalFiles map[string]bool, activeGoal, sessionID string) bool {
	if strings.Contains(fact.Claim, slug) {
		return true
	}
	if activeGoal != "" && fact.ToolName == activeGoal {
		return true
	}
	if sessionID != "" && fact.SessionRef == sessionID {
		return true
	}
	for _, file := range fact.RepoFiles {
		clean := filepath.ToSlash(filepath.Clean(file))
		if staged[clean] || goalFiles[clean] {
			return true
		}
	}
	return false
}

func normalizedSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out[filepath.ToSlash(filepath.Clean(item))] = true
	}
	return out
}

func inferStagedClosureGoal(repoRoot string, staged []string) (string, bool) {
	slugs := map[string]bool{}
	hasClosureArtifact := false
	for _, f := range staged {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(f)))
		parts := strings.Split(clean, "/")
		if len(parts) == 3 && parts[0] == "goals" && parts[1] != "" {
			slugs[parts[1]] = true
			if parts[2] == "evidence.md" || parts[2] == "certification.json" {
				hasClosureArtifact = true
			}
		}
	}
	if len(slugs) != 1 || !hasClosureArtifact {
		return "", false
	}
	for s := range slugs {
		return s, true
	}
	return "", false
}

func humanWindow(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}
