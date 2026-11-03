package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// Diagnostic code for a task checkpoint that is blocked.
const DiagTaskCheckpointBlocked = "TASK_CHECKPOINT_BLOCKED"

// BuildTaskResumePacket constructs a verified resume TaskPacket for taskID under goal slug.
// It integrates the latest task checkpoint, validates spec and source drift, re-evaluates
// completed steps and verification requirements, and marks executable=false with diagnostics if blocked or drifted.
func BuildTaskResumePacket(rootDir, slug, taskID string) (*TaskPacket, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}

	rootDir = resolveGoalsRootDir(rootDir)

	// 1. Build base task packet
	packet, err := BuildTaskPacket(rootDir, slug, taskID)
	if err != nil {
		return nil, fmt.Errorf("build base task packet for %s/%s: %w", slug, taskID, err)
	}
	basePacketDigest := packet.PacketDigest

	// 2. Load latest checkpoint for the task
	cp, cpDiags, err := LoadTaskCheckpointWithDiagnostics(rootDir, slug, taskID)
	if err != nil {
		return nil, fmt.Errorf("load checkpoint for task %s in goal %s: %w", taskID, slug, err)
	}

	packet.Resume = cp
	if len(cpDiags) > 0 {
		packet.Diagnostics = append(packet.Diagnostics, cpDiags...)
	}

	// 3. AC-008: Validate spec drift
	if cp.SpecDigest != "" && cp.SpecDigest != packet.SpecDigest {
		packet.Diagnostics = append(packet.Diagnostics, Diagnostic{
			Code:        DiagPacketSpecStale,
			Severity:    "error",
			GoalSlug:    slug,
			Path:        filepath.Join("goals", slug, "work-package.json"),
			Message:     fmt.Sprintf("specification has drifted since checkpoint: recorded %s, current %s", cp.SpecDigest, packet.SpecDigest),
			Remediation: "Inspect specification amendments and update task packet before resuming",
		})
		packet.Executable = false
	}

	// 4. AC-008: Validate source/candidate drift against checkpoint PacketDigest and CandidateDigest
	if cp.PacketDigest != "" && cp.PacketDigest != basePacketDigest && (cp.SpecDigest == "" || cp.SpecDigest == packet.SpecDigest) {
		packet.Diagnostics = append(packet.Diagnostics, Diagnostic{
			Code:        DiagPacketSourceStale,
			Severity:    "error",
			GoalSlug:    slug,
			Path:        fmt.Sprintf("tasks[%s]", taskID),
			Message:     fmt.Sprintf("source files or candidate have drifted since checkpoint: recorded packet %s, current %s", cp.PacketDigest, basePacketDigest),
			Remediation: "Inspect source modifications and re-verify before resuming",
		})
		packet.Executable = false
	}

	if cp.CandidateDigest != "" && packet.CandidateCommit != "" && cp.CandidateDigest != packet.CandidateCommit {
		packet.Diagnostics = append(packet.Diagnostics, Diagnostic{
			Code:        DiagPacketSourceStale,
			Severity:    "error",
			GoalSlug:    slug,
			Path:        fmt.Sprintf("tasks[%s]", taskID),
			Message:     fmt.Sprintf("candidate commit has drifted since checkpoint: recorded %s, current %s", cp.CandidateDigest, packet.CandidateCommit),
			Remediation: "Inspect git commit differences before continuing",
		})
		packet.Executable = false
	}

	// Check if any pinned files on disk are missing or invalid
	for _, pin := range packet.SourcePins {
		fullPath := filepath.Join(rootDir, filepath.Clean(filepath.ToSlash(pin.Path)))
		if pin.MustBeAbsent {
			if fi, statErr := os.Stat(fullPath); statErr == nil && !fi.IsDir() {
				packet.Diagnostics = append(packet.Diagnostics, Diagnostic{
					Code:        DiagPacketSourceStale,
					Severity:    "error",
					GoalSlug:    slug,
					Path:        pin.Path,
					Message:     fmt.Sprintf("pinned create path %q exists on disk unexpectedly", pin.Path),
					Remediation: "Remove unexpected file or review task scope",
				})
				packet.Executable = false
			}
		} else {
			data, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				packet.Diagnostics = append(packet.Diagnostics, Diagnostic{
					Code:        DiagPacketSourceStale,
					Severity:    "error",
					GoalSlug:    slug,
					Path:        pin.Path,
					Message:     fmt.Sprintf("pinned source file %q is unreadable: %v", pin.Path, readErr),
					Remediation: "Restore missing source file",
				})
				packet.Executable = false
			} else {
				sum := sha256.Sum256(data)
				actualSHA := hex.EncodeToString(sum[:])
				if actualSHA != pin.SHA256 {
					packet.Diagnostics = append(packet.Diagnostics, Diagnostic{
						Code:        DiagPacketSourceStale,
						Severity:    "error",
						GoalSlug:    slug,
						Path:        pin.Path,
						Message:     fmt.Sprintf("pinned source file %q content has drifted: recorded %s, current %s", pin.Path, pin.SHA256, actualSHA),
						Remediation: "Inspect source modifications and re-verify before resuming",
					})
					packet.Executable = false
				}
			}
		}
	}

	// 5. Blocked status enforcement (Requirement 3)
	if cp.Status == TaskCheckpointStatusBlocked {
		packet.Executable = false
		blockerMsg := "task execution is blocked"
		if cp.Blocker != nil && cp.Blocker.Observed != "" {
			blockerMsg = fmt.Sprintf("task execution is blocked: %s (decision needed: %s)", cp.Blocker.Observed, cp.Blocker.DecisionNeeded)
		}
		packet.Diagnostics = append(packet.Diagnostics, Diagnostic{
			Code:        DiagTaskCheckpointBlocked,
			Severity:    "error",
			GoalSlug:    slug,
			Path:        fmt.Sprintf("tasks[%s]", taskID),
			Message:     blockerMsg,
			Remediation: "Planner action or contract amendment required to resolve blocker",
		})
	}

	// 6. Step selection and Next Action determination (AC-007, AC-009)
	completedMap := make(map[string]bool, len(cp.CompletedSteps))
	for _, s := range cp.CompletedSteps {
		completedMap[s] = true
	}

	allStepsCompleted := true
	var nextUncompletedStep *Step
	for i := range packet.Task.Steps {
		st := &packet.Task.Steps[i]
		if !completedMap[st.ID] {
			allStepsCompleted = false
			if nextUncompletedStep == nil {
				nextUncompletedStep = st
			}
		}
	}

	if allStepsCompleted && len(packet.Task.Steps) > 0 {
		// AC-009: A finished implementation with missing acceptance resumes at verification, not at an invented accepted state.
		accResult, _ := LoadTaskAcceptance(rootDir, slug, taskID)
		if accResult == nil || !accResult.Accepted {
			packet.Resume.NextStep = "VERIFY_ACCEPTANCE"
			packet.Resume.Status = TaskCheckpointStatusImplementedUnverified
		} else {
			packet.Resume.NextStep = "COMPLETED"
		}
	} else if nextUncompletedStep != nil {
		// AC-007: Clean checkpoint yields the exact next uncompleted step
		if cp.NextStep != "" && !completedMap[cp.NextStep] {
			packet.Resume.NextStep = cp.NextStep
		} else {
			packet.Resume.NextStep = nextUncompletedStep.ID
		}
	}

	// Recompute packet digest including the resume checkpoint
	newPacketDigest, err := ComputeTaskPacketDigest(packet)
	if err != nil {
		return nil, fmt.Errorf("recompute task packet digest: %w", err)
	}
	packet.PacketDigest = newPacketDigest

	return packet, nil
}
