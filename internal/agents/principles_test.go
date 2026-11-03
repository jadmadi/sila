package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalConstitutionLayers(t *testing.T) {
	layers := CanonicalConstitutionLayers()
	if len(layers) != 5 {
		t.Fatalf("expected 5 canonical constitution layers, got %d", len(layers))
	}

	expectedPrefixes := []string{
		"Layer 1: Radical Simplicity",
		"Layer 2: Human & Agent Ergonomics",
		"Layer 3: Pragmatic Structural Seams",
		"Layer 4: Data Flow & Concurrency",
		"Layer 5: Epistemic Grounding",
	}

	for i, prefix := range expectedPrefixes {
		if !strings.HasPrefix(layers[i].Name, prefix) {
			t.Errorf("layer %d: expected name prefix %q, got %q", i+1, prefix, layers[i].Name)
		}
		if layers[i].Philosophy == "" {
			t.Errorf("layer %d: missing philosophy", i+1)
		}
		if len(layers[i].Invariants) == 0 {
			t.Errorf("layer %d: missing invariants", i+1)
		}
		if layers[i].AntiPattern == "" {
			t.Errorf("layer %d: missing antipattern", i+1)
		}
	}
}

func TestCanonicalMemoryLoop(t *testing.T) {
	loop := CanonicalMemoryLoop()
	expectedVerbs := []string{
		"sila recall",
		"sila doc search",
		"sila memo",
		"sila lesson",
		"sila case",
		"sila facts verify",
		"sila prime",
		"sila gate",
		"sila handoff",
	}

	for _, verb := range expectedVerbs {
		if !strings.Contains(loop, verb) {
			t.Errorf("canonical memory loop missing verb reference %q", verb)
		}
	}
}

func TestStackSpecificRules(t *testing.T) {
	cases := []struct {
		stack          string
		expectedSubstr string
	}{
		{"go", "Pure-Go CGO-Free Portability"},
		{"golang", "Race Detector Discipline"},
		{"ts", "D1 Query Batching"},
		{"cloudflare", "V8 Isolate CPU Limits"},
		{"worker", "Vitest Storage Sandboxes"},
		{"python", "Type Hinting & Mypy"},
		{"rust", "Zero-Cost Abstractions"},
		{"polyglot", "Universal Simplicity"},
	}

	for _, tc := range cases {
		rules := StackRules(tc.stack)
		found := false
		for _, r := range rules {
			if strings.Contains(r, tc.expectedSubstr) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("stack %q missing expected rule containing %q; got %+v", tc.stack, tc.expectedSubstr, rules)
		}
	}

	goRules := StackRules("go")
	if len(goRules) != 9 {
		t.Errorf("expected 9 Go operational checklist rules, got %d", len(goRules))
	}
	foundReleaseRule := false
	for _, r := range goRules {
		if strings.Contains(r, "Release Lifecycle & Value Changelog") {
			foundReleaseRule = true
			break
		}
	}
	if !foundReleaseRule {
		t.Errorf("Go operational checklist missing Release Lifecycle & Value Changelog rule")
	}
	cfRules := StackRules("cloudflare")
	if len(cfRules) != 6 {
		t.Errorf("expected 6 Cloudflare operational checklist rules, got %d", len(cfRules))
	}
}

func TestPrinciplesPackRenderAndDelimiters(t *testing.T) {
	pack := DefaultPrinciplesPack("go")
	md := pack.RenderMarkdown()

	if !strings.Contains(md, "Universal Principles Pack") {
		t.Fatal("rendered markdown missing title")
	}
	if !strings.Contains(md, "Radical Simplicity") {
		t.Fatal("rendered markdown missing Layer 1")
	}
	if !strings.Contains(md, "Pure-Go CGO-Free Portability") {
		t.Fatal("rendered markdown missing Go-specific rules")
	}

	delimited := pack.RenderDelimited()
	if !strings.HasPrefix(delimited, PrinciplesBlockBegin) {
		t.Fatalf("expected begin delimiter prefix, got: %s", delimited[:50])
	}
	if !strings.HasSuffix(delimited, PrinciplesBlockEnd) {
		t.Fatalf("expected end delimiter suffix, got: %s", delimited[len(delimited)-50:])
	}

	extracted, ok := ExtractPrinciplesBlock(delimited)
	if !ok {
		t.Fatal("failed to extract principles block from delimited string")
	}
	if strings.TrimSpace(extracted) != strings.TrimSpace(md) {
		t.Fatalf("extracted block does not match rendered markdown")
	}

	stripped := StripPrinciplesBlock(delimited)
	if strings.TrimSpace(stripped) != "" {
		t.Fatalf("expected empty string after stripping principles from pure block, got: %q", stripped)
	}
}

func TestInjectPrinciplesNonDestructive(t *testing.T) {
	initial := "# My Project\n\nCustom user instructions.\n"
	pack := DefaultPrinciplesPack("polyglot")
	body := pack.RenderMarkdown()

	// 1. Initial injection into user file
	injected := InjectPrinciples(initial, body)
	if !strings.Contains(injected, "# My Project") {
		t.Fatal("user header lost")
	}
	if !strings.Contains(injected, "Custom user instructions.") {
		t.Fatal("user content lost")
	}
	if !strings.Contains(injected, PrinciplesBlockBegin) || !strings.Contains(injected, PrinciplesBlockEnd) {
		t.Fatal("delimiters missing")
	}

	// 2. In-place update with changed body
	newPack := DefaultPrinciplesPack("go")
	newBody := newPack.RenderMarkdown()
	updated := InjectPrinciples(injected, newBody)
	if !strings.Contains(updated, "# My Project") {
		t.Fatal("user header lost on update")
	}
	if !strings.Contains(updated, "Pure-Go CGO-Free Portability") {
		t.Fatal("updated rules missing in replaced block")
	}
	if strings.Count(updated, PrinciplesBlockBegin) != 1 {
		t.Fatalf("expected exactly 1 begin marker, got %d", strings.Count(updated, PrinciplesBlockBegin))
	}
	if strings.Count(updated, PrinciplesBlockEnd) != 1 {
		t.Fatalf("expected exactly 1 end marker, got %d", strings.Count(updated, PrinciplesBlockEnd))
	}

	// 3. Idempotency: re-injecting the same body produces identical string
	idempotent := InjectPrinciples(updated, newBody)
	if idempotent != updated {
		t.Fatalf("InjectPrinciples is not idempotent: diff between runs")
	}
}

func TestDeployTargetsWithPrinciplesMultiHarness(t *testing.T) {
	tmp := t.TempDir()

	// Seed existing custom user files
	agentsCustom := "# Project Orion\n\nAlways enforce strict typing and domain isolation.\n"
	claudeCustom := "# Claude Code Rules\n\nPrefer concise table-driven tests.\n"
	windsurfCustom := "# Windsurf Workspace Rules\n\nUse ripgrep for search.\n"

	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(agentsCustom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "CLAUDE.md"), []byte(claudeCustom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".windsurfrules"), []byte(windsurfCustom), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := CompileOptions{
		RepoRoot:          tmp,
		ProjectName:       "orion",
		IncludePrinciples: true,
		Stack:             "go",
	}

	// First sync run: projects across all 5 harnesses
	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("DeployTargets failed: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("DeployTargets returned errors: %v", res.Errors)
	}

	principlesTargets := []string{
		"AGENTS.md",
		"CLAUDE.md",
		filepath.Join(".cursor", "rules", "000-sila-principles.mdc"),
		".windsurfrules",
		filepath.Join(".github", "copilot-instructions.md"),
	}

	for _, rel := range principlesTargets {
		p := filepath.Join(tmp, rel)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("missing target file %s: %v", rel, err)
		}
		content := string(data)
		if !strings.Contains(content, PrinciplesBlockBegin) {
			t.Fatalf("target %s missing principles begin delimiter:\n%s", rel, content)
		}
		if !strings.Contains(content, PrinciplesBlockEnd) {
			t.Fatalf("target %s missing principles end delimiter:\n%s", rel, content)
		}
		if !strings.Contains(content, "Radical Simplicity") {
			t.Fatalf("target %s missing Layer 1 principles", rel)
		}
		if !strings.Contains(content, "Pure-Go CGO-Free Portability") {
			t.Fatalf("target %s missing Go stack invariants", rel)
		}
	}

	// Verify core MDC rule file exists and has core block
	coreMdcData, err := os.ReadFile(filepath.Join(tmp, ".cursor", "rules", "000-sila-core.mdc"))
	if err != nil {
		t.Fatalf("missing 000-sila-core.mdc: %v", err)
	}
	if !strings.Contains(string(coreMdcData), OwnedBlockBegin) {
		t.Fatalf("000-sila-core.mdc missing OwnedBlockBegin: %s", string(coreMdcData))
	}

	// Verify custom user content was preserved in AGENTS.md, CLAUDE.md, and .windsurfrules
	agentsData, _ := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if !strings.Contains(string(agentsData), "Always enforce strict typing and domain isolation.") {
		t.Fatal("user content in AGENTS.md destroyed")
	}

	claudeData, _ := os.ReadFile(filepath.Join(tmp, "CLAUDE.md"))
	if !strings.Contains(string(claudeData), "Prefer concise table-driven tests.") {
		t.Fatal("user content in CLAUDE.md destroyed")
	}

	wsData, _ := os.ReadFile(filepath.Join(tmp, ".windsurfrules"))
	if !strings.Contains(string(wsData), "Use ripgrep for search.") {
		t.Fatal("user content in .windsurfrules destroyed")
	}

	// Second sync run: Idempotency test (0 writes, all skip)
	secondRes, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("second DeployTargets failed: %v", err)
	}
	if len(secondRes.Errors) > 0 {
		t.Fatalf("second DeployTargets returned errors: %v", secondRes.Errors)
	}
	if len(secondRes.FilesWritten) != 0 {
		t.Fatalf("idempotency failure: files written on 2nd run: %v", secondRes.FilesWritten)
	}

	// Verify plans on 2nd run are all 'skip'
	for _, p := range secondRes.Plan {
		if p.Action != "skip" {
			t.Fatalf("expected skip action for %s on idempotent rerun, got %q (diff: %s)", p.Path, p.Action, p.Diff)
		}
	}
}

func TestDeployTargetsPrinciplesPreview(t *testing.T) {
	tmp := t.TempDir()

	opts := CompileOptions{
		RepoRoot:          tmp,
		ProjectName:       "preview-test",
		IncludePrinciples: true,
		Stack:             "cloudflare",
		Preview:           true,
	}

	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("DeployTargets preview failed: %v", err)
	}
	if !res.Preview {
		t.Fatal("expected res.Preview to be true")
	}
	if len(res.FilesWritten) > 0 {
		t.Fatalf("preview wrote files to disk: %v", res.FilesWritten)
	}

	// Check that plan includes principles files
	foundPrinciplesMdc := false
	foundWindsurf := false
	foundCopilot := false

	for _, p := range res.Plan {
		if strings.Contains(p.Path, "000-sila-principles.mdc") {
			foundPrinciplesMdc = true
			if !strings.Contains(p.Diff, "D1 Query Batching") {
				t.Errorf("000-sila-principles.mdc diff missing Cloudflare rules: %s", p.Diff)
			}
		}
		if p.Path == ".windsurfrules" {
			foundWindsurf = true
		}
		if strings.Contains(p.Path, "copilot-instructions.md") {
			foundCopilot = true
		}
	}

	if !foundPrinciplesMdc {
		t.Error("plan missing .cursor/rules/000-sila-principles.mdc")
	}
	if !foundWindsurf {
		t.Error("plan missing .windsurfrules")
	}
	if !foundCopilot {
		t.Error("plan missing .github/copilot-instructions.md")
	}
}

func TestCanonicalMemoryLoopSpecialistRouting(t *testing.T) {
	loop := CanonicalMemoryLoop()
	if !strings.Contains(loop, "## Specialist Capability Routing (Mandatory Pre-Fallback Checks)") {
		t.Fatal("CanonicalMemoryLoop missing specialist routing section")
	}
	for _, want := range []string{
		"omni_search", "get_message_context", "search_docs",
		"get_operational_guidebook", "audit_goal",
		"skill_search", "skill_load", "route_workflow",
		"create_decision", "list_decisions", "compare_proposals",
		"sila standup", "sila digest", "sila recap",
		"sila persona", "sila hire", "sila audit", "sila skill",
		"sila edit-check", "sila safe-delete", "sila note", "sila vision triage",
	} {
		if !strings.Contains(loop, want) {
			t.Errorf("specialist routing missing %q", want)
		}
	}
	rows := 0
	for _, line := range strings.Split(SpecialistRoutingSection, "\n") {
		if strings.HasPrefix(line, "| ") && !strings.HasPrefix(line, "| Task") && !strings.HasPrefix(line, "| :") {
			rows++
		}
	}
	if rows != 6 {
		t.Fatalf("specialist routing has %d rows, want 6", rows)
	}
}

func TestCanonicalPrinciplesBlockSpecialistRouting(t *testing.T) {
	block := CanonicalPrinciplesBlock()
	if !strings.Contains(block, "## Specialist Capability Routing (Mandatory Pre-Fallback Checks)") {
		t.Fatal("CanonicalPrinciplesBlock missing specialist routing section")
	}
	for _, want := range []string{
		"omni_search", "get_message_context", "search_docs",
		"get_operational_guidebook", "audit_goal",
		"skill_search", "skill_load", "route_workflow",
		"create_decision", "list_decisions", "compare_proposals",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("CanonicalPrinciplesBlock missing %q", want)
		}
	}
}
