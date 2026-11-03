package adoption

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

const (
	// CaseSchemaVersion is the normative schema identifier for adoption cases.
	CaseSchemaVersion = "sila/adoption-case/v1"
	// CatalogSchemaVersion is the normative schema identifier for adoption catalogs.
	CatalogSchemaVersion = "sila/adoption-catalog/v1"
	// EvalReportSchemaVersion is the normative schema identifier for evaluation reports.
	EvalReportSchemaVersion = "sila/adoption-eval/v1"
	// SurfaceMapSchemaVersion is the schema identifier for surface-map.json.
	SurfaceMapSchemaVersion = "sila/adoption-surface-map/v1"
	// DefaultOfflineStartedAt is the deterministic clock-free timestamp for offline runs.
	DefaultOfflineStartedAt = "2026-10-23T00:00:00Z"
)

// Live threshold constants from R4.2.
const (
	ThresholdTriggerRecallRate    = 0.80
	ThresholdPrimeRate            = 0.80
	ThresholdHandoffRate          = 0.70
	ThresholdWriteComplianceRate  = 0.90
	ThresholdMedianTurnsFirstSila = 1.0
	ThresholdForbiddenToolRate    = 0.10
	ThresholdKnownDataCoverage    = 0.80
)

// CaseSetup defines the deterministic offline fixture setup for a case.
type CaseSetup struct {
	Fixture string  `json:"fixture"`
	Goal    *string `json:"goal"`
}

// CaseAssertion defines one behavioral or contract assertion on tool calls.
type CaseAssertion struct {
	Type     string   `json:"type"`
	Tool     string   `json:"tool,omitempty"`
	First    string   `json:"first,omitempty"`
	Second   string   `json:"second,omitempty"`
	Sequence []string `json:"sequence,omitempty"`
}

// AdoptionCase represents one sila/adoption-case/v1 evaluation case.
type AdoptionCase struct {
	Schema            string          `json:"schema"`
	ID                string          `json:"id"`
	Prompt            string          `json:"prompt"`
	ExpectedFirstTool string          `json:"expected_first_tool"`
	ExpectedSequence  []string        `json:"expected_sequence"`
	AllowedTools      []string        `json:"allowed_tools"`
	ForbiddenTools    []string        `json:"forbidden_tools"`
	MaxTurns          int             `json:"max_turns"`
	Harness           string          `json:"harness"`
	Profile           string          `json:"profile"`
	RequiredSurface   string          `json:"required_surface"`
	TimeoutMS         int             `json:"timeout_ms"`
	Setup             CaseSetup       `json:"setup"`
	Assertions        []CaseAssertion `json:"assertions"`
}

// AdoptionCatalog wraps the versioned list of adoption cases.
type AdoptionCatalog struct {
	SchemaVersion  string         `json:"schema_version"`
	CatalogVersion string         `json:"catalog_version"`
	Cases          []AdoptionCase `json:"cases"`
}

// SurfaceMapping maps a case ID to its required CLI, MCP, skill, and rulebook surfaces.
type SurfaceMapping struct {
	CaseID       string `json:"case_id"`
	Moment       string `json:"moment"`
	CLICommand   string `json:"cli_command"`
	MCPTool      string `json:"mcp_tool"`
	SkillLine    string `json:"skill_line"`
	RulebookLine string `json:"rulebook_line"`
	SalamLine    string `json:"salam_line"`
	MCPTrigger   string `json:"mcp_trigger"`
}

// SurfaceMap is the top-level structure for evals/adoption/surface-map.json.
type SurfaceMap struct {
	SchemaVersion string           `json:"schema_version"`
	MapVersion    string           `json:"map_version"`
	Mappings      []SurfaceMapping `json:"mappings"`
}

// CaseResult holds the per-case outcome in sila/adoption-eval/v1.
// Prompt text and filesystem paths are deliberately omitted for privacy (R3.5, R6.6).
type CaseResult struct {
	ID               string   `json:"id"`
	Verdict          string   `json:"verdict"`
	Expected         string   `json:"expected"`
	Observed         string   `json:"observed"`
	Surface          string   `json:"surface"`
	Diagnostics      []string `json:"diagnostics"`
	ToolCalls        []string `json:"tool_calls,omitempty"`
	TurnsToFirstSila int      `json:"turns_to_first_sila,omitempty"`
	ForbiddenCalled  bool     `json:"forbidden_called,omitempty"`
}

// EvalSummary aggregates pass/fail/unknown counts across cases.
type EvalSummary struct {
	Total   int `json:"total"`
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Unknown int `json:"unknown"`
}

// EvalMetrics holds the R4 metrics and denominators.
type EvalMetrics struct {
	TriggerRecallRate          Rate    `json:"trigger_recall_rate"`
	PrimeRate                  Rate    `json:"prime_rate"`
	HandoffRate                Rate    `json:"handoff_rate"`
	WriteComplianceRate        Rate    `json:"write_compliance_rate"`
	MedianTurnsToFirstSilaCall float64 `json:"median_turns_to_first_sila_call"`
	ForbiddenToolRate          Rate    `json:"forbidden_tool_rate"`
	UnknownRate                Rate    `json:"unknown_rate"`
}

// DryRunCasePlan describes a planned case execution in --dry-run mode.
type DryRunCasePlan struct {
	ID                string   `json:"id"`
	Harness           string   `json:"harness"`
	Profile           string   `json:"profile"`
	ExpectedFirstTool string   `json:"expected_first_tool"`
	AllowedTools      []string `json:"allowed_tools"`
	ForbiddenTools    []string `json:"forbidden_tools"`
	MaxTurns          int      `json:"max_turns"`
	TimeoutMS         int      `json:"timeout_ms"`
}

// EvalReport is the sila/adoption-eval/v1 report payload.
type EvalReport struct {
	Schema         string           `json:"schema"`
	Mode           string           `json:"mode"`
	CatalogVersion string           `json:"catalog_version"`
	Harness        string           `json:"harness,omitempty"`
	DryRun         bool             `json:"dry_run,omitempty"`
	StartedAt      string           `json:"started_at"`
	Cases          []CaseResult     `json:"cases"`
	Summary        EvalSummary      `json:"summary"`
	Metrics        *EvalMetrics     `json:"metrics,omitempty"`
	DryRunPlan     []DryRunCasePlan `json:"dry_run_plan,omitempty"`
	Verdict        string           `json:"verdict"`
}

// LiveTurnEvent records one tool call observed during a live harness turn.
type LiveTurnEvent struct {
	ToolName string `json:"tool_name"`
	Turn     int    `json:"turn"`
}

// LiveSessionTrace summarizes session-level continuity calls for R4 funnel rates.
type LiveSessionTrace struct {
	SessionID     string
	GoalBound     bool
	GroundedWrite bool
	ToolsCalled   []string
}

// LiveHarnessDriver drives one case against a live or fake harness and returns observed tool events.
// Prompts must never be persisted to the result files or ledger by the driver.
type LiveHarnessDriver func(ctx context.Context, c AdoptionCase) ([]LiveTurnEvent, error)

// LiveEvalOptions configures RunLiveEval.
type LiveEvalOptions struct {
	Harness          string
	OutDir           string
	DryRun           bool
	CatalogVersion   string
	Store            *store.Store
	Driver           LiveHarnessDriver
	Sessions         []LiveSessionTrace
	ExplicitSessions bool
	Now              time.Time
}

// OfflineEvalOptions allows tests to override surfaces or active tools deterministically.
type OfflineEvalOptions struct {
	CatalogVersion   string
	RulebookContent  *string
	SkillContent     *string
	SalamContent     *string
	SurfaceMap       *SurfaceMap
	AvailableTools   []mcp.Tool
	ProfileTools     map[string][]mcp.Tool
	RequireSkillFile bool
}

var validAssertionTypes = map[string]bool{
	"tool_called":     true,
	"tool_not_called": true,
	"tool_before":     true,
	"sequence_prefix": true,
}

// LoadAdoptionCatalog loads and validates an adoption catalog from disk with DisallowUnknownFields.
func LoadAdoptionCatalog(path string) (*AdoptionCatalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read adoption cases %s: %w", path, err)
	}
	return ParseAdoptionCatalog(raw)
}

// ParseAdoptionCatalog parses and strictly validates raw catalog JSON bytes.
func ParseAdoptionCatalog(raw []byte) (*AdoptionCatalog, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cat AdoptionCatalog
	if err := dec.Decode(&cat); err != nil {
		return nil, fmt.Errorf("decode adoption catalog: %w", err)
	}
	if cat.SchemaVersion != "" && cat.SchemaVersion != CatalogSchemaVersion {
		return nil, fmt.Errorf("unsupported catalog schema_version %q (want %q)", cat.SchemaVersion, CatalogSchemaVersion)
	}
	if cat.CatalogVersion == "" {
		cat.CatalogVersion = "1.0.0"
	}
	if err := ValidateAdoptionCases(cat.Cases); err != nil {
		return nil, err
	}
	return &cat, nil
}

// LoadAdoptionCases loads and validates adoption cases from the JSON catalog at path.
func LoadAdoptionCases(path string) ([]AdoptionCase, error) {
	cat, err := LoadAdoptionCatalog(path)
	if err != nil {
		return nil, err
	}
	return cat.Cases, nil
}

// ValidateAdoptionCases enforces R1 schema and semantic invariants across a case list.
func ValidateAdoptionCases(cases []AdoptionCase) error {
	if len(cases) == 0 {
		return fmt.Errorf("adoption catalog cases list cannot be empty")
	}
	seenIDs := make(map[string]bool, len(cases))
	for i, c := range cases {
		if c.Schema != CaseSchemaVersion {
			return fmt.Errorf("case[%d] (%s): invalid schema %q, want %q", i, c.ID, c.Schema, CaseSchemaVersion)
		}
		if strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("case[%d]: id cannot be empty", i)
		}
		if seenIDs[c.ID] {
			return fmt.Errorf("duplicate case id %q", c.ID)
		}
		seenIDs[c.ID] = true
		if strings.TrimSpace(c.Prompt) == "" {
			return fmt.Errorf("case %s: prompt cannot be empty", c.ID)
		}
		if strings.TrimSpace(c.ExpectedFirstTool) == "" {
			return fmt.Errorf("case %s: expected_first_tool cannot be empty", c.ID)
		}
		if len(c.ExpectedSequence) == 0 {
			return fmt.Errorf("case %s: expected_sequence cannot be empty", c.ID)
		}
		if len(c.AllowedTools) == 0 {
			return fmt.Errorf("case %s: allowed_tools cannot be empty", c.ID)
		}
		if c.MaxTurns < 1 {
			return fmt.Errorf("case %s: max_turns must be >= 1", c.ID)
		}
		if c.TimeoutMS <= 0 {
			return fmt.Errorf("case %s: timeout_ms must be > 0", c.ID)
		}
		if strings.TrimSpace(c.Setup.Fixture) == "" {
			return fmt.Errorf("case %s: setup.fixture cannot be empty", c.ID)
		}
		if c.Profile != "" && c.Profile != "continuity" && c.Profile != "full" {
			return fmt.Errorf("case %s: invalid profile %q", c.ID, c.Profile)
		}
		if c.RequiredSurface != "" && c.RequiredSurface != "mcp" && c.RequiredSurface != "cli" && c.RequiredSurface != "skill" {
			return fmt.Errorf("case %s: invalid required_surface %q", c.ID, c.RequiredSurface)
		}

		allowedSet := make(map[string]bool, len(c.AllowedTools))
		for _, a := range c.AllowedTools {
			allowedSet[a] = true
		}
		for _, f := range c.ForbiddenTools {
			if allowedSet[f] {
				return fmt.Errorf("case %s: tool %q appears in both allowed_tools and forbidden_tools", c.ID, f)
			}
		}

		if len(c.Assertions) == 0 {
			return fmt.Errorf("case %s: assertions cannot be empty", c.ID)
		}
		for j, a := range c.Assertions {
			if !validAssertionTypes[a.Type] {
				return fmt.Errorf("case %s assertion[%d]: unknown assertion type %q", c.ID, j, a.Type)
			}
			switch a.Type {
			case "tool_called", "tool_not_called":
				if strings.TrimSpace(a.Tool) == "" {
					return fmt.Errorf("case %s assertion[%d] (%s): tool is required", c.ID, j, a.Type)
				}
			case "tool_before":
				if strings.TrimSpace(a.First) == "" || strings.TrimSpace(a.Second) == "" {
					return fmt.Errorf("case %s assertion[%d] (tool_before): first and second are required", c.ID, j)
				}
			case "sequence_prefix":
				if len(a.Sequence) == 0 {
					return fmt.Errorf("case %s assertion[%d] (sequence_prefix): sequence is required", c.ID, j)
				}
			}
		}
	}
	return nil
}

// LoadSurfaceMap loads evals/adoption/surface-map.json from rootDir.
func LoadSurfaceMap(rootDir string) (*SurfaceMap, error) {
	path := filepath.Join(rootDir, "evals", "adoption", "surface-map.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var sm SurfaceMap
	if err := dec.Decode(&sm); err != nil {
		return nil, fmt.Errorf("decode surface-map.json: %w", err)
	}
	return &sm, nil
}

// DefaultSurfaceMap returns the builtin surface mappings for the 12 canonical cases.
func DefaultSurfaceMap() *SurfaceMap {
	return &SurfaceMap{
		SchemaVersion: SurfaceMapSchemaVersion,
		MapVersion:    "1.0.0",
		Mappings: []SurfaceMapping{
			{CaseID: "session-start-prime", Moment: "session start", CLICommand: "sila prime", MCPTool: "prime_context", SkillLine: "prime_context", RulebookLine: "sila prime", SalamLine: "sila prime", MCPTrigger: "Call this ONCE at the start of a session"},
			{CaseID: "recall-before-web-search", Moment: "failure/error", CLICommand: "sila recall", MCPTool: "recall", SkillLine: "sila recall", RulebookLine: "sila recall", SalamLine: "sila recall", MCPTrigger: "Find prior resolutions for a problem before searching the web"},
			{CaseID: "prior-research-omni-search", Moment: "where did we research X?", CLICommand: "sila search --omni", MCPTool: "omni_search", SkillLine: "omni_search", RulebookLine: "omni_search", SalamLine: "omni_search", MCPTrigger: "Call BEFORE running raw grep, find, or git log"},
			{CaseID: "decision-made-save-memo", Moment: "decision made", CLICommand: "sila memo", MCPTool: "save_memo", SkillLine: "save_memo", RulebookLine: "sila memo", SalamLine: "sila memo", MCPTrigger: "Call immediately when an architectural decision"},
			{CaseID: "problem-solved-save-lesson", Moment: "problem solved", CLICommand: "sila lesson", MCPTool: "save_lesson", SkillLine: "save_lesson", RulebookLine: "sila lesson", SalamLine: "sila lesson", MCPTrigger: "Call this the moment a fix is proven"},
			{CaseID: "reusable-pattern-save-case", Moment: "reusable pattern", CLICommand: "sila case", MCPTool: "save_case", SkillLine: "save_case", RulebookLine: "sila case", SalamLine: "sila case", MCPTrigger: "Call when introducing a reusable architectural pattern"},
			{CaseID: "session-end-save-handoff", Moment: "session end", CLICommand: "sila handoff", MCPTool: "save_handoff", SkillLine: "save_handoff", RulebookLine: "sila handoff", SalamLine: "sila handoff", MCPTrigger: "Call before ending a session"},
			{CaseID: "specialist-routing-skill-load", Moment: "specialist routing", CLICommand: "sila skill load", MCPTool: "skill_load", SkillLine: "skill_load", RulebookLine: "skill_load", SalamLine: "skill_load", MCPTrigger: "Retrieve complete instructions, metadata, and optional resources"},
			{CaseID: "symbol-rename-blast-radius", Moment: "rename/delete a symbol", CLICommand: "sila edit-check", MCPTool: "check_symbol_blast_radius", SkillLine: "check_symbol_blast_radius", RulebookLine: "sila edit-check", SalamLine: "sila edit-check", MCPTrigger: "Run an in-memory edit-check or safe-delete contract analysis"},
			{CaseID: "temporal-query-activity-digest", Moment: "temporal query", CLICommand: "sila standup", MCPTool: "get_activity_digest", SkillLine: "get_activity_digest", RulebookLine: "sila standup", SalamLine: "sila standup", MCPTrigger: "Summarize recent activity and time-windowed progress"},
			{CaseID: "pre-commit-gate-pipeline", Moment: "pre-commit", CLICommand: "sila gate", MCPTool: "run_gate_pipeline", SkillLine: "run_gate_pipeline", RulebookLine: "sila gate", SalamLine: "sila gate", MCPTrigger: "Execute the sila quality gate validation"},
			{CaseID: "goal-status-list-goals", Moment: "goal status", CLICommand: "sila goals", MCPTool: "list_goals", SkillLine: "list_goals", RulebookLine: "sila goals", SalamLine: "sila goals", MCPTrigger: "List goal packages in the project's goals/ directory"},
		},
	}
}

// ScriptedAgent is a deterministic, model-free, network-free fixture agent (R2.1, R6.3).
// It inspects the active surface map, compiled rulebook, salam briefing, skill content,
// and exposed profile tools; it emits the expected first tool only when both the trigger
// line and tool exposure are verified.
func ScriptedAgent(c AdoptionCase, mapping SurfaceMapping, rulebook, salamText, skillText string, skillPresent bool, exposedTools map[string]mcp.Tool) (string, []string) {
	var diags []string

	if mapping.CaseID == "" {
		diags = append(diags, fmt.Sprintf("missing surface-map entry for case %q", c.ID))
		return "", diags
	}
	if mapping.RulebookLine == "" || !strings.Contains(rulebook, mapping.RulebookLine) {
		diags = append(diags, fmt.Sprintf("rulebook AGENTS.md missing trigger %q", mapping.RulebookLine))
	}
	if mapping.SalamLine == "" || !strings.Contains(salamText, mapping.SalamLine) {
		diags = append(diags, fmt.Sprintf("salam/prime briefing missing trigger %q", mapping.SalamLine))
	}
	if c.RequiredSurface == "skill" && !skillPresent {
		diags = append(diags, "required surface is skill but SKILL.md is missing")
	}
	if skillPresent && mapping.SkillLine != "" && !strings.Contains(skillText, mapping.SkillLine) {
		diags = append(diags, fmt.Sprintf("SKILL.md missing trigger %q", mapping.SkillLine))
	}
	toolDef, ok := exposedTools[c.ExpectedFirstTool]
	if !ok {
		diags = append(diags, fmt.Sprintf("expected_first_tool %q is not exposed in profile %q", c.ExpectedFirstTool, c.Profile))
	} else if mapping.MCPTrigger != "" && !strings.Contains(toolDef.Description, mapping.MCPTrigger) {
		diags = append(diags, fmt.Sprintf("MCP tool %q description missing trigger %q", c.ExpectedFirstTool, mapping.MCPTrigger))
	}

	if len(diags) > 0 {
		return "", diags
	}
	return c.ExpectedFirstTool, nil
}

// RunOfflineEval executes the deterministic offline trigger-adherence contract evaluation (R2).
func RunOfflineEval(cases []AdoptionCase, rootDir string) (EvalReport, error) {
	return RunOfflineEvalWithOptions(cases, rootDir, OfflineEvalOptions{})
}

// RunOfflineEvalWithOptions executes the offline evaluation with optional test overrides.
func RunOfflineEvalWithOptions(cases []AdoptionCase, rootDir string, opts OfflineEvalOptions) (EvalReport, error) {
	if err := ValidateAdoptionCases(cases); err != nil {
		return EvalReport{}, err
	}
	if rootDir == "" {
		rootDir = "."
	}

	// 1. Load surface map
	sm := opts.SurfaceMap
	if sm == nil {
		if loaded, err := LoadSurfaceMap(rootDir); err == nil {
			sm = loaded
		} else {
			sm = DefaultSurfaceMap()
		}
	}
	mapByCase := make(map[string]SurfaceMapping, len(sm.Mappings))
	for _, m := range sm.Mappings {
		mapByCase[m.CaseID] = m
	}

	// 2. Load compiled rulebook surface
	var rulebook string
	if opts.RulebookContent != nil {
		rulebook = *opts.RulebookContent
	} else {
		var parts []string
		if raw, err := os.ReadFile(filepath.Join(rootDir, "AGENTS.md")); err == nil {
			parts = append(parts, string(raw))
		}
		if raw, err := os.ReadFile(filepath.Join(rootDir, "agents", "AGENTS.latest.md")); err == nil {
			parts = append(parts, string(raw))
		}
		if len(parts) == 0 {
			comp := agents.NewCompiler(rootDir)
			if compiled, err := comp.Compile(agents.CompileOptions{RepoRoot: rootDir, ProjectName: "sila"}); err == nil {
				parts = append(parts, compiled)
			}
		}
		rulebook = strings.Join(parts, "\n")
	}

	// 3. Load SKILL.md surface
	var skillText string
	skillPresent := false
	if opts.SkillContent != nil {
		skillText = *opts.SkillContent
		skillPresent = skillText != ""
	} else {
		skillCandidates := []string{
			filepath.Join(rootDir, ".agents", "skills", "sila", "SKILL.md"),
			filepath.Join(rootDir, "SKILL.md"),
		}
		for _, p := range skillCandidates {
			if raw, err := os.ReadFile(p); err == nil {
				skillText = string(raw)
				skillPresent = true
				break
			}
		}
	}

	// 4. Build salam/prime briefing text
	var salamText string
	if opts.SalamContent != nil {
		salamText = *opts.SalamContent
	} else {
		var sb strings.Builder
		for _, l := range salam.SalamLoop {
			sb.WriteString(l + "\n")
		}
		for _, l := range agents.MemoryProtocolExpanded() {
			sb.WriteString(l + "\n")
		}
		for _, l := range agents.TriggerRoutingExpanded() {
			sb.WriteString(l + "\n")
		}
		salamText = sb.String()
	}

	// 5. All tool definitions
	allTools := opts.AvailableTools
	if len(allTools) == 0 {
		allTools = mcp.ToolDefinitions()
	}
	allToolMap := make(map[string]mcp.Tool, len(allTools))
	for _, t := range allTools {
		allToolMap[t.Name] = t
	}

	catVer := opts.CatalogVersion
	if catVer == "" {
		catVer = "1.0.0"
	}

	results := make([]CaseResult, 0, len(cases))
	summary := EvalSummary{Total: len(cases)}
	var sessionTraces []LiveSessionTrace

	for _, c := range cases {
		profile := c.Profile
		if profile == "" {
			profile = "continuity"
		}
		var profTools []mcp.Tool
		if opts.ProfileTools != nil {
			profTools = opts.ProfileTools[profile]
		} else {
			profTools = mcp.FilterToolsByProfile(allTools, profile)
		}
		exposedMap := make(map[string]mcp.Tool, len(profTools))
		for _, t := range profTools {
			exposedMap[t.Name] = t
		}

		var diags []string
		if _, existsInAll := allToolMap[c.ExpectedFirstTool]; !existsInAll {
			diags = append(diags, fmt.Sprintf("expected_first_tool %q does not exist in ToolDefinitions()", c.ExpectedFirstTool))
		}

		mapping := mapByCase[c.ID]
		observed, agentDiags := ScriptedAgent(c, mapping, rulebook, salamText, skillText, skillPresent, exposedMap)
		diags = append(diags, agentDiags...)

		verdict := "pass"
		turns := 1
		var called []string
		if observed == "" || observed != c.ExpectedFirstTool || len(diags) > 0 {
			verdict = "fail"
			turns = 0
		} else {
			called = []string{observed}
			if evalDiags := EvaluateAssertions(c, called); len(evalDiags) > 0 {
				verdict = "fail"
				diags = append(diags, evalDiags...)
			}
		}

		if diags == nil {
			diags = []string{}
		}
		switch verdict {
		case "pass":
			summary.Pass++
		case "fail":
			summary.Fail++
		default:
			summary.Unknown++
		}

		surface := c.RequiredSurface
		if surface == "" {
			surface = "mcp"
		}
		results = append(results, CaseResult{
			ID:               c.ID,
			Verdict:          verdict,
			Expected:         c.ExpectedFirstTool,
			Observed:         observed,
			Surface:          surface,
			Diagnostics:      diags,
			ToolCalls:        called,
			TurnsToFirstSila: turns,
		})

		if len(called) > 0 {
			sessionTraces = append(sessionTraces, LiveSessionTrace{
				SessionID:     c.ID,
				GoalBound:     c.Setup.Goal != nil && *c.Setup.Goal != "",
				GroundedWrite: isWriteTool(observed),
				ToolsCalled:   called,
			})
		}
	}

	metrics := ComputeEvalMetrics(cases, results, coalesceSessionsForOffline(sessionTraces))
	overall := "pass"
	if summary.Fail > 0 {
		overall = "fail"
	} else if summary.Unknown > 0 || summary.Total == 0 {
		overall = "unknown"
	}

	return EvalReport{
		Schema:         EvalReportSchemaVersion,
		Mode:           "offline",
		CatalogVersion: catVer,
		StartedAt:      DefaultOfflineStartedAt,
		Cases:          results,
		Summary:        summary,
		Metrics:        &metrics,
		Verdict:        overall,
	}, nil
}

// EvaluateAssertions checks a case's assertions against an observed tool sequence.
func EvaluateAssertions(c AdoptionCase, toolCalls []string) []string {
	var diags []string
	posMap := make(map[string]int, len(toolCalls))
	for i, name := range toolCalls {
		if _, seen := posMap[name]; !seen {
			posMap[name] = i
		}
	}
	for _, a := range c.Assertions {
		switch a.Type {
		case "tool_called":
			if _, ok := posMap[a.Tool]; !ok {
				diags = append(diags, fmt.Sprintf("assertion tool_called(%s) failed: observed %v", a.Tool, toolCalls))
			}
		case "tool_not_called":
			if _, ok := posMap[a.Tool]; ok {
				diags = append(diags, fmt.Sprintf("assertion tool_not_called(%s) failed: tool was called", a.Tool))
			}
		case "tool_before":
			firstIdx, okFirst := posMap[a.First]
			secondIdx, okSecond := posMap[a.Second]
			if !okFirst {
				diags = append(diags, fmt.Sprintf("assertion tool_before(%s, %s) failed: %s was never called", a.First, a.Second, a.First))
			} else if okSecond && firstIdx > secondIdx {
				diags = append(diags, fmt.Sprintf("assertion tool_before(%s, %s) failed: %s called at %d after %s at %d", a.First, a.Second, a.First, firstIdx, a.Second, secondIdx))
			}
		case "sequence_prefix":
			if len(toolCalls) < len(a.Sequence) {
				diags = append(diags, fmt.Sprintf("assertion sequence_prefix(%v) failed: observed %v", a.Sequence, toolCalls))
				continue
			}
			for i := range a.Sequence {
				if toolCalls[i] != a.Sequence[i] {
					diags = append(diags, fmt.Sprintf("assertion sequence_prefix(%v) failed at index %d: got %s", a.Sequence, i, toolCalls[i]))
					break
				}
			}
		}
	}
	return diags
}

// RunLiveEval executes the opt-in live trigger-adherence evaluation (R3).
// Without a driver or ledger rows, cases resolve to "unknown", never a false "pass".
func RunLiveEval(ctx context.Context, cases []AdoptionCase, opts LiveEvalOptions) (EvalReport, error) {
	if err := ValidateAdoptionCases(cases); err != nil {
		return EvalReport{}, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	catVer := opts.CatalogVersion
	if catVer == "" {
		catVer = "1.0.0"
	}
	harness := opts.Harness
	if harness == "" && len(cases) > 0 {
		harness = cases[0].Harness
	}

	if opts.DryRun {
		plan := make([]DryRunCasePlan, 0, len(cases))
		results := make([]CaseResult, 0, len(cases))
		for _, c := range cases {
			h := harness
			if h == "" {
				h = c.Harness
			}
			plan = append(plan, DryRunCasePlan{
				ID:                c.ID,
				Harness:           h,
				Profile:           c.Profile,
				ExpectedFirstTool: c.ExpectedFirstTool,
				AllowedTools:      c.AllowedTools,
				ForbiddenTools:    c.ForbiddenTools,
				MaxTurns:          c.MaxTurns,
				TimeoutMS:         c.TimeoutMS,
			})
			results = append(results, CaseResult{
				ID:          c.ID,
				Verdict:     "unknown",
				Expected:    c.ExpectedFirstTool,
				Observed:    "",
				Surface:     c.RequiredSurface,
				Diagnostics: []string{"dry-run: session not started"},
			})
		}
		metrics := ComputeEvalMetrics(cases, results, nil)
		return EvalReport{
			Schema:         EvalReportSchemaVersion,
			Mode:           "live",
			CatalogVersion: catVer,
			Harness:        harness,
			DryRun:         true,
			StartedAt:      now.Format(time.RFC3339),
			Cases:          results,
			Summary:        EvalSummary{Total: len(cases), Unknown: len(cases)},
			Metrics:        &metrics,
			DryRunPlan:     plan,
			Verdict:        "unknown",
		}, nil
	}

	results := make([]CaseResult, 0, len(cases))
	summary := EvalSummary{Total: len(cases)}
	var derivedCalls []string
	var goalTraces []LiveSessionTrace

	for _, c := range cases {
		timeout := time.Duration(c.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		caseCtx, cancel := context.WithTimeout(ctx, timeout)
		events, err := executeLiveCase(caseCtx, c, harness, opts)
		cancel()

		res := evaluateLiveCaseEvents(c, events, err)
		switch res.Verdict {
		case "pass":
			summary.Pass++
		case "fail":
			summary.Fail++
		default:
			summary.Unknown++
		}
		results = append(results, res)

		if res.Verdict != "unknown" {
			derivedCalls = append(derivedCalls, res.ToolCalls...)
			if c.Setup.Goal != nil && *c.Setup.Goal != "" {
				wrote := false
				for _, tc := range res.ToolCalls {
					if isWriteTool(tc) {
						wrote = true
						break
					}
				}
				goalTraces = append(goalTraces, LiveSessionTrace{
					SessionID:     c.ID,
					GoalBound:     true,
					GroundedWrite: wrote,
					ToolsCalled:   res.ToolCalls,
				})
			}
		}

		// Record redacted tool events in store if provided (only case ID and tool names, never prompts or paths)
		if opts.Store != nil && len(res.ToolCalls) > 0 {
			for _, toolName := range res.ToolCalls {
				_ = opts.Store.RecordToolEvent(store.ToolEvent{
					Source:    c.RequiredSurface,
					Category:  c.RequiredSurface,
					ToolName:  toolName,
					ProjectID: "adoption-eval",
					SessionID: "case:" + c.ID,
					Agent:     harness,
					Outcome:   res.Verdict,
					CreatedAt: now,
				})
			}
		}
	}

	var sessions []LiveSessionTrace
	if opts.ExplicitSessions || opts.Sessions != nil {
		sessions = opts.Sessions
	} else if len(derivedCalls) > 0 {
		sessions = coalesceLiveSessions(derivedCalls, goalTraces)
	}

	metrics := ComputeEvalMetrics(cases, results, sessions)
	overall := ComputeLiveVerdict(summary, metrics)

	report := EvalReport{
		Schema:         EvalReportSchemaVersion,
		Mode:           "live",
		CatalogVersion: catVer,
		Harness:        harness,
		StartedAt:      now.Format(time.RFC3339),
		Cases:          results,
		Summary:        summary,
		Metrics:        &metrics,
		Verdict:        overall,
	}

	if opts.OutDir != "" {
		if err := writeLiveReportFiles(opts.OutDir, report); err != nil {
			return report, err
		}
	}

	return report, nil
}

func executeLiveCase(ctx context.Context, c AdoptionCase, harness string, opts LiveEvalOptions) ([]LiveTurnEvent, error) {
	if opts.Driver != nil {
		return opts.Driver(ctx, c)
	}
	if opts.Store != nil {
		evs, err := opts.Store.ListToolEvents(store.ToolEventFilter{
			SessionID: "case:" + c.ID,
		})
		if err != nil {
			return nil, err
		}
		var out []LiveTurnEvent
		for i, ev := range evs {
			if harness != "" && ev.Agent != "" && ev.Agent != harness {
				continue
			}
			out = append(out, LiveTurnEvent{ToolName: ev.ToolName, Turn: i + 1})
		}
		return out, nil
	}
	return nil, nil
}

func evaluateLiveCaseEvents(c AdoptionCase, events []LiveTurnEvent, execErr error) CaseResult {
	surface := c.RequiredSurface
	if surface == "" {
		surface = "mcp"
	}
	res := CaseResult{
		ID:          c.ID,
		Expected:    c.ExpectedFirstTool,
		Surface:     surface,
		Diagnostics: []string{},
	}

	if execErr != nil {
		if execErr == context.DeadlineExceeded || strings.Contains(execErr.Error(), "deadline exceeded") {
			res.Verdict = "fail"
			res.Diagnostics = append(res.Diagnostics, "case timed out before completing tool sequence")
			return res
		}
		res.Verdict = "unknown"
		res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("harness unavailable: %v", sanitizeDiagnostic(execErr.Error(), c.Prompt)))
		return res
	}

	if len(events) == 0 {
		res.Verdict = "unknown"
		res.Diagnostics = append(res.Diagnostics, "no tool_events recorded for case")
		return res
	}

	forbiddenSet := make(map[string]bool, len(c.ForbiddenTools))
	for _, f := range c.ForbiddenTools {
		forbiddenSet[f] = true
	}

	var calls []string
	firstSilaTurn := 0
	for i, ev := range events {
		turn := ev.Turn
		if turn <= 0 {
			turn = i + 1
		}
		if c.MaxTurns > 0 && turn > c.MaxTurns {
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("stopped after max_turns=%d", c.MaxTurns))
			break
		}
		calls = append(calls, ev.ToolName)
		if firstSilaTurn == 0 && mcp.IsCoreTool(ev.ToolName) {
			firstSilaTurn = turn
		}
		// Short-circuit on first forbidden tool (R3.1)
		if forbiddenSet[ev.ToolName] {
			res.ForbiddenCalled = true
			res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("forbidden tool %q called at turn %d", ev.ToolName, turn))
			break
		}
	}

	res.ToolCalls = calls
	if len(calls) > 0 {
		res.Observed = calls[0]
	}
	res.TurnsToFirstSila = firstSilaTurn

	if len(calls) == 0 {
		res.Verdict = "unknown"
		res.Diagnostics = append(res.Diagnostics, "no tool calls within max_turns")
		return res
	}

	if res.ForbiddenCalled {
		res.Verdict = "fail"
		return res
	}

	if res.Observed != c.ExpectedFirstTool {
		res.Verdict = "fail"
		res.Diagnostics = append(res.Diagnostics, fmt.Sprintf("expected first tool %q, observed %q", c.ExpectedFirstTool, res.Observed))
		return res
	}

	if assertDiags := EvaluateAssertions(c, calls); len(assertDiags) > 0 {
		res.Verdict = "fail"
		res.Diagnostics = append(res.Diagnostics, assertDiags...)
		return res
	}

	res.Verdict = "pass"
	return res
}

func sanitizeDiagnostic(diag, prompt string) string {
	if prompt != "" {
		diag = strings.ReplaceAll(diag, prompt, "[REDACTED_PROMPT]")
	}
	return diag
}
