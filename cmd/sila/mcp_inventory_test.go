package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
)

// repoFile reads a repository file from the cmd/sila test working directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// markdownSection returns content between the first start marker and the next
// end marker after it, or the whole document when either marker is absent.
func markdownSection(content, start, end string) string {
	i := strings.Index(content, start)
	if i < 0 {
		return content
	}
	content = content[i:]
	if j := strings.Index(content, end); j > 0 {
		content = content[:j]
	}
	return content
}

// TestDocumentedMCPToolCountsMatchRegistry keeps every advertised tool count
// equal to the executable MCP registry size.
func TestDocumentedMCPToolCountsMatchRegistry(t *testing.T) {
	surfaces := []struct {
		name    string
		file    string
		pattern *regexp.Regexp
	}{
		{"README.md (heading)", "README.md", regexp.MustCompile(`Model Context Protocol \(MCP\) Server \((\d+) Tools\)`)},
		{"README.md (map docs/mcp)", "README.md", regexp.MustCompile(`all (\d+) tools`)},
		{"README.md (map html-docs)", "README.md", regexp.MustCompile(`(\d+)-tool MCP reference`)},
		{"docs/mcp.md", "docs/mcp.md", regexp.MustCompile(`## Exposed Tools \((\d+)\)`)},
		{"AGENTS.md", "AGENTS.md", regexp.MustCompile(`(\d+)-tool Model Context Protocol`)},
		{"HANDOFF.md", "HANDOFF.md", regexp.MustCompile(`canonical (\d+)-tool MCP inventory`)},
	}
	for _, s := range surfaces {
		if s.file == "HANDOFF.md" {
			if _, err := os.Stat(filepath.Join("..", "..", s.file)); err != nil {
				t.Logf("HANDOFF.md is private; skipping its MCP count surface")
				continue
			}
		}
		content := repoFile(t, s.file)
		m := s.pattern.FindStringSubmatch(content)
		if m == nil {
			t.Errorf("%s: no declared MCP tool count matching %s", s.name, s.pattern)
			continue
		}
		got, err := strconv.Atoi(m[1])
		if err != nil {
			t.Errorf("%s: parse MCP tool count %q: %v", s.name, m[1], err)
			continue
		}
		if got != mcp.PublicToolCount {
			t.Errorf("%s declares %d MCP tools, registry has %d", s.name, got, mcp.PublicToolCount)
		}
	}

	intro := introMarkdown("", "test.db")
	want := fmt.Sprintf("(%d tools)", mcp.PublicToolCount)
	if !strings.Contains(intro, want) {
		t.Errorf("sila intro does not advertise the registry count %q", want)
	}
}

// TestDocumentedMCPInventoryListsEveryTool keeps the canonical catalog in
// docs/mcp.md in step with the executable registry, and keeps README linked to
// that catalog instead of duplicating it.
func TestDocumentedMCPInventoryListsEveryTool(t *testing.T) {
	tools := mcp.ToolDefinitions()
	section := markdownSection(repoFile(t, "docs/mcp.md"), "## Exposed Tools", "\n---\n")
	var missing []string
	for _, tool := range tools {
		if !strings.Contains(section, "`"+tool.Name+"`") {
			missing = append(missing, tool.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("docs/mcp.md is missing %d MCP tool(s): %v", len(missing), missing)
	}

	if readme := repoFile(t, "README.md"); !strings.Contains(readme, "docs/mcp.md") {
		t.Error("README.md does not link to the canonical MCP tool catalog (docs/mcp.md)")
	}
}

// TestDocumentedMCPPropertiesMatchRegistry validates that all executable tool parameters
// are documented in the canonical docs/mcp.md catalog.
func TestDocumentedMCPPropertiesMatchRegistry(t *testing.T) {
	tools := mcp.ToolDefinitions()
	section := markdownSection(repoFile(t, "docs/mcp.md"), "## Exposed Tools", "\n---\n")
	lines := strings.Split(section, "\n")
	toolParams := make(map[string]string)
	for _, l := range lines {
		if strings.HasPrefix(l, "| `") {
			parts := strings.Split(l, "|")
			if len(parts) >= 6 {
				name := strings.Trim(parts[1], " `")
				toolParams[name] = parts[5]
			}
		}
	}

	for _, tool := range tools {
		params, ok := toolParams[tool.Name]
		if !ok {
			t.Errorf("tool %s not found in docs/mcp.md table", tool.Name)
			continue
		}
		for prop := range tool.InputSchema.Properties {
			if !strings.Contains(params, "`"+prop+"`") {
				t.Errorf("tool %s parameter `%s` is missing from docs/mcp.md table", tool.Name, prop)
			}
		}
	}
}

// TestTier0AndTriggerMCPToolsExistInContinuityProfile locks the rulebook and
// the continuity profile together: a tool named in Tier-0 instructions or in
// the compiled trigger/prime guidance must be reachable without --profile=full.
func TestTier0AndTriggerMCPToolsExistInContinuityProfile(t *testing.T) {
	if len(mcp.ContinuityToolNames) != mcp.ContinuityToolCount {
		t.Fatalf("ContinuityToolNames has %d entries, ContinuityToolCount is %d", len(mcp.ContinuityToolNames), mcp.ContinuityToolCount)
	}
	if store.DefaultContinuityToolCount != mcp.ContinuityToolCount {
		t.Fatalf("store.DefaultContinuityToolCount = %d, mcp.ContinuityToolCount = %d", store.DefaultContinuityToolCount, mcp.ContinuityToolCount)
	}

	defNames := map[string]bool{}
	for _, def := range mcp.ToolDefinitions() {
		defNames[def.Name] = true
	}
	for name := range mcp.ContinuityToolNames {
		if !defNames[name] {
			t.Errorf("ContinuityToolNames contains phantom tool %q", name)
		}
	}

	required := append([]string(nil), mcp.ContinuityToolOrder...)
	for _, name := range required {
		if !defNames[name] {
			t.Errorf("required MCP tool %q is absent from ToolDefinitions", name)
		}
		if !mcp.ContinuityToolNames[name] {
			t.Errorf("required MCP tool %q is excluded from the continuity profile", name)
		}
	}

	agents := repoFile(t, "AGENTS.md")
	guidance := agents +
		repoFile(t, "internal/agents/compiler.go") +
		repoFile(t, "internal/agents/principles.go") +
		repoFile(t, "internal/dream/prime.go")
	for _, name := range required {
		// get_project_facts is the structured-facts retrieval primitive; the
		// prose surfaces route to it through `sila recall`/`omni_search`.
		if name == "get_project_facts" || name == "get_status" {
			continue
		}
		if !strings.Contains(guidance, name) {
			t.Errorf("required continuity tool %q is not referenced by any rulebook or briefing surface", name)
		}
	}
	tier0Defs := map[string]bool{}
	for _, def := range mcp.ToolDefinitions() {
		tier0Defs[def.Name] = true
	}
	for _, tier0 := range []string{"salam", "prime_context", "list_goals", "run_gate_pipeline"} {
		if !strings.Contains(agents, tier0) {
			t.Errorf("AGENTS.md no longer names Tier-0 MCP tool %q", tier0)
		}
		if !tier0Defs[tier0] {
			t.Errorf("Tier-0 MCP tool %q is not defined", tier0)
		}
	}
}
