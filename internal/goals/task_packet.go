package goals

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// TaskPacketSchemaVersion is the canonical schema version for task packets.
const TaskPacketSchemaVersion = "sila/task-packet/v1"

// TaskCheckpointSchemaVersion is the canonical schema version for task checkpoints.
const TaskCheckpointSchemaVersion = "sila/task-checkpoint/v1"

// Standard diagnostic codes for task packets.
const (
	DiagAcceptanceNotAvailable = "ACCEPTANCE_NOT_AVAILABLE"
	DiagAcceptanceIncomplete   = "ACCEPTANCE_INCOMPLETE"
	DiagPacketTooLarge         = "PACKET_TOO_LARGE"
	DiagPacketSourceStale      = "PACKET_SOURCE_STALE"
	DiagPacketSpecStale        = "PACKET_SPEC_STALE"
	DiagPacketDependencyStale  = "PACKET_DEPENDENCY_STALE"
	DiagCreatePathExists       = "CREATE_PATH_EXISTS"
)

// TaskPacket represents a deterministic, self-contained execution package for a single task.
type TaskPacket struct {
	SchemaVersion     string             `json:"schema_version"`
	GoalSlug          string             `json:"goal_slug"`
	TaskID            string             `json:"task_id"`
	Purpose           string             `json:"purpose"`
	Task              TaskContract       `json:"task"`
	Acceptance        []AcceptanceCase   `json:"acceptance"`
	GlobalConstraints []string           `json:"global_constraints"`
	NonGoals          []string           `json:"non_goals"`
	DependencyOutputs []DependencyOutput `json:"dependency_outputs"`
	SpecDigest        string             `json:"spec_digest"`
	SourcePins        []SourcePin        `json:"source_pins"`
	CandidateCommit   string             `json:"candidate_commit"`
	PacketDigest      string             `json:"packet_digest"`
	Executable        bool               `json:"executable"`
	Diagnostics       []Diagnostic       `json:"diagnostics"`
	MaxBytes          int                `json:"max_bytes"`
	Resume            *TaskCheckpoint    `json:"resume"`
}

// DependencyOutput records an accepted artifact output from a prior dependency.
type DependencyOutput struct {
	GoalSlug string `json:"goal_slug"`
	TaskID   string `json:"task_id"`
	Artifact string `json:"artifact"`
	SHA256   string `json:"sha256"`
}

// SourcePin pins the cryptographic digest of an input file at packet creation time.
type SourcePin struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	MustBeAbsent bool   `json:"must_be_absent"`
}

// TaskCheckpoint records intermediate progress and attempt history for resume operations.
type TaskCheckpoint struct {
	SchemaVersion   string              `json:"schema_version"`
	GoalSlug        string              `json:"goal_slug"`
	TaskID          string              `json:"task_id"`
	Sequence        int                 `json:"sequence"`
	SpecDigest      string              `json:"spec_digest"`
	PacketDigest    string              `json:"packet_digest"`
	CandidateDigest string              `json:"candidate_digest"`
	CompletedSteps  []string            `json:"completed_steps"`
	EvidencePaths   []string            `json:"evidence_paths"`
	Attempts        []CheckpointAttempt `json:"attempts"`
	ChangedFiles    []string            `json:"changed_files"`
	NextStep        string              `json:"next_step"`
	Status          string              `json:"status"` // in_progress | blocked | implemented_unverified
	Blocker         *CheckpointBlocker  `json:"blocker"`
}

// CheckpointAttempt captures a single verification or execution attempt within a checkpoint.
type CheckpointAttempt struct {
	Executable       string   `json:"executable"`
	Args             []string `json:"args"`
	ExitCode         int      `json:"exit_code"`
	FailureSignature string   `json:"failure_signature"`
}

// CheckpointBlocker details the obstruction when a checkpoint is blocked.
type CheckpointBlocker struct {
	Code           string `json:"code"`
	Assumption     string `json:"assumption"`
	Observed       string `json:"observed"`
	DecisionNeeded string `json:"decision_needed"`
}

// ComputeTaskPacketDigest computes the deterministic SHA-256 digest of a TaskPacket.
// The packet_digest field itself is excluded from computation.
func ComputeTaskPacketDigest(packet *TaskPacket) (string, error) {
	if packet == nil {
		return "", fmt.Errorf("task packet is nil")
	}

	canon := cloneTaskPacket(packet)
	canon.PacketDigest = ""

	// Ensure slice fields are non-nil for stable JSON serialization
	if canon.GlobalConstraints == nil {
		canon.GlobalConstraints = []string{}
	}
	if canon.NonGoals == nil {
		canon.NonGoals = []string{}
	}
	if canon.DependencyOutputs == nil {
		canon.DependencyOutputs = []DependencyOutput{}
	}
	if canon.SourcePins == nil {
		canon.SourcePins = []SourcePin{}
	}
	if canon.Acceptance == nil {
		canon.Acceptance = []AcceptanceCase{}
	}
	if canon.Diagnostics == nil {
		canon.Diagnostics = []Diagnostic{}
	}

	// Sort SourcePins by Path
	sort.Slice(canon.SourcePins, func(i, j int) bool {
		return canon.SourcePins[i].Path < canon.SourcePins[j].Path
	})

	// Sort Acceptance cases by ID
	sort.Slice(canon.Acceptance, func(i, j int) bool {
		return canon.Acceptance[i].ID < canon.Acceptance[j].ID
	})
	for i := range canon.Acceptance {
		sort.Strings(canon.Acceptance[i].CheckIDs)
	}

	// Sort DependencyOutputs deterministically
	sort.Slice(canon.DependencyOutputs, func(i, j int) bool {
		ki := fmt.Sprintf("%s:%s:%s", canon.DependencyOutputs[i].GoalSlug, canon.DependencyOutputs[i].TaskID, canon.DependencyOutputs[i].Artifact)
		kj := fmt.Sprintf("%s:%s:%s", canon.DependencyOutputs[j].GoalSlug, canon.DependencyOutputs[j].TaskID, canon.DependencyOutputs[j].Artifact)
		return ki < kj
	})

	// Sort Diagnostics deterministically
	sort.Slice(canon.Diagnostics, func(i, j int) bool {
		ki := fmt.Sprintf("%s:%s:%s:%s", canon.Diagnostics[i].Severity, canon.Diagnostics[i].Code, canon.Diagnostics[i].Path, canon.Diagnostics[i].Message)
		kj := fmt.Sprintf("%s:%s:%s:%s", canon.Diagnostics[j].Severity, canon.Diagnostics[j].Code, canon.Diagnostics[j].Path, canon.Diagnostics[j].Message)
		return ki < kj
	})

	canonBytes, err := json.Marshal(canon)
	if err != nil {
		return "", fmt.Errorf("marshal canonical task packet: %w", err)
	}

	h := sha256.New()
	h.Write([]byte(canon.SchemaVersion + "\n"))
	h.Write(canonBytes)

	return hex.EncodeToString(h.Sum(nil)), nil
}

func cloneTaskPacket(p *TaskPacket) *TaskPacket {
	cp := *p

	cp.GlobalConstraints = append([]string(nil), p.GlobalConstraints...)
	cp.NonGoals = append([]string(nil), p.NonGoals...)
	cp.DependencyOutputs = append([]DependencyOutput(nil), p.DependencyOutputs...)
	cp.SourcePins = append([]SourcePin(nil), p.SourcePins...)

	cp.Acceptance = make([]AcceptanceCase, len(p.Acceptance))
	for i, a := range p.Acceptance {
		ca := a
		ca.CheckIDs = append([]string(nil), a.CheckIDs...)
		cp.Acceptance[i] = ca
	}

	cp.Diagnostics = append([]Diagnostic(nil), p.Diagnostics...)

	// Deep copy task
	ct := p.Task
	ct.DependsOn = append([]string(nil), p.Task.DependsOn...)
	ct.Interfaces = append([]string(nil), p.Task.Interfaces...)
	ct.Constraints = append([]string(nil), p.Task.Constraints...)
	ct.AcceptanceIDs = append([]string(nil), p.Task.AcceptanceIDs...)
	ct.StopConditions = append([]string(nil), p.Task.StopConditions...)
	ct.Files.Read = append([]string(nil), p.Task.Files.Read...)
	ct.Files.Edit = append([]string(nil), p.Task.Files.Edit...)
	ct.Files.Create = append([]string(nil), p.Task.Files.Create...)
	ct.Files.Forbid = append([]string(nil), p.Task.Files.Forbid...)
	ct.References = append([]Reference(nil), p.Task.References...)
	ct.Steps = append([]Step(nil), p.Task.Steps...)
	ct.Examples = append([]Example(nil), p.Task.Examples...)
	cp.Task = ct

	if p.Resume != nil {
		cRes := *p.Resume
		cRes.CompletedSteps = append([]string(nil), p.Resume.CompletedSteps...)
		cRes.EvidencePaths = append([]string(nil), p.Resume.EvidencePaths...)
		cRes.Attempts = append([]CheckpointAttempt(nil), p.Resume.Attempts...)
		cRes.ChangedFiles = append([]string(nil), p.Resume.ChangedFiles...)
		if p.Resume.Blocker != nil {
			cB := *p.Resume.Blocker
			cRes.Blocker = &cB
		}
		cp.Resume = &cRes
	}

	return &cp
}

// LoadTaskSelectionAndReview searches for and decodes a task's receipt selection and acceptance review.
func LoadTaskSelectionAndReview(rootDir, slug, taskID string) (*ReceiptSelection, *AcceptanceReview, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	cleanSlug := filepath.Clean(slug)
	cleanTaskID := filepath.Clean(taskID)

	selCandidates := []string{
		filepath.Join(rootDir, "goals", cleanSlug, "acceptance", fmt.Sprintf("%s-selection.json", cleanTaskID)),
		filepath.Join(rootDir, "goals", cleanSlug, "evidence", cleanTaskID, "selection.json"),
		filepath.Join(rootDir, "goals", cleanSlug, "evidence", cleanTaskID, fmt.Sprintf("%s-selection.json", cleanTaskID)),
		filepath.Join(filepath.Dir(TaskAcceptancePath(rootDir, cleanSlug, cleanTaskID)), fmt.Sprintf("%s-selection.json", cleanTaskID)),
		filepath.Join(filepath.Dir(TaskAcceptancePath(rootDir, cleanSlug, cleanTaskID)), "selection.json"),
	}

	revCandidates := []string{
		filepath.Join(rootDir, "goals", cleanSlug, "acceptance", fmt.Sprintf("%s-review.json", cleanTaskID)),
		filepath.Join(rootDir, "goals", cleanSlug, "evidence", cleanTaskID, "review.json"),
		filepath.Join(rootDir, "goals", cleanSlug, "evidence", cleanTaskID, fmt.Sprintf("%s-review.json", cleanTaskID)),
		filepath.Join(filepath.Dir(TaskAcceptancePath(rootDir, cleanSlug, cleanTaskID)), fmt.Sprintf("%s-review.json", cleanTaskID)),
		filepath.Join(filepath.Dir(TaskAcceptancePath(rootDir, cleanSlug, cleanTaskID)), "review.json"),
	}

	var selData []byte
	for _, p := range selCandidates {
		if data, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(data)) > 0 {
			selData = data
			break
		}
	}
	if selData == nil {
		return nil, nil, fmt.Errorf("receipt selection not found for task %s", cleanTaskID)
	}

	var revData []byte
	for _, p := range revCandidates {
		if data, err := os.ReadFile(p); err == nil && len(bytes.TrimSpace(data)) > 0 {
			revData = data
			break
		}
	}
	if revData == nil {
		return nil, nil, fmt.Errorf("acceptance review not found for task %s", cleanTaskID)
	}

	sel, err := DecodeReceiptSelection(selData)
	if err != nil {
		return nil, nil, fmt.Errorf("decode receipt selection for task %s: %w", cleanTaskID, err)
	}

	rev, err := DecodeAcceptanceReview(revData)
	if err != nil {
		return nil, nil, fmt.Errorf("decode acceptance review for task %s: %w", cleanTaskID, err)
	}

	return sel, rev, nil
}

// BuildTaskPacket constructs a deterministic TaskPacket for the requested task in a goal.
func BuildTaskPacket(rootDir, slug, taskID string) (*TaskPacket, error) {
	if rootDir == "" {
		rootDir = "."
	}
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if taskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}

	pkg, err := LoadWorkPackage(rootDir, slug)
	if err != nil {
		return nil, fmt.Errorf("load work package for %s: %w", slug, err)
	}

	// Find the targeted task
	var targetedTask *TaskContract
	for i := range pkg.Tasks {
		if pkg.Tasks[i].ID == taskID {
			targetedTask = &pkg.Tasks[i]
			break
		}
	}
	if targetedTask == nil {
		return nil, fmt.Errorf("task %q not found in work package for goal %s", taskID, slug)
	}

	specDigest, err := WorkPackageDigest(pkg)
	if err != nil {
		return nil, fmt.Errorf("compute work package digest: %w", err)
	}

	var diags []Diagnostic
	executable := true

	// Filter acceptance cases matching targeted task
	acceptanceMap := make(map[string]AcceptanceCase, len(pkg.Acceptance))
	for _, a := range pkg.Acceptance {
		acceptanceMap[a.ID] = a
	}

	var taskCases []AcceptanceCase
	for _, acID := range targetedTask.AcceptanceIDs {
		if ac, ok := acceptanceMap[acID]; ok {
			taskCases = append(taskCases, ac)
		} else {
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceUnowned,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].acceptance_ids", taskID),
				Message:     fmt.Sprintf("task %q references non-existent acceptance case %q", taskID, acID),
				Remediation: "declare the acceptance case in the work package acceptance list",
			})
			executable = false
		}
	}

	// Deterministically sort task acceptance cases
	sort.Slice(taskCases, func(i, j int) bool {
		return taskCases[i].ID < taskCases[j].ID
	})

	// Check repository commit revision
	candidateCommit := ""
	if out, err := exec.Command("git", "-C", rootDir, "rev-parse", "HEAD").Output(); err == nil {
		candidateCommit = strings.TrimSpace(string(out))
	}

	// Build source pins for read, edit, reference, and create files
	seenPaths := make(map[string]bool)
	var pins []SourcePin

	// Pinned input files (must exist on disk)
	var inputFiles []string
	inputFiles = append(inputFiles, targetedTask.Files.Read...)
	inputFiles = append(inputFiles, targetedTask.Files.Edit...)

	for _, p := range inputFiles {
		clean := filepath.Clean(filepath.ToSlash(p))
		if clean == "." || clean == "" || seenPaths[clean] {
			continue
		}
		seenPaths[clean] = true
		fullPath := filepath.Join(rootDir, clean)
		data, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			return nil, fmt.Errorf("referenced file %q does not exist: %w", clean, readErr)
		}
		sum := sha256.Sum256(data)
		pins = append(pins, SourcePin{
			Path:         clean,
			SHA256:       hex.EncodeToString(sum[:]),
			MustBeAbsent: false,
		})
	}

	// References (must exist on disk, check digest match if pinned)
	for _, ref := range targetedTask.References {
		clean := filepath.Clean(filepath.ToSlash(ref.Path))
		if clean == "." || clean == "" {
			continue
		}
		fullPath := filepath.Join(rootDir, clean)
		data, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			return nil, fmt.Errorf("referenced file %q does not exist: %w", clean, readErr)
		}
		sum := sha256.Sum256(data)
		actualSHA := hex.EncodeToString(sum[:])

		if ref.SHA256 != "" && ref.SHA256 != actualSHA {
			diags = append(diags, Diagnostic{
				Code:        DiagReferenceStale,
				Severity:    "warning",
				GoalSlug:    slug,
				Path:        clean,
				Message:     fmt.Sprintf("reference %q digest mismatch: recorded %s, current %s", clean, ref.SHA256, actualSHA),
				Remediation: "update reference digest or re-align with original source excerpt",
			})
		}

		if !seenPaths[clean] {
			seenPaths[clean] = true
			pins = append(pins, SourcePin{
				Path:         clean,
				SHA256:       actualSHA,
				MustBeAbsent: false,
			})
		}
	}

	// Create paths (must be absent or warning if existing)
	for _, p := range targetedTask.Files.Create {
		clean := filepath.Clean(filepath.ToSlash(p))
		if clean == "." || clean == "" || seenPaths[clean] {
			continue
		}
		seenPaths[clean] = true
		fullPath := filepath.Join(rootDir, clean)
		if fi, statErr := os.Stat(fullPath); statErr == nil && !fi.IsDir() {
			data, _ := os.ReadFile(fullPath)
			sum := sha256.Sum256(data)
			pins = append(pins, SourcePin{
				Path:         clean,
				SHA256:       hex.EncodeToString(sum[:]),
				MustBeAbsent: false,
			})
			diags = append(diags, Diagnostic{
				Code:        DiagCreatePathExists,
				Severity:    "warning",
				GoalSlug:    slug,
				Path:        clean,
				Message:     fmt.Sprintf("create path %q unexpectedly exists on disk", clean),
				Remediation: "inspect existing file before overwriting to prevent data loss",
			})
		} else {
			pins = append(pins, SourcePin{
				Path:         clean,
				SHA256:       "",
				MustBeAbsent: true,
			})
		}
	}

	// Sort SourcePins by Path deterministically
	sort.Slice(pins, func(i, j int) bool {
		return pins[i].Path < pins[j].Path
	})

	// Verify that declared prerequisites exist and validate their acceptance
	existingTasks := make(map[string]*TaskContract, len(pkg.Tasks))
	for i := range pkg.Tasks {
		existingTasks[pkg.Tasks[i].ID] = &pkg.Tasks[i]
	}

	var dependencyOutputs []DependencyOutput
	seenDepOutputs := make(map[string]bool)

	for _, depID := range targetedTask.DependsOn {
		depTask, exists := existingTasks[depID]
		if !exists {
			executable = false
			diags = append(diags, Diagnostic{
				Code:        DiagTaskDependencyMissing,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q depends on non-existent task %q", taskID, depID),
				Remediation: "ensure all prerequisite task IDs exist in the work package",
			})
			continue
		}

		// Revalidate prerequisite acceptance strictly: never trust a bare accepted:true record
		sel, rev, err := LoadTaskSelectionAndReview(rootDir, slug, depID)
		if err != nil {
			executable = false
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceIncomplete,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q prerequisite %q has no validated acceptance result from complete current evidence: %v", taskID, depID, err),
				Remediation: "complete acceptance evaluation for prerequisite task before dispatch",
			})
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceNotAvailable,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q depends on [%s] which are not yet certified as accepted", taskID, strings.Join(targetedTask.DependsOn, ", ")),
				Remediation: "prerequisite tasks must complete acceptance before this task can be dispatched",
			})
			continue
		}

		evalRes, err := EvaluateTaskAcceptance(rootDir, slug, depID, *sel, *rev)
		if err != nil || evalRes == nil || !evalRes.Accepted || evalRes.EvidenceStatus != EvidenceStatusAccepted {
			executable = false
			errMsg := "acceptance criteria not satisfied"
			if err != nil {
				errMsg = err.Error()
			} else if evalRes != nil && len(evalRes.Diagnostics) > 0 {
				errMsg = evalRes.Diagnostics[0].Message
			}
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceIncomplete,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q prerequisite %q acceptance revalidation failed: %s", taskID, depID, errMsg),
				Remediation: "re-run acceptance evaluation for prerequisite task",
			})
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceNotAvailable,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q depends on [%s] which are not yet certified as accepted", taskID, strings.Join(targetedTask.DependsOn, ", ")),
				Remediation: "prerequisite tasks must complete acceptance before this task can be dispatched",
			})
			continue
		}

		// Check if persisted acceptance record contradicts or is missing
		savedAcc, _ := LoadTaskAcceptance(rootDir, slug, depID)
		if savedAcc != nil && (!savedAcc.Accepted || savedAcc.EvidenceStatus != EvidenceStatusAccepted) {
			executable = false
			diags = append(diags, Diagnostic{
				Code:        DiagAcceptanceIncomplete,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q prerequisite %q persisted acceptance record is not accepted", taskID, depID),
				Remediation: "complete acceptance evaluation for prerequisite task before dispatch",
			})
			continue
		}

		// Verify candidate source is not stale
		cand, candErr := CaptureTaskCandidate(rootDir, slug)
		if candErr != nil {
			executable = false
			diags = append(diags, Diagnostic{
				Code:        DiagPacketDependencyStale,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q prerequisite %q candidate source is dirty or invalid: %v", taskID, depID, candErr),
				Remediation: "commit clean changes or resolve repository state",
			})
			continue
		}
		if cand != nil {
			curDigest := TaskCandidateDigest(*cand)
			if curDigest != "" && evalRes.CandidateDigest != "" && curDigest != evalRes.CandidateDigest {
				executable = false
				diags = append(diags, Diagnostic{
					Code:        DiagPacketDependencyStale,
					Severity:    "error",
					GoalSlug:    slug,
					Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
					Message:     fmt.Sprintf("task %q prerequisite %q candidate digest %q is stale against current %q", taskID, depID, evalRes.CandidateDigest, curDigest),
					Remediation: "re-run predecessor task against current candidate",
				})
				continue
			}
		}

		// Verify spec is not stale
		if specDigest != "" && evalRes.SpecDigest != "" && specDigest != evalRes.SpecDigest {
			executable = false
			diags = append(diags, Diagnostic{
				Code:        DiagPacketDependencyStale,
				Severity:    "error",
				GoalSlug:    slug,
				Path:        fmt.Sprintf("tasks[%s].depends_on", taskID),
				Message:     fmt.Sprintf("task %q prerequisite %q spec digest %q is stale against current %q", taskID, depID, evalRes.SpecDigest, specDigest),
				Remediation: "re-run predecessor task against current spec",
			})
			continue
		}

		// Verify that all receipt files exist on disk
		receiptsMissing := false
		for _, rp := range evalRes.ReceiptPaths {
			rFull := resolveReceiptPath(rootDir, slug, rp)
			if !fileExists(rFull) {
				executable = false
				receiptsMissing = true
				diags = append(diags, Diagnostic{
					Code:        DiagPacketDependencyStale,
					Severity:    "error",
					GoalSlug:    slug,
					Path:        rp,
					Message:     fmt.Sprintf("receipt %s for prerequisite task %s no longer exists on disk", rp, depID),
					Remediation: "re-run acceptance evaluation for prerequisite task",
				})
			}
		}
		if receiptsMissing {
			continue
		}

		// Check predecessor files on disk (create and edit files)
		artifactsMissing := false
		for _, p := range append(depTask.Files.Create, depTask.Files.Edit...) {
			clean := filepath.Clean(filepath.ToSlash(p))
			if clean == "." || clean == "" {
				continue
			}
			fullPath := filepath.Join(rootDir, clean)
			data, readErr := os.ReadFile(fullPath)
			if readErr != nil {
				executable = false
				artifactsMissing = true
				diags = append(diags, Diagnostic{
					Code:        DiagPacketDependencyStale,
					Severity:    "error",
					GoalSlug:    slug,
					Path:        clean,
					Message:     fmt.Sprintf("predecessor artifact %q for task %q is missing on disk: %v", clean, depID, readErr),
					Remediation: "re-run predecessor task to regenerate required artifact",
				})
				continue
			}
			sum := sha256.Sum256(data)
			hexSHA := hex.EncodeToString(sum[:])

			// If candidate commit is recorded, verify artifact has not been modified against candidate commit
			if evalRes.CandidateDigest != "" {
				if committedData, gitErr := exec.Command("git", "-C", rootDir, "show", evalRes.CandidateDigest+":"+clean).Output(); gitErr == nil {
					cSum := sha256.Sum256(committedData)
					cSHA := hex.EncodeToString(cSum[:])
					if hexSHA != cSHA {
						executable = false
						artifactsMissing = true
						diags = append(diags, Diagnostic{
							Code:        DiagPacketDependencyStale,
							Severity:    "error",
							GoalSlug:    slug,
							Path:        clean,
							Message:     fmt.Sprintf("predecessor artifact %q has modified content: recorded %s, current %s", clean, cSHA, hexSHA),
							Remediation: "re-run predecessor task or re-align dependency digest",
						})
						continue
					}
				}
			}

			key := fmt.Sprintf("%s:%s:%s", slug, depID, clean)
			if !seenDepOutputs[key] {
				seenDepOutputs[key] = true
				dependencyOutputs = append(dependencyOutputs, DependencyOutput{
					GoalSlug: slug,
					TaskID:   depID,
					Artifact: clean,
					SHA256:   hexSHA,
				})
			}
		}
		if artifactsMissing {
			continue
		}

		// Bind candidate digest in dependency_outputs
		if evalRes.CandidateDigest != "" {
			key := fmt.Sprintf("%s:%s:candidate", slug, depID)
			if !seenDepOutputs[key] {
				seenDepOutputs[key] = true
				dependencyOutputs = append(dependencyOutputs, DependencyOutput{
					GoalSlug: slug,
					TaskID:   depID,
					Artifact: "candidate",
					SHA256:   evalRes.CandidateDigest,
				})
			}
		}

		// Bind spec digest in dependency_outputs
		if evalRes.SpecDigest != "" {
			key := fmt.Sprintf("%s:%s:spec", slug, depID)
			if !seenDepOutputs[key] {
				seenDepOutputs[key] = true
				dependencyOutputs = append(dependencyOutputs, DependencyOutput{
					GoalSlug: slug,
					TaskID:   depID,
					Artifact: "spec",
					SHA256:   evalRes.SpecDigest,
				})
			}
		}

		// Also bind any artifacts from receipts
		for _, rp := range evalRes.ReceiptPaths {
			rFull := resolveReceiptPath(rootDir, slug, rp)
			if rData, err := os.ReadFile(rFull); err == nil {
				if ev, err := DecodeTaskEvidence(rData); err == nil {
					for _, art := range ev.Artifacts {
						clean := filepath.Clean(filepath.ToSlash(art.Path))
						if clean == "." || clean == "" {
							continue
						}
						fullPath := filepath.Join(rootDir, clean)
						if data, err := os.ReadFile(fullPath); err == nil {
							sum := sha256.Sum256(data)
							hexSHA := hex.EncodeToString(sum[:])
							key := fmt.Sprintf("%s:%s:%s", slug, depID, clean)
							if !seenDepOutputs[key] {
								seenDepOutputs[key] = true
								dependencyOutputs = append(dependencyOutputs, DependencyOutput{
									GoalSlug: slug,
									TaskID:   depID,
									Artifact: clean,
									SHA256:   hexSHA,
								})
							}
						}
					}
				}
			}
		}
	}

	sort.Slice(dependencyOutputs, func(i, j int) bool {
		ki := fmt.Sprintf("%s:%s:%s", dependencyOutputs[i].GoalSlug, dependencyOutputs[i].TaskID, dependencyOutputs[i].Artifact)
		kj := fmt.Sprintf("%s:%s:%s", dependencyOutputs[j].GoalSlug, dependencyOutputs[j].TaskID, dependencyOutputs[j].Artifact)
		return ki < kj
	})
	if dependencyOutputs == nil {
		dependencyOutputs = []DependencyOutput{}
	}

	// Check SpecReview status
	review, err := LoadSpecReview(rootDir, slug)
	if err != nil || review == nil {
		diags = append(diags, Diagnostic{
			Code:        DiagSpecReviewMissing,
			Severity:    "warning",
			GoalSlug:    slug,
			Message:     fmt.Sprintf("spec review missing for goal %s", slug),
			Remediation: "conduct spec review before starting implementation",
		})
	} else {
		if review.Verdict != VerdictReady {
			executable = false
			diags = append(diags, Diagnostic{
				Code:        DiagSpecNotReady,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("spec review verdict for goal %s is %q", slug, review.Verdict),
				Remediation: "resolve review findings until verdict is ready",
			})
		}
		if review.SpecDigest != specDigest {
			executable = false
			diags = append(diags, Diagnostic{
				Code:        DiagSpecReviewStale,
				Severity:    "error",
				GoalSlug:    slug,
				Message:     fmt.Sprintf("spec review for goal %s is stale against current package digest", slug),
				Remediation: "re-run spec review against current work package revision",
			})
		}
	}

	// If any diagnostic is an error, packet is not executable
	for _, d := range diags {
		if d.Severity == "error" {
			executable = false
			break
		}
	}

	// Sort diagnostics deterministically
	sort.Slice(diags, func(i, j int) bool {
		ki := fmt.Sprintf("%s:%s:%s:%s", diags[i].Severity, diags[i].Code, diags[i].Path, diags[i].Message)
		kj := fmt.Sprintf("%s:%s:%s:%s", diags[j].Severity, diags[j].Code, diags[j].Path, diags[j].Message)
		return ki < kj
	})

	packet := &TaskPacket{
		SchemaVersion:     TaskPacketSchemaVersion,
		GoalSlug:          slug,
		TaskID:            taskID,
		Purpose:           pkg.Purpose,
		Task:              *targetedTask,
		Acceptance:        taskCases,
		GlobalConstraints: append([]string(nil), pkg.Constraints...),
		NonGoals:          append([]string(nil), pkg.NonGoals...),
		DependencyOutputs: dependencyOutputs,
		SpecDigest:        specDigest,
		SourcePins:        pins,
		CandidateCommit:   candidateCommit,
		Executable:        executable,
		Diagnostics:       diags,
		MaxBytes:          0,
		Resume:            nil,
	}

	digest, err := ComputeTaskPacketDigest(packet)
	if err != nil {
		return nil, fmt.Errorf("compute task packet digest: %w", err)
	}
	packet.PacketDigest = digest

	return packet, nil
}

// DecodeTaskPacket strictly decodes a TaskPacket JSON payload, enforcing
// duplicate key rejection, no unknown fields, and valid schema version.
func DecodeTaskPacket(data []byte) (*TaskPacket, error) {
	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, fmt.Errorf("json token validation: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var packet TaskPacket
	if err := dec.Decode(&packet); err != nil {
		return nil, fmt.Errorf("decode task packet: %w", err)
	}

	if packet.SchemaVersion != TaskPacketSchemaVersion {
		return nil, fmt.Errorf("unsupported schema version %q: expected %q", packet.SchemaVersion, TaskPacketSchemaVersion)
	}

	return &packet, nil
}
