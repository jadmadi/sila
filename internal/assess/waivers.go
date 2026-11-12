package assess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GoalWaiver records an explicitly approved exception for one audit probe.
type GoalWaiver struct {
	Probe      string   `json:"probe"`
	Reason     string   `json:"reason"`
	Upstream   []string `json:"upstream"`
	ApprovedBy string   `json:"approved_by"`
	ApprovedAt string   `json:"approved_at"`
	ReviewBy   string   `json:"review_by"`
	Fallback   string   `json:"fallback"`
}

type goalWaiverFile struct {
	Schema  string       `json:"schema"`
	Goal    string       `json:"goal"`
	Waivers []GoalWaiver `json:"waivers"`
}

// LoadGoalWaivers reads goals/<slug>/waiver.json. A missing file means no
// waivers. Invalid or expired waivers are errors, never silent passes.
func LoadGoalWaivers(rootDir, slug string, now time.Time) (map[string]GoalWaiver, error) {
	path := filepath.Join(rootDir, "goals", slug, "waiver.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]GoalWaiver{}, nil
	}
	if err != nil {
		return nil, err
	}
	var file goalWaiverFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("waiver: %w", err)
	}
	if file.Schema != "sila/goal-waiver/v1" {
		return nil, fmt.Errorf("waiver: unsupported schema %q", file.Schema)
	}
	if file.Goal != slug {
		return nil, fmt.Errorf("waiver: goal %q does not match %q", file.Goal, slug)
	}
	out := make(map[string]GoalWaiver, len(file.Waivers))
	for _, waiver := range file.Waivers {
		waiver.Probe = strings.TrimSpace(waiver.Probe)
		if waiver.Probe == "" {
			return nil, fmt.Errorf("waiver: probe is required")
		}
		if len(strings.TrimSpace(waiver.Reason)) < 40 {
			return nil, fmt.Errorf("waiver %q: reason must be at least 40 characters", waiver.Probe)
		}
		if strings.TrimSpace(waiver.ApprovedBy) == "" {
			return nil, fmt.Errorf("waiver %q: approved_by is required", waiver.Probe)
		}
		reviewBy, err := time.Parse("2006-01-02", strings.TrimSpace(waiver.ReviewBy))
		if err != nil {
			return nil, fmt.Errorf("waiver %q: review_by must be a date", waiver.Probe)
		}
		if !reviewBy.After(now.UTC()) {
			return nil, fmt.Errorf("waiver %q expired on %s", waiver.Probe, waiver.ReviewBy)
		}
		if len(waiver.Upstream) == 0 {
			return nil, fmt.Errorf("waiver %q: upstream reference is required", waiver.Probe)
		}
		if strings.TrimSpace(waiver.Fallback) == "" {
			return nil, fmt.Errorf("waiver %q: fallback check is required", waiver.Probe)
		}
		if _, exists := out[waiver.Probe]; exists {
			return nil, fmt.Errorf("waiver %q is duplicated", waiver.Probe)
		}
		out[waiver.Probe] = waiver
	}
	return out, nil
}

// WaivedProbeResult builds the recorded result for a waived probe.
func WaivedProbeResult(probe Probe, waiver GoalWaiver) ProbeResult {
	return ProbeResult{
		ProbeID:       probe.ID,
		ProbeName:     probe.Name,
		Type:          probe.Type,
		RequirementID: probe.RequirementID,
		Passed:        true,
		Waived:        true,
		WaiverReason:  fmt.Sprintf("%s (approved by %s, review by %s)", waiver.Reason, waiver.ApprovedBy, waiver.ReviewBy),
	}
}
