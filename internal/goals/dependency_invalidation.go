package goals

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// TaskRef identifies a specific task within a goal package.
type TaskRef struct {
	GoalSlug string `json:"goal_slug"`
	TaskID   string `json:"task_id"`
}

// AffectedDependentTasks maps downstream task references affected when a goal (or goal/task)
// is reopened or has its acceptance drifted.
func AffectedDependentTasks(rootDir, slug string) ([]TaskRef, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("slug is required")
	}
	rootDir = resolveGoalsRootDir(rootDir)

	targetGoal := slug
	targetTask := ""
	if strings.Contains(slug, "/") {
		parts := strings.SplitN(slug, "/", 2)
		targetGoal = parts[0]
		targetTask = parts[1]
	} else if strings.Contains(slug, ":") {
		parts := strings.SplitN(slug, ":", 2)
		targetGoal = parts[0]
		targetTask = parts[1]
	}
	targetGoal = filepath.Clean(targetGoal)

	graph, err := ScanGoalsDependencies(rootDir)
	if err != nil {
		return nil, fmt.Errorf("scan dependencies: %w", err)
	}

	dependents := graph.TransitiveDependents(targetGoal)
	var refs []TaskRef

	for _, depSlug := range dependents {
		depDir := filepath.Join(rootDir, "goals", depSlug)
		wpPath := filepath.Join(depDir, "work-package.json")
		if fileExists(wpPath) {
			wp, err := LoadWorkPackage(rootDir, depSlug)
			if err != nil {
				continue
			}

			// If a specific task was targeted, check if conditions match it
			if targetTask != "" {
				matchesTask := false
				for _, dc := range wp.DependencyConditions {
					if dc.GoalSlug == targetGoal && (dc.TaskID == "" || dc.TaskID == targetTask) {
						matchesTask = true
						break
					}
				}
				if !matchesTask {
					continue
				}
			}

			for _, t := range wp.Tasks {
				refs = append(refs, TaskRef{
					GoalSlug: depSlug,
					TaskID:   t.ID,
				})
			}
		} else {
			// Legacy goal tasks from plan.md or GetGoalTasks
			tasks, err := GetGoalTasks(rootDir, depSlug)
			if err == nil {
				for _, t := range tasks {
					refs = append(refs, TaskRef{
						GoalSlug: depSlug,
						TaskID:   t.ID,
					})
				}
			}
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].GoalSlug != refs[j].GoalSlug {
			return refs[i].GoalSlug < refs[j].GoalSlug
		}
		return refs[i].TaskID < refs[j].TaskID
	})

	return refs, nil
}
