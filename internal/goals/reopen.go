package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReopenRecord captures a goal reopening and remediation request.
type ReopenRecord struct {
	GoalSlug       string    `json:"goal_slug"`
	ReopenedAt     time.Time `json:"reopened_at"`
	ReopenedBy     string    `json:"reopened_by"`
	Reason         string    `json:"reason"`
	OriginalCommit string    `json:"original_commit,omitempty"`
	ResolvedCommit string    `json:"resolved_commit,omitempty"`
	ResolvedAt     time.Time `json:"resolved_at"`
	Active         bool      `json:"active"`
	TaintedGoals   []string  `json:"tainted_goals,omitempty"`
	ReopenEpoch    int       `json:"reopen_epoch,omitempty"`
}

// ReopenGoal reopens an implemented goal package for remediation.
func ReopenGoal(rootDir, slug, agent, reason string, claim bool) (*ReopenRecord, *GoalClaim, error) {
	if slug == "" {
		return nil, nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	// Check if already actively reopened
	existing, _ := GetActiveReopen(rootDir, slug)
	if existing != nil && existing.Active {
		return nil, nil, fmt.Errorf("goal %q is already actively reopened (by @%s on %s)", slug, existing.ReopenedBy, existing.ReopenedAt.Format(time.RFC3339))
	}

	// Check if the goal was indeed implemented in git
	commits := scanGitCommitsRaw(rootDir, []string{slug})
	origCommit := commits[slug]
	if origCommit == "" {
		if retro, _ := GetActiveRetrospective(rootDir, slug); retro != nil && retro.Commit != "" {
			origCommit = retro.Commit
		}
	}
	if origCommit == "" {
		if dossier, _ := GetActiveDefectDossier(rootDir, slug); dossier != nil && dossier.CommitSHA != "" {
			origCommit = dossier.CommitSHA
		}
	}
	if origCommit == "" {
		if rec, _ := GetActiveAuditReceipt(rootDir, slug); rec != nil && rec.CommitSHA != "" {
			origCommit = rec.CommitSHA
		}
	}
	if origCommit == "" {
		if acc, _ := EvaluateGoalAcceptance(rootDir, slug); acc != nil && acc.Accepted {
			origCommit = "accepted"
		}
	}
	if origCommit == "" {
		return nil, nil, fmt.Errorf("goal %q is not currently implemented; only implemented goals can be reopened", slug)
	}

	now := time.Now().UTC()
	agent = strings.TrimPrefix(agent, "@")
	if agent == "" {
		agent = "agent"
	}
	if reason == "" {
		reason = "reopened for remediation"
	}

	epoch := 1
	var priorRecord ReopenRecord
	if data, readErr := os.ReadFile(filepath.Join(goalDir, "reopen.json")); readErr == nil {
		if json.Unmarshal(data, &priorRecord) == nil && priorRecord.ReopenEpoch > 0 {
			epoch = priorRecord.ReopenEpoch + 1
		}
	}

	var tainted []string
	if depGraph, err := ScanGoalsDependencies(rootDir); err == nil && depGraph != nil {
		tainted = depGraph.TransitiveDependents(slug)
		sort.Strings(tainted)
	}

	rec := &ReopenRecord{
		GoalSlug:       slug,
		ReopenedAt:     now,
		ReopenedBy:     agent,
		Reason:         reason,
		OriginalCommit: origCommit,
		Active:         true,
		TaintedGoals:   tainted,
		ReopenEpoch:    epoch,
	}

	err := func() error {
		unlock, err := acquireGoalLock(goalDir)
		if err != nil {
			return fmt.Errorf("acquire goal lock: %w", err)
		}
		defer unlock()

		data, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return err
		}

		reopenPath := filepath.Join(goalDir, "reopen.json")
		if err := AtomicWriteFile(reopenPath, data, 0644); err != nil {
			return fmt.Errorf("write reopen.json: %w", err)
		}

		if err := archiveReopenVerificationArtifacts(goalDir, now); err != nil {
			return fmt.Errorf("archive verification artifacts: %w", err)
		}

		if !claim {
			_ = os.Remove(filepath.Join(goalDir, "claim.json"))
		}
		return nil
	}()
	if err != nil {
		return nil, nil, err
	}

	var goalClaim *GoalClaim
	if claim {
		// Claim the goal immediately for the reopening agent
		var claimErr error
		goalClaim, claimErr = ClaimGoal(rootDir, slug, agent, "remediation: "+reason, true)
		if claimErr != nil {
			return rec, nil, fmt.Errorf("reopened but failed to claim: %w", claimErr)
		}
	}

	return rec, goalClaim, nil
}

// archiveReopenVerificationArtifacts moves stale certification evidence into
// goals/<slug>/amendments/ so it cannot remain active alongside reopen.json.
func archiveReopenVerificationArtifacts(goalDir string, ts time.Time) error {
	amendDir := filepath.Join(goalDir, "amendments")
	for _, name := range []string{"certification.json", "audit-receipt.json"} {
		src := filepath.Join(goalDir, name)
		if !fileExists(src) {
			continue
		}
		if err := os.MkdirAll(amendDir, 0755); err != nil {
			return err
		}
		dst := filepath.Join(amendDir, fmt.Sprintf("%s.%d", name, ts.UnixNano()))
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

// GetActiveReopen returns the active ReopenRecord for a goal if present and active.
func GetActiveReopen(rootDir, slug string) (*ReopenRecord, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	reopenPath := filepath.Join(rootDir, "goals", slug, "reopen.json")
	if !fileExists(reopenPath) {
		return nil, nil
	}
	data, err := os.ReadFile(reopenPath)
	if err != nil {
		return nil, err
	}
	var rec ReopenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	if !rec.Active {
		return nil, nil
	}
	return &rec, nil
}

// ResolveReopen marks an active reopen record as satisfied by a resolution commit.
func ResolveReopen(rootDir, slug, commitHash string) error {
	if slug == "" {
		return fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	reopenPath := filepath.Join(goalDir, "reopen.json")
	if !fileExists(reopenPath) {
		return nil
	}
	data, err := os.ReadFile(reopenPath)
	if err != nil {
		return err
	}
	var rec ReopenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return err
	}
	if !rec.Active {
		return nil
	}
	rec.Active = false
	rec.ResolvedCommit = commitHash
	rec.ResolvedAt = time.Now().UTC()
	updatedData, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(reopenPath, updatedData, 0644)
}
