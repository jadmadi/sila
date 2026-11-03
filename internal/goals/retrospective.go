package goals

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// RetrospectiveRecord captures an atomic retrospective goal implementation record.
type RetrospectiveRecord struct {
	GoalSlug   string    `json:"goal_slug"`
	Status     string    `json:"status"`
	Commit     string    `json:"commit"`
	RecordedAt time.Time `json:"recorded_at"`
}

// GetActiveRetrospective returns the RetrospectiveRecord for a goal if recorded as implemented.
// It checks goals/<slug>/retrospective.json and falls back to inspecting goal.md for Status: implemented and Commit.
func GetActiveRetrospective(rootDir, slug string) (*RetrospectiveRecord, error) {
	if slug == "" {
		return nil, nil
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	jsonPath := filepath.Join(goalDir, "retrospective.json")
	if fileExists(jsonPath) {
		if data, err := os.ReadFile(jsonPath); err == nil {
			var rec RetrospectiveRecord
			if err := json.Unmarshal(data, &rec); err == nil && strings.EqualFold(rec.Status, "implemented") && rec.Commit != "" {
				return &rec, nil
			}
		}
	}

	goalPath := filepath.Join(goalDir, "goal.md")
	if fileExists(goalPath) {
		if data, err := os.ReadFile(goalPath); err == nil {
			content := string(data)
			status, commit := parseGoalStatusAndCommit(content)
			if strings.EqualFold(status, "implemented") && commit != "" {
				return &RetrospectiveRecord{
					GoalSlug:   slug,
					Status:     "implemented",
					Commit:     commit,
					RecordedAt: time.Now().UTC(),
				}, nil
			}
		}
	}

	return nil, nil
}

func parseGoalStatusAndCommit(content string) (status, commit string) {
	// 1. Check frontmatter
	if m := frontmatterRE.FindStringSubmatch(content); len(m) > 1 {
		fm := m[1]
		for line := range strings.SplitSeq(fm, "\n") {
			parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
			if len(parts) == 2 {
				k := strings.ToLower(strings.TrimSpace(parts[0]))
				v := strings.Trim(strings.TrimSpace(stripInlineComment(parts[1])), `"'`)
				if k == "status" {
					status = v
				} else if k == "commit" || k == "sha" || k == "hash" {
					commit = v
				}
			}
		}
	}

	// 2. Check markdown bullet points if not set in frontmatter
	if status == "" {
		statusLineRE := regexp.MustCompile(`(?m)^[-*]\s*\*\*Status\*\*:\s*([^\n\r]+)`)
		if m := statusLineRE.FindStringSubmatch(content); len(m) > 1 {
			status = strings.Trim(strings.TrimSpace(stripInlineComment(m[1])), `"'`+"`")
		}
	}
	if commit == "" {
		commitLineRE := regexp.MustCompile(`(?m)^[-*]\s*\*\*Commit\*\*:\s*([^\n\r]+)`)
		if m := commitLineRE.FindStringSubmatch(content); len(m) > 1 {
			commit = strings.Trim(strings.TrimSpace(stripInlineComment(m[1])), `"'`+"`")
		}
	}

	return status, commit
}
