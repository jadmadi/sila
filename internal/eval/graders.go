package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/assist"
)

// BaseGrader provides shared fields for all grader implementations.
type BaseGrader struct {
	GraderName   string
	GraderType   string
	GraderWeight float64
	GraderArm    string // "with", "without", "both"
}

func (b *BaseGrader) Name() string    { return b.GraderName }
func (b *BaseGrader) Type() string    { return b.GraderType }
func (b *BaseGrader) Weight() float64 { return b.GraderWeight }
func (b *BaseGrader) Arm() string     { return b.GraderArm }

// InvalidGrader represents a grader configuration that failed to parse or load.
type InvalidGrader struct {
	BaseGrader
	Err error
}

func (g *InvalidGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	return &GradeResult{
		GraderName: g.GraderName,
		GraderType: "invalid",
		Passed:     false,
		Score:      0,
		MaxScore:   g.GraderWeight,
		Detail:     fmt.Sprintf("Invalid grader: %v", g.Err),
	}, nil
}

// ParseGrader parses a grader from a Markdown file with YAML frontmatter.
func ParseGrader(name, content string) (Grader, error) {
	meta, body := ParseFrontmatterDoc(content)
	if unclosed, ok := meta["_unclosed_frontmatter"].(bool); ok && unclosed {
		return nil, fmt.Errorf("unclosed frontmatter in grader %q", name)
	}
	gType, _ := meta["type"].(string)
	if gType == "" {
		gType = "regex"
	}

	weight := 1.0
	if w, ok := meta["weight"].(float64); ok && w > 0 {
		weight = w
	} else if wInt, ok := meta["weight"].(int); ok && wInt > 0 {
		weight = float64(wInt)
	}

	arm := "both"
	if a, ok := meta["arm"].(string); ok && a != "" {
		arm = strings.ToLower(a)
	}

	base := BaseGrader{
		GraderName:   name,
		GraderType:   gType,
		GraderWeight: weight,
		GraderArm:    arm,
	}

	switch gType {
	case "tool_used":
		g := &ToolUsedGrader{BaseGrader: base, Min: 1, Max: -1}
		if t, ok := meta["tool"].(string); ok {
			g.Tool = t
		}
		if im, ok := meta["input_match"].(string); ok {
			if _, err := regexp.Compile(im); err != nil {
				return nil, fmt.Errorf("invalid regex for input_match in grader %q: %w", name, err)
			}
			g.InputMatch = im
		}
		if minVal, ok := meta["min"].(int); ok {
			g.Min = minVal
		}
		if maxVal, ok := meta["max"].(int); ok {
			g.Max = maxVal
		}
		return g, nil

	case "tool_order":
		g := &ToolOrderGrader{BaseGrader: base}
		if b, ok := meta["before"].(string); ok {
			g.Before = b
		}
		if a, ok := meta["after"].(string); ok {
			g.After = a
		}
		return g, nil

	case "regex":
		g := &RegexGrader{
			BaseGrader: base,
			MatchType:  "contains",
			Target:     "message",
		}
		if p, ok := meta["pattern"].(string); ok {
			g.Pattern = p
		} else if body != "" {
			g.Pattern = body
		}
		if _, err := regexp.Compile(g.Pattern); err != nil {
			return nil, fmt.Errorf("invalid regex pattern %q in grader %q: %w", g.Pattern, name, err)
		}
		if mt, ok := meta["match_type"].(string); ok {
			g.MatchType = strings.ToLower(mt)
		}
		if tgt, ok := meta["target"].(string); ok {
			g.Target = strings.ToLower(tgt)
		}
		if path, ok := meta["path"].(string); ok {
			g.Path = path
		}
		if cnt, ok := meta["count"].(int); ok {
			g.Count = cnt
		}
		return g, nil

	case "file_exists":
		g := &FileExistsGrader{BaseGrader: base}
		if p, ok := meta["path"].(string); ok {
			g.Path = p
		}
		if g.Path == "" {
			return nil, fmt.Errorf("file_exists grader %q missing required 'path'", name)
		}
		if mb, ok := meta["min_bytes"].(int); ok {
			g.MinBytes = int64(mb)
		}
		if pat, ok := meta["pattern"].(string); ok {
			if _, err := regexp.Compile(pat); err != nil {
				return nil, fmt.Errorf("invalid regex pattern %q in grader %q: %w", pat, name, err)
			}
			g.Pattern = pat
		}
		return g, nil

	case "sila_contract":
		g := &SilaContractGrader{
			BaseGrader: base,
			Action:     "test_runner",
		}
		if act, ok := meta["action"].(string); ok {
			g.Action = act
		}
		if cmd, ok := meta["command"].(string); ok {
			g.Command = cmd
		}
		if gate, ok := meta["gate"].(string); ok {
			g.Gate = gate
		}
		return g, nil

	case "llm", "llm_judge":
		g := &LLMJudgeGrader{
			BaseGrader:    base,
			Model:         "claude-3-5-haiku",
			Samples:       1,
			PassThreshold: 1,
			Rubric:        body,
		}
		g.BaseGrader.GraderType = "llm_judge"
		if m, ok := meta["model"].(string); ok && m != "" {
			g.Model = m
		}
		if p, ok := meta["provider"].(string); ok && p != "" {
			g.Provider = p
		}
		if s, ok := meta["samples"].(int); ok {
			g.Samples = s
		}
		if pt, ok := meta["pass_threshold"].(int); ok {
			g.PassThreshold = pt
		}
		return g, nil

	case "keyword_clause", "rubric_clause":
		g := &KeywordClauseGrader{
			BaseGrader: base,
			Rubric:     body,
		}
		g.BaseGrader.GraderType = "keyword_clause"
		return g, nil

	default:
		return nil, fmt.Errorf("unknown grader type %q in grader %q", gType, name)
	}
}

// ToolUsedGrader verifies that specific tools were (or were not) called.
type ToolUsedGrader struct {
	BaseGrader
	Tool       string
	InputMatch string
	Min        int
	Max        int
}

func (g *ToolUsedGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	matches := 0
	var inputRE *regexp.Regexp
	if g.InputMatch != "" {
		var err error
		inputRE, err = regexp.Compile(g.InputMatch)
		if err != nil {
			// Fallback to substring matching
			inputRE = nil
		}
	}

	for _, tc := range res.ToolCalls {
		if g.Tool != "" && !strings.EqualFold(tc.Tool, g.Tool) {
			continue
		}
		if g.InputMatch != "" {
			if inputRE != nil {
				if !inputRE.MatchString(tc.Input) {
					continue
				}
			} else {
				if !strings.Contains(tc.Input, g.InputMatch) {
					continue
				}
			}
		}
		matches++
	}

	passed := true
	if matches < g.Min {
		passed = false
	}
	if g.Max >= 0 && matches > g.Max {
		passed = false
	}

	score := 0.0
	if passed {
		score = g.GraderWeight
	}

	detail := fmt.Sprintf("Tool %q matched %d times (min: %d, max: %d)", g.Tool, matches, g.Min, g.Max)
	return &GradeResult{
		GraderName: g.GraderName,
		GraderType: g.GraderType,
		Passed:     passed,
		Score:      score,
		MaxScore:   g.GraderWeight,
		Detail:     detail,
	}, nil
}

// ToolOrderGrader verifies sequential tool call execution order.
type ToolOrderGrader struct {
	BaseGrader
	Before string
	After  string
}

func (g *ToolOrderGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	beforeIdx := -1
	afterIdx := -1

	for i, tc := range res.ToolCalls {
		if strings.EqualFold(tc.Tool, g.Before) && beforeIdx == -1 {
			beforeIdx = i
		}
		if strings.EqualFold(tc.Tool, g.After) && afterIdx == -1 {
			afterIdx = i
		}
	}

	passed := beforeIdx != -1 && afterIdx != -1 && beforeIdx < afterIdx
	score := 0.0
	if passed {
		score = g.GraderWeight
	}

	detail := fmt.Sprintf("Tool %q (call #%d) before %q (call #%d)", g.Before, beforeIdx, g.After, afterIdx)
	return &GradeResult{
		GraderName: g.GraderName,
		GraderType: g.GraderType,
		Passed:     passed,
		Score:      score,
		MaxScore:   g.GraderWeight,
		Detail:     detail,
	}, nil
}

// RegexGrader performs deterministic regex or pattern matching against text or files.
type RegexGrader struct {
	BaseGrader
	Pattern   string
	MatchType string // "contains", "not_contains", "count"
	Target    string // "message", "trace", "file"
	Path      string
	Count     int
}

func (g *RegexGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	var targetText string

	switch g.Target {
	case "file":
		if g.Path == "" {
			return nil, fmt.Errorf("file target requires 'path'")
		}
		fullPath := filepath.Join(workDir, g.Path)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return &GradeResult{
				GraderName: g.GraderName,
				GraderType: g.GraderType,
				Passed:     false,
				Score:      0,
				MaxScore:   g.GraderWeight,
				Detail:     fmt.Sprintf("Failed to read file %s: %v", g.Path, err),
			}, nil
		}
		targetText = string(data)

	case "trace":
		var sb strings.Builder
		for _, step := range res.Trace {
			sb.WriteString(step.Content)
			sb.WriteString("\n")
			for _, tc := range step.ToolCalls {
				sb.WriteString(tc.Input)
				sb.WriteString("\n")
				sb.WriteString(tc.Output)
				sb.WriteString("\n")
			}
		}
		targetText = sb.String()

	default: // "message"
		targetText = res.Output
	}

	re, err := regexp.Compile(g.Pattern)
	var matches int
	if err == nil {
		matches = len(re.FindAllString(targetText, -1))
	} else {
		// Fallback literal substring search
		matches = strings.Count(targetText, g.Pattern)
	}

	passed := false
	switch g.MatchType {
	case "not_contains":
		passed = matches == 0
	case "count":
		passed = matches == g.Count
	default: // "contains"
		passed = matches > 0
	}

	score := 0.0
	if passed {
		score = g.GraderWeight
	}

	detail := fmt.Sprintf("Pattern %q matched %d times in %s (match_type: %s)", g.Pattern, matches, g.Target, g.MatchType)
	return &GradeResult{
		GraderName: g.GraderName,
		GraderType: g.GraderType,
		Passed:     passed,
		Score:      score,
		MaxScore:   g.GraderWeight,
		Detail:     detail,
	}, nil
}

// FileExistsGrader asserts files were created or modified on disk.
type FileExistsGrader struct {
	BaseGrader
	Path     string
	MinBytes int64
	Pattern  string
}

func (g *FileExistsGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	target := filepath.Join(workDir, g.Path)
	info, err := os.Stat(target)
	if err != nil {
		return &GradeResult{
			GraderName: g.GraderName,
			GraderType: g.GraderType,
			Passed:     false,
			Score:      0,
			MaxScore:   g.GraderWeight,
			Detail:     fmt.Sprintf("File %s does not exist", g.Path),
		}, nil
	}

	if g.MinBytes > 0 && info.Size() < g.MinBytes {
		return &GradeResult{
			GraderName: g.GraderName,
			GraderType: g.GraderType,
			Passed:     false,
			Score:      0,
			MaxScore:   g.GraderWeight,
			Detail:     fmt.Sprintf("File %s size %d < min_bytes %d", g.Path, info.Size(), g.MinBytes),
		}, nil
	}

	if g.Pattern != "" {
		data, err := os.ReadFile(target)
		if err != nil {
			return &GradeResult{
				GraderName: g.GraderName,
				GraderType: g.GraderType,
				Passed:     false,
				Score:      0,
				MaxScore:   g.GraderWeight,
				Detail:     fmt.Sprintf("Failed to read file %s: %v", g.Path, err),
			}, nil
		}
		matched, _ := regexp.Match(g.Pattern, data)
		if !matched {
			return &GradeResult{
				GraderName: g.GraderName,
				GraderType: g.GraderType,
				Passed:     false,
				Score:      0,
				MaxScore:   g.GraderWeight,
				Detail:     fmt.Sprintf("File %s does not match pattern %q", g.Path, g.Pattern),
			}, nil
		}
	}

	return &GradeResult{
		GraderName: g.GraderName,
		GraderType: g.GraderType,
		Passed:     true,
		Score:      g.GraderWeight,
		MaxScore:   g.GraderWeight,
		Detail:     fmt.Sprintf("File %s verified (%d bytes)", g.Path, info.Size()),
	}, nil
}

// SilaContractGrader executes native workspace commands or gate checks in workDir.
type SilaContractGrader struct {
	BaseGrader
	Action  string // "test_runner", "verify_gate", "command"
	Command string
	Gate    string
}

func (g *SilaContractGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	cmdStr := g.Command
	if cmdStr == "" {
		switch g.Action {
		case "verify_gate":
			cmdStr = "sila gate pre-commit"
			if g.Gate != "" {
				cmdStr = "sila gate " + g.Gate
			}
		default: // "test_runner"
			cmdStr = "go test ./..."
		}
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = workDir
	if res != nil && res.Arm != "" {
		cmd.Env = append(os.Environ(), "SILA_EVAL_ARM="+res.Arm)
	}
	configureProcessGroup(cmd)
	out, err := cmd.CombinedOutput()
	passed := err == nil

	score := 0.0
	if passed {
		score = g.GraderWeight
	}

	detail := fmt.Sprintf("Contract execution %q: exit %v (output: %s)", cmdStr, err == nil, strings.TrimSpace(string(out)))
	if len(detail) > 300 {
		detail = detail[:297] + "..."
	}

	return &GradeResult{
		GraderName: g.GraderName,
		GraderType: g.GraderType,
		Passed:     passed,
		Score:      score,
		MaxScore:   g.GraderWeight,
		Detail:     detail,
	}, nil
}

// KeywordClauseGrader evaluates agent output against deterministic keyword clauses extracted from a rubric.
type KeywordClauseGrader struct {
	BaseGrader
	Rubric string
}

func (g *KeywordClauseGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	lines := strings.Split(g.Rubric, "\n")
	var clauses []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") ||
			strings.HasPrefix(l, "1.") || strings.HasPrefix(l, "2.") || strings.HasPrefix(l, "3.") ||
			strings.HasPrefix(l, "4.") || strings.HasPrefix(l, "5.") || strings.HasPrefix(l, "6.") ||
			strings.HasPrefix(l, "7.") || strings.HasPrefix(l, "8.") || strings.HasPrefix(l, "9.") {
			clause := strings.TrimLeft(l, "0123456789.-* ")
			if clause != "" {
				clauses = append(clauses, clause)
			}
		}
	}

	if len(clauses) == 0 {
		return &GradeResult{
			GraderName: g.GraderName,
			GraderType: "keyword_clause",
			Passed:     false,
			Score:      0,
			MaxScore:   g.GraderWeight,
			Detail:     "unparseable rubric: zero deterministic clauses found (requires bulleted or numbered criteria)",
		}, nil
	}

	var failedClauses []string
	lowerOut := strings.ToLower(res.Output)

	stopWords := map[string]bool{
		"must": true, "should": true, "that": true, "with": true, "have": true, "from": true,
		"this": true, "they": true, "will": true, "does": true, "done": true, "been": true,
		"avoid": true, "avoids": true, "avoiding": true, "never": true, "forbidden": true,
		"suggest": true, "suggests": true, "suggesting": true, "mention": true, "mentions": true,
		"mentioning": true, "contain": true, "contains": true, "include": true, "includes": true,
		"check": true, "checks": true, "verify": true, "verifies": true,
	}

	for _, clause := range clauses {
		lowerClause := strings.ToLower(clause)
		words := strings.Fields(lowerClause)
		isNegation := strings.Contains(lowerClause, "not") ||
			strings.Contains(lowerClause, "avoid") ||
			strings.Contains(lowerClause, "never") ||
			strings.Contains(lowerClause, "no ") ||
			strings.Contains(lowerClause, "forbidden") ||
			strings.Contains(lowerClause, "must not")

		var sigWords []string
		for _, w := range words {
			clean := strings.Trim(w, ".,`'\"();:[]{}")
			if len(clean) >= 3 && !stopWords[clean] {
				sigWords = append(sigWords, clean)
			}
		}

		matched := false
		for _, w := range sigWords {
			if strings.Contains(lowerOut, w) {
				matched = true
				break
			}
		}

		if isNegation {
			if matched {
				failedClauses = append(failedClauses, clause)
			}
		} else {
			if !matched && len(sigWords) > 0 {
				failedClauses = append(failedClauses, clause)
			}
		}
	}

	passed := len(failedClauses) == 0
	score := 0.0
	if passed {
		score = g.GraderWeight
	}

	detail := fmt.Sprintf("Deterministic keyword clause check: %d/%d criteria satisfied (%d unsatisfied)",
		len(clauses)-len(failedClauses), len(clauses), len(failedClauses))
	return &GradeResult{
		GraderName: g.GraderName,
		GraderType: "keyword_clause",
		Passed:     passed,
		Score:      score,
		MaxScore:   g.GraderWeight,
		Detail:     detail,
	}, nil
}

// LLMJudgeGrader evaluates agent output against a natural language rubric using an assistance model judge.
type LLMJudgeGrader struct {
	BaseGrader
	Model         string
	Provider      string
	Samples       int
	PassThreshold int
	Rubric        string
	JudgeProvider assist.Provider // Injected or configured provider
}

func (g *LLMJudgeGrader) Grade(ctx context.Context, res *RunResult, workDir string) (*GradeResult, error) {
	rubricTrimmed := strings.TrimSpace(g.Rubric)
	if rubricTrimmed == "" {
		return &GradeResult{
			GraderName:    g.GraderName,
			GraderType:    "llm_judge",
			Passed:        false,
			Score:         0,
			MaxScore:      g.GraderWeight,
			JudgeModel:    g.Model,
			JudgeProvider: g.Provider,
			Detail:        "empty or unparseable rubric: no evaluation criteria provided",
			Unavailable:   false,
		}, nil
	}

	samples := g.Samples
	if samples <= 0 {
		samples = 1
	}
	threshold := g.PassThreshold
	if threshold <= 0 {
		threshold = 1
	}
	if threshold > samples {
		return &GradeResult{
			GraderName:    g.GraderName,
			GraderType:    "llm_judge",
			Passed:        false,
			Score:         0,
			MaxScore:      g.GraderWeight,
			JudgeModel:    g.Model,
			JudgeProvider: g.Provider,
			Detail:        fmt.Sprintf("invalid judge configuration: pass_threshold (%d) > samples (%d)", threshold, samples),
			Unavailable:   false,
		}, nil
	}

	var p assist.Provider
	if g.JudgeProvider != nil {
		p = g.JudgeProvider
	} else if strings.EqualFold(g.Provider, "mock") || strings.EqualFold(g.Model, "mock") {
		p = assist.NewMockAdapter()
	} else if g.Provider != "" && !strings.EqualFold(g.Provider, "typesafe") && !strings.EqualFold(g.Provider, "jev") {
		return &GradeResult{
			GraderName:    g.GraderName,
			GraderType:    "llm_judge",
			Passed:        false,
			Score:         0,
			MaxScore:      g.GraderWeight,
			JudgeModel:    g.Model,
			JudgeProvider: g.Provider,
			Unavailable:   true,
			Detail:        fmt.Sprintf("judge provider %q is unsupported; configured judge cannot execute", g.Provider),
		}, nil
	} else {
		p = assist.ActiveProviderFor("eval", "")
	}

	if p == nil {
		provName := g.Provider
		if provName == "" {
			provName = "unconfigured"
		}
		return &GradeResult{
			GraderName:    g.GraderName,
			GraderType:    "llm_judge",
			Passed:        false,
			Score:         0,
			MaxScore:      g.GraderWeight,
			JudgeModel:    g.Model,
			JudgeProvider: provName,
			Unavailable:   true,
			Detail:        fmt.Sprintf("judge model %q (provider: %s) is unavailable: offline evaluation has no consented assist provider; keyword matching cannot masquerade as model judgment", g.Model, provName),
		}, nil
	}

	questions := map[string]assist.Question{
		"rubric": {
			Type:         assist.TypeScore,
			Instructions: fmt.Sprintf("Evaluate whether the agent output satisfies the following rubric:\n\n%s", rubricTrimmed),
			Criteria:     rubricTrimmed,
		},
	}
	state := map[string]any{
		"output":  res.Output,
		"rubric":  rubricTrimmed,
		"case":    res.CaseID,
		"harness": res.Harness,
	}

	passedSamples := 0
	var sampleDetails []string
	var lastEvidence string

	for i := 0; i < samples; i++ {
		resp, err := p.Evaluate(ctx, g.Model, state, questions)
		if err != nil {
			return &GradeResult{
				GraderName:    g.GraderName,
				GraderType:    "llm_judge",
				Passed:        false,
				Score:         0,
				MaxScore:      g.GraderWeight,
				JudgeModel:    g.Model,
				JudgeProvider: string(p.Name()),
				Detail:        fmt.Sprintf("judge evaluation failed on sample %d/%d: %v", i+1, samples, err),
			}, nil
		}

		if inv, ok := resp.InvalidAnswers["rubric"]; ok {
			return &GradeResult{
				GraderName:    g.GraderName,
				GraderType:    "llm_judge",
				Passed:        false,
				Score:         0,
				MaxScore:      g.GraderWeight,
				JudgeModel:    resp.Model,
				JudgeProvider: string(resp.Provider),
				Detail:        fmt.Sprintf("judge returned invalid response on sample %d/%d: %s", i+1, samples, inv),
			}, nil
		}

		samplePassed := false
		if sc, ok := resp.Scores["rubric"]; ok {
			lastEvidence = fmt.Sprintf("score=%.2f, confidence=%.2f", sc.Score, sc.Confidence)
			if sc.Score >= 1.0 {
				samplePassed = true
			}
		} else if noul, ok := resp.Nouls["rubric"]; ok {
			lastEvidence = fmt.Sprintf("p(yes)=%.2f", noul.Noul)
			if noul.Noul >= 0.5 {
				samplePassed = true
			}
		} else if ch, ok := resp.Choices["rubric"]; ok {
			lastEvidence = fmt.Sprintf("choice=%s, confidence=%.2f", ch.Choice, ch.Confidence)
			if !strings.EqualFold(ch.Choice, "fail") && !strings.EqualFold(ch.Choice, "no") && !strings.EqualFold(ch.Choice, "unknown") {
				samplePassed = true
			}
		} else {
			return &GradeResult{
				GraderName:    g.GraderName,
				GraderType:    "llm_judge",
				Passed:        false,
				Score:         0,
				MaxScore:      g.GraderWeight,
				JudgeModel:    resp.Model,
				JudgeProvider: string(resp.Provider),
				Detail:        fmt.Sprintf("missing judgment response for rubric on sample %d/%d", i+1, samples),
			}, nil
		}

		if samplePassed {
			passedSamples++
			sampleDetails = append(sampleDetails, fmt.Sprintf("sample %d: pass (%s)", i+1, lastEvidence))
		} else {
			sampleDetails = append(sampleDetails, fmt.Sprintf("sample %d: fail (%s)", i+1, lastEvidence))
		}
	}

	passed := passedSamples >= threshold
	score := 0.0
	if passed {
		score = g.GraderWeight
	}

	detail := fmt.Sprintf("LLM judge [%s/%s] result: %d/%d samples passed (threshold %d); evidence: %s",
		p.Name(), g.Model, passedSamples, samples, threshold, strings.Join(sampleDetails, "; "))

	return &GradeResult{
		GraderName:    g.GraderName,
		GraderType:    "llm_judge",
		Passed:        passed,
		Score:         score,
		MaxScore:      g.GraderWeight,
		JudgeModel:    g.Model,
		JudgeProvider: string(p.Name()),
		Detail:        detail,
		Evidence:      lastEvidence,
		Unavailable:   false,
	}, nil
}
