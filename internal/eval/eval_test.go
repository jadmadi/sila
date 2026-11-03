package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestParseFrontmatterDoc(t *testing.T) {
	doc := `---
name: my-case
max_turns: 8
tags: ["safety", "migration"]
allowed_tools: ["bash", "read"]
model: claude-3-7-sonnet
strict: true
weight: 2.5
---
# Prompt Header
Here is the test prompt.
`
	meta, body := ParseFrontmatterDoc(doc)
	if meta["name"] != "my-case" {
		t.Errorf("expected name 'my-case', got %v", meta["name"])
	}
	if meta["max_turns"] != 8 {
		t.Errorf("expected max_turns 8, got %v", meta["max_turns"])
	}
	if meta["strict"] != true {
		t.Errorf("expected strict true, got %v", meta["strict"])
	}
	if meta["weight"] != 2.5 {
		t.Errorf("expected weight 2.5, got %v", meta["weight"])
	}
	tags, ok := meta["tags"].([]string)
	if !ok || len(tags) != 2 || tags[0] != "safety" || tags[1] != "migration" {
		t.Errorf("unexpected tags: %v", meta["tags"])
	}
	if !strings.Contains(body, "Here is the test prompt.") {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestGraders(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 1. ToolUsedGrader: Success
	tuGrader := &ToolUsedGrader{
		BaseGrader: BaseGrader{GraderName: "used-skill", GraderType: "tool_used", GraderWeight: 1.0},
		Tool:       "Skill",
		InputMatch: `"skill":\s*"db-migrate"`,
		Min:        1,
		Max:        -1,
	}

	resWithTool := &RunResult{
		ToolCalls: []ToolCallRecord{
			{Tool: "ReadFile", Input: `{"path": "schema.sql"}`},
			{Tool: "Skill", Input: `{"skill": "db-migrate", "dry_run": true}`},
		},
	}
	gr, err := tuGrader.Grade(ctx, resWithTool, tmpDir)
	if err != nil || !gr.Passed || gr.Score != 1.0 {
		t.Fatalf("expected ToolUsedGrader pass, got: %+v (err: %v)", gr, err)
	}

	// 2. ToolUsedGrader: Forbidden tool (min: 0, max: 0)
	forbiddenGrader := &ToolUsedGrader{
		BaseGrader: BaseGrader{GraderName: "no-destructive-rm", GraderType: "tool_used", GraderWeight: 2.0},
		Tool:       "Bash",
		InputMatch: `rm -rf`,
		Min:        0,
		Max:        0,
	}
	grForbidden, _ := forbiddenGrader.Grade(ctx, resWithTool, tmpDir)
	if !grForbidden.Passed || grForbidden.Score != 2.0 {
		t.Fatalf("expected forbidden check to pass when tool not called, got: %+v", grForbidden)
	}

	// 3. ToolOrderGrader
	orderGrader := &ToolOrderGrader{
		BaseGrader: BaseGrader{GraderName: "read-before-skill", GraderType: "tool_order", GraderWeight: 1.0},
		Before:     "ReadFile",
		After:      "Skill",
	}
	grOrder, err := orderGrader.Grade(ctx, resWithTool, tmpDir)
	if err != nil || !grOrder.Passed {
		t.Fatalf("expected order check pass, got: %+v", grOrder)
	}

	// 4. RegexGrader (target: message)
	regexGrader := &RegexGrader{
		BaseGrader: BaseGrader{GraderName: "has-token", GraderType: "regex", GraderWeight: 1.5},
		Pattern:    `CONCURRENTLY`,
		MatchType:  "contains",
		Target:     "message",
	}
	resRegex := &RunResult{Output: "CREATE INDEX CONCURRENTLY idx_users_email ON users(email);"}
	grRegex, _ := regexGrader.Grade(ctx, resRegex, tmpDir)
	if !grRegex.Passed || grRegex.Score != 1.5 {
		t.Fatalf("expected regex pass, got: %+v", grRegex)
	}

	// 5. FileExistsGrader
	testFile := filepath.Join(tmpDir, "migration.sql")
	_ = os.WriteFile(testFile, []byte("-- Migration v2\nCREATE TABLE test (id INT);"), 0644)
	feGrader := &FileExistsGrader{
		BaseGrader: BaseGrader{GraderName: "created-sql", GraderType: "file_exists", GraderWeight: 1.0},
		Path:       "migration.sql",
		MinBytes:   5,
		Pattern:    `CREATE TABLE test`,
	}
	grFE, _ := feGrader.Grade(ctx, resWithTool, tmpDir)
	if !grFE.Passed {
		t.Fatalf("expected file exists pass, got: %+v", grFE)
	}

	// 6. SilaContractGrader
	contractGrader := &SilaContractGrader{
		BaseGrader: BaseGrader{GraderName: "exit-zero", GraderType: "sila_contract", GraderWeight: 1.0},
		Command:    "echo 'contract check passed'",
	}
	grContract, _ := contractGrader.Grade(ctx, resWithTool, tmpDir)
	if !grContract.Passed {
		t.Fatalf("expected contract pass, got: %+v", grContract)
	}
}

func TestExecuteCaseAndSuite(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Scaffold test case
	caseDir, err := InitCaseTemplate(tmpDir, "db-safety", false)
	if err != nil {
		t.Fatalf("init case template: %v", err)
	}

	// Update sila_contract.md to use a portable echo command in the test
	contractFile := filepath.Join(caseDir, "graders", "sila_contract.md")
	_ = os.WriteFile(contractFile, []byte(`---
type: sila_contract
action: command
command: "echo ok"
weight: 2.0
---
`), 0644)

	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("load case: %v", err)
	}

	mockRunner := &MockRunner{
		DefaultOutput: "All operations completed with expected_token verified.",
	}

	res, err := ExecuteCase(ctx, mockRunner, c, "", "single", false)
	if err != nil {
		t.Fatalf("execute case: %v", err)
	}

	if !res.Passed {
		t.Errorf("expected case pass, got: %+v", res)
	}

	// Suite test
	suiteRes, err := ExecuteSuite(ctx, mockRunner, []*Case{c}, "", "", "")
	if err != nil {
		t.Fatalf("execute suite: %v", err)
	}
	if suiteRes.TotalCases != 1 || suiteRes.PassedCases != 1 {
		t.Errorf("unexpected suite result: %+v", suiteRes)
	}
}

func TestAblationAndRecommendations(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	caseDir, err := InitCaseTemplate(tmpDir, "sql-indexing", false)
	if err != nil {
		t.Fatalf("init case: %v", err)
	}

	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("load case: %v", err)
	}

	rulePath := filepath.Join(tmpDir, "rules", "db.md")
	_ = os.MkdirAll(filepath.Dir(rulePath), 0755)
	_ = os.WriteFile(rulePath, []byte("Rule content for db indexing"), 0644)
	c.Prompt.Rules = append(c.Prompt.Rules, rulePath)

	mockRunner := &MockRunner{
		DefaultOutput: "Here is expected_token applied.",
	}

	abRes, err := RunAblationCase(ctx, mockRunner, c, "rules/db.md", tmpDir)
	if err != nil {
		t.Fatalf("run ablation: %v", err)
	}

	if abRes.CaseID != c.ID {
		t.Errorf("expected case id %s, got %s", c.ID, abRes.CaseID)
	}

	// Test HTML and JSON report generation
	htmlPath := filepath.Join(tmpDir, "report.html")
	jsonPath := filepath.Join(tmpDir, "report.json")

	suiteRes := &SuiteResult{
		Timestamp:   time.Now(),
		Harness:     "mock",
		TotalCases:  1,
		PassedCases: 1,
		TotalScore:  3.0,
		MaxScore:    3.0,
		Cases:       []CaseResult{*abRes.ResultWith},
		Ablations:   []AblationResult{*abRes},
	}

	if err := GenerateHTMLReport(suiteRes, htmlPath); err != nil {
		t.Fatalf("generate html report: %v", err)
	}
	if err := WriteJSONReport(suiteRes, jsonPath); err != nil {
		t.Fatalf("write json report: %v", err)
	}

	data, err := os.ReadFile(htmlPath)
	if err != nil || !strings.Contains(string(data), "Sila Epistemic Evaluation Report") {
		t.Errorf("invalid html report content")
	}
}

func TestSynthesizeFromLessonAndFeedback(t *testing.T) {
	tmpDir := t.TempDir()

	lessonFact := store.Fact{
		Claim: "Lesson: Missing WAL mode in SQLite | cause: default rollback journal locks writers | fix: execute PRAGMA journal_mode=WAL | tags: sqlite,perf",
	}

	caseDir, err := SynthesizeFromLesson(lessonFact, tmpDir)
	if err != nil {
		t.Fatalf("synthesize from lesson: %v", err)
	}

	if _, err := os.Stat(filepath.Join(caseDir, "prompt.md")); err != nil {
		t.Errorf("prompt.md missing in synthesized case")
	}
	if _, err := os.Stat(filepath.Join(caseDir, "graders", "fix_applied.md")); err != nil {
		t.Errorf("fix_applied.md missing in synthesized case")
	}
	if _, err := os.Stat(filepath.Join(caseDir, "graders", "avoid_cause.md")); err != nil {
		t.Errorf("avoid_cause.md missing in synthesized case")
	}

	fb := store.Feedback{
		Category: "bug",
		Content:  "Memory leak when streaming large responses without flush",
	}
	fbCaseDir, err := SynthesizeFromFeedback(fb, tmpDir)
	if err != nil {
		t.Fatalf("synthesize from feedback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fbCaseDir, "prompt.md")); err != nil {
		t.Errorf("prompt.md missing in feedback case")
	}
}

type failRunner struct {
	exitCode int
	errMsg   string
}

func (f *failRunner) Name() string      { return "fixture-runner" }
func (f *failRunner) IsAvailable() bool { return true }
func (f *failRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	return &RunResult{
		CaseID:   req.Case.ID,
		Harness:  f.Name(),
		Arm:      req.Arm,
		ExitCode: f.exitCode,
		Error:    f.errMsg,
	}, nil
}

func TestFailureAccounting(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 1. Nonzero exit code with zero graders: must NOT pass, PassRate must be 0
	caseZeroGraders := &Case{
		ID:     "zero-graders-fail",
		Prompt: PromptSpec{Body: "do work"},
	}
	fr := &failRunner{exitCode: 7, errMsg: "fixture harness failed"}
	res1, err := ExecuteCase(ctx, fr, caseZeroGraders, tmpDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error: %v", err)
	}
	if res1.Passed {
		t.Errorf("expected res1.Passed = false for exit code 7, got true")
	}
	if res1.PassRate != 0 {
		t.Errorf("expected res1.PassRate = 0, got %f", res1.PassRate)
	}

	// 2. Failed scaffold script: must fail and record scaffold error
	caseBadScaffold := &Case{
		ID: "bad-scaffold",
		Prompt: PromptSpec{
			Body:           "do work",
			ScaffoldScript: "exit 42",
		},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "pass", GraderType: "regex", GraderWeight: 1.0},
				Pattern:    ".*",
			},
		},
	}
	mockRunner := &MockRunner{DefaultOutput: "ok"}
	res2, err := ExecuteCase(ctx, mockRunner, caseBadScaffold, tmpDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error: %v", err)
	}
	if res2.Passed {
		t.Errorf("expected res2.Passed = false for failed scaffold")
	}
	if !strings.Contains(res2.Error, "scaffold script failed") {
		t.Errorf("expected scaffold failure in res2.Error, got: %q", res2.Error)
	}

	// 3. Invalid grader on disk: must NOT be silently dropped; must fail case
	invalidCaseDir := filepath.Join(tmpDir, "invalid-case")
	_ = os.MkdirAll(filepath.Join(invalidCaseDir, "graders"), 0755)
	_ = os.WriteFile(filepath.Join(invalidCaseDir, "prompt.md"), []byte("---\nname: invalid-case\n---\nPrompt"), 0644)
	_ = os.WriteFile(filepath.Join(invalidCaseDir, "graders", "broken.md"), []byte("---\ntype: unknown_grader_type\n---\nRubric"), 0644)

	cInvalid, err := LoadCase(invalidCaseDir)
	if err != nil {
		t.Fatalf("LoadCase failed: %v", err)
	}
	if len(cInvalid.Graders) != 1 {
		t.Fatalf("expected 1 grader (InvalidGrader), got %d", len(cInvalid.Graders))
	}
	res3, err := ExecuteCase(ctx, mockRunner, cInvalid, tmpDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error: %v", err)
	}
	if res3.Passed {
		t.Errorf("expected case with invalid grader to fail, got passed=true")
	}

	// 4. Zero applicable scoring criteria: must fail
	caseNoCriteria := &Case{
		ID:     "no-criteria",
		Prompt: PromptSpec{Body: "nothing to score"},
	}
	res4, err := ExecuteCase(ctx, mockRunner, caseNoCriteria, tmpDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error: %v", err)
	}
	if res4.Passed {
		t.Errorf("expected zero scoring criteria case to fail, got passed=true")
	}
	if res4.Error != "zero applicable scoring criteria" {
		t.Errorf("expected error 'zero applicable scoring criteria', got: %q", res4.Error)
	}

	// 5. Zero selected cases in suite: must fail with zero cases selected error
	suiteZero, err := ExecuteSuite(ctx, mockRunner, []*Case{cInvalid}, tmpDir, "nonexistent-case", "")
	if err != nil {
		t.Fatalf("unexpected ExecuteSuite error: %v", err)
	}
	if suiteZero.Passed {
		t.Errorf("expected suite with zero selected cases to have Passed=false")
	}
	if suiteZero.TotalCases != 0 {
		t.Errorf("expected TotalCases=0, got %d", suiteZero.TotalCases)
	}
	if suiteZero.Error != "zero evaluation cases selected" {
		t.Errorf("expected 'zero evaluation cases selected', got %q", suiteZero.Error)
	}

	// 6. Denominator includes attempted failed cases
	validCase := &Case{
		ID:     "valid-case",
		Prompt: PromptSpec{Body: "good prompt"},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "pass", GraderType: "regex", GraderWeight: 1.0},
				Pattern:    "ok",
			},
		},
	}
	suiteTwo, err := ExecuteSuite(ctx, mockRunner, []*Case{validCase, caseNoCriteria}, tmpDir, "", "")
	if err != nil {
		t.Fatalf("unexpected ExecuteSuite error: %v", err)
	}
	if suiteTwo.TotalCases != 2 {
		t.Errorf("expected TotalCases=2, got %d", suiteTwo.TotalCases)
	}
	if suiteTwo.PassedCases != 1 {
		t.Errorf("expected PassedCases=1, got %d", suiteTwo.PassedCases)
	}
	if suiteTwo.Passed {
		t.Errorf("expected suite with 1 failure to have Passed=false")
	}

	// 7. Explicit mock use labeled synthetic
	if !suiteTwo.Synthetic {
		t.Errorf("expected mock suite to have Synthetic=true")
	}
	if !res2.Synthetic {
		t.Errorf("expected mock case to have Synthetic=true")
	}

	// 8. AutoDetect fails if no real harness is available
	emptyReg := &Registry{runners: make(map[string]HarnessRunner)}
	_, errAuto := emptyReg.AutoDetect()
	if errAuto == nil {
		t.Errorf("expected AutoDetect to fail when no real runner is available")
	}
}

type mockOutputRunner struct {
	name     string
	exitCode int
	output   string
	errMsg   string
}

func (m *mockOutputRunner) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock-output-runner"
}
func (m *mockOutputRunner) IsAvailable() bool { return true }
func (m *mockOutputRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	return &RunResult{
		CaseID:   req.Case.ID,
		Harness:  m.Name(),
		Arm:      req.Arm,
		Output:   m.output,
		ExitCode: m.exitCode,
		Error:    m.errMsg,
	}, nil
}

type multiCaseRunner struct {
	name    string
	results map[string]*RunResult
}

func (m *multiCaseRunner) Name() string {
	if m.name != "" {
		return m.name
	}
	return "multi-case-runner"
}
func (m *multiCaseRunner) IsAvailable() bool { return true }
func (m *multiCaseRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if res, ok := m.results[req.Case.ID]; ok {
		cp := *res
		cp.Arm = req.Arm
		return &cp, nil
	}
	return &RunResult{CaseID: req.Case.ID, Harness: m.Name(), Arm: req.Arm, ExitCode: 0}, nil
}

func TestFailureAccounting_Exit7WithMatchingOutput(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 1. Single case: Harness exits 7, but output matches grader criteria
	c := &Case{
		ID:     "exit-7-matching-output",
		Prompt: PromptSpec{Body: "produce the magic token"},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "magic-token", GraderType: "regex", GraderWeight: 2.0},
				Pattern:    "MAGIC_TOKEN_CONFIRMED",
			},
		},
	}

	fr := &mockOutputRunner{
		exitCode: 7,
		output:   "Processing step 1...\nMAGIC_TOKEN_CONFIRMED\nFatal error: terminated with exit 7",
		errMsg:   "exit status 7",
	}

	res, err := ExecuteCase(ctx, fr, c, tmpDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error: %v", err)
	}

	// Must NOT earn quality credit
	if res.Passed {
		t.Errorf("expected res.Passed = false, got true")
	}
	if res.TotalScore != 0.0 {
		t.Errorf("expected res.TotalScore = 0.0, got %f", res.TotalScore)
	}
	if res.MaxScore != 2.0 {
		t.Errorf("expected res.MaxScore = 2.0 (retained in denominator), got %f", res.MaxScore)
	}
	if res.PassRate != 0.0 {
		t.Errorf("expected res.PassRate = 0.0, got %f", res.PassRate)
	}
	if !strings.Contains(res.Error, "7") {
		t.Errorf("expected res.Error to mention exit code 7, got %q", res.Error)
	}
	// Verify diagnostic preservation: grader output check was recorded
	if len(res.Grades) != 1 {
		t.Fatalf("expected 1 grader result recorded in diagnostics, got %d", len(res.Grades))
	}
	if !res.Grades[0].Passed {
		t.Errorf("expected grader diagnostic to record match, got Passed=false")
	}

	// 2. Mixed suite: 1 passing case (weight 2.0), 1 exit-7 failing case (weight 3.0)
	cPassing := &Case{
		ID:     "passing-case",
		Prompt: PromptSpec{Body: "produce good output"},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "pass-token", GraderType: "regex", GraderWeight: 2.0},
				Pattern:    "ALL_GOOD",
			},
		},
	}
	cFailing := &Case{
		ID:     "failing-exit7-case",
		Prompt: PromptSpec{Body: "crash with token"},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "fail-token", GraderType: "regex", GraderWeight: 3.0},
				Pattern:    "FAIL_TOKEN",
			},
		},
	}

	mixedRunner := &multiCaseRunner{
		results: map[string]*RunResult{
			"passing-case": {
				CaseID:   "passing-case",
				Output:   "ALL_GOOD",
				ExitCode: 0,
			},
			"failing-exit7-case": {
				CaseID:   "failing-exit7-case",
				Output:   "FAIL_TOKEN present but harness crash",
				ExitCode: 7,
				Error:    "exit status 7",
			},
		},
	}

	suiteRes, err := ExecuteSuite(ctx, mixedRunner, []*Case{cPassing, cFailing}, tmpDir, "", "")
	if err != nil {
		t.Fatalf("unexpected ExecuteSuite error: %v", err)
	}

	if suiteRes.Passed {
		t.Errorf("expected mixed suite Passed = false, got true")
	}
	if suiteRes.TotalCases != 2 {
		t.Errorf("expected TotalCases = 2, got %d", suiteRes.TotalCases)
	}
	if suiteRes.PassedCases != 1 {
		t.Errorf("expected PassedCases = 1, got %d", suiteRes.PassedCases)
	}
	// TotalScore must reflect ONLY the passing case (2.0)
	if suiteRes.TotalScore != 2.0 {
		t.Errorf("expected suite TotalScore = 2.0, got %f", suiteRes.TotalScore)
	}
	// MaxScore must reflect BOTH cases (2.0 + 3.0 = 5.0)
	if suiteRes.MaxScore != 5.0 {
		t.Errorf("expected suite MaxScore = 5.0, got %f", suiteRes.MaxScore)
	}

	// 3. Ablation on exit-7 case cannot recommend "keep"
	ruleFile := filepath.Join(tmpDir, "rule.md")
	_ = os.WriteFile(ruleFile, []byte("Rule content"), 0644)
	cFailing.Prompt.Rules = []string{ruleFile}

	abRes, err := RunAblationCase(ctx, mixedRunner, cFailing, "rule.md", tmpDir)
	if err != nil {
		t.Fatalf("unexpected RunAblationCase error: %v", err)
	}
	if abRes.Recommendation == "keep" {
		t.Errorf("expected ablation recommendation != 'keep' for failed execution, got %q (%s)", abRes.Recommendation, abRes.Reason)
	}
	if abRes.Recommendation != "insufficient_evidence" {
		t.Errorf("expected 'insufficient_evidence', got %q", abRes.Recommendation)
	}
	if abRes.ScoreWith != 0.0 {
		t.Errorf("expected ScoreWith = 0.0, got %f", abRes.ScoreWith)
	}

	// 4. Timeout run
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel() // pre-canceled
	resTimeout, err := ExecuteCase(canceledCtx, fr, c, tmpDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error on canceled context: %v", err)
	}
	if resTimeout.Passed {
		t.Errorf("expected resTimeout.Passed = false")
	}
	if resTimeout.TotalScore != 0.0 {
		t.Errorf("expected resTimeout.TotalScore = 0.0, got %f", resTimeout.TotalScore)
	}
	if resTimeout.MaxScore != 2.0 {
		t.Errorf("expected resTimeout.MaxScore = 2.0, got %f", resTimeout.MaxScore)
	}
	if resTimeout.PassRate != 0.0 {
		t.Errorf("expected resTimeout.PassRate = 0.0, got %f", resTimeout.PassRate)
	}
	if !strings.Contains(resTimeout.Error, "canceled") && !strings.Contains(resTimeout.Error, "timed out") {
		t.Errorf("expected error mentioning canceled/timed out, got %q", resTimeout.Error)
	}

	// 5. Case with 1 valid grader and 1 invalid grader: must earn 0 credit
	cMixedGraders := &Case{
		ID:     "mixed-graders-case",
		Prompt: PromptSpec{Body: "mixed grader check"},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "valid-g", GraderType: "regex", GraderWeight: 1.5},
				Pattern:    "MATCH",
			},
			&InvalidGrader{
				BaseGrader: BaseGrader{GraderName: "broken-g", GraderType: "invalid", GraderWeight: 2.0},
				Err:        fmt.Errorf("syntax error in rubric"),
			},
		},
	}
	goodRunner := &mockOutputRunner{exitCode: 0, output: "MATCH confirmed"}
	resMixedG, err := ExecuteCase(ctx, goodRunner, cMixedGraders, tmpDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error: %v", err)
	}
	if resMixedG.Passed {
		t.Errorf("expected resMixedG.Passed = false")
	}
	if resMixedG.TotalScore != 0.0 {
		t.Errorf("expected resMixedG.TotalScore = 0.0, got %f", resMixedG.TotalScore)
	}
	if resMixedG.MaxScore != 3.5 {
		t.Errorf("expected resMixedG.MaxScore = 3.5, got %f", resMixedG.MaxScore)
	}
	if resMixedG.PassRate != 0.0 {
		t.Errorf("expected resMixedG.PassRate = 0.0, got %f", resMixedG.PassRate)
	}
	if !strings.Contains(resMixedG.Error, "invalid") {
		t.Errorf("expected error mentioning invalid grader, got %q", resMixedG.Error)
	}
}

func TestWorkspaceIsolationAndBoundedExecution(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()

	sentinelFile := filepath.Join(workDir, "sentinel.txt")
	if err := os.WriteFile(sentinelFile, []byte("ORIGINAL_CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}

	mockRunner := &MockRunner{DefaultOutput: "ok"}

	// 1. Workspace isolation: scaffold writes to sentinel.txt and creates leaked.txt
	caseWithScaffold := &Case{
		ID: "isolate-test",
		Prompt: PromptSpec{
			Body:           "write something",
			ScaffoldScript: "echo MUTATED > sentinel.txt; echo LEAK > leaked.txt",
		},
		Graders: []Grader{
			&SilaContractGrader{
				BaseGrader: BaseGrader{GraderName: "check-mutated", GraderType: "contract", GraderWeight: 1.0},
				Command:    "grep -q MUTATED sentinel.txt",
			},
		},
	}

	res, err := ExecuteCase(ctx, mockRunner, caseWithScaffold, workDir, "with", false)
	if err != nil {
		t.Fatalf("unexpected ExecuteCase error: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected case to pass inside isolated dir, got error: %s", res.Error)
	}

	// Verify caller repository sentinel is preserved
	sentinelData, err := os.ReadFile(sentinelFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(sentinelData) != "ORIGINAL_CONTENT" {
		t.Fatalf("expected caller sentinel to remain ORIGINAL_CONTENT, got %q", string(sentinelData))
	}

	// Verify leaked.txt was not created in workDir
	if _, err := os.Stat(filepath.Join(workDir, "leaked.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected leaked.txt to not exist in workDir")
	}

	// 2. Ablation arm isolation: "with" and "without" arms don't share files
	// In "with", scaffold writes arm_with.txt. In "without", check that arm_with.txt does not exist.
	caseAblation := &Case{
		ID: "ablation-iso",
		Prompt: PromptSpec{
			Body:           "ablation prompt",
			ScaffoldScript: `if [ "$SILA_EVAL_ARM" = "with" ]; then echo "arm_with" > arm_with.txt; fi`,
		},
		Graders: []Grader{
			&SilaContractGrader{
				BaseGrader: BaseGrader{GraderName: "check-arm", GraderType: "contract", GraderWeight: 1.0},
				Command:    `if [ "$SILA_EVAL_ARM" = "without" ]; then test ! -f arm_with.txt; else test -f arm_with.txt; fi`,
			},
		},
	}

	resWith, err := ExecuteCase(ctx, mockRunner, caseAblation, workDir, "with", false)
	if err != nil || !resWith.Passed {
		t.Fatalf("expected arm 'with' to pass, got err=%v res=%+v", err, resWith)
	}
	resWithout, err := ExecuteCase(ctx, mockRunner, caseAblation, workDir, "without", false)
	if err != nil || !resWithout.Passed {
		t.Fatalf("expected arm 'without' to pass, got err=%v res=%+v", err, resWithout)
	}

	// 3. Timeout / cancellation stops child execution
	cancelCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	caseSlow := &Case{
		ID: "slow-case",
		Prompt: PromptSpec{
			Body:           "slow prompt",
			ScaffoldScript: "sleep 5",
		},
		Graders: []Grader{
			&SilaContractGrader{
				BaseGrader: BaseGrader{GraderName: "cmd", GraderType: "contract", GraderWeight: 1.0},
				Command:    "echo ok",
			},
		},
	}
	resSlow, err := ExecuteCase(cancelCtx, mockRunner, caseSlow, workDir, "single", false)
	if err != nil {
		t.Fatalf("ExecuteCase should return result on timeout, got error: %v", err)
	}
	if resSlow.Passed {
		t.Fatalf("expected timed-out case to not pass")
	}
	if !strings.Contains(resSlow.Error, "timed out") && !strings.Contains(resSlow.Error, "signal") && !strings.Contains(resSlow.Error, "killed") && !strings.Contains(resSlow.Error, "canceled") {
		t.Fatalf("expected error mentioning timeout/cancel/signal, got: %s", resSlow.Error)
	}

	// 4. Invalid control fields: max_turns < 0
	caseInvalidTurns := &Case{
		ID: "invalid-turns",
		Prompt: PromptSpec{
			Body:     "bad turns",
			MaxTurns: -1,
		},
	}
	_, errInvalid := ExecuteCase(ctx, mockRunner, caseInvalidTurns, workDir, "single", false)
	if errInvalid == nil || !strings.Contains(errInvalid.Error(), "max_turns") {
		t.Fatalf("expected max_turns error, got: %v", errInvalid)
	}

	// 5. Unsupported harness fails explicitly
	caseUnsupported := &Case{
		ID: "unsupported-harness",
		Prompt: PromptSpec{
			Body:      "prompt",
			Harnesses: []string{"aider", "claude"},
		},
	}
	resUnsupp, err := ExecuteCase(ctx, mockRunner, caseUnsupported, workDir, "single", false)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resUnsupp.Passed || !strings.Contains(resUnsupp.Error, "unsupported harness") {
		t.Fatalf("expected unsupported harness failure, got %+v", resUnsupp)
	}
}

func TestJudgeProvenanceAndKeywordClauseGrading(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()

	// 1. KeywordClauseGrader with prose rubric (no numbered or bulleted items) fails visibly
	proseGrader := &KeywordClauseGrader{
		BaseGrader: BaseGrader{GraderName: "prose-check", GraderType: "keyword_clause", GraderWeight: 1.0},
		Rubric:     "The assistant should explain SQLite locking semantics in clean detail.",
	}
	resProse, err := proseGrader.Grade(ctx, &RunResult{Output: "Here is an explanation of locking"}, workDir)
	if err != nil {
		t.Fatalf("unexpected grade error: %v", err)
	}
	if resProse.Passed || resProse.Score != 0 {
		t.Fatalf("expected prose rubric without bulleted clauses to fail, got passed=%v score=%v", resProse.Passed, resProse.Score)
	}
	if !strings.Contains(resProse.Detail, "unparseable rubric") {
		t.Fatalf("expected detail to mention unparseable rubric, got: %s", resProse.Detail)
	}

	// 2. KeywordClauseGrader with positive and negation clauses
	clauseGrader := &KeywordClauseGrader{
		BaseGrader: BaseGrader{GraderName: "clause-check", GraderType: "keyword_clause", GraderWeight: 2.0},
		Rubric: `
- Mentions WAL journal mode
- Avoids suggesting CGO drivers
`,
	}
	// Case A: passes both
	resGood, _ := clauseGrader.Grade(ctx, &RunResult{Output: "We enable WAL mode and use modernc pure-Go SQLite"}, workDir)
	if !resGood.Passed || resGood.Score != 2.0 {
		t.Fatalf("expected clause check to pass, got %+v", resGood)
	}
	// Case B: fails negation (suggests CGO)
	resBadNeg, _ := clauseGrader.Grade(ctx, &RunResult{Output: "We enable WAL mode by using cgo drivers"}, workDir)
	if resBadNeg.Passed || resBadNeg.Score != 0 {
		t.Fatalf("expected negation failure when forbidden term is present, got %+v", resBadNeg)
	}

	// 3. LLMJudgeGrader with empty rubric fails
	emptyJudge := &LLMJudgeGrader{
		BaseGrader: BaseGrader{GraderName: "empty-judge", GraderType: "llm_judge", GraderWeight: 1.0},
		Rubric:     "   ",
	}
	resEmpty, _ := emptyJudge.Grade(ctx, &RunResult{Output: "anything"}, workDir)
	if resEmpty.Passed || resEmpty.Score != 0 || !strings.Contains(resEmpty.Detail, "empty or unparseable rubric") {
		t.Fatalf("expected empty rubric to fail with score 0, got: %+v", resEmpty)
	}

	// 4. LLMJudgeGrader invalid threshold configuration
	badConfigJudge := &LLMJudgeGrader{
		BaseGrader:    BaseGrader{GraderName: "bad-cfg", GraderType: "llm_judge", GraderWeight: 1.0},
		Rubric:        "- Check quality",
		Samples:       2,
		PassThreshold: 3,
	}
	resBadCfg, _ := badConfigJudge.Grade(ctx, &RunResult{Output: "anything"}, workDir)
	if resBadCfg.Passed || resBadCfg.Score != 0 || !strings.Contains(resBadCfg.Detail, "pass_threshold") {
		t.Fatalf("expected pass_threshold > samples to fail, got: %+v", resBadCfg)
	}

	// 5. LLMJudgeGrader default offline / unconsented: returns unavailable, not simulated pass
	offlineJudge := &LLMJudgeGrader{
		BaseGrader:    BaseGrader{GraderName: "hosted-judge", GraderType: "llm_judge", GraderWeight: 1.0},
		Model:         "claude-3-5-haiku",
		Rubric:        "- Must explain architecture",
		Samples:       1,
		PassThreshold: 1,
	}
	resOffline, _ := offlineJudge.Grade(ctx, &RunResult{Output: "Must explain architecture"}, workDir)
	if resOffline.Passed {
		t.Fatalf("expected unconsented offline judge to not pass")
	}
	if !resOffline.Unavailable {
		t.Fatalf("expected unconsented offline judge to have Unavailable=true")
	}
	if resOffline.Score != 0 {
		t.Fatalf("expected score 0 for unavailable judge, got %v", resOffline.Score)
	}
	if !strings.Contains(resOffline.Detail, "unavailable") || strings.Contains(resOffline.Detail, "passed") {
		t.Fatalf("expected honest unavailable detail, got: %s", resOffline.Detail)
	}

	// 6. LLMJudgeGrader with mock / fake provider executes and reports provenance
	mockJudge := &LLMJudgeGrader{
		BaseGrader:    BaseGrader{GraderName: "mock-judge", GraderType: "llm_judge", GraderWeight: 3.0},
		Model:         "mock-evaluator",
		Provider:      "mock",
		Rubric:        "Explain invariant rules",
		Samples:       2,
		PassThreshold: 2,
	}
	resMock, err := mockJudge.Grade(ctx, &RunResult{Output: "Here is an invariant rule"}, workDir)
	if err != nil {
		t.Fatalf("unexpected mock judge error: %v", err)
	}
	if !resMock.Passed || resMock.Score != 3.0 {
		t.Fatalf("expected mock judge to pass with full weight, got: %+v", resMock)
	}
	if resMock.JudgeProvider != "mock" {
		t.Fatalf("expected JudgeProvider 'mock', got %q", resMock.JudgeProvider)
	}
	if resMock.JudgeModel != "mock-evaluator" {
		t.Fatalf("expected JudgeModel 'mock-evaluator', got %q", resMock.JudgeModel)
	}
	if resMock.Unavailable {
		t.Fatalf("expected mock judge to not be unavailable")
	}
	if !strings.Contains(resMock.Detail, "2/2 samples passed") {
		t.Fatalf("expected detail to mention 2/2 samples passed, got: %s", resMock.Detail)
	}

	// 7. ParseGrader parsing parity
	docJudge := `---
type: llm_judge
model: gpt-4o-mini
provider: mock
samples: 3
pass_threshold: 2
---
- Verify schema migrations
`
	parsedJudge, err := ParseGrader("judge-test", docJudge)
	if err != nil {
		t.Fatalf("failed to parse llm_judge: %v", err)
	}
	llmG, ok := parsedJudge.(*LLMJudgeGrader)
	if !ok {
		t.Fatalf("expected *LLMJudgeGrader, got %T", parsedJudge)
	}
	if llmG.Model != "gpt-4o-mini" || llmG.Provider != "mock" || llmG.Samples != 3 || llmG.PassThreshold != 2 {
		t.Fatalf("unexpected fields on parsed LLMJudgeGrader: %+v", llmG)
	}

	docClause := `---
type: keyword_clause
weight: 1.5
---
- Check zero allocations
`
	parsedClause, err := ParseGrader("clause-test", docClause)
	if err != nil {
		t.Fatalf("failed to parse keyword_clause: %v", err)
	}
	if _, ok := parsedClause.(*KeywordClauseGrader); !ok {
		t.Fatalf("expected *KeywordClauseGrader, got %T", parsedClause)
	}
}

type ablationCaptureRunner struct {
	mu       sync.Mutex
	requests []RunRequest
	passWith bool
	passWout bool
	tokensW  int
	tokensWo int
}

func (c *ablationCaptureRunner) Name() string      { return "capture" }
func (c *ablationCaptureRunner) IsAvailable() bool { return true }
func (c *ablationCaptureRunner) IsSynthetic() bool { return false }

func (c *ablationCaptureRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	c.mu.Unlock()

	out := "pass output"
	if req.Arm == "with" && !c.passWith {
		out = "fail output"
	}
	if req.Arm == "without" && !c.passWout {
		out = "fail output"
	}

	toks := c.tokensW
	if req.Arm == "without" {
		toks = c.tokensWo
	}

	return &RunResult{
		CaseID:    req.Case.ID,
		Harness:   c.Name(),
		Arm:       req.Arm,
		Synthetic: false,
		Output:    out,
		ExitCode:  0,
		Duration:  5 * time.Millisecond,
		Tokens: TokenUsage{
			PromptTokens:     toks,
			CompletionTokens: 10,
			TotalTokens:      toks + 10,
			Measured:         true,
		},
	}, nil
}

func TestTargetedAblationContractAndEvidence(t *testing.T) {
	ctx := context.Background()
	workDir := t.TempDir()

	targetFile := filepath.Join(workDir, "target_rule.md")
	if err := os.WriteFile(targetFile, []byte("TARGET_RULE_CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}
	controlFile := filepath.Join(workDir, "control_rule.md")
	if err := os.WriteFile(controlFile, []byte("CONTROL_RULE_CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}

	c := &Case{
		ID: "ablation-case",
		Prompt: PromptSpec{
			Body:  "Test task prompt",
			Rules: []string{targetFile, controlFile},
		},
		Graders: []Grader{
			&RegexGrader{
				BaseGrader: BaseGrader{GraderName: "check-pass", GraderType: "regex", GraderWeight: 1.0},
				Pattern:    "pass",
			},
			&ToolUsedGrader{
				BaseGrader: BaseGrader{GraderName: "check-tool", GraderType: "tool_used", GraderWeight: 1.0},
				Tool:       "ReadFile",
				Min:        0,
				Max:        10,
			},
		},
	}

	// 1. Missing target fails before execution
	_, errMissing := RunAblationCase(ctx, &MockRunner{}, c, "nonexistent_rule.md", workDir)
	if errMissing == nil || !strings.Contains(errMissing.Error(), "not found or unreadable") {
		t.Fatalf("expected missing target error, got: %v", errMissing)
	}

	// 2. Rule resolution: with arm has T+C, without arm has C
	runner := &ablationCaptureRunner{passWith: true, passWout: false, tokensW: 500, tokensWo: 300}
	ab, err := RunAblationCase(ctx, runner, c, "target_rule.md", workDir)
	if err != nil {
		t.Fatalf("unexpected RunAblationCase error: %v", err)
	}

	if len(runner.requests) != 2 {
		t.Fatalf("expected exactly 2 runner requests, got %d", len(runner.requests))
	}

	reqWith := runner.requests[0]
	reqWithout := runner.requests[1]

	if reqWith.Target != "target_rule.md" || reqWithout.Target != "target_rule.md" {
		t.Errorf("expected Target to be 'target_rule.md' on both requests, got with=%q without=%q", reqWith.Target, reqWithout.Target)
	}
	if reqWith.OmitTarget != false {
		t.Errorf("expected OmitTarget=false on with arm")
	}
	if reqWithout.OmitTarget != true {
		t.Errorf("expected OmitTarget=true on without arm")
	}

	// Check prompt rules resolution
	promptWith, _ := buildPromptWithRules(c.Prompt.Body, c.Prompt.Rules, reqWith.Target, reqWith.OmitTarget)
	if !strings.Contains(promptWith, "TARGET_RULE_CONTENT") || !strings.Contains(promptWith, "CONTROL_RULE_CONTENT") {
		t.Errorf("expected with arm to include both T and C, got: %s", promptWith)
	}

	promptWithout, _ := buildPromptWithRules(c.Prompt.Body, c.Prompt.Rules, reqWithout.Target, reqWithout.OmitTarget)
	if strings.Contains(promptWithout, "TARGET_RULE_CONTENT") {
		t.Errorf("expected without arm to omit target T, got: %s", promptWithout)
	}
	if !strings.Contains(promptWithout, "CONTROL_RULE_CONTENT") {
		t.Errorf("expected without arm to keep control C, got: %s", promptWithout)
	}

	// 3. Symmetrical criteria: max score is identical across both arms (tool_used is not excluded)
	if ab.ResultWith.MaxScore != ab.ResultWithout.MaxScore {
		t.Errorf("expected identical max score across arms, got with=%.1f without=%.1f", ab.ResultWith.MaxScore, ab.ResultWithout.MaxScore)
	}

	// 4. Recommendation with positive gain and reasonable tokens: "keep"
	if ab.Recommendation != "keep" {
		t.Errorf("expected recommendation 'keep', got %q (%s)", ab.Recommendation, ab.Reason)
	}
	if ab.TokensMeasured != true {
		t.Errorf("expected TokensMeasured=true")
	}

	// 5. Synthetic runner abstains from prune/keep decisions
	mockRunner := &MockRunner{DefaultOutput: "pass output"}
	abMock, err := RunAblationCase(ctx, mockRunner, c, "target_rule.md", workDir)
	if err != nil {
		t.Fatalf("unexpected mock ablation error: %v", err)
	}
	if abMock.Recommendation != "insufficient_evidence" {
		t.Errorf("expected synthetic mock run to yield 'insufficient_evidence', got %q", abMock.Recommendation)
	}
	if abMock.EfficacyPer1k != 0.0 {
		t.Errorf("expected 0.0 efficacy for synthetic run, got %v", abMock.EfficacyPer1k)
	}

	// 6. Demote when marginal gain is small and token rent is excessive (>1500)
	runnerDemote := &ablationCaptureRunner{passWith: true, passWout: false, tokensW: 2500, tokensWo: 500}
	var marginalGraders []Grader
	marginalGraders = append(marginalGraders, &RegexGrader{BaseGrader: BaseGrader{GraderName: "g1", GraderType: "regex", GraderWeight: 1.0}, Pattern: "pass"})
	for i := 2; i <= 20; i++ {
		marginalGraders = append(marginalGraders, &RegexGrader{BaseGrader: BaseGrader{GraderName: fmt.Sprintf("g%d", i), GraderType: "regex", GraderWeight: 1.0}, Pattern: "output"})
	}
	cMarginal := &Case{
		ID:      "marginal-case",
		Prompt:  c.Prompt,
		Graders: marginalGraders,
	}
	abDemote, _ := RunAblationCase(ctx, runnerDemote, cMarginal, "target_rule.md", workDir)
	if abDemote.Recommendation != "demote" {
		t.Errorf("expected recommendation 'demote' for high token cost with small delta, got %q", abDemote.Recommendation)
	}
}
