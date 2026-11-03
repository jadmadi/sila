package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
)

// Report represents a structured evaluation report authored by a persona wearing the Reporter Hat.
type Report struct {
	ID             string       `json:"id"`
	AssociatedGoal string       `json:"associated_goal"` // Slug of linked Sila goal (or "unassigned")
	CreatedAt      time.Time    `json:"created_at"`      // Last writing timestamp (initial authoring)
	UpdatedAt      time.Time    `json:"updated_at"`      // Last update timestamp (evidence update or goal link)
	PersonaID      string       `json:"persona_id"`      // Persona slug e.g. "accessibility-sentinel"
	PersonaNumID   int          `json:"persona_num_id"`  // Sequential numeric ID e.g. 12
	PersonaName    string       `json:"persona_name"`    // Full display role name
	Domain         string       `json:"domain"`          // Operational domain e.g. "access", "ui-ux"
	TargetURL      string       `json:"target_url"`      // Inspected URL or repository boundary
	Status         string       `json:"status"`          // "pass", "warning", "critical"
	Phase          int          `json:"phase,omitempty"` // Disjoint execution phase (1..6)
	Summary        string       `json:"summary"`         // Executive overview
	Findings       []Finding    `json:"findings"`        // Evidenced issues discovered
	Suggestions    []Suggestion `json:"suggestions"`     // Actionable recommendations
	SkillsUsed     []string     `json:"skills_used"`     // Skills invoked during verification
}

// ReportsDir returns the canonical path to .sila/reports/ within the active project.
func ReportsDir(baseDir string) string {
	silaDir := config.ProjectConfigDir(baseDir)
	return filepath.Join(silaDir, "reports")
}

// SaveReport stores a structured report under .sila/reports/<id>.json.
func SaveReport(baseDir string, rep *Report) error {
	if rep == nil {
		return fmt.Errorf("cannot save nil report")
	}

	dir := ReportsDir(baseDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create reports directory: %w", err)
	}

	now := time.Now().UTC()
	if rep.CreatedAt.IsZero() {
		rep.CreatedAt = now
	}
	rep.UpdatedAt = now

	if rep.ID == "" {
		slugPart := rep.PersonaID
		if slugPart == "" {
			slugPart = "eval"
		}
		rep.ID = fmt.Sprintf("rep_%s_%d", slugPart, now.Unix())
	}

	if rep.AssociatedGoal == "" {
		rep.AssociatedGoal = "unassigned"
	}

	filePath := filepath.Join(dir, rep.ID+".json")
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report %s: %w", rep.ID, err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("write report file %s: %w", filePath, err)
	}

	return nil
}

// GetReport retrieves a specific report by ID from .sila/reports/<id>.json.
func GetReport(baseDir, id string) (*Report, error) {
	normID := strings.TrimSuffix(id, ".json")
	filePath := filepath.Join(ReportsDir(baseDir), normID+".json")

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read report %s: %w", normID, err)
	}

	var rep Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("parse report %s: %w", normID, err)
	}

	return &rep, nil
}

// ListReports scans .sila/reports/ and returns all reports sorted by UpdatedAt descending.
func ListReports(baseDir string) ([]*Report, error) {
	dir := ReportsDir(baseDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Report{}, nil
		}
		return nil, fmt.Errorf("read reports dir: %w", err)
	}

	var reports []*Report
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var rep Report
		if err := json.Unmarshal(data, &rep); err != nil {
			continue
		}
		reports = append(reports, &rep)
	}

	sort.Slice(reports, func(i, j int) bool {
		return reports[i].UpdatedAt.After(reports[j].UpdatedAt)
	})

	return reports, nil
}

// FindReportsForGoal filters reports in .sila/reports/ by associated goal slug.
func FindReportsForGoal(baseDir, goalSlug string) ([]*Report, error) {
	normGoal := strings.TrimPrefix(goalSlug, "goals/")
	all, err := ListReports(baseDir)
	if err != nil {
		return nil, err
	}

	var matched []*Report
	for _, r := range all {
		cleanAssoc := strings.TrimPrefix(r.AssociatedGoal, "goals/")
		if strings.EqualFold(cleanAssoc, normGoal) {
			matched = append(matched, r)
		}
	}

	return matched, nil
}

// LinkReportToGoal updates a report's AssociatedGoal and marks UpdatedAt.
func LinkReportToGoal(baseDir, reportID, goalSlug string) error {
	rep, err := GetReport(baseDir, reportID)
	if err != nil {
		return err
	}

	rep.AssociatedGoal = strings.TrimPrefix(goalSlug, "goals/")
	return SaveReport(baseDir, rep)
}
