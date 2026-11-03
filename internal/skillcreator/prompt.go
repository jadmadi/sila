package skillcreator

import (
	"fmt"
	"regexp"
	"strings"
)

// SkillTopic captures parsed skill subject metadata.
type SkillTopic struct {
	RawInput string   `json:"raw_input"`
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Keywords []string `json:"keywords"`
}

var nonAlphaNumRegex = regexp.MustCompile(`[^a-z0-9]+`)

// ParseTopic normalizes arbitrary command line words into a clean skill slug and title.
func ParseTopic(args []string) SkillTopic {
	raw := strings.TrimSpace(strings.Join(args, " "))
	if raw == "" {
		raw = "custom-skill"
	}

	// Clean punctuation and slashes
	cleaned := strings.ReplaceAll(raw, "/", " ")
	tokens := strings.Fields(strings.ToLower(cleaned))

	var keywords []string
	var slugTokens []string

	stopWords := map[string]bool{
		"a": true, "an": true, "the": true, "and": true, "or": true,
		"for": true, "of": true, "to": true, "in": true, "etc": true,
	}

	for _, t := range tokens {
		t = strings.Trim(t, ".,;:-_")
		if t == "" {
			continue
		}
		keywords = append(keywords, t)
		if !stopWords[t] {
			slugTokens = append(slugTokens, t)
		}
	}

	if len(slugTokens) == 0 {
		slugTokens = []string{"custom", "skill"}
	}
	if len(slugTokens) > 6 {
		slugTokens = slugTokens[:6]
	}

	slug := strings.Join(slugTokens, "-")
	slug = nonAlphaNumRegex.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "custom-skill"
	}

	// Title formulation
	var titleWords []string
	for _, w := range strings.Split(slug, "-") {
		if len(w) > 0 {
			titleWords = append(titleWords, strings.ToUpper(w[:1])+w[1:])
		}
	}
	title := strings.Join(titleWords, " ")
	if title == "" {
		title = "Custom Skill"
	}

	return SkillTopic{
		RawInput: raw,
		Slug:     slug,
		Title:    title,
		Keywords: keywords,
	}
}

// AssembleGrillPrompt creates the turnkey multi-methodology master prompt.
func AssembleGrillPrompt(grounding *ProjectGrounding, topic SkillTopic) string {
	var b strings.Builder

	b.WriteString("# Master Prompt: Interactive Tailored Project Skill Creator\n\n")
	fmt.Fprintf(&b, "**Target Skill Slug**: `%s`\n", topic.Slug)
	fmt.Fprintf(&b, "**Target Destination**: `.agents/skills/%s/SKILL.md`\n", topic.Slug)
	fmt.Fprintf(&b, "**Skill Title**: %s\n", topic.Title)
	fmt.Fprintf(&b, "**Requested Domain**: %s\n\n", topic.RawInput)

	b.WriteString("## Mission & Methodological Fusion\n")
	b.WriteString("You are a Principal Agent Systems Architect tasked with authoring a **bespoke, project-tailored agent skill** ")
	b.WriteString("rather than a generic template. You must fuse four distinct methodologies:\n\n")

	b.WriteString("1. **`grill-me` (Adversarial Requirements Interrogation)**:\n")
	b.WriteString("   - Do NOT assume or silently fill in vague requirements.\n")
	b.WriteString("   - Ask the user **ONE focused question at a time** in chat.\n")
	b.WriteString("   - Probe non-negotiable failure boundaries, edge cases, negative examples, and execution guardrails.\n")
	b.WriteString("   - Continue the interview until you achieve ~95% confidence on the exact operational flow.\n\n")

	b.WriteString("2. **`skill-creator` (Authoritative Anatomy & Progressive Disclosure)**:\n")
	b.WriteString("   - Enforce YAML frontmatter with exact `name` and action-oriented `description` (listing clear triggers).\n")
	b.WriteString("   - Organize into: Overview, Trigger Scenarios, Core Workflows, Invariants & Guardrails, and Rubric.\n")
	b.WriteString("   - Keep content concise, high-density, and free of conversational filler.\n\n")

	b.WriteString("3. **`plannotator` (Verifiable Checkpoint Scoping)**:\n")
	b.WriteString("   - Structure workflows into numbered checkpoints that an executing agent can verify and resume from.\n")
	b.WriteString("   - Specify concrete command lines, verification tests, and evidence expectations for each step.\n\n")

	b.WriteString("4. **Self-Improvement Mechanism**:\n")
	b.WriteString("   - Embed a `## Self-Improvement & Quality Rubric` inside the generated `SKILL.md`.\n")
	b.WriteString("   - Define explicit criteria for the agent to audit its own output after executing the skill.\n")
	b.WriteString("   - Hook into Sila's feedback loop (`sila lesson`, `sila feedback`) so execution failures are recorded as durable lessons to improve future runs.\n\n")

	b.WriteString("## Project Ground Truth (Injected Repository Context)\n")
	if len(grounding.TechStack) > 0 {
		fmt.Fprintf(&b, "- **Tech Stack Detected**: %s\n", strings.Join(grounding.TechStack, ", "))
	}
	if len(grounding.Manifests) > 0 {
		fmt.Fprintf(&b, "- **Project Manifests**: %s\n", strings.Join(grounding.Manifests, ", "))
	}
	if len(grounding.ExistingSkills) > 0 {
		fmt.Fprintf(&b, "- **Existing Project Skills**: %s\n", strings.Join(grounding.ExistingSkills, ", "))
	}
	if len(grounding.ActiveGoals) > 0 {
		b.WriteString("- **Active Epistemic Goals**:\n")
		for _, g := range grounding.ActiveGoals {
			fmt.Fprintf(&b, "  • %s\n", g)
		}
	}
	if len(grounding.NegativeLessons) > 0 {
		b.WriteString("- **Past Lessons & Negative Memory** (Do NOT contradict these):\n")
		for _, l := range grounding.NegativeLessons {
			fmt.Fprintf(&b, "  • %s\n", l)
		}
	}
	if grounding.RulesSummary != "" {
		b.WriteString("\n### Project Architecture Rules (`AGENTS.md`):\n```markdown\n")
		b.WriteString(grounding.RulesSummary)
		b.WriteString("\n```\n")
	}

	b.WriteString("\n## Interrogation Protocol\n")
	b.WriteString("To begin, ask the user your FIRST grounding question. Frame questions around the project's actual stack and invariants:\n")
	b.WriteString("1. Inquire about the exact trigger conditions: When should an agent reach for this skill versus standard instructions?\n")
	b.WriteString("2. Clarify the input artifacts and required toolchain/commands (e.g. linters, test harnesses, or MCP servers).\n")
	b.WriteString("3. Probe for non-negotiables: What are the absolute failure modes or forbidden actions for this skill?\n")
	b.WriteString("4. Once aligned (~95% confidence), synthesize the final skill and write it directly to `.agents/skills/" + topic.Slug + "/SKILL.md`.\n")

	return b.String()
}
