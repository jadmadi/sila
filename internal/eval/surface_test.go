package eval

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSurfaceBudgetAndAutoTrigger(t *testing.T) {
	t.Run("auto-injects missing skill trigger when within AlwaysOnMax", func(t *testing.T) {
		orig := ComponentTree{
			"AGENTS.md": "# Core Instructions\nFollow all rules.\n",
			"skills/pdf-layout/SKILL.md": strings.Join([]string{
				"---",
				"name: pdf-layout",
				"description: inspecting PDF visual layout",
				"---",
				"# PDF Layout Skill",
				"Run `scripts/check_pdf.py` to inspect layout.",
			}, "\n"),
			"scripts/check_pdf.py": "#!/usr/bin/env python3\nprint('ok')\n",
		}

		updated := AutoTriggerSkills(orig, CodexSurface)
		if strings.Contains(orig["AGENTS.md"], "pdf-layout") {
			t.Fatalf("AutoTriggerSkills mutated original tree in place")
		}
		wantCue := "- When inspecting PDF visual layout, load the pdf-layout skill."
		if !strings.Contains(updated["AGENTS.md"], wantCue) {
			t.Fatalf("expected AGENTS.md to contain %q, got:\n%s", wantCue, updated["AGENTS.md"])
		}
		if err := ValidateTree(updated, CodexSurface); err != nil {
			t.Fatalf("expected ValidateTree to pass after AutoTriggerSkills, got: %v", err)
		}
	})

	t.Run("does not auto-inject when trigger would exceed AlwaysOnMax", func(t *testing.T) {
		nearLimit := strings.Repeat("a", CodexSurface.AlwaysOnMax-10)
		orig := ComponentTree{
			"AGENTS.md": nearLimit,
			"skills/pdf-layout/SKILL.md": strings.Join([]string{
				"---",
				"name: pdf-layout",
				"description: inspecting PDF visual layout",
				"---",
				"Body",
			}, "\n"),
		}
		updated := AutoTriggerSkills(orig, CodexSurface)
		if updated["AGENTS.md"] != nearLimit {
			t.Fatalf("expected AutoTriggerSkills not to overflow budget")
		}
		err := ValidateTree(updated, CodexSurface)
		if err == nil || !strings.Contains(err.Error(), "never mentioned in always-on") {
			t.Fatalf("expected missing skill trigger error from ValidateTree, got: %v", err)
		}
	})

	t.Run("read write and diff component trees deterministically", func(t *testing.T) {
		tmp := t.TempDir()
		base := ComponentTree{
			"AGENTS.md":                 "# Base\n- When checking csv, load the csv-audit skill.\n",
			"skills/csv-audit/SKILL.md": "---\ndescription: checking csv\n---\nUse scripts/audit.py\n",
			"scripts/audit.py":          "print(1)\n",
		}
		if err := WriteTree(tmp, base); err != nil {
			t.Fatalf("WriteTree error: %v", err)
		}
		loaded, err := ReadTree(tmp, CodexSurface)
		if err != nil {
			t.Fatalf("ReadTree error: %v", err)
		}
		if len(loaded) != 3 {
			t.Fatalf("expected 3 files in loaded tree, got %d: %v", len(loaded), loaded)
		}
		if err := ValidateTree(loaded, CodexSurface); err != nil {
			t.Fatalf("expected loaded tree to validate cleanly, got: %v", err)
		}

		cand := CloneTree(loaded)
		cand["AGENTS.md"] += "\nExtra rule\n"
		cand[".codex/hooks.json"] = `{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"echo ok"}]}]}}`
		delete(cand, "scripts/audit.py")

		diffs := DiffComponentTrees(loaded, cand, CodexSurface)
		if len(diffs) != 3 {
			t.Fatalf("expected 3 diffs, got %d: %+v", len(diffs), diffs)
		}
		if diffs[0].Path != ".codex/hooks.json" || diffs[0].Component != CompHooks || diffs[0].Action != "added" {
			t.Fatalf("unexpected diff[0]: %+v", diffs[0])
		}
		if diffs[1].Path != "AGENTS.md" || diffs[1].Component != CompSystemPrompt || diffs[1].Action != "modified" {
			t.Fatalf("unexpected diff[1]: %+v", diffs[1])
		}
		if diffs[2].Path != filepath.ToSlash("scripts/audit.py") || diffs[2].Component != CompScripts || diffs[2].Action != "deleted" {
			t.Fatalf("unexpected diff[2]: %+v", diffs[2])
		}
	})
}

func TestSurfaceRejectInvalid(t *testing.T) {
	cases := []struct {
		name    string
		spec    SurfaceSpec
		tree    ComponentTree
		wantErr string
	}{
		{
			name:    "empty tree",
			spec:    CodexSurface,
			tree:    ComponentTree{},
			wantErr: "harness tree is empty",
		},
		{
			name: "missing primary always-on file",
			spec: CodexSurface,
			tree: ComponentTree{
				"scripts/helper.py": "print('hi')",
			},
			wantErr: "AGENTS.md is missing or empty",
		},
		{
			name: "always-on budget overflow",
			spec: CodexSurface,
			tree: ComponentTree{
				"AGENTS.md": strings.Repeat("x", CodexSurface.AlwaysOnMax+25),
			},
			wantErr: "exceeds budget",
		},
		{
			name: "skill missing frontmatter description",
			spec: CodexSurface,
			tree: ComponentTree{
				"AGENTS.md":                    "Use broken-skill when needed.",
				"skills/broken-skill/SKILL.md": "# No frontmatter here\n",
			},
			wantErr: "missing YAML frontmatter with `description:`",
		},
		{
			name: "skill frontmatter name mismatch",
			spec: CodexSurface,
			tree: ComponentTree{
				"AGENTS.md": "Use right-dir when needed.",
				"skills/right-dir/SKILL.md": strings.Join([]string{
					"---",
					"name: wrong-name",
					"description: doing something",
					"---",
					"Body",
				}, "\n"),
			},
			wantErr: "frontmatter `name: wrong-name` does not match directory `right-dir`",
		},
		{
			name: "dangling script reference in skill",
			spec: CodexSurface,
			tree: ComponentTree{
				"AGENTS.md": "When auditing rows, load the row-check skill.",
				"skills/row-check/SKILL.md": strings.Join([]string{
					"---",
					"name: row-check",
					"description: auditing rows",
					"---",
					"Run scripts/check_rows.py on the output.",
				}, "\n"),
			},
			wantErr: "skills/row-check/SKILL.md references scripts/check_rows.py which is not in the harness tree",
		},
		{
			name: "subagent missing description frontmatter",
			spec: OpenCodeSurface,
			tree: ComponentTree{
				"prompts/systemprompt.md":      "Core system prompt",
				"sub_agents/reviewer/AGENT.md": "Just markdown without frontmatter",
			},
			wantErr: "sub_agents/reviewer/AGENT.md: missing YAML frontmatter with `description:`",
		},
		{
			name: "malformed hooks.json invalid event",
			spec: CodexSurface,
			tree: ComponentTree{
				"AGENTS.md":         "Core rules",
				".codex/hooks.json": `{"hooks":{"InvalidEvent":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
			},
			wantErr: "unknown event 'InvalidEvent'",
		},
		{
			name: "leaked grader verdict marker",
			spec: CodexSurface,
			tree: ComponentTree{
				"AGENTS.md": "Core rules\nPASS: grader_01 verified",
			},
			wantErr: "leaked grader verdict or rubric marker",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTree(tc.tree, tc.spec)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}
