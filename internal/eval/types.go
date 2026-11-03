package eval

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Case represents a single evaluation scenario.
type Case struct {
	ID         string     `json:"id"`
	Path       string     `json:"path"`
	Prompt     PromptSpec `json:"prompt"`
	CaseConfig CaseConfig `json:"case_config"`
	Graders    []Grader   `json:"-"`
}

// PromptSpec holds the parsed prompt.md frontmatter and markdown prompt body.
type PromptSpec struct {
	Name           string   `json:"name,omitempty"`
	Body           string   `json:"body"`
	MaxTurns       int      `json:"max_turns"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Model          string   `json:"model,omitempty"`
	Harnesses      []string `json:"harnesses,omitempty"`
	Rules          []string `json:"rules,omitempty"`
	GoalsContract  string   `json:"goals_contract,omitempty"`
	ScaffoldScript string   `json:"scaffold_script,omitempty"`
}

// CaseConfig holds optional execution environment configuration from case.yaml.
type CaseConfig struct {
	ScaffoldScript string   `json:"scaffold_script,omitempty"`
	HistoryFile    string   `json:"history_file,omitempty"`
	AddDirs        []string `json:"add_dirs,omitempty"`
	Rules          []string `json:"rules,omitempty"`
	GoalsContract  string   `json:"goals_contract,omitempty"`
	Surface        string   `json:"surface,omitempty"`
}

// ResolvedRule represents a fully loaded and hashed rule file injected into an evaluation arm.
type ResolvedRule struct {
	Path     string `json:"path"`     // Absolute canonical path
	RelPath  string `json:"rel_path"` // Relative path as declared in case or resolved from root
	Content  string `json:"content"`  // Exact byte content
	Hash     string `json:"hash"`     // SHA-256 hex digest of Content
	Bytes    int    `json:"bytes"`    // Byte count
	IsTarget bool   `json:"is_target"`
}

// RunRequest encapsulates an invocation of a harness on a case.
type RunRequest struct {
	Case          *Case             `json:"case"`
	Harness       string            `json:"harness"`
	WorkDir       string            `json:"work_dir"`
	Arm           string            `json:"arm"` // "with", "without", "single"
	Target        string            `json:"target,omitempty"`
	OmitTarget    bool              `json:"omit_target,omitempty"`
	ResolvedRules []ResolvedRule    `json:"resolved_rules,omitempty"`
	ExtraEnv      map[string]string `json:"extra_env,omitempty"`
	MaxTurns      int               `json:"max_turns,omitempty"`
	Model         string            `json:"model,omitempty"`
	AllowedTools  []string          `json:"allowed_tools,omitempty"`
}

// ToolCallRecord captures an individual tool call during execution.
type ToolCallRecord struct {
	Tool      string `json:"tool"`
	Input     string `json:"input"`
	Output    string `json:"output,omitempty"`
	Turn      int    `json:"turn"`
	Timestamp string `json:"timestamp,omitempty"`
}

// TraceStep records a conversational turn or tool interaction in the run.
type TraceStep struct {
	Turn      int              `json:"turn"`
	Role      string           `json:"role"` // "user", "assistant", "tool"
	Content   string           `json:"content"`
	ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"`
}

// TokenUsage captures token consumption metrics for cost and ablation ROI.
type TokenUsage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	EstimatedCost    float64 `json:"estimated_cost"` // in USD
	Measured         bool    `json:"measured,omitempty"`
}

// RunResult contains the raw execution output from the harness.
type RunResult struct {
	CaseID        string           `json:"case_id"`
	Harness       string           `json:"harness"`
	Arm           string           `json:"arm"`
	Synthetic     bool             `json:"synthetic,omitempty"`
	Output        string           `json:"output"`
	ExitCode      int              `json:"exit_code"`
	Duration      time.Duration    `json:"duration_ms"`
	ToolCalls     []ToolCallRecord `json:"tool_calls"`
	Trace         []TraceStep      `json:"trace"`
	Tokens        TokenUsage       `json:"tokens"`
	ResolvedRules []ResolvedRule   `json:"resolved_rules,omitempty"`
	Error         string           `json:"error,omitempty"`
}

// Grader evaluates a RunResult against specific quality or behavioral criteria.
type Grader interface {
	Name() string
	Type() string
	Weight() float64
	Arm() string // "with", "without", "both"
	Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error)
}

// GradeResult holds the outcome of a single grader evaluation.
type GradeResult struct {
	GraderName    string  `json:"grader_name"`
	GraderType    string  `json:"grader_type"`
	Passed        bool    `json:"passed"`
	Score         float64 `json:"score"`
	MaxScore      float64 `json:"max_score"`
	Detail        string  `json:"detail,omitempty"`
	IndicatorOnly bool    `json:"indicator_only,omitempty"`
	JudgeModel    string  `json:"judge_model,omitempty"`
	JudgeProvider string  `json:"judge_provider,omitempty"`
	Unavailable   bool    `json:"unavailable,omitempty"`
	Evidence      string  `json:"evidence,omitempty"`
}

// CaseResult aggregates the run and grader evaluations for a single test case.
//
// TotalScore represents verified quality credit earned by the case.
// If the execution failed (e.g. nonzero harness exit code, timeout, harness error,
// failed scaffold, zero applicable criteria, or invalid/unavailable grader),
// TotalScore is strictly 0.0 — failed executions receive zero quality credit.
//
// MaxScore represents the sum of applicable grader weights for this case.
// Failed cases retain their MaxScore so they remain in suite denominators.
//
// PassRate is the ratio of TotalScore to MaxScore (TotalScore / MaxScore).
// For failed executions, PassRate is 0.0.
//
// Passed is true only when execution succeeded without error AND all applicable
// non-indicator criteria passed.
type CaseResult struct {
	CaseID        string         `json:"case_id"`
	Harness       string         `json:"harness"`
	Arm           string         `json:"arm"`
	Synthetic     bool           `json:"synthetic,omitempty"`
	Passed        bool           `json:"passed"`
	TotalScore    float64        `json:"total_score"`
	MaxScore      float64        `json:"max_score"`
	PassRate      float64        `json:"pass_rate"`
	Duration      time.Duration  `json:"duration_ms"`
	Grades        []GradeResult  `json:"grades"`
	RunResult     *RunResult     `json:"run_result,omitempty"`
	ResolvedRules []ResolvedRule `json:"resolved_rules,omitempty"`
	Error         string         `json:"error,omitempty"`
}

// AblationResult compares performance between With-Target and Without-Target arms.
//
// Delta is ScoreWith/PassRate difference between arms. If an arm failed execution,
// it earns 0 quality credit and Recommendation is set to "insufficient_evidence",
// preventing failed runs from producing "keep" recommendations.
type AblationResult struct {
	CaseID          string            `json:"case_id"`
	Target          string            `json:"target"`
	TargetHash      string            `json:"target_hash,omitempty"`
	ControlHashes   map[string]string `json:"control_hashes,omitempty"`
	InjectedWith    []ResolvedRule    `json:"injected_with,omitempty"`
	InjectedWithout []ResolvedRule    `json:"injected_without,omitempty"`
	ScoreWith       float64           `json:"score_with"`
	ScoreWithout    float64           `json:"score_without"`
	Delta           float64           `json:"delta"`
	TokensWith      int               `json:"tokens_with"`
	TokensWithout   int               `json:"tokens_without"`
	TokenDelta      int               `json:"token_delta"`
	TokensMeasured  bool              `json:"tokens_measured,omitempty"`
	EfficacyPer1k   float64           `json:"efficacy_per_1k_tokens"`
	Recommendation  string            `json:"recommendation"` // "keep", "prune", "demote", "insufficient_evidence"
	Reason          string            `json:"reason,omitempty"`
	ResultWith      *CaseResult       `json:"result_with,omitempty"`
	ResultWithout   *CaseResult       `json:"result_without,omitempty"`
}

// SuiteResult aggregates all case runs and ablation studies across a test suite.
//
// TotalScore is the sum of verified quality credit earned across all evaluated cases.
// Cases that failed execution contribute 0.0 to TotalScore.
//
// MaxScore is the sum of MaxScore across all evaluated cases, ensuring that failed
// or timed-out cases remain in the denominator.
//
// Passed is true only if TotalCases > 0 and PassedCases == TotalCases (all cases passed).
type SuiteResult struct {
	Timestamp   time.Time        `json:"timestamp"`
	SuiteDir    string           `json:"suite_dir"`
	Harness     string           `json:"harness"`
	Synthetic   bool             `json:"synthetic,omitempty"`
	Passed      bool             `json:"passed"`
	TotalCases  int              `json:"total_cases"`
	PassedCases int              `json:"passed_cases"`
	TotalScore  float64          `json:"total_score"`
	MaxScore    float64          `json:"max_score"`
	Duration    time.Duration    `json:"duration_ms"`
	Cases       []CaseResult     `json:"cases"`
	Ablations   []AblationResult `json:"ablations,omitempty"`
	Error       string           `json:"error,omitempty"`
}

// ParseFrontmatterDoc splits YAML frontmatter from Markdown body without external dependencies.
func ParseFrontmatterDoc(raw string) (map[string]any, string) {
	meta := make(map[string]any)
	scanner := bufio.NewScanner(strings.NewReader(raw))
	if !scanner.Scan() {
		return meta, raw
	}
	firstLine := strings.TrimSpace(scanner.Text())
	if firstLine != "---" {
		return meta, raw
	}

	var fmLines []string
	var bodyLines []string
	inFm := true

	for scanner.Scan() {
		line := scanner.Text()
		if inFm && strings.TrimSpace(line) == "---" {
			inFm = false
			continue
		}
		if inFm {
			fmLines = append(fmLines, line)
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	if inFm {
		meta["_unclosed_frontmatter"] = true
		return meta, raw // unclosed frontmatter
	}

	// Simple YAML key-value parser supporting strings, ints, floats, booleans, and arrays
	for _, line := range fmLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon == -1 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
		valStr := strings.TrimSpace(trimmed[colon+1:])

		// Array parsing: ["a", "b"] or [a, b]
		if strings.HasPrefix(valStr, "[") && strings.HasSuffix(valStr, "]") {
			inner := strings.Trim(valStr, "[]")
			var items []string
			for _, part := range strings.Split(inner, ",") {
				part = strings.Trim(strings.TrimSpace(part), `"'`)
				if part != "" {
					items = append(items, part)
				}
			}
			meta[key] = items
			continue
		}

		// Clean quotes
		unquoted := strings.Trim(valStr, `"'`)

		// Boolean
		if strings.EqualFold(unquoted, "true") {
			meta[key] = true
			continue
		}
		if strings.EqualFold(unquoted, "false") {
			meta[key] = false
			continue
		}

		// Integer
		if intVal, err := strconv.Atoi(unquoted); err == nil {
			meta[key] = intVal
			continue
		}

		// Float
		if floatVal, err := strconv.ParseFloat(unquoted, 64); err == nil {
			meta[key] = floatVal
			continue
		}

		meta[key] = unquoted
	}

	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	return meta, body
}

// LoadCase loads an evaluation case from its directory.
func LoadCase(caseDir string) (*Case, error) {
	promptFile := filepath.Join(caseDir, "prompt.md")
	content, err := os.ReadFile(promptFile)
	if err != nil {
		return nil, fmt.Errorf("read prompt.md in %s: %w", caseDir, err)
	}

	meta, body := ParseFrontmatterDoc(string(content))
	spec := PromptSpec{
		Body:     body,
		MaxTurns: 5,
	}

	if name, ok := meta["name"].(string); ok && name != "" {
		spec.Name = name
	} else {
		spec.Name = filepath.Base(caseDir)
	}

	if mt, ok := meta["max_turns"].(int); ok && mt > 0 {
		spec.MaxTurns = mt
	}
	if model, ok := meta["model"].(string); ok {
		spec.Model = model
	}
	if gc, ok := meta["goals_contract"].(string); ok {
		spec.GoalsContract = gc
	}
	if sc, ok := meta["scaffold_script"].(string); ok {
		spec.ScaffoldScript = sc
	}

	if tools, ok := meta["allowed_tools"].([]string); ok {
		spec.AllowedTools = tools
	}
	if tags, ok := meta["tags"].([]string); ok {
		spec.Tags = tags
	}
	if harnesses, ok := meta["harnesses"].([]string); ok {
		spec.Harnesses = harnesses
	}
	if rules, ok := meta["rules"].([]string); ok {
		spec.Rules = rules
	}

	c := &Case{
		ID:     filepath.Base(caseDir),
		Path:   caseDir,
		Prompt: spec,
	}

	// Load optional case.yaml
	caseYaml := filepath.Join(caseDir, "case.yaml")
	if data, err := os.ReadFile(caseYaml); err == nil {
		c.CaseConfig = parseCaseYaml(string(data))
	}

	// Load graders
	gradersDir := filepath.Join(caseDir, "graders")
	if entries, err := os.ReadDir(gradersDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			gPath := filepath.Join(gradersDir, e.Name())
			gName := strings.TrimSuffix(e.Name(), ".md")
			gData, err := os.ReadFile(gPath)
			if err != nil {
				c.Graders = append(c.Graders, &InvalidGrader{
					BaseGrader: BaseGrader{GraderName: gName, GraderType: "invalid", GraderWeight: 1.0, GraderArm: "both"},
					Err:        fmt.Errorf("read grader file: %w", err),
				})
				continue
			}
			g, err := ParseGrader(gName, string(gData))
			if err != nil {
				c.Graders = append(c.Graders, &InvalidGrader{
					BaseGrader: BaseGrader{GraderName: gName, GraderType: "invalid", GraderWeight: 1.0, GraderArm: "both"},
					Err:        err,
				})
				continue
			}
			if g != nil {
				c.Graders = append(c.Graders, g)
			}
		}
	}

	return c, nil
}

func parseCaseYaml(raw string) CaseConfig {
	var cfg CaseConfig
	meta, _ := ParseFrontmatterDoc("---\n" + raw + "\n---")
	if sc, ok := meta["scaffold_script"].(string); ok {
		cfg.ScaffoldScript = sc
	}
	if hf, ok := meta["history_file"].(string); ok {
		cfg.HistoryFile = hf
	}
	if gc, ok := meta["goals_contract"].(string); ok {
		cfg.GoalsContract = gc
	}
	if dirs, ok := meta["add_dirs"].([]string); ok {
		cfg.AddDirs = dirs
	}
	if rules, ok := meta["rules"].([]string); ok {
		cfg.Rules = rules
	}
	return cfg
}

// LoadSuite finds all test cases in a directory (or its 'evals' subdirectory).
func LoadSuite(root string) ([]*Case, error) {
	evalDir := root
	if info, err := os.Stat(filepath.Join(root, "evals")); err == nil && info.IsDir() {
		evalDir = filepath.Join(root, "evals")
	}

	entries, err := os.ReadDir(evalDir)
	if err != nil {
		return nil, fmt.Errorf("read eval directory %s: %w", evalDir, err)
	}

	var cases []*Case
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "mocks" {
			continue
		}
		caseDir := filepath.Join(evalDir, e.Name())
		if _, err := os.Stat(filepath.Join(caseDir, "prompt.md")); err == nil {
			c, err := LoadCase(caseDir)
			if err == nil && c != nil {
				cases = append(cases, c)
			}
		}
	}

	return cases, nil
}
