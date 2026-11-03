package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// spyRunner records harness invocations and returns customizable responses.
type spyRunner struct {
	mu           sync.Mutex
	name         string
	isSynthetic  bool
	callCount    int
	calls        []RunRequest
	customRun    func(ctx context.Context, req RunRequest) (*RunResult, error)
	defaultCode  int
	defaultOut   string
	defaultError string
}

func (s *spyRunner) Name() string {
	if s.name != "" {
		return s.name
	}
	return "spy"
}
func (s *spyRunner) IsAvailable() bool { return true }
func (s *spyRunner) IsSynthetic() bool { return s.isSynthetic }

func (s *spyRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	s.mu.Lock()
	s.callCount++
	s.calls = append(s.calls, req)
	s.mu.Unlock()

	if s.customRun != nil {
		return s.customRun(ctx, req)
	}

	prompt, _ := buildPromptWithResolvedRules(req.Case.Prompt.Body, req.ResolvedRules)
	out := s.defaultOut
	if out == "" {
		out = "spy output for " + req.Case.ID
	}

	pTokens := len(prompt) / 4
	cTokens := len(out) / 4

	return &RunResult{
		CaseID:        req.Case.ID,
		Harness:       s.Name(),
		Arm:           req.Arm,
		Synthetic:     s.isSynthetic,
		Output:        out,
		ExitCode:      s.defaultCode,
		Duration:      10 * time.Millisecond,
		Tokens:        TokenUsage{PromptTokens: pTokens, CompletionTokens: cTokens, TotalTokens: pTokens + cTokens, Measured: true},
		ResolvedRules: req.ResolvedRules,
		Error:         s.defaultError,
	}, nil
}

// Remediation 1: Add a runner spy and scoped-path fixtures proving no execution on unreadable target evidence.
func TestAblationPreExecutionRejectionsWithSpyRunner(t *testing.T) {
	tmpDir := t.TempDir()
	rulesDir := filepath.Join(tmpDir, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}

	validRule := filepath.Join(rulesDir, "valid.md")
	if err := os.WriteFile(validRule, []byte("valid rule content"), 0644); err != nil {
		t.Fatal(err)
	}

	emptyRule := filepath.Join(rulesDir, "empty.md")
	if err := os.WriteFile(emptyRule, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	unreadableRule := filepath.Join(rulesDir, "unreadable.md")
	if err := os.WriteFile(unreadableRule, []byte("secret"), 0000); err != nil {
		t.Fatal(err)
	}

	subDirRule := filepath.Join(rulesDir, "subfolder")
	if err := os.MkdirAll(subDirRule, 0755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		target      string
		caseRules   []string
		errContains string
	}{
		{
			name:        "missing target file",
			target:      "nonexistent-target.md",
			caseRules:   []string{"rules/valid.md"},
			errContains: "not found or unreadable",
		},
		{
			name:        "unreadable target file (permission denied)",
			target:      "rules/unreadable.md",
			caseRules:   []string{"rules/valid.md"},
			errContains: "unreadable",
		},
		{
			name:        "escaping target (parent directory traversal)",
			target:      "../../etc/passwd",
			caseRules:   []string{"rules/valid.md"},
			errContains: "escapes source workspace",
		},
		{
			name:        "empty target file (absent bytes)",
			target:      "rules/empty.md",
			caseRules:   []string{"rules/valid.md"},
			errContains: "contains 0 bytes",
		},
		{
			name:        "directory target (unsupported path)",
			target:      "rules/subfolder",
			caseRules:   []string{"rules/valid.md"},
			errContains: "resolved to directory",
		},
		{
			name:        "unreadable control rule",
			target:      "rules/valid.md",
			caseRules:   []string{"rules/unreadable.md"},
			errContains: "unreadable",
		},
		{
			name:        "escaping control rule",
			target:      "rules/valid.md",
			caseRules:   []string{"../secret.md"},
			errContains: "escapes source workspace",
		},
		{
			name:        "empty control rule",
			target:      "rules/valid.md",
			caseRules:   []string{"rules/empty.md"},
			errContains: "contains 0 bytes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy := &spyRunner{name: "spy-runner"}
			c := &Case{
				ID:   "test-case",
				Path: tmpDir,
				Prompt: PromptSpec{
					Body:  "Perform action",
					Rules: tc.caseRules,
				},
			}

			res, err := RunAblationCase(context.Background(), spy, c, tc.target, tmpDir)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil result: %+v", tc.errContains, res)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("expected error containing %q, got %q", tc.errContains, err.Error())
			}

			// PROVE 0 HARNESS RUNS OCCURRED BEFORE PRE-EXECUTION VALIDATION
			if spy.callCount != 0 {
				t.Fatalf("expected exactly 0 runner calls, got %d", spy.callCount)
			}
		})
	}
}

// Acceptance Criterion 1 & 4: Relative rule paths resolve against explicit source root independent of process cwd.
func TestAblationRuleResolutionIndependentOfCwd(t *testing.T) {
	ws := t.TempDir()
	rulesDir := filepath.Join(ws, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}

	targetContent := "TARGET RULE: Always use pure Go SQLite"
	ctrlContent := "CONTROL RULE: Always verify tests with -race"

	if err := os.WriteFile(filepath.Join(rulesDir, "target.md"), []byte(targetContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "control.md"), []byte(ctrlContent), 0644); err != nil {
		t.Fatal(err)
	}

	caseDir := filepath.Join(ws, "evals", "case1")
	if err := os.MkdirAll(caseDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Change process cwd to an unrelated directory
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	unrelatedDir := t.TempDir()
	if err := os.Chdir(unrelatedDir); err != nil {
		t.Fatal(err)
	}

	c := &Case{
		ID:   "case1",
		Path: caseDir,
		Prompt: PromptSpec{
			Body:  "Run test command",
			Rules: []string{"rules/control.md"},
		},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "check-out", GraderType: "regex", GraderWeight: 1.0, GraderArm: "both"},
				Pattern:    "spy",
				Target:     "output",
			},
		},
	}

	spy := &spyRunner{name: "claude"} // non-synthetic name
	abRes, err := RunAblationCase(context.Background(), spy, c, "rules/target.md", ws)
	if err != nil {
		t.Fatalf("unexpected error resolving relative rules from workDir: %v", err)
	}

	if abRes == nil {
		t.Fatal("expected non-nil ablation result")
	}

	// Verify spy received 2 calls (with and without)
	if spy.callCount != 2 {
		t.Fatalf("expected 2 runner calls, got %d", spy.callCount)
	}

	// Verify resolved rules in both calls
	callWith := spy.calls[0]
	callWithout := spy.calls[1]

	if callWith.Arm != "with" || callWithout.Arm != "without" {
		t.Fatalf("expected calls in order [with, without], got [%s, %s]", callWith.Arm, callWithout.Arm)
	}

	// In With arm: Target + Control
	if len(callWith.ResolvedRules) != 2 {
		t.Fatalf("expected 2 resolved rules in With arm, got %d", len(callWith.ResolvedRules))
	}
	hasTargetInWith := false
	hasCtrlInWith := false
	for _, r := range callWith.ResolvedRules {
		if r.IsTarget && r.Content == targetContent {
			hasTargetInWith = true
		}
		if !r.IsTarget && r.Content == ctrlContent {
			hasCtrlInWith = true
		}
	}
	if !hasTargetInWith || !hasCtrlInWith {
		t.Fatalf("With arm missing expected rules: target=%v ctrl=%v", hasTargetInIn(callWith.ResolvedRules, targetContent), hasTargetInIn(callWith.ResolvedRules, ctrlContent))
	}

	// In Without arm: Control only
	if len(callWithout.ResolvedRules) != 1 {
		t.Fatalf("expected 1 resolved rule in Without arm, got %d", len(callWithout.ResolvedRules))
	}
	if callWithout.ResolvedRules[0].IsTarget || callWithout.ResolvedRules[0].Content != ctrlContent {
		t.Fatalf("Without arm rule unexpected: %+v", callWithout.ResolvedRules[0])
	}
}

func hasTargetInIn(rules []ResolvedRule, content string) bool {
	for _, r := range rules {
		if r.Content == content {
			return true
		}
	}
	return false
}

// Acceptance Criterion 2: Capture hashes of bytes actually injected into each arm; prove target present only in with arm and controls match byte-for-byte.
func TestAblationByteInjectionParityAndHashes(t *testing.T) {
	ws := t.TempDir()
	rulesDir := filepath.Join(ws, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}

	targetBytes := []byte("TARGET: Use strict schema validation")
	ctrl1Bytes := []byte("CTRL 1: Enforce zero-allocation JSON streaming")
	ctrl2Bytes := []byte("CTRL 2: Require atomic transaction commits")

	targetSum := sha256.Sum256(targetBytes)
	targetHash := hex.EncodeToString(targetSum[:])

	ctrl1Sum := sha256.Sum256(ctrl1Bytes)
	ctrl1Hash := hex.EncodeToString(ctrl1Sum[:])

	ctrl2Sum := sha256.Sum256(ctrl2Bytes)
	ctrl2Hash := hex.EncodeToString(ctrl2Sum[:])

	if err := os.WriteFile(filepath.Join(rulesDir, "target.md"), targetBytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "ctrl1.md"), ctrl1Bytes, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "ctrl2.md"), ctrl2Bytes, 0644); err != nil {
		t.Fatal(err)
	}

	caseDir := filepath.Join(ws, "case")
	if err := os.MkdirAll(caseDir, 0755); err != nil {
		t.Fatal(err)
	}

	c := &Case{
		ID:   "parity-case",
		Path: caseDir,
		Prompt: PromptSpec{
			Body:  "Execute operation",
			Rules: []string{"rules/ctrl1.md", "rules/ctrl2.md"},
		},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "match", GraderType: "regex", GraderWeight: 1.0, GraderArm: "both"},
				Pattern:    "spy",
				Target:     "output",
			},
		},
	}

	spy := &spyRunner{name: "claude"}
	abRes, err := RunAblationCase(context.Background(), spy, c, "rules/target.md", ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 1. Verify TargetHash matches sha256 of targetBytes
	if abRes.TargetHash != targetHash {
		t.Fatalf("expected TargetHash %q, got %q", targetHash, abRes.TargetHash)
	}

	// 2. Verify ControlHashes contains both controls
	if len(abRes.ControlHashes) != 2 {
		t.Fatalf("expected 2 control hashes, got %d: %+v", len(abRes.ControlHashes), abRes.ControlHashes)
	}

	foundCtrl1 := false
	foundCtrl2 := false
	for _, h := range abRes.ControlHashes {
		if h == ctrl1Hash {
			foundCtrl1 = true
		}
		if h == ctrl2Hash {
			foundCtrl2 = true
		}
	}
	if !foundCtrl1 || !foundCtrl2 {
		t.Fatalf("missing expected control hashes: ctrl1=%v ctrl2=%v", foundCtrl1, foundCtrl2)
	}

	// 3. Verify InjectedWith (Target + Controls)
	if len(abRes.InjectedWith) != 3 {
		t.Fatalf("expected 3 rules in InjectedWith, got %d", len(abRes.InjectedWith))
	}
	targetCountInWith := 0
	for _, r := range abRes.InjectedWith {
		if r.Hash == targetHash {
			targetCountInWith++
			if !r.IsTarget {
				t.Errorf("expected IsTarget true for target rule in InjectedWith")
			}
			if r.Content != string(targetBytes) {
				t.Errorf("target rule content mismatch in InjectedWith")
			}
		}
	}
	if targetCountInWith != 1 {
		t.Fatalf("expected exactly 1 target rule in InjectedWith, got %d", targetCountInWith)
	}

	// 4. Verify InjectedWithout (Controls only; Target absent)
	if len(abRes.InjectedWithout) != 2 {
		t.Fatalf("expected 2 rules in InjectedWithout, got %d", len(abRes.InjectedWithout))
	}
	for _, r := range abRes.InjectedWithout {
		if r.Hash == targetHash || r.IsTarget {
			t.Fatalf("target rule leaked into InjectedWithout: %+v", r)
		}
	}

	// 5. Verify controls in InjectedWith match InjectedWithout byte-for-byte
	withoutMap := make(map[string]ResolvedRule)
	for _, r := range abRes.InjectedWithout {
		withoutMap[r.Hash] = r
	}
	for _, r := range abRes.InjectedWith {
		if r.Hash == targetHash {
			continue
		}
		withoutRule, ok := withoutMap[r.Hash]
		if !ok {
			t.Fatalf("control in InjectedWith not found in InjectedWithout: %+v", r)
		}
		if r.Content != withoutRule.Content || r.Bytes != withoutRule.Bytes {
			t.Fatalf("byte-for-byte mismatch between arms for control %s", r.RelPath)
		}
	}
}

// Acceptance Criterion 4: Target file mutation changes captured TargetHash.
func TestAblationTargetMutationChangesHash(t *testing.T) {
	ws := t.TempDir()
	rulesDir := filepath.Join(ws, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(rulesDir, "target.md")
	if err := os.WriteFile(targetPath, []byte("VERSION 1 CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}

	caseDir := filepath.Join(ws, "case")
	if err := os.MkdirAll(caseDir, 0755); err != nil {
		t.Fatal(err)
	}

	c := &Case{
		ID:   "mutate-case",
		Path: caseDir,
		Prompt: PromptSpec{
			Body: "Verify mutation",
		},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "regex", GraderType: "regex", GraderWeight: 1.0, GraderArm: "both"},
				Pattern:    "spy",
				Target:     "output",
			},
		},
	}

	spy := &spyRunner{name: "claude"}
	res1, err := RunAblationCase(context.Background(), spy, c, "rules/target.md", ws)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the target file
	v2Bytes := []byte("VERSION 2 CONTENT - CHANGED RULES")
	if err := os.WriteFile(targetPath, v2Bytes, 0644); err != nil {
		t.Fatal(err)
	}

	v2Sum := sha256.Sum256(v2Bytes)
	v2ExpectedHash := hex.EncodeToString(v2Sum[:])

	res2, err := RunAblationCase(context.Background(), spy, c, "rules/target.md", ws)
	if err != nil {
		t.Fatal(err)
	}

	if res1.TargetHash == res2.TargetHash {
		t.Fatalf("expected target mutation to change hash, but both are %q", res1.TargetHash)
	}
	if res2.TargetHash != v2ExpectedHash {
		t.Fatalf("expected hash %q for v2, got %q", v2ExpectedHash, res2.TargetHash)
	}
}

// Acceptance Criterion 3: Carry execution-failure eligibility into comparisons/recommendations.
func TestAblationFailureAwareRecommendations(t *testing.T) {
	ws := t.TempDir()
	rulesDir := filepath.Join(ws, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "target.md"), []byte("target"), 0644); err != nil {
		t.Fatal(err)
	}

	caseDir := filepath.Join(ws, "case")
	if err := os.MkdirAll(caseDir, 0755); err != nil {
		t.Fatal(err)
	}

	baseCase := &Case{
		ID:   "fail-eval-case",
		Path: caseDir,
		Prompt: PromptSpec{
			Body: "Do work",
		},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "pass-grader", GraderType: "regex", GraderWeight: 1.0, GraderArm: "both"},
				Pattern:    "success",
				Target:     "output",
			},
		},
	}

	t.Run("with arm execution error yields insufficient_evidence", func(t *testing.T) {
		spy := &spyRunner{
			name: "claude",
			customRun: func(ctx context.Context, req RunRequest) (*RunResult, error) {
				if req.Arm == "with" {
					return &RunResult{
						CaseID:   req.Case.ID,
						Harness:  "claude",
						Arm:      req.Arm,
						ExitCode: 1, // Nonzero exit code
						Error:    "process crashed",
					}, nil
				}
				return &RunResult{
					CaseID:   req.Case.ID,
					Harness:  "claude",
					Arm:      req.Arm,
					ExitCode: 0,
					Output:   "success",
				}, nil
			},
		}

		res, err := RunAblationCase(context.Background(), spy, baseCase, "rules/target.md", ws)
		if err != nil {
			t.Fatal(err)
		}
		if res.Recommendation != "insufficient_evidence" {
			t.Fatalf("expected recommendation 'insufficient_evidence', got %q", res.Recommendation)
		}
		if !strings.Contains(res.Reason, "execution error") {
			t.Fatalf("expected reason to mention execution error, got %q", res.Reason)
		}
	})

	t.Run("without arm execution error yields insufficient_evidence", func(t *testing.T) {
		spy := &spyRunner{
			name: "claude",
			customRun: func(ctx context.Context, req RunRequest) (*RunResult, error) {
				if req.Arm == "without" {
					return &RunResult{
						CaseID:   req.Case.ID,
						Harness:  "claude",
						Arm:      req.Arm,
						ExitCode: 127,
						Error:    "command not found",
					}, nil
				}
				return &RunResult{
					CaseID:   req.Case.ID,
					Harness:  "claude",
					Arm:      req.Arm,
					ExitCode: 0,
					Output:   "success",
				}, nil
			},
		}

		res, err := RunAblationCase(context.Background(), spy, baseCase, "rules/target.md", ws)
		if err != nil {
			t.Fatal(err)
		}
		if res.Recommendation != "insufficient_evidence" {
			t.Fatalf("expected recommendation 'insufficient_evidence', got %q", res.Recommendation)
		}
	})

	t.Run("synthetic mock runner yields insufficient_evidence", func(t *testing.T) {
		mockRunner := &MockRunner{}
		mockRunner.SetMockResponse("fail-eval-case", "success", nil)

		res, err := RunAblationCase(context.Background(), mockRunner, baseCase, "rules/target.md", ws)
		if err != nil {
			t.Fatal(err)
		}
		if res.Recommendation != "insufficient_evidence" {
			t.Fatalf("expected synthetic mock runner to yield 'insufficient_evidence', got %q", res.Recommendation)
		}
	})
}

// Acceptance Criterion 4: Identical outcomes with real tokens cannot become false claims.
func TestAblationIdenticalOutcomes(t *testing.T) {
	ws := t.TempDir()
	rulesDir := filepath.Join(ws, "rules")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "target.md"), []byte("target rules"), 0644); err != nil {
		t.Fatal(err)
	}

	caseDir := filepath.Join(ws, "case")
	if err := os.MkdirAll(caseDir, 0755); err != nil {
		t.Fatal(err)
	}

	baseCase := &Case{
		ID:   "identical-outcomes",
		Path: caseDir,
		Prompt: PromptSpec{
			Body: "Run task",
		},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "pass", GraderType: "regex", GraderWeight: 1.0, GraderArm: "both"},
				Pattern:    "success",
				Target:     "output",
			},
		},
	}

	t.Run("delta == 0 and tokenDelta > 0 on successful runs yields prune", func(t *testing.T) {
		spy := &spyRunner{
			name: "claude", // real harness name
			customRun: func(ctx context.Context, req RunRequest) (*RunResult, error) {
				pTokens := 100
				if req.Arm == "with" {
					pTokens = 150 // target adds 50 tokens
				}
				return &RunResult{
					CaseID:   req.Case.ID,
					Harness:  "claude",
					Arm:      req.Arm,
					ExitCode: 0,
					Output:   "success",
					Tokens: TokenUsage{
						PromptTokens:     pTokens,
						CompletionTokens: 20,
						TotalTokens:      pTokens + 20,
						Measured:         true,
					},
				}, nil
			},
		}

		res, err := RunAblationCase(context.Background(), spy, baseCase, "rules/target.md", ws)
		if err != nil {
			t.Fatal(err)
		}
		if res.Delta != 0 {
			t.Fatalf("expected delta 0, got %f", res.Delta)
		}
		if res.TokenDelta <= 0 {
			t.Fatalf("expected positive token delta, got %d", res.TokenDelta)
		}
		if res.Recommendation != "prune" {
			t.Fatalf("expected recommendation 'prune', got %q", res.Recommendation)
		}
		if !strings.Contains(res.Reason, "accuracy gain") {
			t.Fatalf("expected reason to mention 0%% accuracy gain, got %q", res.Reason)
		}
	})

	t.Run("delta == 0 and tokenDelta == 0 yields insufficient_evidence", func(t *testing.T) {
		spy := &spyRunner{
			name: "claude",
			customRun: func(ctx context.Context, req RunRequest) (*RunResult, error) {
				return &RunResult{
					CaseID:   req.Case.ID,
					Harness:  "claude",
					Arm:      req.Arm,
					ExitCode: 0,
					Output:   "success",
					Tokens: TokenUsage{
						PromptTokens:     100,
						CompletionTokens: 20,
						TotalTokens:      120,
						Measured:         true,
					},
				}, nil
			},
		}

		res, err := RunAblationCase(context.Background(), spy, baseCase, "rules/target.md", ws)
		if err != nil {
			t.Fatal(err)
		}
		if res.Recommendation != "insufficient_evidence" {
			t.Fatalf("expected recommendation 'insufficient_evidence', got %q", res.Recommendation)
		}
	})
}

// Acceptance Criterion 3: Reports expose evidence and distinguish measured tokens.
func TestAblationHTMLReportAndJSONReportEvidence(t *testing.T) {
	tmpDir := t.TempDir()
	htmlPath := filepath.Join(tmpDir, "report.html")
	jsonPath := filepath.Join(tmpDir, "report.json")

	res := &SuiteResult{
		Timestamp:   time.Now(),
		Harness:     "claude",
		TotalCases:  1,
		PassedCases: 1,
		TotalScore:  1.0,
		MaxScore:    1.0,
		Ablations: []AblationResult{
			{
				CaseID:         "ablate-1",
				Target:         "rules/security.md",
				TargetHash:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				ControlHashes:  map[string]string{"rules/ctrl.md": "cafebabe1234567890"},
				ScoreWith:      1.0,
				ScoreWithout:   0.5,
				Delta:          0.5,
				TokensWith:     250,
				TokensWithout:  150,
				TokenDelta:     100,
				TokensMeasured: true,
				EfficacyPer1k:  5.0,
				Recommendation: "keep",
				Reason:         "target improves accuracy",
			},
		},
	}

	if err := GenerateHTMLReport(res, htmlPath); err != nil {
		t.Fatalf("GenerateHTMLReport failed: %v", err)
	}

	content, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	htmlStr := string(content)

	// Verify TargetHash preview is rendered
	if !strings.Contains(htmlStr, "e3b0c44298fc") {
		t.Fatalf("expected HTML report to contain target hash prefix, got:\n%s", htmlStr)
	}
	// Verify control count is rendered
	if !strings.Contains(htmlStr, "Controls: 1 verified") {
		t.Fatalf("expected HTML report to contain controls verified count, got:\n%s", htmlStr)
	}

	if err := WriteJSONReport(res, jsonPath); err != nil {
		t.Fatalf("WriteJSONReport failed: %v", err)
	}

	jsonData, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}

	var parsed SuiteResult
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("parse JSON report: %v", err)
	}

	if len(parsed.Ablations) != 1 {
		t.Fatalf("expected 1 ablation in parsed JSON, got %d", len(parsed.Ablations))
	}
	if parsed.Ablations[0].TargetHash != res.Ablations[0].TargetHash {
		t.Fatalf("target hash mismatch in JSON: %s vs %s", parsed.Ablations[0].TargetHash, res.Ablations[0].TargetHash)
	}
	if parsed.Ablations[0].ControlHashes["rules/ctrl.md"] != "cafebabe1234567890" {
		t.Fatalf("control hashes mismatch in JSON")
	}
}
