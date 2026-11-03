package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContextFileWeight represents the measured context token weight of a single file.
type ContextFileWeight struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Tokens int    `json:"tokens"`
	Bytes  int64  `json:"bytes"`
	Heavy  bool   `json:"heavy"`
}

// ContextBudget encapsulates the deterministic always-load context surface weight.
type ContextBudget struct {
	TotalTokens int                 `json:"total_tokens"`
	TotalBytes  int64               `json:"total_bytes"`
	Status      string              `json:"status"` // "light", "watch", "heavy"
	Files       []ContextFileWeight `json:"files"`
	TrimTargets []string            `json:"trim_targets,omitempty"`
}

// WindowPressure defines the formal context pressure level inspired by Raven's WindowPressure.
type WindowPressure string

const (
	PressureNormal   WindowPressure = "NORMAL"   // < 15,000 tokens: full fidelity
	PressureWatch    WindowPressure = "WATCH"    // 15,000 - 25,000 tokens: soft compression
	PressureHeavy    WindowPressure = "HEAVY"    // 25,000 - 40,000 tokens: medium compression
	PressureCritical WindowPressure = "CRITICAL" // > 40,000 tokens: emergency compaction
)

// ShrinkResult records the telemetry and pruned output of the multi-pressure degradation engine.
type ShrinkResult struct {
	OriginalTokens int            `json:"original_tokens"`
	FinalTokens    int            `json:"final_tokens"`
	Pressure       WindowPressure `json:"pressure"`
	SectionsPruned []string       `json:"sections_pruned,omitempty"`
	Payload        string         `json:"payload"`
}

// Pressure thresholds in estimated tokens.
const (
	PressureThresholdNormal = 15000
	PressureThresholdWatch  = 25000
	PressureThresholdHeavy  = 40000
)

// DeterminePressure calculates the WindowPressure from an estimated token count.
func DeterminePressure(tokens int) WindowPressure {
	if tokens < PressureThresholdNormal {
		return PressureNormal
	} else if tokens <= PressureThresholdWatch {
		return PressureWatch
	} else if tokens <= PressureThresholdHeavy {
		return PressureHeavy
	}
	return PressureCritical
}

// ParsePressure parses a human or CLI pressure string into a WindowPressure enum.
func ParsePressure(s string) (WindowPressure, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "NORMAL":
		return PressureNormal, true
	case "WATCH":
		return PressureWatch, true
	case "HEAVY":
		return PressureHeavy, true
	case "CRITICAL":
		return PressureCritical, true
	default:
		return PressureNormal, false
	}
}

const (
	// SoftCeilingTokensPerFile is the soft threshold where an always-load instruction file
	// is flagged as heavy and a candidate for loss-free history trimming (approx 24 KB).
	SoftCeilingTokensPerFile = 6000

	// HardCeilingTokensPerFile flags severe context hog files (approx 40 KB).
	HardCeilingTokensPerFile = 10000

	// BudgetWatchTokens is the total threshold where always-load context enters "watch" state.
	BudgetWatchTokens = 25000

	// BudgetHeavyTokens is the total threshold where always-load context is critically heavy.
	BudgetHeavyTokens = 45000
)

// AlwaysLoadFilenames lists standard files AI coding agents load on every session startup.
var AlwaysLoadFilenames = []string{
	"AGENTS.md",
	"CLAUDE.md",
	".cursorrules",
	".windsurfrules",
	filepath.Join(".github", "copilot-instructions.md"),
}

// EstimateTokens calculates a deterministic token count from character length (chars / 4).
func EstimateTokens(chars int) int {
	if chars <= 0 {
		return 0
	}
	return (chars + 3) / 4
}

// MeasureContextBudget deterministically calculates the token weight of always-load
// instruction files in projectDir and the active session handoff.
func (s *Store) MeasureContextBudget(projectDir string, handoff *Handoff) ContextBudget {
	if projectDir == "" {
		cwd, err := os.Getwd()
		if err == nil {
			projectDir = cwd
		}
	}

	cb := ContextBudget{
		Files:       make([]ContextFileWeight, 0, len(AlwaysLoadFilenames)+1),
		TrimTargets: make([]string, 0),
	}

	seenReal := make(map[string]bool, len(AlwaysLoadFilenames))
	for _, rel := range AlwaysLoadFilenames {
		target := filepath.Join(projectDir, rel)
		fi, err := os.Stat(target)
		if err != nil || fi.IsDir() {
			continue
		}
		// A symlink such as CLAUDE.md -> AGENTS.md is one file, not two: count
		// the resolved path once so the surface meter does not double it.
		if resolved, err := filepath.EvalSymlinks(target); err == nil {
			if seenReal[resolved] {
				continue
			}
			seenReal[resolved] = true
		}
		data, err := os.ReadFile(target)
		if err != nil {
			continue
		}
		chars := len(data)
		toks := EstimateTokens(chars)
		heavy := toks > SoftCeilingTokensPerFile

		fw := ContextFileWeight{
			Name:   filepath.Base(rel),
			Path:   rel,
			Tokens: toks,
			Bytes:  fi.Size(),
			Heavy:  heavy,
		}
		cb.Files = append(cb.Files, fw)
		cb.TotalTokens += toks
		cb.TotalBytes += fi.Size()

		if heavy {
			cb.TrimTargets = append(cb.TrimTargets, fmt.Sprintf("%s is %d tok (soft ceiling: %d tok)", rel, toks, SoftCeilingTokensPerFile))
		}
	}

	if handoff != nil {
		hChars := len(handoff.Summary)
		for _, item := range handoff.OpenItems {
			hChars += len(item) + 2
		}
		for _, d := range handoff.Decisions {
			hChars += len(d) + 2
		}
		if hChars > 0 {
			hToks := EstimateTokens(hChars)
			hHeavy := hToks > SoftCeilingTokensPerFile
			fw := ContextFileWeight{
				Name:   "active_handoff",
				Path:   "session_handoff",
				Tokens: hToks,
				Bytes:  int64(hChars),
				Heavy:  hHeavy,
			}
			cb.Files = append(cb.Files, fw)
			cb.TotalTokens += hToks
			cb.TotalBytes += int64(hChars)
			if hHeavy {
				cb.TrimTargets = append(cb.TrimTargets, fmt.Sprintf("active handoff is %d tok (soft ceiling: %d tok)", hToks, SoftCeilingTokensPerFile))
			}
		}
	}

	// Sort files heaviest first
	sort.Slice(cb.Files, func(i, j int) bool {
		return cb.Files[i].Tokens > cb.Files[j].Tokens
	})

	// Determine status
	hasHardCeilingViolation := false
	for _, f := range cb.Files {
		if f.Tokens >= HardCeilingTokensPerFile {
			hasHardCeilingViolation = true
			break
		}
	}

	if cb.TotalTokens >= BudgetHeavyTokens || hasHardCeilingViolation {
		cb.Status = "heavy"
	} else if cb.TotalTokens >= BudgetWatchTokens || len(cb.TrimTargets) > 0 {
		cb.Status = "watch"
	} else {
		cb.Status = "light"
	}

	return cb
}

// FormatSummary formats a concise 1-line summary of context weight.
func (cb *ContextBudget) FormatSummary() string {
	if cb.TotalTokens == 0 {
		return "none detected"
	}

	statusLabel := "Light"
	switch cb.Status {
	case "watch":
		statusLabel = "Watch"
	case "heavy":
		statusLabel = "Heavy"
	}

	var parts []string
	for i, f := range cb.Files {
		if i >= 3 {
			break
		}
		k := float64(f.Tokens) / 1000.0
		if k >= 1.0 {
			parts = append(parts, fmt.Sprintf("%s: %.1fk", f.Name, k))
		} else {
			parts = append(parts, fmt.Sprintf("%s: %dt", f.Name, f.Tokens))
		}
	}

	kTotal := float64(cb.TotalTokens) / 1000.0
	var totalStr string
	if kTotal >= 1.0 {
		totalStr = fmt.Sprintf("~%.1fk tok", kTotal)
	} else {
		totalStr = fmt.Sprintf("~%d tok", cb.TotalTokens)
	}

	if len(parts) > 0 {
		return fmt.Sprintf("%s [%s] (%s)", totalStr, statusLabel, strings.Join(parts, ", "))
	}
	return fmt.Sprintf("%s [%s]", totalStr, statusLabel)
}
