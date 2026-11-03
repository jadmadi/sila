package goals

import (
	"fmt"
	"path/filepath"
)

// ValidateEpistemicTaskAttribution validates that commit attribution matches plan task completion.
// It generates diagnostics if commits exist but tasks remain unchecked in plan.md.
func ValidateEpistemicTaskAttribution(rootDir string, g *Goal) []Diagnostic {
	var diags []Diagnostic
	if g == nil {
		return diags
	}
	if g.Commit != "" && g.TasksTotal > 0 && g.TasksMet < g.TasksTotal {
		diags = append(diags, Diagnostic{
			Code:        DiagTaskInconsistent,
			Severity:    "warning",
			GoalSlug:    g.Slug,
			Path:        filepath.Join("goals", g.Slug, "plan.md"),
			Message:     fmt.Sprintf("commit %s attributed but %d/%d tasks remain unchecked in plan.md; all tasks must be checked before implementation is certified", g.Commit, g.TasksTotal-g.TasksMet, g.TasksTotal),
			Remediation: "Complete remaining tasks and check checkboxes in plan.md",
		})
	}
	return diags
}
