package skillcreator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillDraft holds structured components for a skill before file synthesis.
type SkillDraft struct {
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Overview    string            `json:"overview"`
	Triggers    []string          `json:"triggers"`
	Workflows   []string          `json:"workflows"`
	Invariants  []string          `json:"invariants"`
	Rubric      []string          `json:"rubric"`
	Scripts     map[string]string `json:"scripts,omitempty"`
	References  map[string]string `json:"references,omitempty"`
}

// ScaffoldResult summarizes the generated skill directory.
type ScaffoldResult struct {
	SkillDir     string   `json:"skill_dir"`
	SkillMDPath  string   `json:"skill_md_path"`
	FilesWritten []string `json:"files_written"`
	BytesWritten int      `json:"bytes_written"`
}

// ScaffoldSkill writes the canonical skill package to .agents/skills/<slug>/.
func ScaffoldSkill(projectDir string, draft SkillDraft, force bool) (*ScaffoldResult, error) {
	if draft.Slug == "" {
		return nil, fmt.Errorf("skill slug cannot be empty")
	}
	if draft.Description == "" {
		return nil, fmt.Errorf("skill description cannot be empty")
	}

	cleanSlug := filepath.Clean(draft.Slug)
	if cleanSlug == "." || cleanSlug == ".." || filepath.Base(cleanSlug) != cleanSlug || strings.ContainsAny(draft.Slug, `/\\`) {
		return nil, fmt.Errorf("invalid skill slug %q: must not contain path separators or parent directory references", draft.Slug)
	}

	skillDir := filepath.Join(projectDir, ".agents", "skills", cleanSlug)
	skillMDPath := filepath.Join(skillDir, "SKILL.md")

	if _, err := os.Stat(skillMDPath); err == nil && !force {
		return nil, fmt.Errorf("skill already exists at %s (use --force to overwrite)", skillMDPath)
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return nil, fmt.Errorf("create skill directory %s: %w", skillDir, err)
	}

	var filesWritten []string
	totalBytes := 0

	// Assemble SKILL.md
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", cleanSlug)
	fmt.Fprintf(&b, "description: %s\n", strings.TrimSpace(draft.Description))
	b.WriteString("---\n\n")

	title := draft.Name
	if title == "" {
		title = cleanSlug
	}
	fmt.Fprintf(&b, "# %s\n\n", title)

	if draft.Overview != "" {
		b.WriteString("## Overview\n\n")
		b.WriteString(strings.TrimSpace(draft.Overview))
		b.WriteString("\n\n")
	}

	if len(draft.Triggers) > 0 {
		b.WriteString("## Trigger Scenarios\n\n")
		b.WriteString("Activate this skill whenever:\n")
		for _, t := range draft.Triggers {
			fmt.Fprintf(&b, "- %s\n", t)
		}
		b.WriteString("\n")
	}

	if len(draft.Workflows) > 0 {
		b.WriteString("## Core Workflows\n\n")
		for i, w := range draft.Workflows {
			if strings.HasPrefix(w, "###") || strings.HasPrefix(w, "1.") {
				b.WriteString(w + "\n\n")
			} else {
				fmt.Fprintf(&b, "### Step %d: %s\n\n", i+1, w)
			}
		}
	}

	if len(draft.Invariants) > 0 {
		b.WriteString("## Project Invariants & Non-Negotiables\n\n")
		for _, inv := range draft.Invariants {
			fmt.Fprintf(&b, "- ⚠️ **%s**\n", inv)
		}
		b.WriteString("\n")
	}

	// Embedded Self-Improvement & Quality Rubric
	b.WriteString("## Verification & Self-Improvement Rubric\n\n")
	b.WriteString("Before declaring this skill execution complete, the executing agent must verify:\n")
	if len(draft.Rubric) > 0 {
		for _, r := range draft.Rubric {
			fmt.Fprintf(&b, "- [ ] %s\n", r)
		}
	} else {
		b.WriteString("- [ ] All acceptance criteria in the prompt were explicitly validated.\n")
		b.WriteString("- [ ] Zero regressions or broken tests under `-race` test suites.\n")
		b.WriteString("- [ ] Code strictly honors repository architectural boundaries.\n")
	}
	b.WriteString("\n### Self-Improvement Feedback Loop\n")
	b.WriteString("- If this skill encounters an unexpected edge case, execution bug, or suboptimal output, ")
	b.WriteString("the agent must record the finding using `sila lesson` or `sila feedback` so subsequent executions self-improve.\n")

	data := []byte(b.String())
	if err := os.WriteFile(skillMDPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", skillMDPath, err)
	}
	filesWritten = append(filesWritten, skillMDPath)
	totalBytes += len(data)

	// Scaffold scripts/ if provided (with path traversal security sentry)
	if len(draft.Scripts) > 0 {
		scriptsDir := filepath.Join(skillDir, "scripts")
		if err := os.MkdirAll(scriptsDir, 0755); err != nil {
			return nil, fmt.Errorf("create scripts dir %s: %w", scriptsDir, err)
		}
		for name, content := range draft.Scripts {
			cleanName := filepath.Clean(name)
			if cleanName == "." || cleanName == ".." || filepath.Base(cleanName) != cleanName || strings.ContainsAny(name, `/\\`) {
				return nil, fmt.Errorf("invalid script filename %q: must not contain path separators or traversal", name)
			}
			sPath := filepath.Join(scriptsDir, cleanName)
			if err := os.WriteFile(sPath, []byte(content), 0755); err != nil {
				return nil, fmt.Errorf("write script %s: %w", sPath, err)
			}
			filesWritten = append(filesWritten, sPath)
			totalBytes += len(content)
		}
	}

	// Scaffold references/ if provided (with path traversal security sentry)
	if len(draft.References) > 0 {
		refsDir := filepath.Join(skillDir, "references")
		if err := os.MkdirAll(refsDir, 0755); err != nil {
			return nil, fmt.Errorf("create references dir %s: %w", refsDir, err)
		}
		for name, content := range draft.References {
			cleanName := filepath.Clean(name)
			if cleanName == "." || cleanName == ".." || filepath.Base(cleanName) != cleanName || strings.ContainsAny(name, `/\\`) {
				return nil, fmt.Errorf("invalid reference filename %q: must not contain path separators or traversal", name)
			}
			rPath := filepath.Join(refsDir, cleanName)
			if err := os.WriteFile(rPath, []byte(content), 0644); err != nil {
				return nil, fmt.Errorf("write reference %s: %w", rPath, err)
			}
			filesWritten = append(filesWritten, rPath)
			totalBytes += len(content)
		}
	}

	return &ScaffoldResult{
		SkillDir:     skillDir,
		SkillMDPath:  skillMDPath,
		FilesWritten: filesWritten,
		BytesWritten: totalBytes,
	}, nil
}

// DefaultSkillDraft creates a customized initial draft based on project grounding and parsed topic.
func DefaultSkillDraft(grounding *ProjectGrounding, topic SkillTopic) SkillDraft {
	stackStr := "the project"
	if len(grounding.TechStack) > 0 {
		stackStr = strings.Join(grounding.TechStack, " & ")
	}

	desc := fmt.Sprintf("Execute %s workflows tailored for %s. Use when performing %s auditing, verification, or task implementation.",
		topic.Title, stackStr, topic.Slug)

	overview := fmt.Sprintf("This skill provides an automated, project-specific procedure for %s within %s. "+
		"It enforces architectural invariants, verification gates, and quality standards established in the codebase.",
		strings.ToLower(topic.Title), grounding.ProjectID)

	triggers := []string{
		fmt.Sprintf("User explicitly asks to run, check, or audit %s", strings.ToLower(topic.Title)),
		fmt.Sprintf("The task mentions '%s' or '%s'", topic.Slug, strings.Join(topic.Keywords, " ")),
		fmt.Sprintf("Pre-flight release checks or milestone reviews requiring %s certification", strings.ToLower(topic.Title)),
	}

	workflows := []string{
		fmt.Sprintf("Scope & Inventory: Collect relevant source files, manifests, and documentation for %s.", strings.ToLower(topic.Title)),
		"Execution & Audit: Run domain verification checks against project contracts and quality rubrics.",
		"Evidence & Reporting: Document evidenced findings and generate actionable remediation recommendations.",
	}

	invariants := []string{
		"Never bypass repository test suites or quality gates.",
		"Preserve strict directory isolation and architectural boundaries.",
	}
	if strings.Contains(stackStr, "Go") {
		invariants = append(invariants, "Verify all code changes pass with race detection: go test -race ./...")
	}
	if strings.Contains(stackStr, "Cloudflare") {
		invariants = append(invariants, "Respect Cloudflare Workers CPU and memory isolate constraints.")
	}

	rubric := []string{
		fmt.Sprintf("All %s criteria are validated against actual project code.", strings.ToLower(topic.Title)),
		"Concrete, reproducible evidence is provided for every finding.",
		"No breaking changes or unverified assumptions introduced.",
	}

	return SkillDraft{
		Slug:        topic.Slug,
		Name:        topic.Title,
		Description: desc,
		Overview:    overview,
		Triggers:    triggers,
		Workflows:   workflows,
		Invariants:  invariants,
		Rubric:      rubric,
	}
}
