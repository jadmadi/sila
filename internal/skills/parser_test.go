package skills

import (
	"strings"
	"testing"
)

func TestParseSkillContent_Basic(t *testing.T) {
	content := `---
name: simple-skill
description: A simple test skill
tags:
  - testing
  - go
---
# Simple Skill
Instructions here.
`
	pf := ParseSkillContent("simple-skill", content)
	if pf.Name != "simple-skill" {
		t.Errorf("got name %q, want %q", pf.Name, "simple-skill")
	}
	if pf.Description != "A simple test skill" {
		t.Errorf("got description %q, want %q", pf.Description, "A simple test skill")
	}
	if len(pf.Tags) != 2 || pf.Tags[0] != "testing" || pf.Tags[1] != "go" {
		t.Errorf("unexpected tags: %v", pf.Tags)
	}
	if !strings.Contains(pf.Body, "# Simple Skill") {
		t.Errorf("expected body to contain header, got %q", pf.Body)
	}
}

func TestParseSkillContent_FoldedBlockScalar(t *testing.T) {
	content := `---
name: cf-cpu-audit
description: >-
  Scan a Cloudflare Workers repository for operations that excessively consume
  CPU time limits. Identifies global scope misses, memory buffering, pure-JS crypto,
  blocking iterations, and missing waitUntil() deferral.
license: MIT
---
# Body
`
	pf := ParseSkillContent("cf-cpu-audit", content)
	if !strings.HasPrefix(pf.Description, "Scan a Cloudflare Workers repository") {
		t.Errorf("failed to parse folded block scalar, got %q", pf.Description)
	}
	if strings.Contains(pf.Description, "\n") {
		t.Errorf("folded block scalar should join consecutive lines, got %q", pf.Description)
	}
	if !strings.HasSuffix(pf.Description, "waitUntil() deferral.") {
		t.Errorf("folded block scalar should end with last sentence, got %q", pf.Description)
	}
}

func TestParseSkillContent_LiteralBlockScalar(t *testing.T) {
	content := `---
name: multi-line-code
description: |-
  Line 1
  Line 2
  Line 3
---
# Instructions
`
	pf := ParseSkillContent("multi-line-code", content)
	expected := "Line 1\nLine 2\nLine 3"
	if pf.Description != expected {
		t.Errorf("literal block scalar got %q, want %q", pf.Description, expected)
	}
}

func TestParseSkillContent_FlowListAndQuotes(t *testing.T) {
	content := `---
name: "quoted-name"
description: 'single-quoted description'
tags: ["alpha", "beta", 'gamma']
compatibility: [cli, mcp]
---
Content
`
	pf := ParseSkillContent("fallback", content)
	if pf.Name != "quoted-name" {
		t.Errorf("got name %q, want %q", pf.Name, "quoted-name")
	}
	if pf.Description != "single-quoted description" {
		t.Errorf("got description %q, want %q", pf.Description, "single-quoted description")
	}
	if len(pf.Tags) != 3 || pf.Tags[0] != "alpha" || pf.Tags[1] != "beta" || pf.Tags[2] != "gamma" {
		t.Errorf("unexpected tags: %v", pf.Tags)
	}
	if len(pf.Compatibility) != 2 || pf.Compatibility[0] != "cli" || pf.Compatibility[1] != "mcp" {
		t.Errorf("unexpected compatibility: %v", pf.Compatibility)
	}
}
