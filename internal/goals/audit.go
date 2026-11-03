package goals

import (
	"fmt"
)

// EpistemicCompletionStatus represents whether a goal's tasks and commit attributions match.
type EpistemicCompletionStatus string

const (
	// EpistemicComplete indicates all declared tasks in plan.md are checked.
	EpistemicComplete EpistemicCompletionStatus = "complete"
	// EpistemicPartial indicates matching commits exist, but unchecked tasks remain in plan.md.
	EpistemicPartial EpistemicCompletionStatus = "partial"
	// EpistemicUnchecked indicates no tasks have been verified or checked.
	EpistemicUnchecked EpistemicCompletionStatus = "unchecked"
)

// CheckEpistemicCompletion determines the completion state given task counts and commit SHA.
func CheckEpistemicCompletion(tasksMet, tasksTotal int, commit string) EpistemicCompletionStatus {
	if tasksTotal == 0 {
		if commit != "" {
			return EpistemicComplete
		}
		return EpistemicUnchecked
	}
	if tasksMet >= tasksTotal {
		return EpistemicComplete
	}
	if commit != "" {
		return EpistemicPartial
	}
	return EpistemicUnchecked
}

// FormatEpistemicWarning returns an explicit human-readable warning when commits exist with incomplete tasks.
func FormatEpistemicWarning(commit string, tasksMet, tasksTotal int) string {
	if commit == "" || tasksTotal == 0 || tasksMet >= tasksTotal {
		return ""
	}
	return fmt.Sprintf("commit %s attributed but %d/%d tasks remain unchecked in plan.md", commit, tasksTotal-tasksMet, tasksTotal)
}
