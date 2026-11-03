package goals

import (
	"fmt"
	"os"
	"path/filepath"
)

// ContractTasks converts a structured WorkPackage into a slice of GoalTask items.
// Each task receives its typed ID (e.g. T-001), 1-based display index, title as Text,
// Completed state from verified acceptance, and File="work-package.json".
func ContractTasks(pkg *WorkPackage) []GoalTask {
	return ContractTasksWithDirectory(pkg, "")
}

// ContractTasksWithDirectory converts a structured WorkPackage into GoalTask items,
// evaluating completed status and acceptance_status from goals/<slug>/acceptance/<task-id>.json.
func ContractTasksWithDirectory(pkg *WorkPackage, goalDir string) []GoalTask {
	if pkg == nil {
		return nil
	}

	specDigest := ""
	if dig, err := WorkPackageDigest(pkg); err == nil {
		specDigest = dig
	}

	tasks := make([]GoalTask, len(pkg.Tasks))
	for i, t := range pkg.Tasks {
		completed := false
		accStatus := string(EvidenceStatusNotRun)

		if goalDir != "" {
			accPath := taskAcceptancePathInGoalDir(goalDir, t.ID)
			if fileExists(accPath) {
				if data, err := os.ReadFile(accPath); err == nil {
					if accRes, err := DecodeAcceptanceResult(data); err == nil && accRes != nil {
						if accRes.Accepted && accRes.EvidenceStatus == EvidenceStatusAccepted {
							if specDigest != "" && accRes.SpecDigest != specDigest {
								accStatus = string(EvidenceStatusStale)
								completed = false
							} else {
								accStatus = string(EvidenceStatusAccepted)
								completed = true
							}
						} else {
							accStatus = string(accRes.EvidenceStatus)
							completed = false
						}
					}
				}
			}
		}

		tasks[i] = GoalTask{
			ID:               t.ID,
			Index:            i + 1,
			Text:             t.Title,
			Completed:        completed,
			File:             "work-package.json",
			AcceptanceStatus: accStatus,
		}
	}
	return tasks
}

// LoadWorkPackageTasksIfPresent checks for a work-package.json in goalDir.
// If it exists and is valid, it returns the structured tasks. If the file is missing,
// it returns (nil, nil). If the file exists but is corrupt or invalid, it returns an error.
func LoadWorkPackageTasksIfPresent(goalDir string) ([]GoalTask, error) {
	wpPath := filepath.Join(goalDir, "work-package.json")
	data, err := os.ReadFile(wpPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	pkg, err := DecodeWorkPackage(data)
	if err != nil {
		return nil, fmt.Errorf("corrupt work-package.json in %s: %w", wpPath, err)
	}
	return ContractTasksWithDirectory(pkg, goalDir), nil
}
