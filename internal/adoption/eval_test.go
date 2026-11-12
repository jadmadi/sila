package adoption

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/mcp"
)

func TestLoadAdoptionCases_ValidAndInvalid(t *testing.T) {
	catalogPath := filepath.Join("..", "..", "evals", "adoption", "cases.json")
	cases, err := LoadAdoptionCases(catalogPath)
	if err != nil {
		t.Fatalf("LoadAdoptionCases(%s) failed: %v", catalogPath, err)
	}
	if len(cases) < 12 {
		t.Fatalf("expected >= 12 adoption cases, got %d", len(cases))
	}

	// Verify schema.json exists and is valid JSON Schema draft 2020-12
	schemaBytes, err := os.ReadFile(filepath.Join("..", "..", "evals", "adoption", "schema.json"))
	if err != nil {
		t.Fatalf("read schema.json: %v", err)
	}
	var schemaObj map[string]any
	if err := json.Unmarshal(schemaBytes, &schemaObj); err != nil {
		t.Fatalf("unmarshal schema.json: %v", err)
	}
	if schemaObj["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("unexpected $schema: %v", schemaObj["$schema"])
	}

	tmpDir := t.TempDir()
	writeTmp := func(name, content string) string {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return p
	}

	invalidCases := []struct {
		name    string
		payload string
		wantErr string
	}{
		{
			name:    "unknown field",
			payload: `{"schema_version":"sila/adoption-catalog/v1","cases":[{"schema":"sila/adoption-case/v1","id":"c1","prompt":"hi","expected_first_tool":"recall","expected_sequence":["recall"],"allowed_tools":["recall"],"forbidden_tools":["grep"],"max_turns":1,"harness":"command-code","profile":"continuity","required_surface":"mcp","timeout_ms":1000,"setup":{"fixture":"f","goal":null},"assertions":[{"type":"tool_called","tool":"recall"}],"unexpected_field":123}]}`,
			wantErr: "unknown field",
		},
		{
			name:    "missing prompt",
			payload: `{"schema_version":"sila/adoption-catalog/v1","cases":[{"schema":"sila/adoption-case/v1","id":"c1","prompt":"   ","expected_first_tool":"recall","expected_sequence":["recall"],"allowed_tools":["recall"],"forbidden_tools":["grep"],"max_turns":1,"harness":"command-code","profile":"continuity","required_surface":"mcp","timeout_ms":1000,"setup":{"fixture":"f","goal":null},"assertions":[{"type":"tool_called","tool":"recall"}]}]}`,
			wantErr: "prompt cannot be empty",
		},
		{
			name:    "forbidden and allowed overlap",
			payload: `{"schema_version":"sila/adoption-catalog/v1","cases":[{"schema":"sila/adoption-case/v1","id":"c1","prompt":"test prompt","expected_first_tool":"recall","expected_sequence":["recall"],"allowed_tools":["recall","grep"],"forbidden_tools":["grep"],"max_turns":1,"harness":"command-code","profile":"continuity","required_surface":"mcp","timeout_ms":1000,"setup":{"fixture":"f","goal":null},"assertions":[{"type":"tool_called","tool":"recall"}]}]}`,
			wantErr: "both allowed_tools and forbidden_tools",
		},
		{
			name:    "duplicate case ID",
			payload: `{"schema_version":"sila/adoption-catalog/v1","cases":[{"schema":"sila/adoption-case/v1","id":"dup","prompt":"p1","expected_first_tool":"recall","expected_sequence":["recall"],"allowed_tools":["recall"],"forbidden_tools":["grep"],"max_turns":1,"harness":"command-code","profile":"continuity","required_surface":"mcp","timeout_ms":1000,"setup":{"fixture":"f","goal":null},"assertions":[{"type":"tool_called","tool":"recall"}]},{"schema":"sila/adoption-case/v1","id":"dup","prompt":"p2","expected_first_tool":"recall","expected_sequence":["recall"],"allowed_tools":["recall"],"forbidden_tools":["grep"],"max_turns":1,"harness":"command-code","profile":"continuity","required_surface":"mcp","timeout_ms":1000,"setup":{"fixture":"f","goal":null},"assertions":[{"type":"tool_called","tool":"recall"}]}]}`,
			wantErr: "duplicate case id",
		},
		{
			name:    "empty case list",
			payload: `{"schema_version":"sila/adoption-catalog/v1","cases":[]}`,
			wantErr: "cases list cannot be empty",
		},
		{
			name:    "unknown assertion type",
			payload: `{"schema_version":"sila/adoption-catalog/v1","cases":[{"schema":"sila/adoption-case/v1","id":"c1","prompt":"p1","expected_first_tool":"recall","expected_sequence":["recall"],"allowed_tools":["recall"],"forbidden_tools":["grep"],"max_turns":1,"harness":"command-code","profile":"continuity","required_surface":"mcp","timeout_ms":1000,"setup":{"fixture":"f","goal":null},"assertions":[{"type":"not_a_real_assertion","tool":"recall"}]}]}`,
			wantErr: "unknown assertion type",
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTmp(strings.ReplaceAll(tc.name, " ", "_")+".json", tc.payload)
			_, err := LoadAdoptionCases(p)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestRunOfflineEval_PassAndMutations(t *testing.T) {
	if _, err := os.Stat(filepath.Join("..", "..", ".agents", "skills", "sila", "SKILL.md")); err != nil {
		t.Skip("skill vault not mounted in this environment")
	}
	rootDir := filepath.Join("..", "..")
	cases, err := LoadAdoptionCases(filepath.Join(rootDir, "evals", "adoption", "cases.json"))
	if err != nil {
		t.Fatalf("LoadAdoptionCases: %v", err)
	}

	rep, err := RunOfflineEval(cases, rootDir)
	if err != nil {
		t.Fatalf("RunOfflineEval: %v", err)
	}
	if rep.Verdict != "pass" {
		t.Fatalf("expected offline verdict pass, got %s (results: %+v)", rep.Verdict, rep.Cases)
	}
	if rep.Summary.Total != 12 || rep.Summary.Pass != 12 || rep.Summary.Fail != 0 {
		t.Fatalf("unexpected summary: %+v", rep.Summary)
	}

	// Mutate 1: remove one trigger phrase from compiled rulebook ("sila recall")
	rulebookBytes, err := os.ReadFile(filepath.Join(rootDir, "agents", "AGENTS.latest.md"))
	if err != nil {
		t.Fatalf("read AGENTS.latest.md: %v", err)
	}
	mutatedRulebook := strings.ReplaceAll(string(rulebookBytes), "sila recall", "removed_trigger_phrase")
	repMutRule, err := RunOfflineEvalWithOptions(cases, rootDir, OfflineEvalOptions{
		RulebookContent: &mutatedRulebook,
	})
	if err != nil {
		t.Fatalf("RunOfflineEvalWithOptions (mutated rulebook): %v", err)
	}
	if repMutRule.Verdict != "fail" || repMutRule.Summary.Fail == 0 {
		t.Fatalf("expected offline failure when trigger phrase is removed from rulebook, got %+v", repMutRule.Summary)
	}

	// Mutate 2: remove "recall" from continuity profile tools
	repMutTool, err := RunOfflineEvalWithOptions(cases, rootDir, OfflineEvalOptions{
		ProfileTools: map[string][]mcp.Tool{
			"continuity": {
				{Name: "prime_context", Description: "Call this ONCE at the start of a session"},
			},
		},
	})
	if err != nil {
		t.Fatalf("RunOfflineEvalWithOptions (mutated profile): %v", err)
	}
	if repMutTool.Verdict != "fail" || repMutTool.Summary.Fail == 0 {
		t.Fatalf("expected offline failure when expected tool is removed from profile, got %+v", repMutTool.Summary)
	}

	// Mutate 3: missing SKILL.md fails when a case has required_surface: "skill"
	emptySkill := ""
	repMutSkill, err := RunOfflineEvalWithOptions(cases, rootDir, OfflineEvalOptions{
		SkillContent: &emptySkill,
	})
	if err != nil {
		t.Fatalf("RunOfflineEvalWithOptions (missing SKILL.md): %v", err)
	}
	if repMutSkill.Verdict != "fail" {
		t.Fatalf("expected offline failure when SKILL.md is missing, got verdict %s", repMutSkill.Verdict)
	}
	foundSkillFail := false
	for _, cr := range repMutSkill.Cases {
		if cr.ID == "specialist-routing-skill-load" && cr.Verdict == "fail" {
			foundSkillFail = true
		}
	}
	if !foundSkillFail {
		t.Fatalf("expected specialist-routing-skill-load to fail when SKILL.md is missing")
	}
}

func TestScriptedAgent_DeterminismAndMissingTrigger(t *testing.T) {
	rootDir := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(rootDir, ".agents", "skills", "sila", "SKILL.md")); err != nil {
		t.Skip("skill vault not mounted in this environment")
	}
	cases, err := LoadAdoptionCases(filepath.Join(rootDir, "evals", "adoption", "cases.json"))
	if err != nil {
		t.Fatalf("LoadAdoptionCases: %v", err)
	}
	sm, err := LoadSurfaceMap(rootDir)
	if err != nil {
		t.Fatalf("LoadSurfaceMap: %v", err)
	}
	var mapping SurfaceMapping
	for _, m := range sm.Mappings {
		if m.CaseID == "recall-before-web-search" {
			mapping = m
			break
		}
	}
	var recallCase AdoptionCase
	for _, c := range cases {
		if c.ID == "recall-before-web-search" {
			recallCase = c
			break
		}
	}

	rulebookBytes, _ := os.ReadFile(filepath.Join(rootDir, "agents", "AGENTS.latest.md"))
	skillBytes, _ := os.ReadFile(filepath.Join(rootDir, ".agents", "skills", "sila", "SKILL.md"))
	exposed := map[string]mcp.Tool{
		"recall": {Name: "recall", Description: "Find prior resolutions for a problem before searching the web"},
	}

	obs1, diags1 := ScriptedAgent(recallCase, mapping, string(rulebookBytes), "sila recall", string(skillBytes), true, exposed)
	obs2, diags2 := ScriptedAgent(recallCase, mapping, string(rulebookBytes), "sila recall", string(skillBytes), true, exposed)
	if obs1 != "recall" || obs1 != obs2 || len(diags1) != 0 || len(diags2) != 0 {
		t.Fatalf("expected deterministic 'recall', got %q (%v) and %q (%v)", obs1, diags1, obs2, diags2)
	}

	// Missing trigger line produces empty observed tool and diagnostic
	obsMissing, diagsMissing := ScriptedAgent(recallCase, mapping, "no trigger here", "sila recall", string(skillBytes), true, exposed)
	if obsMissing != "" || len(diagsMissing) == 0 {
		t.Fatalf("expected empty observed tool and diagnostic when trigger is missing, got %q (%v)", obsMissing, diagsMissing)
	}
}

func TestRunLiveEval_PassFailUnknownAndPrivacy(t *testing.T) {
	rootDir := filepath.Join("..", "..")
	cases, err := LoadAdoptionCases(filepath.Join(rootDir, "evals", "adoption", "cases.json"))
	if err != nil {
		t.Fatalf("LoadAdoptionCases: %v", err)
	}

	// 1. All pass via fake driver
	outDir := t.TempDir()
	passRep, err := RunLiveEval(context.Background(), cases, LiveEvalOptions{
		Harness: "fake-harness",
		OutDir:  outDir,
		Driver: func(ctx context.Context, c AdoptionCase) ([]LiveTurnEvent, error) {
			events := make([]LiveTurnEvent, len(c.ExpectedSequence))
			for i, name := range c.ExpectedSequence {
				events[i] = LiveTurnEvent{ToolName: name, Turn: i + 1}
			}
			return events, nil
		},
	})
	if err != nil {
		t.Fatalf("RunLiveEval pass: %v", err)
	}
	if passRep.Verdict != "pass" || passRep.Summary.Pass != len(cases) {
		t.Fatalf("expected live pass verdict, got %s (%+v, metrics: %+v)", passRep.Verdict, passRep.Summary, passRep.Metrics)
	}

	// Verify privacy: neither report JSON nor written files contain prompts or paths
	rawReport, err := os.ReadFile(filepath.Join(outDir, "report.json"))
	if err != nil {
		t.Fatalf("read report.json: %v", err)
	}
	for _, c := range cases {
		if strings.Contains(string(rawReport), c.Prompt) {
			t.Fatalf("privacy violation: report.json leaked prompt %q", c.Prompt)
		}
	}
	if strings.Contains(string(rawReport), outDir) {
		t.Fatalf("privacy violation: report.json leaked filesystem path %q", outDir)
	}

	// 2. Forbidden tool short-circuit -> fail and forbidden_tool_rate increments
	forbidRep, err := RunLiveEval(context.Background(), cases, LiveEvalOptions{
		Harness: "fake-harness",
		Driver: func(ctx context.Context, c AdoptionCase) ([]LiveTurnEvent, error) {
			if len(c.ForbiddenTools) > 0 {
				return []LiveTurnEvent{
					{ToolName: c.ForbiddenTools[0], Turn: 1},
					{ToolName: c.ExpectedFirstTool, Turn: 2},
				}, nil
			}
			return []LiveTurnEvent{{ToolName: c.ExpectedFirstTool, Turn: 1}}, nil
		},
	})
	if err != nil {
		t.Fatalf("RunLiveEval forbidden: %v", err)
	}
	if forbidRep.Verdict != "fail" {
		t.Fatalf("expected fail verdict on forbidden tool, got %s", forbidRep.Verdict)
	}
	if forbidRep.Metrics == nil || forbidRep.Metrics.ForbiddenToolRate.Value == 0 {
		t.Fatalf("expected non-zero forbidden_tool_rate, got %+v", forbidRep.Metrics)
	}
	// Short-circuit: ToolCalls stops at the first forbidden tool
	if len(forbidRep.Cases[0].ToolCalls) != 1 {
		t.Fatalf("expected forbidden tool short-circuit to length 1, got %v", forbidRep.Cases[0].ToolCalls)
	}

	// 3. Timeout and empty ledger rows -> unknown; <80% data -> aggregate unknown
	unknownRep, err := RunLiveEval(context.Background(), cases, LiveEvalOptions{
		Harness: "fake-harness",
		Driver: func(ctx context.Context, c AdoptionCase) ([]LiveTurnEvent, error) {
			if c.ID == "session-start-prime" {
				return []LiveTurnEvent{{ToolName: c.ExpectedFirstTool, Turn: 1}}, nil
			}
			if c.ID == "recall-before-web-search" {
				return nil, context.DeadlineExceeded
			}
			return nil, nil // no ledger rows -> unknown
		},
	})
	if err != nil {
		t.Fatalf("RunLiveEval unknown: %v", err)
	}
	if unknownRep.Verdict != "unknown" {
		t.Fatalf("expected aggregate unknown when <80%% cases have data, got %s", unknownRep.Verdict)
	}

	// 4. Dry-run mode emits plan and unknown verdict without executing driver
	dryRep, err := RunLiveEval(context.Background(), cases, LiveEvalOptions{
		Harness: "command-code",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("RunLiveEval dry-run: %v", err)
	}
	if dryRep.Verdict != "unknown" || len(dryRep.DryRunPlan) != len(cases) {
		t.Fatalf("expected dry-run unknown verdict with %d plan items, got %s (%d)", len(cases), dryRep.Verdict, len(dryRep.DryRunPlan))
	}
}

func TestComputeEvalMetrics_BoundariesAndZeroDenominator(t *testing.T) {
	// Zero denominator -> unknown verdict
	emptyMetrics := ComputeEvalMetrics(nil, nil, nil)
	if emptyMetrics.TriggerRecallRate.Denominator != 0 || emptyMetrics.TriggerRecallRate.Value != 0 {
		t.Fatalf("expected zero-denominator metric, got %+v", emptyMetrics.TriggerRecallRate)
	}
	if ComputeLiveVerdict(EvalSummary{}, emptyMetrics) != "unknown" {
		t.Fatalf("expected unknown live verdict for zero denominators")
	}

	// Threshold boundary check: 8 of 10 cases pass (0.8000 == pass), 0 forbidden (0.0000 == pass)
	var synthCases []AdoptionCase
	var synthResults []CaseResult
	for i := 0; i < 10; i++ {
		id := "c" + string(rune('0'+i))
		synthCases = append(synthCases, AdoptionCase{
			ID:                id,
			ExpectedFirstTool: "recall",
			ExpectedSequence:  []string{"recall"},
			ForbiddenTools:    []string{"web_search"},
		})
		observed := "recall"
		verdict := "pass"
		turns := 1
		if i >= 8 {
			observed = "other_tool"
			verdict = "fail"
		}
		synthResults = append(synthResults, CaseResult{
			ID:               id,
			Verdict:          verdict,
			Expected:         "recall",
			Observed:         observed,
			ToolCalls:        []string{observed},
			TurnsToFirstSila: turns,
		})
	}
	sessions := []LiveSessionTrace{
		{
			SessionID:     "s1",
			GoalBound:     true,
			GroundedWrite: true,
			ToolsCalled:   []string{"prime_context", "recall", "save_memo", "save_handoff"},
		},
	}
	m := ComputeEvalMetrics(synthCases, synthResults, sessions)
	if m.TriggerRecallRate.Value != 0.8 || m.TriggerRecallRate.Denominator != 10 {
		t.Fatalf("expected 0.8000 trigger_recall_rate (10), got %+v", m.TriggerRecallRate)
	}
	if m.ForbiddenToolRate.Value != 0.0 || m.ForbiddenToolRate.Denominator != 10 {
		t.Fatalf("expected 0.0000 forbidden_tool_rate (10), got %+v", m.ForbiddenToolRate)
	}
	verdict := ComputeLiveVerdict(EvalSummary{Total: 10, Pass: 8, Fail: 2}, m)
	if verdict != "pass" {
		t.Fatalf("expected boundary 0.8000 to pass live verdict, got %s", verdict)
	}
}
