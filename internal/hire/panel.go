package hire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PanelRole defines the cognitive role assigned to a model in the advisory jury.
type PanelRole string

const (
	RolePlanner PanelRole = "planner" // High-level architecture, contracts, invariants
	RoleCoder   PanelRole = "coder"   // Concrete syntax, edge cases, implementation
	RoleAuditor PanelRole = "auditor" // Adversarial scrutiny, security, blind spots
)

// RoleConfig defines the endpoint and model configuration for a specific role.
type RoleConfig struct {
	Model       string  `json:"model"`
	Endpoint    string  `json:"endpoint,omitempty"`
	APIKey      string  `json:"api_key,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
}

// PanelConfig defines the full three-model advisory jury configuration.
type PanelConfig struct {
	Enabled         bool                     `json:"enabled"`
	DefaultEndpoint string                   `json:"default_endpoint"`
	Roles           map[PanelRole]RoleConfig `json:"roles"`
	TimeoutSeconds  int                      `json:"timeout_seconds"`
}

// DefaultPanelConfig returns a local-first panel configuration targeting local Ollama by default.
func DefaultPanelConfig() *PanelConfig {
	return &PanelConfig{
		Enabled:         false,
		DefaultEndpoint: "http://localhost:11434/v1",
		TimeoutSeconds:  30,
		Roles: map[PanelRole]RoleConfig{
			RolePlanner: {
				Model:       "claude-opus-5-5-max",
				Endpoint:    "http://localhost:11434/v1",
				Temperature: 0.1,
				MaxTokens:   2048,
			},
			RoleCoder: {
				Model:       "grok-4.7-xhigh-fast",
				Endpoint:    "http://localhost:11434/v1",
				Temperature: 0.2,
				MaxTokens:   2048,
			},
			RoleAuditor: {
				Model:       "gpt-5.6-sol-max",
				Endpoint:    "http://localhost:11434/v1",
				Temperature: 0.0,
				MaxTokens:   2048,
			},
		},
	}
}

// LoadPanelConfig loads panel configuration from `.sila/models.json` if present,
// falling back to DefaultPanelConfig().
func LoadPanelConfig(projectDir string) *PanelConfig {
	cfg := DefaultPanelConfig()

	var configPath string
	if projectDir != "" {
		configPath = filepath.Join(projectDir, ".sila", "models.json")
	} else {
		configPath = filepath.Join(".sila", "models.json")
	}

	data, err := os.ReadFile(configPath)
	if err == nil {
		var userCfg PanelConfig
		if err := json.Unmarshal(data, &userCfg); err == nil {
			if userCfg.DefaultEndpoint != "" {
				cfg.DefaultEndpoint = userCfg.DefaultEndpoint
			}
			if userCfg.TimeoutSeconds > 0 {
				cfg.TimeoutSeconds = userCfg.TimeoutSeconds
			}
			for r, rc := range userCfg.Roles {
				cfg.Roles[r] = rc
			}
			cfg.Enabled = userCfg.Enabled
		}
	}

	return cfg
}

// FindingSeverity categorizes whether an audit finding is corroborated by consensus.
type FindingSeverity string

const (
	SeverityConsensus FindingSeverity = "CONSENSUS"  // Corroborated by >=2 models
	SeverityLoneModel FindingSeverity = "LONE_MODEL" // Raised by exactly 1 model
)

// PanelFinding represents a single issue raised during multi-model review.
type PanelFinding struct {
	Severity    FindingSeverity `json:"severity"`
	Role        PanelRole       `json:"role"`
	Model       string          `json:"model"`
	Category    string          `json:"category"`
	Description string          `json:"description"`
}

// PanelReviewResult encapsulates the combined findings of the advisory jury.
type PanelReviewResult struct {
	EvaluatedAt       time.Time            `json:"evaluated_at"`
	Duration          time.Duration        `json:"duration"`
	Passed            bool                 `json:"passed"`
	ConsensusScore    float64              `json:"consensus_score"` // 0.0 to 1.0
	ConsensusFindings []PanelFinding       `json:"consensus_findings"`
	LoneFindings      []PanelFinding       `json:"lone_findings"`
	RawResponses      map[PanelRole]string `json:"raw_responses"`
	OfflineFallback   bool                 `json:"offline_fallback"`
	Summary           string               `json:"summary"`
}

// SynthesizeJuryResults merges independent role responses into a unified consensus verdict.
func SynthesizeJuryResults(responses map[PanelRole]string, cfg *PanelConfig) *PanelReviewResult {
	result := &PanelReviewResult{
		EvaluatedAt:       time.Now().UTC(),
		RawResponses:      responses,
		ConsensusFindings: []PanelFinding{},
		LoneFindings:      []PanelFinding{},
		Passed:            true,
		ConsensusScore:    1.0,
	}

	// Extract flagged issues from each model response
	roleIssues := make(map[PanelRole][]string)
	for role, resp := range responses {
		lines := strings.Split(resp, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "ISSUE:") || strings.HasPrefix(trimmed, "DEFECT:") || strings.HasPrefix(trimmed, "- FAIL:") {
				desc := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(trimmed, "ISSUE:"), "DEFECT:"), "- FAIL:"))
				if desc != "" {
					roleIssues[role] = append(roleIssues[role], desc)
				}
			}
		}
	}

	// Cross-reference issues to identify consensus vs lone findings
	allFindings := make(map[string]map[PanelRole]bool)
	for role, issues := range roleIssues {
		for _, issue := range issues {
			norm := strings.ToLower(issue)
			if allFindings[norm] == nil {
				allFindings[norm] = make(map[PanelRole]bool)
			}
			allFindings[norm][role] = true
		}
	}

	for normIssue, roles := range allFindings {
		firstRole := RoleAuditor
		for r := range roles {
			firstRole = r
			break
		}
		modelName := "unknown"
		if cfg != nil {
			if rc, ok := cfg.Roles[firstRole]; ok {
				modelName = rc.Model
			}
		}

		if len(roles) >= 2 {
			result.ConsensusFindings = append(result.ConsensusFindings, PanelFinding{
				Severity:    SeverityConsensus,
				Role:        firstRole,
				Model:       modelName,
				Category:    "integrity",
				Description: normIssue,
			})
			result.Passed = false
			result.ConsensusScore -= 0.3
		} else {
			result.LoneFindings = append(result.LoneFindings, PanelFinding{
				Severity:    SeverityLoneModel,
				Role:        firstRole,
				Model:       modelName,
				Category:    "advisory",
				Description: normIssue,
			})
			result.ConsensusScore -= 0.05
		}
	}

	if result.ConsensusScore < 0 {
		result.ConsensusScore = 0
	}

	if len(result.ConsensusFindings) > 0 {
		result.Summary = fmt.Sprintf("Advisory jury flagged %d consensus defects and %d advisory notes.", len(result.ConsensusFindings), len(result.LoneFindings))
	} else if len(result.LoneFindings) > 0 {
		result.Summary = fmt.Sprintf("Advisory jury passed with %d lone-model advisory notes.", len(result.LoneFindings))
	} else {
		result.Summary = "Advisory jury reached unanimous clean consensus."
	}

	return result
}
