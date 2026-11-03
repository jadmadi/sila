package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompilerCompile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	compiler := NewCompiler(tmp)

	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "test-project",
		Domains:     nil,
	}

	content, err := compiler.Compile(opts)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	if !strings.Contains(content, "# AGENTS.md — test-project") {
		t.Errorf("expected header with project name, got: %s", content)
	}
	if !strings.Contains(content, "Safety Rails") {
		t.Errorf("expected Core Hub safety rails, got: %s", content)
	}
	// Maintainer-specific policy must not be in the default core hub.
	if strings.Contains(content, "npm or yarn") || strings.Contains(content, "Zero GitHub Workflows") {
		t.Errorf("maintainer policy leaked into default core hub: %s", content)
	}

	// Opt-in policy pack
	optsPacked := CompileOptions{RepoRoot: tmp, ProjectName: "test-project", PolicyPack: "maintainer"}
	packed, err := compiler.Compile(optsPacked)
	if err != nil {
		t.Fatalf("Compile (policy pack) error: %v", err)
	}
	if !strings.Contains(packed, "Zero GitHub Workflows") {
		t.Errorf("opt-in maintainer policy pack not applied: %s", packed)
	}

	// Exactly one H1: the hub's own title must not duplicate the project title.
	h1 := 0
	for line := range strings.SplitSeq(content, "\n") {
		if strings.HasPrefix(line, "# ") {
			h1++
		}
	}
	if h1 != 1 {
		t.Errorf("compiled rules have %d H1 headings, want 1:\n%s", h1, content)
	}
}

// Composing into a file that already has a title must not add a second H1.
func TestComposeAgentsFileKeepsOneTitle(t *testing.T) {
	existing := "# My Rules\n\nSome text.\n"
	managed := "# AGENTS.md — project\n\nBody line.\n"
	out := ComposeAgentsFile(existing, managed)
	h1 := 0
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "# ") {
			h1++
		}
	}
	if h1 != 1 {
		t.Fatalf("composed file has %d H1 headings, want 1:\n%s", h1, out)
	}
	if !strings.Contains(out, "My Rules") || strings.Contains(out, "# AGENTS.md — project") {
		t.Fatalf("composed file did not preserve the user title or dropped the wrong one:\n%s", out)
	}

	// With no user content the managed title stays.
	only := ComposeAgentsFile("", managed)
	if !strings.Contains(only, "# AGENTS.md — project") {
		t.Fatalf("managed title missing when the file is new:\n%s", only)
	}
}

func TestDeployTargets(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "demo",
	}

	res, err := DeployTargets(tmp, opts, []string{"claude", "cursor", "devin", "gemini"})
	if err != nil {
		t.Fatalf("DeployTargets: %v", err)
	}

	if len(res.Errors) > 0 {
		t.Fatalf("expected no errors, got: %v", res.Errors)
	}

	// Verify AGENTS.md exists
	if _, err := os.Stat(filepath.Join(tmp, "AGENTS.md")); err != nil {
		t.Errorf("AGENTS.md missing: %v", err)
	}

	// Verify CLAUDE.md exists
	if _, err := os.Stat(filepath.Join(tmp, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md missing: %v", err)
	}

	// Verify GEMINI.md exists
	if _, err := os.Stat(filepath.Join(tmp, "GEMINI.md")); err != nil {
		t.Errorf("GEMINI.md missing: %v", err)
	}

	// Verify Cursor rule exists
	if _, err := os.Stat(filepath.Join(tmp, ".cursor", "rules", "000-sila-core.mdc")); err != nil {
		t.Errorf(".cursor/rules/000-sila-core.mdc missing: %v", err)
	}

	// Verify Devin script exists
	if _, err := os.Stat(filepath.Join(tmp, ".devin", "setup.sh")); err != nil {
		t.Errorf(".devin/setup.sh missing: %v", err)
	}
}

func TestDetectDrift(t *testing.T) {
	tmp := t.TempDir()
	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "demo",
	}

	// Initially empty repo has drift
	drift, err := DetectDrift(tmp, opts)
	if err != nil {
		t.Fatalf("DetectDrift: %v", err)
	}
	if !drift.HasDrift {
		t.Errorf("expected drift on empty repository")
	}

	// Deploy targets
	_, err = DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("DeployTargets: %v", err)
	}

	// Now drift should be clean
	drift2, err := DetectDrift(tmp, opts)
	if err != nil {
		t.Fatalf("DetectDrift after deploy: %v", err)
	}
	if drift2.HasDrift {
		t.Errorf("expected no drift after deploy, details: %v", drift2.Details)
	}
}

func TestCompilerComplianceDomain(t *testing.T) {
	// Look up repo root which contains agents/
	repoRoot := filepath.Join("..", "..")
	compiler := NewCompiler(repoRoot)
	domains := compiler.ListDomains()

	hasCompliance := false
	for _, d := range domains {
		if d == "compliance" {
			hasCompliance = true
			break
		}
	}
	if !hasCompliance {
		t.Fatalf("expected ListDomains() to include 'compliance', got: %v", domains)
	}

	opts := CompileOptions{
		RepoRoot:    t.TempDir(),
		ProjectName: "compliance-test",
		Domains:     []string{"compliance"},
	}
	compiled, err := compiler.Compile(opts)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if !strings.Contains(compiled, "AGENTS.compliance.md") && !strings.Contains(compiled, "Legal Compliance") {
		t.Errorf("expected compliance domain content in compiled rules, got: %s", compiled)
	}
}

func TestFilterRulesForStack_MultilineAndEmptyHeaders(t *testing.T) {
	sample := `# Rules

## 0. Hard Prohibitions
- **Zero GitHub Workflows**: All builds must be local.
- **No Python or Bash automation scripts**: All helper utilities,
  sync jobs, and CLI
  tools MUST be written in Go.
- **No unapproved package managers**: Use pnpm.

## 5. Scripting & Tooling Language Standards
- **Go is the mandatory language for scripts**: All automation, helper utilities, CLI
  tools, sync jobs, and scrapers MUST be written in Go.
- **Pure-Go SQLite engine**: Always use modernc.org/sqlite with CGO_ENABLED=0 only.
- **No Python/Bash sprawl**: Do not write Python or Bash scripts.

## 6. General Rules
- Keep code clean.
`

	filtered := FilterRulesForStack(sample, "node")

	// Multi-line Go rule must be completely gone, not leaving dangling fragments
	if strings.Contains(filtered, "No Python or Bash automation scripts") {
		t.Errorf("failed to filter first line of multiline Go rule:\n%s", filtered)
	}
	if strings.Contains(filtered, "tools MUST be written in Go") {
		t.Errorf("failed to filter continuation line of multiline Go rule:\n%s", filtered)
	}
	if strings.Contains(filtered, "All helper utilities,") {
		t.Errorf("dangling fragment found in filtered output:\n%s", filtered)
	}

	// Empty section heading ## 5 should be removed
	if strings.Contains(filtered, "Scripting & Tooling Language Standards") {
		t.Errorf("expected empty section heading to be stripped:\n%s", filtered)
	}

	// Non-Go rules should remain intact
	if !strings.Contains(filtered, "Zero GitHub Workflows") {
		t.Errorf("non-Go rule stripped unexpectedly:\n%s", filtered)
	}
	if !strings.Contains(filtered, "Keep code clean.") {
		t.Errorf("general rule stripped unexpectedly:\n%s", filtered)
	}
}

func TestDetectStack(t *testing.T) {
	// Node project
	tmpNode := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpNode, "package.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DetectStack(tmpNode); got != "node" {
		t.Errorf("DetectStack(node) = %q, want 'node'", got)
	}

	// Python project
	tmpPy := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpPy, "pyproject.toml"), []byte("[project]\nname='test'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DetectStack(tmpPy); got != "python" {
		t.Errorf("DetectStack(python) = %q, want 'python'", got)
	}

	// Go project
	tmpGo := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpGo, "go.mod"), []byte("module example.com/test\n\ngo 1.26\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DetectStack(tmpGo); got != "go" {
		t.Errorf("DetectStack(go) = %q, want 'go'", got)
	}

	// Polyglot without Go
	tmpPolyNoGo := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpPolyNoGo, "package.json"), []byte(`{"name":"test"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpPolyNoGo, "pyproject.toml"), []byte("[project]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DetectStack(tmpPolyNoGo); got != "polyglot-no-go" {
		t.Errorf("DetectStack(polyglot-no-go) = %q, want 'polyglot-no-go'", got)
	}
}

func TestDeployTargets_AutoDetectsStackAndGuardsTools(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "pyproject.toml"), []byte("[project]\nname='test'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "test-py",
	}

	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("DeployTargets failed: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("DeployTargets errors: %v", res.Errors)
	}

	agentsData, err := os.ReadFile(filepath.Join(tmp, "AGENTS.md"))
	if err != nil {
		t.Fatalf("missing AGENTS.md: %v", err)
	}
	agentsStr := string(agentsData)
	if strings.Contains(agentsStr, "CGO_ENABLED=0") {
		t.Errorf("AGENTS.md contains CGO_ENABLED=0 in a python project: %s", agentsStr)
	}
	if strings.Contains(agentsStr, "No Python or Bash automation scripts") {
		t.Errorf("AGENTS.md contains Go-scripting invariant in a python project: %s", agentsStr)
	}

	for _, unprompted := range []string{"CLAUDE.md", "GEMINI.md", ".cursor", ".devin"} {
		p := filepath.Join(tmp, unprompted)
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("unprompted path %q should not exist on disk", unprompted)
		}
	}
}

func TestDeployTargets_DeploysToolsWhenIntegrationPresent(t *testing.T) {
	tmp := t.TempDir()
	for _, dir := range []string{".claude", ".gemini", ".cursor", ".devin"} {
		if err := os.MkdirAll(filepath.Join(tmp, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}

	opts := CompileOptions{
		RepoRoot:    tmp,
		ProjectName: "test-integrated",
	}

	res, err := DeployTargets(tmp, opts, []string{"all"})
	if err != nil {
		t.Fatalf("DeployTargets failed: %v", err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("DeployTargets errors: %v", res.Errors)
	}

	// Verify tool files were deployed
	if _, err := os.Lstat(filepath.Join(tmp, "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md missing despite .claude harness: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(tmp, "GEMINI.md")); err != nil {
		t.Errorf("GEMINI.md missing despite .gemini harness: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".cursor", "rules", "000-sila-core.mdc")); err != nil {
		t.Errorf(".cursor rule missing despite .cursor harness: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".devin", "setup.sh")); err != nil {
		t.Errorf(".devin/setup.sh missing despite .devin harness: %v", err)
	}
}

func TestCompileProjectManagedBlock(t *testing.T) {
	c := NewCompiler("")
	opts := CompileOptions{
		ProjectName: "test-proj",
	}
	out, err := c.CompileProjectManagedBlock(opts)
	if err != nil {
		t.Fatalf("CompileProjectManagedBlock failed: %v", err)
	}

	if len(out) > 3500 {
		t.Errorf("output size %d bytes exceeds 3,500 byte limit", len(out))
	}
	if !strings.Contains(out, "## 0. Safety Rails") {
		t.Errorf("missing Safety Rails section")
	}
	if !strings.Contains(out, "## 1. Working Relationship & Discipline") {
		t.Errorf("missing Working Relationship & Discipline section")
	}
	if !strings.Contains(out, "## Sila Trigger Contract") {
		t.Errorf("missing Sila Trigger Contract section")
	}
	if strings.Contains(out, "## Plain English") {
		t.Errorf("contains global Plain English guide which should be excluded from project block")
	}
	if strings.Contains(out, "## Working with Node.js and npm") {
		t.Errorf("contains global Node.js rules which should be excluded from project block")
	}
}

func TestTriggerContractFullSurfaceBudget(t *testing.T) {
	lines := strings.Count(strings.TrimSpace(TriggerContract), "\n") + 1
	if lines > 16 {
		t.Errorf("TriggerContract has %d lines, want <=16", lines)
	}
	if len(TriggerContract) > 1400 {
		t.Errorf("TriggerContract is %d bytes, want <=1400", len(TriggerContract))
	}
	for _, want := range []string{
		"sila search", "sila doc search", "sila standup", "sila digest", "sila recap",
		"sila guide", "sila learn", "sila persona", "sila hire", "sila audit", "sila skill",
		"sila edit-check", "sila safe-delete", "sila note", "sila decision", "sila vision triage",
		"omni_search", "get_message_context", "search_docs", "get_operational_guidebook",
		"audit_goal", "skill_search", "skill_load", "create_decision",
	} {
		if !strings.Contains(TriggerContract, want) {
			t.Errorf("TriggerContract missing %q", want)
		}
	}
	t.Logf("TriggerContract: %d lines, %d bytes", lines, len(TriggerContract))
}

func TestHubCarriesTriggerContractVerbatim(t *testing.T) {
	hub, err := os.ReadFile(filepath.Join("..", "..", "agents", "AGENTS.latest.md"))
	if err != nil {
		t.Fatalf("read agents/AGENTS.latest.md: %v", err)
	}
	if !strings.Contains(string(hub), TriggerContract) {
		t.Fatal("agents/AGENTS.latest.md does not contain the shared TriggerContract verbatim")
	}
	if !strings.Contains(DefaultCoreHubContent, TriggerContract) {
		t.Fatal("DefaultCoreHubContent does not contain the shared TriggerContract verbatim")
	}
}

func TestCompileProjectManagedBlockFullSurface(t *testing.T) {
	c := NewCompiler("")
	out, err := c.CompileProjectManagedBlock(CompileOptions{ProjectName: "routing-proj"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "## Sila Trigger Contract"); got != 1 {
		t.Fatalf("managed block has %d trigger contracts, want 1", got)
	}
	for _, want := range []string{"sila search", "sila standup", "sila guide", "sila edit-check", "sila decision", "omni_search", "audit_goal"} {
		if !strings.Contains(out, want) {
			t.Errorf("managed block missing %q", want)
		}
	}
	if len(out) >= MaxRuleFileBytes {
		t.Fatalf("managed block is %d bytes, must stay below %d", len(out), MaxRuleFileBytes)
	}
	t.Logf("managed block: %d bytes", len(out))

	again, err := c.CompileProjectManagedBlock(CompileOptions{ProjectName: "routing-proj"})
	if err != nil {
		t.Fatal(err)
	}
	if out != again {
		t.Fatal("CompileProjectManagedBlock is not idempotent")
	}
}

func TestCompileHubWithTriggerContractDoesNotDuplicate(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "agents", "AGENTS.latest.md"), []byte(DefaultCoreHubContent), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCompiler(tmp)
	out, err := c.Compile(CompileOptions{RepoRoot: tmp, ProjectName: "routing-proj"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(out, "## Sila Trigger Contract"); got != 1 {
		t.Fatalf("compiled hub has %d trigger contracts, want 1", got)
	}
	for _, want := range []string{
		"sila search", "sila doc search", "sila standup", "sila digest", "sila guide",
		"sila learn", "sila persona", "sila hire", "sila audit", "sila skill",
		"sila edit-check", "sila safe-delete", "sila note", "sila decision",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("compiled hub missing %q", want)
		}
	}
	if len(out) >= MaxRuleFileBytes {
		t.Fatalf("compiled hub is %d bytes, must stay below %d", len(out), MaxRuleFileBytes)
	}
}

func TestCompileAndManagedBlockShareManagedBody(t *testing.T) {
	tmp := t.TempDir()
	c := NewCompiler(tmp)
	opts := CompileOptions{RepoRoot: tmp, ProjectName: "hash-proj"}
	full, err := c.Compile(opts)
	if err != nil {
		t.Fatal(err)
	}
	block, err := c.CompileProjectManagedBlock(opts)
	if err != nil {
		t.Fatal(err)
	}
	if HashManagedBody(full) == HashManagedBody(block) {
		return
	}
	fl := strings.Split(full, "\n")
	bl := strings.Split(block, "\n")
	for i := 0; i < len(fl) && i < len(bl); i++ {
		if fl[i] != bl[i] {
			t.Fatalf("managed body diverges at line %d:\nfull:  %q\nblock: %q", i+1, fl[i], bl[i])
		}
	}
	t.Fatalf("managed body lengths diverge: full=%d block=%d", len(full), len(block))
}
