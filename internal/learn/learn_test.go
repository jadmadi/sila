package learn

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func caseFixture() OperationalLearning {
	return OperationalLearning{
		ID:            "case-dependency-injection",
		ProjectID:     "case-proj",
		Kind:          KindCase,
		Domain:        DomainGeneral,
		TechStack:     "go",
		Title:         "Dependency injection",
		Intent:        "Dependency injection",
		Approach:      "constructor injection",
		Invariant:     "explicit dependencies",
		GuardrailRule: HarmonizeRule("explicit dependencies"),
		Tags:          []string{"architecture"},
		Confidence:    "high",
	}
}

func TestCompileGuidebook_CaseKindRendering(t *testing.T) {
	gb := CompileGuidebook([]OperationalLearning{caseFixture()}, "")
	if gb.TotalLearnings != 1 {
		t.Fatalf("total learnings = %d, want 1", gb.TotalLearnings)
	}

	md := gb.ToMarkdown()
	for _, want := range []string{"**Intent**", "**Approach**", "**Invariant**", "constructor injection", "explicit dependencies"} {
		if !strings.Contains(md, want) {
			t.Errorf("case markdown missing %q:\n%s", want, md)
		}
	}
	for _, forbidden := range []string{"Symptom", "Root Cause", "Pivotal Fix", "Architectural procedural pattern", "**Fix**"} {
		if strings.Contains(md, forbidden) {
			t.Errorf("case markdown leaked lesson label/text %q:\n%s", forbidden, md)
		}
	}

	htmlOut := gb.ToHTML()
	for _, want := range []string{">Intent<", ">Approach<", ">Invariant<", "constructor injection", "explicit dependencies"} {
		if !strings.Contains(htmlOut, want) {
			t.Errorf("case HTML missing %q", want)
		}
	}
	for _, forbidden := range []string{"Symptom", "Root Cause", "Pivotal Fix", "Architectural procedural pattern"} {
		if strings.Contains(htmlOut, forbidden) {
			t.Errorf("case HTML leaked lesson label/text %q", forbidden)
		}
	}

	jsonData, err := gb.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Guidebook
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Sections) != 1 || len(decoded.Sections[0].Learnings) != 1 {
		t.Fatalf("unexpected guidebook shape: %+v", decoded.Sections)
	}
	got := decoded.Sections[0].Learnings[0]
	if got.Kind != KindCase || got.Intent != "Dependency injection" || got.Approach != "constructor injection" || got.Invariant != "explicit dependencies" {
		t.Fatalf("case JSON fields = %+v", got)
	}
	for _, want := range []string{`"kind": "case"`, `"intent": "Dependency injection"`, `"approach": "constructor injection"`, `"invariant": "explicit dependencies"`} {
		if !strings.Contains(string(jsonData), want) {
			t.Errorf("case JSON missing %q:\n%s", want, jsonData)
		}
	}
	for _, forbidden := range []string{`"symptom"`, `"root_cause"`, `"pivotal_fix"`} {
		if strings.Contains(string(jsonData), forbidden) {
			t.Errorf("case JSON leaked lesson field %q:\n%s", forbidden, jsonData)
		}
	}

	toon := gb.ToTOON()
	if !strings.Contains(toon, "cases[1]{kind,domain,title,stack,intent,approach,invariant}:") {
		t.Errorf("case TOON missing case table:\n%s", toon)
	}
	for _, forbidden := range []string{"symptom", "root_cause", "pivotal_fix", "learnings["} {
		if strings.Contains(toon, forbidden) {
			t.Errorf("case TOON leaked lesson data %q:\n%s", forbidden, toon)
		}
	}

	summary := gb.ToSummary()
	if !strings.Contains(summary, "0 lessons, 1 cases, 0 memos, 0 decisions") {
		t.Errorf("case summary missing kind breakdown:\n%s", summary)
	}
}

func TestCompileGuidebook_LessonKindRenderingUnchanged(t *testing.T) {
	// Kind is intentionally empty to prove legacy lesson-shaped records still
	// render as lessons.
	lesson := OperationalLearning{
		ID:            "legacy-lesson",
		ProjectID:     "legacy-proj",
		Domain:        DomainTesting,
		TechStack:     "go",
		Title:         "Flaky test isolation",
		Symptom:       "Order-dependent test failure",
		RootCause:     "Shared database state",
		PivotalFix:    "Add beforeEach cleanup",
		GuardrailRule: "Always reset storage in beforeEach",
		Tags:          []string{"testing"},
		Confidence:    "high",
	}

	gb := CompileGuidebook([]OperationalLearning{lesson}, "")
	md := gb.ToMarkdown()
	for _, want := range []string{"Symptom & Failure Mode", "Root Cause", "Pivotal Fix", "Guardrail Rule & Architectural Invariant", "Always reset storage in beforeEach"} {
		if !strings.Contains(md, want) {
			t.Errorf("lesson markdown missing %q:\n%s", want, md)
		}
	}

	htmlOut := gb.ToHTML()
	for _, want := range []string{"Symptom &amp; Failure Mode", "Root Cause", "Pivotal Fix", "Guardrail Rule &amp; Invariant"} {
		if !strings.Contains(htmlOut, want) {
			t.Errorf("lesson HTML missing %q", want)
		}
	}

	toon := gb.ToTOON()
	if !strings.Contains(toon, "learnings[1]{kind,domain,title,stack,symptom,cause,fix,rule}:") {
		t.Errorf("lesson TOON missing table:\n%s", toon)
	}
	if !strings.Contains(toon, "lesson,testing,Flaky test isolation") {
		t.Errorf("lesson TOON row missing kind:\n%s", toon)
	}

	jsonData, err := gb.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Guidebook
	if err := json.Unmarshal(jsonData, &decoded); err != nil {
		t.Fatal(err)
	}
	got := decoded.Sections[0].Learnings[0]
	if got.Kind != KindLesson {
		t.Errorf("legacy record kind = %q, want lesson", got.Kind)
	}
	if got.Symptom != lesson.Symptom || got.RootCause != lesson.RootCause || got.PivotalFix != lesson.PivotalFix {
		t.Errorf("lesson fields changed: %+v", got)
	}

	summary := gb.ToSummary()
	if !strings.Contains(summary, "1 lessons, 0 cases, 0 memos, 0 decisions") {
		t.Errorf("lesson summary missing kind breakdown:\n%s", summary)
	}
}

func TestFormatParseCaseKindRoundTrip(t *testing.T) {
	orig := caseFixture()
	orig.Directive = ""

	formatted := FormatOperationalLearning(orig)
	for _, want := range []string{"kind: case", "intent: Dependency injection", "approach: constructor injection", "invariant: explicit dependencies"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatted case missing %q: %s", want, formatted)
		}
	}
	if strings.Contains(formatted, "symptom:") || strings.Contains(formatted, "cause:") || strings.Contains(formatted, "fix:") {
		t.Errorf("formatted case contains lesson fields: %s", formatted)
	}

	parsed, ok := ParseOperationalLearning(formatted)
	if !ok {
		t.Fatal("failed to parse formatted case")
	}
	if parsed.Kind != KindCase {
		t.Errorf("kind = %q, want case", parsed.Kind)
	}
	if parsed.Intent != orig.Intent || parsed.Approach != orig.Approach || parsed.Invariant != orig.Invariant {
		t.Errorf("case fields lost: %+v", parsed)
	}
	if parsed.Symptom != "" || parsed.RootCause != "" || parsed.PivotalFix != "" {
		t.Errorf("case parse synthesized lesson fields: %+v", parsed)
	}

	memo := OperationalLearning{
		Kind:          KindMemo,
		Title:         "Always keep migrations idempotent",
		Directive:     "Always keep migrations idempotent across environments",
		GuardrailRule: "Always keep migrations idempotent across environments",
	}
	parsedMemo, ok := ParseOperationalLearning(FormatOperationalLearning(memo))
	if !ok || parsedMemo.Kind != KindMemo || parsedMemo.Directive != memo.Directive {
		t.Fatalf("memo round-trip = %+v ok=%v", parsedMemo, ok)
	}
	if parsedMemo.Symptom != "" || parsedMemo.RootCause != "" || parsedMemo.PivotalFix != "" {
		t.Errorf("memo parse synthesized lesson fields: %+v", parsedMemo)
	}
}

func TestHarvest_CaseAndDirectiveKinds(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "case_kind_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	caseFact, err := st.SaveCase(proj.ID, "Dependency injection", "constructor injection", "explicit dependencies", "architecture,go", "agent", tempDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemoWithKind(proj.ID, "Always keep migrations idempotent across environments", "agent", tempDir, nil, "memo"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemoWithKind(proj.ID, "Use D1 batching for bulk inserts", "agent", tempDir, nil, "decision"); err != nil {
		t.Fatal(err)
	}

	learnings, err := Harvest(st, HarvesterOptions{ScanAllStores: false, IncludeMemos: true, RedactSecrets: true})
	if err != nil {
		t.Fatal(err)
	}

	var gotCase, gotMemo, gotDecision *OperationalLearning
	for i := range learnings {
		switch learnings[i].Kind {
		case KindCase:
			gotCase = &learnings[i]
		case KindMemo:
			gotMemo = &learnings[i]
		case KindDecision:
			gotDecision = &learnings[i]
		}
	}

	if gotCase == nil {
		t.Fatalf("case not harvested: %+v", learnings)
	}
	if gotCase.Intent != "Dependency injection" || gotCase.Approach != "constructor injection" || gotCase.Invariant != "explicit dependencies" {
		t.Errorf("case fields = %+v", gotCase)
	}
	if gotCase.Symptom != "" || gotCase.RootCause != "" || gotCase.PivotalFix != "" {
		t.Errorf("case synthesized lesson fields: %+v", gotCase)
	}
	if gotCase.GuardrailRule != "Ensure explicit dependencies" {
		t.Errorf("case guardrail = %q", gotCase.GuardrailRule)
	}

	if gotMemo == nil || gotMemo.Directive != "Always keep migrations idempotent across environments" {
		t.Fatalf("memo = %+v", gotMemo)
	}
	if gotMemo.Symptom != "" || gotMemo.RootCause != "" || gotMemo.PivotalFix != "" {
		t.Errorf("memo synthesized lesson fields: %+v", gotMemo)
	}

	if gotDecision == nil || gotDecision.Directive != "Use D1 batching for bulk inserts" {
		t.Fatalf("decision = %+v", gotDecision)
	}
	if gotDecision.Symptom != "" || gotDecision.RootCause != "" || gotDecision.PivotalFix != "" {
		t.Errorf("decision synthesized lesson fields: %+v", gotDecision)
	}

	gb := CompileGuidebook([]OperationalLearning{*gotCase}, "")
	if strings.Contains(gb.ToMarkdown(), "Architectural procedural pattern") {
		t.Fatal("guidebook still synthesizes the generic case root cause")
	}
	// Rendering is display-only: the stored fact claim must be byte-identical.
	_ = gb.ToHTML()
	_, _ = gb.ToJSON()
	_ = gb.ToTOON()
	_ = gb.ToSummary()

	stored, err := st.FactByID(caseFact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Claim != caseFact.Claim {
		t.Fatalf("rendering rewrote the stored case fact: got %+v, want claim %q", stored, caseFact.Claim)
	}
}

func TestDeduplicateBySymptomCauseHash_DistinctCasesSurvive(t *testing.T) {
	first := caseFixture()
	second := caseFixture()
	second.ID = "case-caching"
	second.Title = "Caching"
	second.Intent = "Caching"
	second.Approach = "write-through cache"
	second.Invariant = "never serve stale data"

	got := DeduplicateBySymptomCauseHash([]OperationalLearning{first, second})
	if len(got) != 2 {
		t.Fatalf("distinct cases merged: %d -> %+v", len(got), got)
	}

	duplicate := caseFixture()
	duplicate.ID = "case-duplicate"
	duplicate.ProjectID = "other-proj"
	got = DeduplicateBySymptomCauseHash([]OperationalLearning{first, duplicate})
	if len(got) != 1 {
		t.Fatalf("identical cases not merged: %d -> %+v", len(got), got)
	}
	if got[0].SourceOccurrences != 2 {
		t.Fatalf("merged case occurrences = %d, want 2", got[0].SourceOccurrences)
	}
}

func TestFilterLearningsMatchesCaseFields(t *testing.T) {
	learnings := []OperationalLearning{caseFixture()}
	for _, query := range []string{"Dependency injection", "constructor", "explicit dependencies", "architecture"} {
		if matched := FilterLearnings(learnings, query); len(matched) != 1 {
			t.Errorf("FilterLearnings(%q) matched %d, want 1", query, len(matched))
		}
	}
	if matched := FilterLearnings(learnings, "no-such-term"); len(matched) != 0 {
		t.Errorf("unexpected match: %+v", matched)
	}
}

func TestFormatAndParseOperationalLearning(t *testing.T) {
	orig := OperationalLearning{
		Title:         "Synthetic Secret Sanitization in Test Fixtures",
		Domain:        DomainSecurity,
		TechStack:     "git, pre-commit, regex-sentry",
		Symptom:       "Local git commit blocked by pre-commit sentry.",
		RootCause:     "Test fixtures for secret redaction included contiguous mock private keys.",
		PivotalFix:    "Fragment mock credentials into split string concatenations.",
		GuardrailRule: "Never author synthetic credentials as single contiguous strings in test fixtures.",
		Tags:          []string{"security", "testing", "pre-commit"},
	}

	formatted := FormatOperationalLearning(orig)
	if !strings.HasPrefix(formatted, "OperationalLearning: Synthetic Secret Sanitization in Test Fixtures") {
		t.Errorf("unexpected formatted prefix: %s", formatted)
	}
	if !strings.Contains(formatted, "domain: security") {
		t.Errorf("formatted missing domain: %s", formatted)
	}
	if !strings.Contains(formatted, "stack: git, pre-commit, regex-sentry") {
		t.Errorf("formatted missing stack: %s", formatted)
	}

	parsed, ok := ParseOperationalLearning(formatted)
	if !ok {
		t.Fatalf("failed to parse formatted operational learning")
	}

	if parsed.Title != orig.Title {
		t.Errorf("title mismatch: got %q, want %q", parsed.Title, orig.Title)
	}
	if parsed.Domain != orig.Domain {
		t.Errorf("domain mismatch: got %q, want %q", parsed.Domain, orig.Domain)
	}
	if parsed.TechStack != orig.TechStack {
		t.Errorf("tech stack mismatch: got %q, want %q", parsed.TechStack, orig.TechStack)
	}
	if parsed.Symptom != orig.Symptom {
		t.Errorf("symptom mismatch: got %q, want %q", parsed.Symptom, orig.Symptom)
	}
	if parsed.RootCause != orig.RootCause {
		t.Errorf("root cause mismatch: got %q, want %q", parsed.RootCause, orig.RootCause)
	}
	if parsed.PivotalFix != orig.PivotalFix {
		t.Errorf("fix mismatch: got %q, want %q", parsed.PivotalFix, orig.PivotalFix)
	}
	if parsed.GuardrailRule != orig.GuardrailRule {
		t.Errorf("rule mismatch: got %q, want %q", parsed.GuardrailRule, orig.GuardrailRule)
	}
	if len(parsed.Tags) != len(orig.Tags) {
		t.Errorf("tags length mismatch: got %v, want %v", parsed.Tags, orig.Tags)
	}
}

func TestClassifyDomain(t *testing.T) {
	tests := []struct {
		text string
		tags []string
		want string
	}{
		{"goroutine leak sentry detected in go test -race", nil, DomainGo},
		{"gofmt -w cmd/ failed before gate", []string{"testing"}, DomainGo},
		{"typescript build error in tsconfig.json with esm modules", nil, DomainTypeScript},
		{"cloudflare worker isolate hit CPU limit in wrangler", nil, DomainCloudflare},
		{"unisolated D1 table mutations in vitest pool workers", []string{"storage"}, DomainCloudflare},
		{"sqlite foreign keys violated after migration apply", []string{"sql"}, DomainD1},
		{"subshell mount path was missing in systemd unit", []string{"linux"}, DomainDevOps},
		{"pre-commit hook blocked branch push in isolated worktree", []string{"pipeline"}, DomainCICD},
		{"leaked bearer token credential in commit diff", []string{"redact"}, DomainSecurity},
		{"flaky test fixture without beforeEach reset hook", []string{"vitest"}, DomainTesting},
		{"missing aria-label and keyboard trap on modal dialog", []string{"wcag"}, DomainA11y},
		{"general architectural refactor with radical simplicity", nil, DomainGeneral},
	}

	for _, tc := range tests {
		got := ClassifyDomain(tc.text, tc.tags)
		if got != tc.want {
			t.Errorf("ClassifyDomain(%q, %v) = %q, want %q", tc.text, tc.tags, got, tc.want)
		}
	}
}

func TestCompileGuidebook_Formats(t *testing.T) {
	learnings := BuiltinFoundationalLearnings()

	gb := CompileGuidebook(learnings, "")
	if gb.TotalLearnings != len(learnings) {
		t.Errorf("expected %d total learnings, got %d", len(learnings), gb.TotalLearnings)
	}

	// 1. Markdown
	md := gb.ToMarkdown()
	if !strings.Contains(md, "# Sila Developer Operational Guidebook") {
		t.Errorf("markdown missing header: %s", md)
	}
	if !strings.Contains(md, "Synthetic Secret Sanitization in Test Fixtures") {
		t.Errorf("markdown missing learning title: %s", md)
	}
	if !strings.Contains(md, "> [!IMPORTANT] **Guardrail Rule & Architectural Invariant**") {
		t.Errorf("markdown missing rule callout: %s", md)
	}

	// 2. HTML
	html := gb.ToHTML()
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Errorf("html missing doctype: %s", html)
	}
	if !strings.Contains(html, "Sila Developer Operational Guidebook") {
		t.Errorf("html missing title: %s", html)
	}
	if !strings.Contains(html, "filterCards()") {
		t.Errorf("html missing client-side search script: %s", html)
	}

	// 3. JSON
	jsonData, err := gb.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if !strings.Contains(string(jsonData), `"total_learnings":`) {
		t.Errorf("json missing total_learnings: %s", string(jsonData))
	}

	// 4. TOON
	toon := gb.ToTOON()
	if !strings.Contains(toon, "guidebook_summary:") {
		t.Errorf("toon missing summary header: %s", toon)
	}
	if !strings.Contains(toon, "learnings[") {
		t.Errorf("toon missing learnings table: %s", toon)
	}
}

func TestHarvest_WithStoreAndRedaction(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "harvest_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject("/w/harvestproj")
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// Insert custom operational learning with a token to verify redaction
	tokenToRedact := "ghp_" + "123456789012345678901234567890123456"
	customOp := OperationalLearning{
		Title:         "GitHub Token Verification in Agent Tooling",
		Domain:        DomainSecurity,
		TechStack:     "go, github-api",
		Symptom:       "API rate limit hit with token " + tokenToRedact,
		RootCause:     "Token lacked required fine-grained repo scope",
		PivotalFix:    "Regenerate token with least-privilege permissions",
		GuardrailRule: "Always scope CI tokens to minimal necessary read/write capabilities",
		Tags:          []string{"github", "token", "security"},
	}

	formatted := FormatOperationalLearning(customOp)
	_, err = st.SaveOperationalLearning(proj.ID, formatted, "antigravity", tempDir, nil)
	if err != nil {
		t.Fatalf("SaveOperationalLearning: %v", err)
	}

	// Insert standard lesson
	_, err = st.SaveLesson(proj.ID, "D1 lock contention under bulk insert", "unbatched statements", "use env.DB.batch()", "d1,sqlite", "devin", tempDir, nil)
	if err != nil {
		t.Fatalf("SaveLesson: %v", err)
	}

	// Harvest
	learnings, err := Harvest(st, DefaultHarvesterOptions())
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}

	if len(learnings) < 7 { // 5 foundational + 2 inserted
		t.Errorf("expected at least 7 harvested learnings, got %d", len(learnings))
	}

	// Verify token was redacted
	var foundRedacted bool
	for _, l := range learnings {
		if strings.Contains(l.Symptom, tokenToRedact) {
			t.Errorf("unredacted token found in harvested learning symptom: %s", l.Symptom)
		}
		if strings.Contains(l.Symptom, "[REDACTED") {
			foundRedacted = true
		}
	}
	if !foundRedacted {
		t.Errorf("expected redacted token placeholder in harvested learning")
	}

	// Harvest with domain filter
	cfLearnings, err := Harvest(st, HarvesterOptions{Domain: DomainCloudflare})
	if err != nil {
		t.Fatalf("Harvest domain filter: %v", err)
	}
	for _, l := range cfLearnings {
		if l.Domain != DomainCloudflare {
			t.Errorf("expected only cloudflare domain, got: %s", l.Domain)
		}
	}
}

func TestFilterLearnings_And_Summary(t *testing.T) {
	learnings := BuiltinFoundationalLearnings()
	gb := CompileGuidebook(learnings, "")

	// Test ToSummary
	summary := gb.ToSummary()
	if !strings.Contains(summary, "Sila Operational Guidebook:") {
		t.Errorf("summary missing title: %s", summary)
	}
	if !strings.Contains(summary, "learnings across") {
		t.Errorf("summary missing breakdown: %s", summary)
	}

	// Test FilterLearnings
	matched := FilterLearnings(learnings, "gofmt")
	if len(matched) != 1 {
		t.Fatalf("expected 1 match for 'gofmt', got %d", len(matched))
	}
	if matched[0].Domain != DomainGo {
		t.Errorf("expected domain %s, got %s", DomainGo, matched[0].Domain)
	}

	noMatch := FilterLearnings(learnings, "nonexistent-keyword-xyz-123")
	if len(noMatch) != 0 {
		t.Errorf("expected 0 matches, got %d", len(noMatch))
	}
}

func BenchmarkCompileGuidebook_1000Learnings(b *testing.B) {
	// Synthesize 1,200 realistic operational learnings across 10 domains
	items := make([]OperationalLearning, 1200)
	domains := AllDomains
	for i := 0; i < 1200; i++ {
		d := domains[i%len(domains)]
		items[i] = OperationalLearning{
			ID:            "op-bench-" + string(rune(i)),
			ProjectID:     "project-bench",
			Domain:        d,
			TechStack:     d + ", runtime, compiler",
			Title:         "High Scale Operational Invariant " + string(rune(i)),
			Symptom:       "Occasional edge condition failure under high load",
			RootCause:     "Resource contention or lack of synchronization",
			PivotalFix:    "Apply deterministic bounded pool pattern",
			GuardrailRule: "Always enforce bounded buffer pools under high concurrency",
			Tags:          []string{d, "benchmark", "performance"},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gb := CompileGuidebook(items, "")
		_ = gb.ToMarkdown()
		_ = gb.ToHTML()
		_ = gb.ToTOON()
	}
}

func TestHarvest_ExcludesStaleLearnings(t *testing.T) {
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "stale_learning_test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject("/w/stale-project")
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	op := OperationalLearning{
		Title:         "Stale adaptive rule",
		Domain:        "android",
		Symptom:       "old rule",
		RootCause:     "outdated guidance",
		PivotalFix:    "replace with current guidance",
		GuardrailRule: "old rule must not appear",
	}
	fact, err := st.SaveOperationalLearning(proj.ID, FormatOperationalLearning(op), "test", tempDir, nil)
	if err != nil {
		t.Fatalf("SaveOperationalLearning: %v", err)
	}
	if err := st.MarkFactStale(fact.ID); err != nil {
		t.Fatalf("MarkFactStale: %v", err)
	}

	learnings, err := Harvest(st, HarvesterOptions{ScanAllStores: false, IncludeMemos: true, RedactSecrets: true})
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}
	for _, l := range learnings {
		if l.ID == fact.ID {
			t.Fatalf("stale fact %s must not appear in harvested learnings", fact.ID)
		}
	}
}

func TestAutoHarvest_LessonAndOperationalLearning_SymptomCauseHash(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "auto_harvest_test.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	projA, _ := st.EnsureProject("/w/project-alpha")
	projB, _ := st.EnsureProject("/w/project-beta")

	// 1. Insert Go lesson in Project A
	_, err = st.SaveLesson(projA.ID, "Unclosed goroutines leaking in test suite", "missing context cancel", "call cancel() in defer", "golang,goroutine", "antigravity", tempDir, nil)
	if err != nil {
		t.Fatalf("SaveLesson: %v", err)
	}

	// 2. Insert same Go lesson in Project B (same symptom & cause) to verify SymptomCauseHash deduplication
	_, err = st.SaveLesson(projB.ID, "Unclosed goroutines leaking in test suite", "missing context cancel", "always defer cancel()", "golang,goroutine", "claude", tempDir, nil)
	if err != nil {
		t.Fatalf("SaveLesson: %v", err)
	}

	// 3. Insert D1 operational learning in Project A
	opD1 := OperationalLearning{
		Title:         "Cloudflare D1 Transaction Rollback",
		Domain:        DomainD1,
		TechStack:     "cloudflare-workers, d1",
		Symptom:       "Uncommitted D1 batch mutations locked database",
		RootCause:     "Transaction error without proper catch rollback handler",
		PivotalFix:    "Wrap D1 batch operations in try-catch and emit rollback",
		GuardrailRule: "Always handle transaction failure with explicit rollback",
		Tags:          []string{"d1", "database", "transactions"},
	}
	_, err = st.SaveOperationalLearning(projA.ID, FormatOperationalLearning(opD1), "antigravity", tempDir, nil)
	if err != nil {
		t.Fatalf("SaveOperationalLearning: %v", err)
	}

	// 4. Harvest with default options (which scans all stores / current store)
	opts := DefaultHarvesterOptions()
	opts.ScanAllStores = false // In-memory/tempDB only
	learnings, err := Harvest(st, opts)
	if err != nil {
		t.Fatalf("Harvest: %v", err)
	}

	// Verify deduplication: the two Go lessons must be merged into 1 finding with "verified" confidence
	var foundGoLesson *OperationalLearning
	var foundD1Op *OperationalLearning
	for _, l := range learnings {
		if strings.Contains(l.Symptom, "Unclosed goroutines leaking") {
			foundGoLesson = &l
		}
		if strings.Contains(l.Symptom, "Uncommitted D1 batch mutations") {
			foundD1Op = &l
		}
	}

	if foundGoLesson == nil {
		t.Fatalf("expected harvested Go lesson, got nil")
	}
	if foundGoLesson.Domain != DomainGo {
		t.Errorf("expected domain %s for Go lesson, got %s", DomainGo, foundGoLesson.Domain)
	}
	if foundGoLesson.Confidence != "verified" {
		t.Errorf("expected multi-project deduplicated lesson to have 'verified' confidence, got %q", foundGoLesson.Confidence)
	}
	if foundGoLesson.SourceOccurrences < 2 {
		t.Errorf("expected at least 2 source occurrences, got %d", foundGoLesson.SourceOccurrences)
	}

	if foundD1Op == nil {
		t.Fatalf("expected harvested D1 operational learning, got nil")
	}
	if foundD1Op.Domain != DomainD1 {
		t.Errorf("expected domain %s for D1 learning, got %s", DomainD1, foundD1Op.Domain)
	}

	// 5. Compile guidebook and verify inclusion in summary and sections
	gb := CompileGuidebook(learnings, "")
	summary := gb.ToSummary()
	if !strings.Contains(summary, "Golang Engineering Disciplines") {
		t.Errorf("summary missing Golang domain: %s", summary)
	}
	if !strings.Contains(summary, "Cloudflare D1 & SQLite Data Tier") {
		t.Errorf("summary missing D1 domain: %s", summary)
	}

	// 6. Verify managed rules export includes new lessons
	rulesBlock := GenerateManagedRulesBlock(learnings, []string{"golang", "d1"})
	if !strings.Contains(rulesBlock, "Unclosed goroutines leaking in test suite") {
		t.Errorf("rules export missing Go lesson: %s", rulesBlock)
	}
	if !strings.Contains(rulesBlock, "Cloudflare D1 Transaction Rollback") {
		t.Errorf("rules export missing D1 learning: %s", rulesBlock)
	}
}
