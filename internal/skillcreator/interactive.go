package skillcreator

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// RunInteractiveGrill conducts a multi-turn terminal interview to produce a tailored SkillDraft.
func RunInteractiveGrill(ui render.UI, in io.Reader, out io.Writer, grounding *ProjectGrounding, topic SkillTopic) (SkillDraft, error) {
	scanner := bufio.NewScanner(in)

	fmt.Fprintf(out, "%s sila · interactive skill authoring grill\n\n", ui.Cyan("·"))
	fmt.Fprintf(out, "  Skill Target: %s\n", ui.Bold(topic.Title))
	fmt.Fprintf(out, "  Destination:  %s\n", ui.Dim(".agents/skills/"+topic.Slug+"/SKILL.md"))
	if len(grounding.TechStack) > 0 {
		fmt.Fprintf(out, "  Grounded In:  %s\n", strings.Join(grounding.TechStack, ", "))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "I will ask you 4 focused questions to calibrate this skill to your project's exact needs.")
	fmt.Fprintln(out, "Press Enter with empty text to accept suggested defaults.")
	fmt.Fprintln(out)

	draft := DefaultSkillDraft(grounding, topic)

	// Question 1: Triggers & Intent
	fmt.Fprintf(out, "%s %s\n", ui.Bold("Q1 [Triggers]:"), "When should an agent reach for this skill versus standard instructions?")
	fmt.Fprintf(out, "   %s %s\n", ui.Dim("Default:"), draft.Triggers[0])
	fmt.Fprint(out, "   Your Answer > ")
	if scanner.Scan() {
		ans := strings.TrimSpace(scanner.Text())
		if ans != "" {
			draft.Triggers = []string{ans}
		}
	}
	fmt.Fprintln(out)

	// Question 2: Workflows & Execution Steps
	fmt.Fprintf(out, "%s %s\n", ui.Bold("Q2 [Workflows]:"), "What concrete execution steps or CLI commands should the agent perform?")
	fmt.Fprintf(out, "   %s %s\n", ui.Dim("Default:"), draft.Workflows[0])
	fmt.Fprint(out, "   Your Answer > ")
	if scanner.Scan() {
		ans := strings.TrimSpace(scanner.Text())
		if ans != "" {
			var steps []string
			for _, s := range strings.Split(ans, ";") {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					steps = append(steps, trimmed)
				}
			}
			if len(steps) > 0 {
				draft.Workflows = steps
			}
		}
	}
	fmt.Fprintln(out)

	// Question 3: Non-negotiables & Invariants
	fmt.Fprintf(out, "%s %s\n", ui.Bold("Q3 [Invariants]:"), "What must this skill NEVER do? (Forbidden actions / failure boundaries)")
	fmt.Fprintf(out, "   %s %s\n", ui.Dim("Default:"), draft.Invariants[0])
	fmt.Fprint(out, "   Your Answer > ")
	if scanner.Scan() {
		ans := strings.TrimSpace(scanner.Text())
		if ans != "" {
			draft.Invariants = append([]string{ans}, draft.Invariants...)
		}
	}
	fmt.Fprintln(out)

	// Question 4: Quality & Self-Improvement Rubric
	fmt.Fprintf(out, "%s %s\n", ui.Bold("Q4 [Rubric]:"), "What specific acceptance criteria verify this skill succeeded before completion?")
	fmt.Fprintf(out, "   %s %s\n", ui.Dim("Default:"), draft.Rubric[0])
	fmt.Fprint(out, "   Your Answer > ")
	if scanner.Scan() {
		ans := strings.TrimSpace(scanner.Text())
		if ans != "" {
			draft.Rubric = append([]string{ans}, draft.Rubric...)
		}
	}
	if err := scanner.Err(); err != nil {
		return draft, fmt.Errorf("scanner error: %w", err)
	}

	fmt.Fprintf(out, "%s Requirement confidence reached ~95%%. Ready to scaffold.\n\n", ui.Green("✔"))
	return draft, nil
}
