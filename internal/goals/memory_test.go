package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMemoryProtocol(t *testing.T) {
	cases := map[string]string{
		"# Goal\n\n- **Memory Protocol**: required\n": "required",
		"# Goal\n\n- Memory Protocol: legacy\n":       "legacy",
		"# Goal\n\nNo marker here.\n":                 "legacy",
		"# Goal\n\n- **Memory Protocol**: custom\n":   "custom",
	}
	for input, want := range cases {
		if got := ParseMemoryProtocol(input); got != want {
			t.Fatalf("ParseMemoryProtocol(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseMemoryWrites(t *testing.T) {
	valid := "## Memory Writes\n\n```json\n" + `{
	  "schema":"sila/memory-writes/v1",
	  "goal_slug":"demo",
	  "prime":{"ran":true},
	  "recall":[{"query":"x","matches":1}],
	  "memos":[{"id":"fact_1","text":"decision"}],
	  "lessons":[],
	  "cases":[],
	  "facts_verify":{"exit_code":0},
	  "handoff":{"id":"hoff_1"}
	}` + "\n```\n"
	mw, diags := ParseMemoryWrites(valid)
	if len(diags) > 0 || mw == nil || mw.GoalSlug != "demo" || len(mw.Memos) != 1 {
		t.Fatalf("valid block failed: %+v %v", mw, diags)
	}

	if mw, diags := ParseMemoryWrites("# Evidence\n\nVerdict: PASS\n"); mw != nil || len(diags) != 0 {
		t.Fatalf("missing block should be nil/no diagnostics: %+v %v", mw, diags)
	}

	unknown := strings.Replace(valid, `"goal_slug":"demo"`, `"goal_slug":"demo","unexpected":true`, 1)
	if _, diags := ParseMemoryWrites(unknown); len(diags) == 0 || !strings.Contains(diags[0], "MEMORY_SCHEMA_INVALID") {
		t.Fatalf("unknown field should fail: %v", diags)
	}

	duplicate := valid + "\n" + valid
	if _, diags := ParseMemoryWrites(duplicate); len(diags) == 0 || !strings.Contains(strings.Join(diags, " "), "MEMORY_BLOCK_DUPLICATE") {
		t.Fatalf("duplicate block should fail: %v", diags)
	}
}

func TestValidateMemoryWrites(t *testing.T) {
	missing := &MemoryWrites{Schema: "sila/memory-writes/v1", GoalSlug: "demo"}
	diags := ValidateMemoryWrites(missing, "demo", nil)
	for _, want := range []string{"MEMORY_PRIME_MISSING", "MEMORY_RECALL_MISSING", "MEMORY_WRITE_MISSING", "MEMORY_HANDOFF_MISSING"} {
		if !strings.Contains(strings.Join(diags, " "), want) {
			t.Fatalf("missing %s in %v", want, diags)
		}
	}

	waived := &MemoryWrites{
		Schema:   "sila/memory-writes/v1",
		GoalSlug: "demo",
		Facts:    MemoryFactsVerify{ExitCode: 0},
		Waivers: []MemoryWaiver{
			{Verb: "prime", Reason: "store unavailable at start"},
			{Verb: "recall", Reason: "greenfield task"},
			{Verb: "memo", Reason: "pure refactor"},
			{Verb: "handoff", Reason: "single-turn fixture"},
		},
	}
	if diags := ValidateMemoryWrites(waived, "demo", nil); len(diags) != 0 {
		t.Fatalf("waivers should satisfy the contract: %v", diags)
	}

	if diags := ValidateMemoryWrites(missing, "other", nil); !strings.Contains(strings.Join(diags, " "), "MEMORY_GOAL_MISMATCH") {
		t.Fatalf("wrong goal should fail: %v", diags)
	}

	// Verify mid-session handoff deferral ("id": "session-end" or "deferred": true) passes without store handoff lookup
	deferredByID := &MemoryWrites{
		Schema:   "sila/memory-writes/v1",
		GoalSlug: "demo",
		Prime:    MemoryPrime{Ran: true},
		Recall:   []MemoryRecallQuery{{Query: "q", Matches: 1}},
		Memos:    []MemoryRecord{{ID: "m1"}},
		Facts:    MemoryFactsVerify{ExitCode: 0},
		Handoff:  MemoryHandoff{ID: "session-end", Summary: "mid-session goal"},
	}
	if diags := ValidateMemoryWrites(deferredByID, "demo", nil); len(diags) != 0 {
		t.Fatalf("expected handoff.id=session-end to pass, got: %v", diags)
	}
	deferredByFlag := &MemoryWrites{
		Schema:   "sila/memory-writes/v1",
		GoalSlug: "demo",
		Prime:    MemoryPrime{Ran: true},
		Recall:   []MemoryRecallQuery{{Query: "q", Matches: 1}},
		Memos:    []MemoryRecord{{ID: "m1"}},
		Facts:    MemoryFactsVerify{ExitCode: 0},
		Handoff:  MemoryHandoff{Deferred: true, Summary: "deferred to session end"},
	}
	if diags := ValidateMemoryWrites(deferredByFlag, "demo", nil); len(diags) != 0 {
		t.Fatalf("expected handoff.deferred=true to pass, got: %v", diags)
	}
}

func TestCreateGoalWritesMemoryContract(t *testing.T) {
	root := t.TempDir()
	goal, err := CreateGoal(CreateGoalParams{
		RootDir:      root,
		Slug:         "memory-demo",
		Title:        "Memory Demo",
		FilesToTouch: []string{"demo.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if goal.MemoryProtocol != "required" {
		t.Fatalf("created goal protocol = %q, want required", goal.MemoryProtocol)
	}
	data, err := os.ReadFile(filepath.Join(root, "goals", "memory-demo", "goal.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"- **Memory Protocol**: required", "## Memory Plan", "sila/memory-writes/v1"} {
		if !strings.Contains(content, want) {
			t.Fatalf("goal.md missing %q:\n%s", want, content)
		}
	}
}

func TestValidateGoalsStrictMemory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "goals", "legacy-goal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# Goal: Legacy\n\n## Dependencies & Execution Order\n- **Mode**: Independent\n- **Sequence**: 1\n- **Tier**: roadmap\n"
	if err := os.WriteFile(filepath.Join(dir, "goal.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.md"), []byte("# Facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# Plan\n\n- [ ] Do it\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := ValidateGoals(root, ValidateOptions{StrictMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diag := range rep.Diagnostics {
		if diag.Code == "MEMORY_PROTOCOL_MISSING" && diag.Severity == "error" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("strict memory did not promote the legacy warning: %+v", rep.Diagnostics)
	}
}

func TestCertifyNoMemoryRequiresReason(t *testing.T) {
	root := t.TempDir()
	if _, err := CreateGoal(CreateGoalParams{RootDir: root, Slug: "cert-waiver", Title: "Cert Waiver"}); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(root, "goals", "cert-waiver", "evidence.json")
	if err := os.WriteFile(evidence, []byte(`{"facts":[{"id":"proof","fact":"fixture proof","passed":true,"verified":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CertifyGoalWithOptions(root, "cert-waiver", evidence, "test", "", CertifyOptions{NoMemory: true}); err == nil || !strings.Contains(err.Error(), "MEMORY_WAIVER_REASON_REQUIRED") {
		t.Fatalf("missing waiver reason should fail: %v", err)
	}
	cert, err := CertifyGoalWithOptions(root, "cert-waiver", evidence, "test", "", CertifyOptions{NoMemory: true, Reason: "fixture proof"})
	if err != nil {
		t.Fatal(err)
	}
	if cert.Memory == nil || cert.Memory.Status != "waived" || !cert.Memory.Waived || cert.Memory.WaiverReason != "fixture proof" {
		t.Fatalf("unexpected waived certification: %+v", cert.Memory)
	}
}

func TestCertifyRequiresMemoryBlock(t *testing.T) {
	root := t.TempDir()
	if _, err := CreateGoal(CreateGoalParams{RootDir: root, Slug: "cert-memory", Title: "Cert Memory"}); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(root, "goals", "cert-memory", "evidence.md")
	if err := os.WriteFile(evidence, []byte("# Evidence\n\nVerdict: PASS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := CertifyGoal(root, "cert-memory", evidence, "test", "")
	if err == nil || !strings.Contains(err.Error(), "MEMORY_BLOCK_MISSING") {
		t.Fatalf("required goal certified without a memory block: %v", err)
	}
}

func TestLegacyCertifyDiagnosticsAndScanGoalsMemoryStatus(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "goals", "legacy-cert")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goal.md"), []byte("# Goal: Legacy Cert\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "facts.md"), []byte("# Facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# Plan\n\n- [x] Do it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(dir, "evidence.json")
	if err := os.WriteFile(evidence, []byte(`{"facts":[{"id":"proof","fact":"fixture proof","passed":true,"verified":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cert, err := CertifyGoal(root, "legacy-cert", evidence, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	if cert.Memory == nil || cert.Memory.Status != "legacy_skipped" {
		t.Fatalf("expected legacy_skipped status, got: %+v", cert.Memory)
	}
	if len(cert.Memory.Diagnostics) == 0 || !strings.Contains(cert.Memory.Diagnostics[0], "memory protocol not required for legacy goals") {
		t.Fatalf("expected legacy diagnostic message, got: %+v", cert.Memory.Diagnostics)
	}

	audit, err := ScanGoals(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Goals) != 1 {
		t.Fatalf("expected 1 goal, got %d", len(audit.Goals))
	}
	g := audit.Goals[0]
	if g.MemoryStatus != cert.Memory.Status || g.Memory == nil || g.Memory.Status != cert.Memory.Status {
		t.Fatalf("ScanGoals memory status mismatch: goal.MemoryStatus=%q goal.Memory=%+v cert.Memory=%+v", g.MemoryStatus, g.Memory, cert.Memory)
	}
}
