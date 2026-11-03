package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SupersededRecord captures the record of a goal being superseded by another goal or commit.
type SupersededRecord struct {
	GoalSlug     string    `json:"goal_slug"`
	SupersededBy string    `json:"superseded_by"`
	SupersededAt time.Time `json:"superseded_at"`
	Agent        string    `json:"agent"`
	Reason       string    `json:"reason"`
	Active       bool      `json:"active"`
}

// SupersedeGoal marks a goal as superseded by another goal, release, or commit.
func SupersedeGoal(rootDir, slug, supersededBy, agent, reason string) (*SupersededRecord, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}

	existing, _ := GetActiveSupersede(rootDir, slug)
	if existing != nil && existing.Active {
		return nil, fmt.Errorf("goal %q is already actively superseded (by %s on %s)", slug, existing.SupersededBy, existing.SupersededAt.Format(time.RFC3339))
	}

	agent = strings.TrimPrefix(agent, "@")
	if agent == "" {
		agent = "agent"
	}
	if reason == "" {
		if supersededBy != "" {
			reason = fmt.Sprintf("superseded by %s", supersededBy)
		} else {
			reason = "superseded by subsequent architecture"
		}
	}

	rec := &SupersededRecord{
		GoalSlug:     slug,
		SupersededBy: supersededBy,
		SupersededAt: time.Now().UTC(),
		Agent:        agent,
		Reason:       reason,
		Active:       true,
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal superseded.json: %w", err)
	}

	supPath := filepath.Join(goalDir, "superseded.json")
	if err := AtomicWriteFile(supPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write superseded.json: %w", err)
	}

	// Release any active claim if held
	claimPath := filepath.Join(goalDir, "claim.json")
	if fileExists(claimPath) {
		_ = os.Remove(claimPath)
	}
	lockPath := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockPath) {
		_ = os.Remove(lockPath)
	}
	_ = RemoveCharter(rootDir, slug)

	// Auto-sync README
	if audit, err := ScanGoals(rootDir); err == nil {
		_ = SyncReadme(rootDir, audit)
	}

	return rec, nil
}

// GetActiveSupersede returns the active superseded record if one exists.
func GetActiveSupersede(rootDir, slug string) (*SupersededRecord, error) {
	if slug == "" {
		return nil, nil
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	supPath := filepath.Join(rootDir, "goals", slug, "superseded.json")
	if !fileExists(supPath) {
		return nil, nil
	}
	data, err := os.ReadFile(supPath)
	if err != nil {
		return nil, err
	}
	var rec SupersededRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	if !rec.Active {
		return nil, nil
	}
	return &rec, nil
}
