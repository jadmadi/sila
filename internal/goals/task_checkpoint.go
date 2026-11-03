package goals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// DiagCheckpointPointerRecovered signals that a truncated or corrupt latest.json
	// pointer was recovered from an immutable sequence checkpoint.
	DiagCheckpointPointerRecovered = "CHECKPOINT_POINTER_RECOVERED"

	// TaskCheckpoint status constants conforming to the v1 specification.
	TaskCheckpointStatusInProgress            = "in_progress"
	TaskCheckpointStatusBlocked               = "blocked"
	TaskCheckpointStatusImplementedUnverified = "implemented_unverified"
)

var sequenceFileRegex = regexp.MustCompile(`^sequence-([0-9]{4,})\.json$`)

// TaskCheckpointDir returns the base directory where checkpoints for a given task are stored.
func TaskCheckpointDir(rootDir, slug, taskID string) string {
	cleanSlug := filepath.Clean(slug)
	cleanTaskID := filepath.Clean(taskID)
	return filepath.Join(resolveGoalsRootDir(rootDir), "goals", cleanSlug, "checkpoints", cleanTaskID)
}

// TaskCheckpointLatestPath returns the path to the replaceable latest.json pointer.
func TaskCheckpointLatestPath(rootDir, slug, taskID string) string {
	return filepath.Join(TaskCheckpointDir(rootDir, slug, taskID), "latest.json")
}

// TaskCheckpointSequencePath returns the path to a specific immutable sequence checkpoint file.
func TaskCheckpointSequencePath(rootDir, slug, taskID string, sequence int) string {
	return filepath.Join(TaskCheckpointDir(rootDir, slug, taskID), fmt.Sprintf("sequence-%04d.json", sequence))
}

// ValidateTaskCheckpoint verifies structural and domain constraints of a TaskCheckpoint.
func ValidateTaskCheckpoint(cp *TaskCheckpoint) error {
	if cp == nil {
		return fmt.Errorf("task checkpoint is nil")
	}
	if cp.SchemaVersion != TaskCheckpointSchemaVersion {
		return fmt.Errorf("unsupported schema_version %q: expected %q", cp.SchemaVersion, TaskCheckpointSchemaVersion)
	}
	if strings.TrimSpace(cp.GoalSlug) == "" {
		return fmt.Errorf("missing goal_slug")
	}
	if !kebabCaseSlugRegex.MatchString(cp.GoalSlug) {
		return fmt.Errorf("invalid goal_slug %q: must be kebab-case", cp.GoalSlug)
	}
	if strings.TrimSpace(cp.TaskID) == "" {
		return fmt.Errorf("missing task_id")
	}
	if cp.Sequence < 1 {
		return fmt.Errorf("invalid sequence %d: must be >= 1", cp.Sequence)
	}

	switch cp.Status {
	case TaskCheckpointStatusInProgress, TaskCheckpointStatusBlocked, TaskCheckpointStatusImplementedUnverified:
	default:
		return fmt.Errorf("invalid status %q: must be %s, %s, or %s",
			cp.Status, TaskCheckpointStatusInProgress, TaskCheckpointStatusBlocked, TaskCheckpointStatusImplementedUnverified)
	}

	if cp.Status == TaskCheckpointStatusBlocked {
		if cp.Blocker == nil {
			return fmt.Errorf("blocker details are required when status is blocked")
		}
		if strings.TrimSpace(cp.Blocker.Code) == "" {
			return fmt.Errorf("blocker code is required when status is blocked")
		}
	}

	if cp.Blocker != nil {
		if strings.TrimSpace(cp.Blocker.Code) == "" {
			return fmt.Errorf("blocker code cannot be empty when blocker is provided")
		}
	}

	for i, att := range cp.Attempts {
		if strings.TrimSpace(att.Executable) == "" {
			return fmt.Errorf("attempt %d: executable is required", i)
		}
	}

	return nil
}

// DecodeTaskCheckpoint strictly decodes and validates a TaskCheckpoint from JSON bytes.
// It rejects unknown fields, duplicate keys, invalid enums, and trailing data.
func DecodeTaskCheckpoint(data []byte) (*TaskCheckpoint, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("empty checkpoint data")
	}

	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var cp TaskCheckpoint
	if err := dec.Decode(&cp); err != nil {
		return nil, fmt.Errorf("decode task checkpoint: %w", err)
	}

	if err := ValidateTaskCheckpoint(&cp); err != nil {
		return nil, err
	}

	return &cp, nil
}

// LoadTaskCheckpointWithDiagnostics reads the latest checkpoint for a task,
// automatically recovering from valid immutable sequence checkpoints if latest.json is truncated or corrupt.
func LoadTaskCheckpointWithDiagnostics(rootDir, slug, taskID string) (*TaskCheckpoint, []Diagnostic, error) {
	if !kebabCaseSlugRegex.MatchString(slug) {
		return nil, nil, fmt.Errorf("invalid goal slug %q", slug)
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, nil, fmt.Errorf("missing task_id")
	}

	rootDir = resolveGoalsRootDir(rootDir)
	dir := TaskCheckpointDir(rootDir, slug, taskID)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, nil, fmt.Errorf("%w: no checkpoints directory for task %s in goal %s", os.ErrNotExist, taskID, slug)
	}

	latestPath := TaskCheckpointLatestPath(rootDir, slug, taskID)
	latestData, readErr := os.ReadFile(latestPath)
	if readErr == nil && len(bytes.TrimSpace(latestData)) > 0 {
		if cp, decErr := DecodeTaskCheckpoint(latestData); decErr == nil {
			return cp, nil, nil
		}
	}

	// AC-003: Truncated latest pointers recover from the last valid immutable checkpoint with a diagnostic.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read checkpoints dir: %w", err)
	}

	var seqs []int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		matches := sequenceFileRegex.FindStringSubmatch(entry.Name())
		if len(matches) == 2 {
			if s, convErr := strconv.Atoi(matches[1]); convErr == nil && s >= 1 {
				seqs = append(seqs, s)
			}
		}
	}

	sort.Sort(sort.Reverse(sort.IntSlice(seqs)))

	for _, seq := range seqs {
		seqPath := TaskCheckpointSequencePath(rootDir, slug, taskID, seq)
		seqData, err := os.ReadFile(seqPath)
		if err != nil || len(bytes.TrimSpace(seqData)) == 0 {
			continue
		}

		recoveredCP, decErr := DecodeTaskCheckpoint(seqData)
		if decErr != nil {
			continue
		}

		// Re-write latest.json atomically using the valid immutable checkpoint data
		_ = AtomicWriteFile(latestPath, seqData, 0644)

		diag := Diagnostic{
			Code:        DiagCheckpointPointerRecovered,
			Severity:    "warning",
			GoalSlug:    slug,
			Path:        filepath.Join("goals", slug, "checkpoints", taskID, "latest.json"),
			Message:     fmt.Sprintf("latest.json was corrupt or missing; recovered from sequence %d", seq),
			Remediation: "The latest pointer was automatically restored from the latest valid immutable sequence checkpoint.",
		}

		return recoveredCP, []Diagnostic{diag}, nil
	}

	return nil, nil, fmt.Errorf("no valid checkpoint found for task %s in %s", taskID, dir)
}

// LoadTaskCheckpoint loads the active checkpoint for taskID under goal slug.
func LoadTaskCheckpoint(rootDir, slug, taskID string) (*TaskCheckpoint, error) {
	cp, _, err := LoadTaskCheckpointWithDiagnostics(rootDir, slug, taskID)
	return cp, err
}

// LoadGoalClaim reads and returns the active GoalClaim for a goal slug, if present.
func LoadGoalClaim(rootDir, slug string) (*GoalClaim, error) {
	claimPath := filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug, "claim.json")
	return readClaim(claimPath)
}

// SaveTaskCheckpoint atomically writes an immutable sequence checkpoint and updates the latest pointer.
// It verifies monotonic sequences under the goal lock, enforces append-only attempts, and evaluates circuit breakers.
func SaveTaskCheckpoint(rootDir, slug string, checkpoint TaskCheckpoint) error {
	if !kebabCaseSlugRegex.MatchString(slug) {
		return fmt.Errorf("invalid goal slug %q", slug)
	}
	if checkpoint.GoalSlug == "" {
		checkpoint.GoalSlug = slug
	} else if checkpoint.GoalSlug != slug {
		return fmt.Errorf("goal_slug mismatch: checkpoint has %q, target is %q", checkpoint.GoalSlug, slug)
	}
	if checkpoint.SchemaVersion == "" {
		checkpoint.SchemaVersion = TaskCheckpointSchemaVersion
	}
	if checkpoint.Status == "" {
		checkpoint.Status = TaskCheckpointStatusInProgress
	}

	// Canonicalize slices to empty arrays instead of null in JSON output
	if checkpoint.CompletedSteps == nil {
		checkpoint.CompletedSteps = []string{}
	}
	if checkpoint.EvidencePaths == nil {
		checkpoint.EvidencePaths = []string{}
	}
	if checkpoint.Attempts == nil {
		checkpoint.Attempts = []CheckpointAttempt{}
	}
	if checkpoint.ChangedFiles == nil {
		checkpoint.ChangedFiles = []string{}
	}
	for i := range checkpoint.Attempts {
		if checkpoint.Attempts[i].Args == nil {
			checkpoint.Attempts[i].Args = []string{}
		}
	}

	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)

	unlock, err := acquireGoalLock(goalDir)
	if err != nil {
		return fmt.Errorf("acquire goal lock: %w", err)
	}
	defer unlock()

	// Load existing checkpoint to enforce monotonic sequence, append-only attempts, and circuit breaker
	prevCP, _ := LoadTaskCheckpoint(rootDir, slug, checkpoint.TaskID)
	currentSequence := 0
	if prevCP != nil {
		currentSequence = prevCP.Sequence
	} else {
		// Even if latest.json is missing, scan existing sequence files to prevent stale sequence writes
		dir := TaskCheckpointDir(rootDir, slug, checkpoint.TaskID)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				matches := sequenceFileRegex.FindStringSubmatch(entry.Name())
				if len(matches) == 2 {
					if s, convErr := strconv.Atoi(matches[1]); convErr == nil && s > currentSequence {
						currentSequence = s
					}
				}
			}
		}
	}

	// AC-003: Monotonic checkpoint sequence numbers and compare-and-swap
	if currentSequence == 0 {
		if checkpoint.Sequence != 1 {
			return fmt.Errorf("sequence conflict: initial checkpoint must have sequence 1, got %d", checkpoint.Sequence)
		}
	} else {
		if checkpoint.Sequence <= currentSequence {
			return fmt.Errorf("sequence conflict: stale sequence %d (current sequence is %d)", checkpoint.Sequence, currentSequence)
		}
		if checkpoint.Sequence != currentSequence+1 {
			return fmt.Errorf("sequence conflict: cannot save sequence %d over current sequence %d (expected %d)",
				checkpoint.Sequence, currentSequence, currentSequence+1)
		}
	}

	// AC-003: Append-only attempts validation
	if prevCP != nil && len(prevCP.Attempts) > 0 {
		if len(checkpoint.Attempts) < len(prevCP.Attempts) {
			return fmt.Errorf("append-only violation: checkpoint has %d attempts, but previous sequence %d had %d attempts",
				len(checkpoint.Attempts), prevCP.Sequence, len(prevCP.Attempts))
		}
		for i := 0; i < len(prevCP.Attempts); i++ {
			if !checkpointAttemptsEqual(checkpoint.Attempts[i], prevCP.Attempts[i]) {
				return fmt.Errorf("append-only violation: historical attempt %d was modified", i)
			}
		}
	}

	// AC-002: Evaluate repeated failure signatures and circuit breaker
	isMaterialChange := prevCP != nil && (checkpoint.SpecDigest != prevCP.SpecDigest ||
		checkpoint.PacketDigest != prevCP.PacketDigest ||
		checkpoint.CandidateDigest != prevCP.CandidateDigest)
	interveningProgress := prevCP != nil && len(checkpoint.CompletedSteps) > len(prevCP.CompletedSteps)

	m := len(checkpoint.Attempts)
	if m >= 2 && !isMaterialChange && !interveningProgress {
		last := checkpoint.Attempts[m-1]
		penult := checkpoint.Attempts[m-2]

		// Repeated identical failure signatures trigger the circuit breaker
		if last.ExitCode != 0 && penult.ExitCode != 0 &&
			last.FailureSignature != "" &&
			last.FailureSignature == penult.FailureSignature {
			checkpoint.Status = TaskCheckpointStatusBlocked
			if checkpoint.Blocker == nil {
				checkpoint.Blocker = &CheckpointBlocker{
					Code:           "CIRCUIT_BREAKER_TRIGGERED",
					Assumption:     "attempt would resolve failure",
					Observed:       fmt.Sprintf("consecutive attempts failed with identical failure signature: %s", last.FailureSignature),
					DecisionNeeded: "planner action required to resolve repeated failure",
				}
			}
		}
	}

	// If previously blocked and attempting to resume as in_progress without material change, intervening progress,
	// or an intervening successful attempt, enforce the blocked state
	if prevCP != nil && prevCP.Status == TaskCheckpointStatusBlocked && checkpoint.Status == TaskCheckpointStatusInProgress {
		hasNewSuccessfulAttempt := m > len(prevCP.Attempts) && checkpoint.Attempts[m-1].ExitCode == 0
		if !isMaterialChange && !interveningProgress && !hasNewSuccessfulAttempt {
			checkpoint.Status = TaskCheckpointStatusBlocked
			if checkpoint.Blocker == nil {
				checkpoint.Blocker = prevCP.Blocker
			}
		}
	}

	// Validate checkpoint invariants
	if err := ValidateTaskCheckpoint(&checkpoint); err != nil {
		return fmt.Errorf("validate checkpoint: %w", err)
	}

	// Ensure checkpoint directory exists
	checkpointsDir := TaskCheckpointDir(rootDir, slug, checkpoint.TaskID)
	if err := os.MkdirAll(checkpointsDir, 0755); err != nil {
		return fmt.Errorf("create checkpoints dir: %w", err)
	}

	// Serialize formatted JSON
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	data = append(data, '\n')

	// 1. Write immutable sequence checkpoint file
	seqPath := TaskCheckpointSequencePath(rootDir, slug, checkpoint.TaskID, checkpoint.Sequence)
	if err := AtomicWriteFile(seqPath, data, 0644); err != nil {
		return fmt.Errorf("atomic write sequence file: %w", err)
	}

	// 2. Write replaceable latest pointer file
	latestPath := TaskCheckpointLatestPath(rootDir, slug, checkpoint.TaskID)
	if err := AtomicWriteFile(latestPath, data, 0644); err != nil {
		return fmt.Errorf("atomic write latest pointer: %w", err)
	}

	return nil
}

func checkpointAttemptsEqual(a, b CheckpointAttempt) bool {
	if a.Executable != b.Executable || a.ExitCode != b.ExitCode || a.FailureSignature != b.FailureSignature {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	return true
}
