package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HarnessRunner defines the interface for running prompts through an AI CLI agent.
type HarnessRunner interface {
	Name() string
	Run(ctx context.Context, req RunRequest) (*RunResult, error)
	IsAvailable() bool
}

// Registry maintains the collection of available harness runners.
type Registry struct {
	mu      sync.RWMutex
	runners map[string]HarnessRunner
}

// NewRegistry creates a registry populated with default harness runners.
func NewRegistry() *Registry {
	r := &Registry{
		runners: make(map[string]HarnessRunner),
	}
	r.Register(&MockRunner{})
	r.Register(&ClaudeRunner{})
	r.Register(&AgyRunner{})
	return r
}

// Register adds or overrides a runner in the registry.
func (r *Registry) Register(runner HarnessRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runners[strings.ToLower(runner.Name())] = runner
}

// Get retrieves a runner by name.
func (r *Registry) Get(name string) (HarnessRunner, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	runner, ok := r.runners[strings.ToLower(name)]
	return runner, ok
}

// AutoDetect selects the best available runner (claude -> agy).
// Simulated/mock runners are intentionally excluded; mock use must be explicit.
func (r *Registry) AutoDetect() (HarnessRunner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, name := range []string{"claude", "agy"} {
		if runner, ok := r.runners[name]; ok && runner.IsAvailable() {
			return runner, nil
		}
	}
	return nil, fmt.Errorf("no real evaluation harness available on PATH (checked: claude, agy); to use simulated runner, specify --harness=mock explicitly")
}

// ClaudeRunner executes prompts via Claude Code (`claude -p`).
type ClaudeRunner struct {
	BinPath string
}

func (c *ClaudeRunner) Name() string { return "claude" }

func (c *ClaudeRunner) getBin() string {
	if c.BinPath != "" {
		return c.BinPath
	}
	// Check standard user local bin
	userHome, _ := os.UserHomeDir()
	cand := filepath.Join(userHome, ".local", "bin", "claude")
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	return "claude"
}

func (c *ClaudeRunner) IsAvailable() bool {
	_, err := exec.LookPath(c.getBin())
	return err == nil
}

func buildPromptWithResolvedRules(body string, rules []ResolvedRule) (string, int) {
	if len(rules) == 0 {
		return body, 0
	}
	var activeRules []string
	for _, r := range rules {
		if strings.TrimSpace(r.Content) != "" {
			activeRules = append(activeRules, r.Content)
		}
	}
	if len(activeRules) == 0 {
		return body, 0
	}

	ruleText := strings.Join(activeRules, "\n---\n")
	tokens := len(ruleText) / 4
	fullPrompt := fmt.Sprintf("SYSTEM RULES:\n%s\n\nUSER PROMPT:\n%s", ruleText, body)
	return fullPrompt, tokens
}

func buildPromptWithRules(body string, rules []string, target string, omitTarget bool) (string, int) {
	if len(rules) == 0 {
		return body, 0
	}
	var activeRules []string
	targetBase := filepath.Base(target)
	targetName := strings.TrimSuffix(targetBase, filepath.Ext(targetBase))

	for _, rPath := range rules {
		rBase := filepath.Base(rPath)
		rName := strings.TrimSuffix(rBase, filepath.Ext(rBase))

		isTarget := false
		if target != "" && (rPath == target || rBase == target || rBase == targetBase || rName == targetName) {
			isTarget = true
		}

		if omitTarget && isTarget {
			// Skip the ablated target rule in the "without" arm
			continue
		}

		data, err := os.ReadFile(rPath)
		if err == nil {
			activeRules = append(activeRules, string(data))
		}
	}

	if len(activeRules) == 0 {
		return body, 0
	}

	ruleText := strings.Join(activeRules, "\n---\n")
	tokens := len(ruleText) / 4
	fullPrompt := fmt.Sprintf("SYSTEM RULES:\n%s\n\nUSER PROMPT:\n%s", ruleText, body)
	return fullPrompt, tokens
}

func (c *ClaudeRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	start := time.Now()
	bin := c.getBin()

	var prompt string
	if len(req.ResolvedRules) > 0 {
		prompt, _ = buildPromptWithResolvedRules(req.Case.Prompt.Body, req.ResolvedRules)
	} else {
		prompt, _ = buildPromptWithRules(req.Case.Prompt.Body, req.Case.Prompt.Rules, req.Target, req.OmitTarget)
	}

	args := []string{"-p", prompt}
	if req.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(req.MaxTurns))
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if len(req.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.AllowedTools, ","))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.WorkDir
	configureProcessGroup(cmd)

	for k, v := range req.ExtraEnv {
		cmd.Env = append(cmd.Environ(), fmt.Sprintf("%s=%s", k, v))
	}

	out, err := cmd.CombinedOutput()
	dur := time.Since(start)

	outStr := string(out)
	exitCode := 0
	errMsg := ""
	if err != nil {
		exitCode = 1
		errMsg = err.Error()
	}

	// Heuristic token estimation (4 chars ~ 1 token)
	promptTokens := len(prompt) / 4
	compTokens := len(outStr) / 4

	// Parse simple tool call outputs if any are logged in output
	var toolCalls []ToolCallRecord
	for _, line := range strings.Split(outStr, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[tool:") || strings.HasPrefix(line, "Calling tool ") {
			toolCalls = append(toolCalls, ToolCallRecord{
				Tool: line,
				Turn: 1,
			})
		}
	}

	return &RunResult{
		CaseID:    req.Case.ID,
		Harness:   c.Name(),
		Arm:       req.Arm,
		Output:    outStr,
		ExitCode:  exitCode,
		Duration:  dur,
		ToolCalls: toolCalls,
		Tokens: TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: compTokens,
			TotalTokens:      promptTokens + compTokens,
			EstimatedCost:    float64(promptTokens)*0.000003 + float64(compTokens)*0.000015,
		},
		ResolvedRules: req.ResolvedRules,
		Error:         errMsg,
	}, nil
}

// AgyRunner executes prompts via Antigravity CLI (`agy -p`).
type AgyRunner struct {
	BinPath string
}

func (a *AgyRunner) Name() string { return "agy" }

func (a *AgyRunner) getBin() string {
	if a.BinPath != "" {
		return a.BinPath
	}
	userHome, _ := os.UserHomeDir()
	cand := filepath.Join(userHome, ".local", "bin", "agy")
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	return "agy"
}

func (a *AgyRunner) IsAvailable() bool {
	_, err := exec.LookPath(a.getBin())
	return err == nil
}

func (a *AgyRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	start := time.Now()
	bin := a.getBin()

	var prompt string
	if len(req.ResolvedRules) > 0 {
		prompt, _ = buildPromptWithResolvedRules(req.Case.Prompt.Body, req.ResolvedRules)
	} else {
		prompt, _ = buildPromptWithRules(req.Case.Prompt.Body, req.Case.Prompt.Rules, req.Target, req.OmitTarget)
	}
	args := []string{"-p", prompt}
	if req.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(req.MaxTurns))
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.WorkDir
	configureProcessGroup(cmd)

	out, err := cmd.CombinedOutput()
	dur := time.Since(start)

	outStr := string(out)
	exitCode := 0
	errMsg := ""
	if err != nil {
		exitCode = 1
		errMsg = err.Error()
	}

	promptTokens := len(prompt) / 4
	compTokens := len(outStr) / 4

	return &RunResult{
		CaseID:   req.Case.ID,
		Harness:  a.Name(),
		Arm:      req.Arm,
		Output:   outStr,
		ExitCode: exitCode,
		Duration: dur,
		Tokens: TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: compTokens,
			TotalTokens:      promptTokens + compTokens,
		},
		ResolvedRules: req.ResolvedRules,
		Error:         errMsg,
	}, nil
}

// SyntheticRunner is implemented by runners that provide simulated responses.
type SyntheticRunner interface {
	IsSynthetic() bool
}

// IsSyntheticRunner checks if the given runner is synthetic or simulated.
func IsSyntheticRunner(r HarnessRunner) bool {
	if r == nil {
		return false
	}
	if sr, ok := r.(SyntheticRunner); ok {
		return sr.IsSynthetic()
	}
	return strings.EqualFold(r.Name(), "mock") || strings.EqualFold(r.Name(), "synthetic")
}

// MockRunner provides deterministic, canned responses for offline testing.
type MockRunner struct {
	mu            sync.Mutex
	CannedOutputs map[string]string
	CannedTools   map[string][]ToolCallRecord
	DefaultOutput string
}

func (m *MockRunner) Name() string      { return "mock" }
func (m *MockRunner) IsAvailable() bool { return true }
func (m *MockRunner) IsSynthetic() bool { return true }

func (m *MockRunner) SetMockResponse(caseID, output string, tools []ToolCallRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.CannedOutputs == nil {
		m.CannedOutputs = make(map[string]string)
	}
	if m.CannedTools == nil {
		m.CannedTools = make(map[string][]ToolCallRecord)
	}
	m.CannedOutputs[caseID] = output
	m.CannedTools[caseID] = tools
}

func (m *MockRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := m.DefaultOutput
	if out == "" {
		out = fmt.Sprintf("Mock response for case %s (prompt: %s)", req.Case.ID, req.Case.Prompt.Body)
	}
	if canned, ok := m.CannedOutputs[req.Case.ID]; ok {
		out = canned
	}

	var tools []ToolCallRecord
	if cannedTools, ok := m.CannedTools[req.Case.ID]; ok {
		tools = cannedTools
	}

	var prompt string
	if len(req.ResolvedRules) > 0 {
		prompt, _ = buildPromptWithResolvedRules(req.Case.Prompt.Body, req.ResolvedRules)
	} else {
		prompt, _ = buildPromptWithRules(req.Case.Prompt.Body, req.Case.Prompt.Rules, req.Target, req.OmitTarget)
	}
	promptTokens := len(prompt) / 4
	compTokens := len(out) / 4

	return &RunResult{
		CaseID:    req.Case.ID,
		Harness:   m.Name(),
		Arm:       req.Arm,
		Synthetic: true,
		Output:    out,
		ExitCode:  0,
		Duration:  5 * time.Millisecond,
		ToolCalls: tools,
		Tokens: TokenUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: compTokens,
			TotalTokens:      promptTokens + compTokens,
			EstimatedCost:    0.0001,
			Measured:         false,
		},
		ResolvedRules: req.ResolvedRules,
	}, nil
}

func calculateMaxScore(graders []Grader, arm string) float64 {
	total := 0.0
	for _, g := range graders {
		gArm := g.Arm()
		if gArm != "both" && gArm != "" && gArm != arm {
			continue
		}
		total += g.Weight()
	}
	return total
}

func seedIsolatedWorkspace(srcDir, dstDir string, addDirs []string) error {
	if srcDir == "" {
		return nil
	}
	info, err := os.Stat(srcDir)
	if err != nil || !info.IsDir() {
		return nil
	}

	err = filepath.Walk(srcDir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil || rel == "." {
			return nil
		}

		// Never follow symlinks into an isolated eval workspace. A symlinked
		// sibling repository (for example cli/sila-skills -> ../sila-skills)
		// would otherwise be copied as a file and fail with "is a directory".
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		base := fi.Name()
		if fi.IsDir() {
			if base == ".git" || base == ".bin" || base == "node_modules" || base == "dist" || base == ".worktrees" || strings.HasPrefix(base, "sila-eval-") {
				return filepath.SkipDir
			}
		}

		if !fi.IsDir() {
			if strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".db-wal") || strings.HasSuffix(base, ".db-shm") || strings.HasSuffix(base, ".sock") {
				return nil
			}
		}

		targetPath := filepath.Join(dstDir, rel)
		if fi.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		return copyFile(path, targetPath, fi.Mode())
	})
	if err != nil {
		return fmt.Errorf("seed isolated workspace from %s: %w", srcDir, err)
	}

	for _, ad := range addDirs {
		adSrc := ad
		if !filepath.IsAbs(adSrc) {
			adSrc = filepath.Join(srcDir, ad)
		}
		if sfi, err := os.Stat(adSrc); err == nil && sfi.IsDir() {
			adDst := filepath.Join(dstDir, filepath.Base(ad))
			_ = filepath.Walk(adSrc, func(p string, fi os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(adSrc, p)
				if rel == "." {
					return os.MkdirAll(adDst, 0755)
				}
				tp := filepath.Join(adDst, rel)
				if fi.IsDir() {
					return os.MkdirAll(tp, 0755)
				}
				return copyFile(p, tp, fi.Mode())
			})
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

// ExecuteCase runs a single case through the given runner and evaluates its graders.
func ExecuteCase(ctx context.Context, runner HarnessRunner, c *Case, workDir string, arm string, omitTarget bool, target ...string) (*CaseResult, error) {
	targetRule := ""
	if len(target) > 0 {
		targetRule = target[0]
	}
	return ExecuteCaseWithRules(ctx, runner, c, workDir, arm, nil, targetRule, omitTarget)
}

// ExecuteCaseWithRules runs a single case through the given runner with explicit resolved rules.
func ExecuteCaseWithRules(ctx context.Context, runner HarnessRunner, c *Case, workDir string, arm string, resolvedRules []ResolvedRule, target string, omitTarget bool) (*CaseResult, error) {
	start := time.Now()
	isSynthetic := IsSyntheticRunner(runner)

	maxPossibleScore := calculateMaxScore(c.Graders, arm)

	// Validate control fields
	if c.Prompt.MaxTurns < 0 {
		return nil, fmt.Errorf("invalid max_turns %d: must be >= 0", c.Prompt.MaxTurns)
	}
	if len(c.Prompt.Harnesses) > 0 {
		supported := false
		for _, h := range c.Prompt.Harnesses {
			if strings.EqualFold(h, runner.Name()) {
				supported = true
				break
			}
		}
		if !supported {
			return &CaseResult{
				CaseID:        c.ID,
				Harness:       runner.Name(),
				Arm:           arm,
				Synthetic:     isSynthetic,
				Passed:        false,
				TotalScore:    0,
				MaxScore:      maxPossibleScore,
				PassRate:      0,
				Duration:      time.Since(start),
				ResolvedRules: resolvedRules,
				Error:         fmt.Sprintf("unsupported harness %q for case %s (allowed: %s)", runner.Name(), c.ID, strings.Join(c.Prompt.Harnesses, ", ")),
			}, nil
		}
	}

	// Always create an isolated temporary workspace per-case and per-arm.
	// Scaffold and harness commands MUST NEVER mutate the caller's repository.
	execDir, err := os.MkdirTemp("", "sila-eval-"+c.ID+"-"+arm+"-*")
	if err != nil {
		return nil, fmt.Errorf("create temp workdir: %w", err)
	}
	defer os.RemoveAll(execDir)

	// Seed workspace from workDir if specified, without touching workDir.
	if workDir != "" {
		if err := seedIsolatedWorkspace(workDir, execDir, c.CaseConfig.AddDirs); err != nil {
			return nil, fmt.Errorf("seed workspace from %s: %w", workDir, err)
		}
	}

	// Run scaffold script if defined
	scaffold := c.Prompt.ScaffoldScript
	if scaffold == "" {
		scaffold = c.CaseConfig.ScaffoldScript
	}
	if scaffold != "" {
		cmd := exec.CommandContext(ctx, "sh", "-c", scaffold)
		cmd.Dir = execDir
		cmd.Env = append(os.Environ(), "SILA_EVAL_ARM="+arm)
		configureProcessGroup(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return &CaseResult{
				CaseID:        c.ID,
				Harness:       runner.Name(),
				Arm:           arm,
				Synthetic:     isSynthetic,
				Passed:        false,
				TotalScore:    0,
				MaxScore:      maxPossibleScore,
				PassRate:      0,
				Duration:      time.Since(start),
				ResolvedRules: resolvedRules,
				Error:         fmt.Sprintf("scaffold script failed: %v: %s", err, strings.TrimSpace(string(out))),
			}, nil
		}
	}

	req := RunRequest{
		Case:          c,
		Harness:       runner.Name(),
		WorkDir:       execDir,
		Arm:           arm,
		Target:        target,
		OmitTarget:    omitTarget,
		ResolvedRules: resolvedRules,
		MaxTurns:      c.Prompt.MaxTurns,
		Model:         c.Prompt.Model,
		AllowedTools:  c.Prompt.AllowedTools,
	}

	runRes, err := runner.Run(ctx, req)
	if err != nil {
		return &CaseResult{
			CaseID:        c.ID,
			Harness:       runner.Name(),
			Arm:           arm,
			Synthetic:     isSynthetic,
			Passed:        false,
			TotalScore:    0,
			MaxScore:      maxPossibleScore,
			PassRate:      0,
			Duration:      time.Since(start),
			ResolvedRules: resolvedRules,
			Error:         fmt.Sprintf("harness run failed: %v", err),
		}, nil
	}

	runRes.Synthetic = isSynthetic
	if len(runRes.ResolvedRules) == 0 && len(resolvedRules) > 0 {
		runRes.ResolvedRules = resolvedRules
	}

	// Evaluate graders
	var grades []GradeResult
	totalScore := 0.0
	maxScore := 0.0
	allPassed := true
	applicableGraders := 0

	// Check harness exit code and timeout
	if runRes.ExitCode != 0 {
		allPassed = false
	}
	if runRes.Error != "" {
		allPassed = false
	}
	if ctx.Err() != nil {
		allPassed = false
	}

	for _, g := range c.Graders {
		// Filter grader by arm if specified
		gArm := g.Arm()
		if gArm != "both" && gArm != "" && gArm != arm {
			continue
		}

		applicableGraders++
		gRes, err := g.Grade(ctx, runRes, execDir)
		if err != nil {
			gRes = &GradeResult{
				GraderName: g.Name(),
				GraderType: g.Type(),
				Passed:     false,
				Score:      0,
				MaxScore:   g.Weight(),
				Detail:     fmt.Sprintf("Evaluation error: %v", err),
			}
		}

		if !gRes.IndicatorOnly {
			totalScore += gRes.Score
			maxScore += gRes.MaxScore
			if !gRes.Passed {
				allPassed = false
			}
		}
		grades = append(grades, *gRes)
	}

	if maxScore == 0 {
		maxScore = maxPossibleScore
	}

	executionFailed := false
	var caseError string

	if ctx.Err() != nil {
		executionFailed = true
		allPassed = false
		caseError = fmt.Sprintf("execution timed out or canceled: %v", ctx.Err())
	} else if runRes.ExitCode != 0 {
		executionFailed = true
		allPassed = false
		if runRes.Error != "" {
			caseError = fmt.Sprintf("harness exited with code %d: %s", runRes.ExitCode, runRes.Error)
		} else {
			caseError = fmt.Sprintf("harness exited with code %d", runRes.ExitCode)
		}
	} else if runRes.Error != "" {
		executionFailed = true
		allPassed = false
		caseError = fmt.Sprintf("harness execution error: %s", runRes.Error)
	} else if applicableGraders == 0 || maxScore == 0 {
		executionFailed = true
		allPassed = false
		caseError = "zero applicable scoring criteria"
	} else {
		for _, gr := range grades {
			if gr.GraderType == "invalid" {
				executionFailed = true
				allPassed = false
				caseError = fmt.Sprintf("grader %q is invalid (%s)", gr.GraderName, gr.Detail)
				break
			}
			if gr.Unavailable {
				executionFailed = true
				allPassed = false
				caseError = fmt.Sprintf("grader %q is unavailable (%s)", gr.GraderName, gr.Detail)
				break
			}
		}
	}

	passRate := 0.0
	if executionFailed {
		totalScore = 0.0
		passRate = 0.0
		allPassed = false
	} else {
		if maxScore > 0 {
			passRate = totalScore / maxScore
		}
	}

	return &CaseResult{
		CaseID:        c.ID,
		Harness:       runner.Name(),
		Arm:           arm,
		Synthetic:     isSynthetic,
		Passed:        allPassed,
		TotalScore:    totalScore,
		MaxScore:      maxScore,
		PassRate:      passRate,
		Duration:      time.Since(start),
		Grades:        grades,
		RunResult:     runRes,
		ResolvedRules: resolvedRules,
		Error:         caseError,
	}, nil
}

// ExecuteSuite executes all cases in the suite with optional filtering and parallelism.
func ExecuteSuite(ctx context.Context, runner HarnessRunner, cases []*Case, workDir string, filterCase, filterTag string) (*SuiteResult, error) {
	start := time.Now()
	isSynthetic := IsSyntheticRunner(runner)
	var filtered []*Case

	for _, c := range cases {
		if filterCase != "" && !strings.EqualFold(c.ID, filterCase) && !strings.Contains(strings.ToLower(c.ID), strings.ToLower(filterCase)) {
			continue
		}
		if filterTag != "" {
			tagMatch := false
			for _, t := range c.Prompt.Tags {
				if strings.EqualFold(t, filterTag) {
					tagMatch = true
					break
				}
			}
			if !tagMatch {
				continue
			}
		}
		filtered = append(filtered, c)
	}

	if len(filtered) == 0 {
		return &SuiteResult{
			Timestamp:   start,
			Harness:     runner.Name(),
			Synthetic:   isSynthetic,
			Passed:      false,
			TotalCases:  0,
			PassedCases: 0,
			TotalScore:  0,
			MaxScore:    0,
			Duration:    time.Since(start),
			Cases:       nil,
			Error:       "zero evaluation cases selected",
		}, nil
	}

	var results []CaseResult
	totalScore := 0.0
	maxScore := 0.0
	passedCount := 0

	for _, c := range filtered {
		res, err := ExecuteCase(ctx, runner, c, workDir, "single", false)
		if err != nil {
			return nil, err
		}
		results = append(results, *res)
		totalScore += res.TotalScore
		maxScore += res.MaxScore
		if res.Passed {
			passedCount++
		}
	}

	suitePassed := len(filtered) > 0 && passedCount == len(filtered)

	return &SuiteResult{
		Timestamp:   time.Now(),
		Harness:     runner.Name(),
		Synthetic:   isSynthetic,
		Passed:      suitePassed,
		TotalCases:  len(filtered),
		PassedCases: passedCount,
		TotalScore:  totalScore,
		MaxScore:    maxScore,
		Duration:    time.Since(start),
		Cases:       results,
	}, nil
}

// RunRolloutWithTree materializes tree into an isolated rollout directory under workDir and invokes runner.Run.
func RunRolloutWithTree(ctx context.Context, runner HarnessRunner, c *Case, workDir, label string, tree ComponentTree) (*RunResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rolloutDir := filepath.Join(workDir, label)
	if err := os.MkdirAll(rolloutDir, 0755); err != nil {
		return nil, fmt.Errorf("create rollout dir %s: %w", rolloutDir, err)
	}
	if len(tree) > 0 {
		if err := WriteTree(rolloutDir, tree); err != nil {
			return nil, fmt.Errorf("materialize harness tree for %s: %w", label, err)
		}
	}
	return runner.Run(ctx, RunRequest{
		Case:    c,
		Harness: runner.Name(),
		WorkDir: rolloutDir,
		Arm:     label,
	})
}
