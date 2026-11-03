package skills

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// JSONConfig mirrors google3/third_party/jetski/cortex/customizations.JSONConfig
// with an optional Sila ownership envelope for safe lifecycle management.
type JSONConfig struct {
	SilaEnvelope *SilaEnvelope `json:"_sila,omitempty"`
	Entries      []PathEntry   `json:"entries,omitempty"`
	Exclude      []string      `json:"exclude,omitempty"`
}

// PathEntry mirrors google3/third_party/jetski/cortex/customizations.PathEntry
type PathEntry struct {
	Path        string   `json:"path"`
	IncludeOnly []string `json:"include_only,omitempty"`
}

// SilaEnvelope guarantees provenance, concurrency collision detection, and safe rollback.
type SilaEnvelope struct {
	Managed         bool      `json:"managed"`
	Version         int       `json:"version"`
	ProjectID       string    `json:"project_id,omitempty"`
	ContentHash     string    `json:"content_hash"`
	UpdatedAt       time.Time `json:"updated_at"`
	ManagedExcludes []string  `json:"managed_excludes,omitempty"`
}

// SilaMetadata is an alias for SilaEnvelope for backwards and cross-spec compatibility.
type SilaMetadata = SilaEnvelope

// SkillSummary provides a compact, low-token representation of a skill for progressive disclosure.
type SkillSummary struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Trigger         string `json:"trigger"` // 1-sentence prompt trigger
	EstimatedTokens int    `json:"estimated_tokens"`
	HasScripts      bool   `json:"has_scripts"`
	Source          string `json:"source"` // "project" or "global"
}

// FusedSkillResult represents a skill or grounded lesson ranked by Reciprocal Rank Fusion.
type FusedSkillResult struct {
	SkillSummary
	RRFScore float64 `json:"rrf_score"`
	Tier     int     `json:"tier"` // 1 = Authoritative Skill, 2 = Grounded Runbook Lesson
	Path     string  `json:"path,omitempty"`
}

// ExtractTrigger extracts a 1-sentence prompt trigger from a description.
func ExtractTrigger(description string) string {
	d := strings.TrimSpace(description)
	if d == "" {
		return ""
	}
	// Check for first sentence boundary
	if idx := strings.Index(d, ". "); idx > 0 {
		return d[:idx+1]
	}
	if strings.HasSuffix(d, ".") {
		return d
	}
	if len(d) > 120 {
		return d[:117] + "..."
	}
	return d
}

// DiscoveredSkill represents an advertised skill exposed to the agent prompt or catalog.
type DiscoveredSkill struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Source      string `json:"source"` // "project" or "global"
}

// SkillResource represents an auxiliary file (script, reference, asset) inside a skill bundle.
type SkillResource struct {
	Path    string `json:"path"`
	Type    string `json:"type"` // "script", "reference", "asset"
	Content string `json:"content,omitempty"`
}

// Skill represents a unified skill model across CLI, MCP, and harness discovery.
type Skill struct {
	Slug          string          `json:"slug"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Path          string          `json:"path"`
	BundleDir     string          `json:"bundle_dir,omitempty"`
	Source        string          `json:"source"` // "project" or "global"
	ContentHash   string          `json:"content_hash,omitempty"`
	Instructions  string          `json:"instructions,omitempty"`
	HasScripts    bool            `json:"has_scripts"`
	HasRefs       bool            `json:"has_references"`
	HasAssets     bool            `json:"has_assets,omitempty"`
	Scripts       []string        `json:"scripts,omitempty"`
	References    []string        `json:"references,omitempty"`
	Assets        []string        `json:"assets,omitempty"`
	Tags          []string        `json:"tags,omitempty"`
	Compatibility []string        `json:"compatibility,omitempty"`
	Triggers      []string        `json:"triggers,omitempty"`
	Resources     []SkillResource `json:"resources,omitempty"`
	RawMarkdown   string          `json:"raw_markdown,omitempty"`
}

// ToSummary converts a full Skill to a compact SkillSummary.
func (s *Skill) ToSummary() SkillSummary {
	trigger := ""
	if len(s.Triggers) > 0 && s.Triggers[0] != "" {
		trigger = s.Triggers[0]
	} else {
		trigger = ExtractTrigger(s.Description)
	}
	toks := (len(s.Instructions) + 3) / 4
	if toks == 0 && len(s.RawMarkdown) > 0 {
		toks = (len(s.RawMarkdown) + 3) / 4
	}
	return SkillSummary{
		Slug:            s.Slug,
		Name:            s.Name,
		Trigger:         trigger,
		EstimatedTokens: toks,
		HasScripts:      s.HasScripts,
		Source:          s.Source,
	}
}

// Validate checks for structural integrity, syntax correctness, and regex validity.
func (c *JSONConfig) Validate() error {
	if len(c.Entries) == 0 && len(c.Exclude) == 0 {
		return fmt.Errorf("invalid skills configuration: at least one entry or exclude pattern required")
	}

	for i, entry := range c.Entries {
		if strings.TrimSpace(entry.Path) == "" {
			return fmt.Errorf("entries[%d].path cannot be empty", i)
		}
		for j, pattern := range entry.IncludeOnly {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("entries[%d].include_only[%d] invalid regex %q: %w", i, j, pattern, err)
			}
		}
	}

	for i, pattern := range c.Exclude {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("exclude[%d] invalid regex %q: %w", i, pattern, err)
		}
	}

	return nil
}
