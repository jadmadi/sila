package goals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// TaskEvidenceSchemaVersion defines the canonical v1 schema version for task evidence envelopes.
const TaskEvidenceSchemaVersion = "sila/task-evidence/v1"

// EvidenceOutcome defines the evaluation result of an acceptance case or check.
type EvidenceOutcome string

const (
	OutcomePass    EvidenceOutcome = "pass"
	OutcomeFail    EvidenceOutcome = "fail"
	OutcomeBlocked EvidenceOutcome = "blocked"
	OutcomeNotRun  EvidenceOutcome = "not_run"
)

// IsValid reports whether o is a supported EvidenceOutcome.
func (o EvidenceOutcome) IsValid() bool {
	switch o {
	case OutcomePass, OutcomeFail, OutcomeBlocked, OutcomeNotRun:
		return true
	default:
		return false
	}
}

// EvidenceOrigin defines the producer source of the evidence receipt.
type EvidenceOrigin string

const (
	OriginRunner   EvidenceOrigin = "runner"
	OriginImported EvidenceOrigin = "imported"
)

// IsValid reports whether o is a supported EvidenceOrigin.
func (o EvidenceOrigin) IsValid() bool {
	switch o {
	case OriginRunner, OriginImported:
		return true
	default:
		return false
	}
}

// TaskCandidate captures the exact build and runtime environment identity
// under which acceptance checks were executed.
type TaskCandidate struct {
	Commit        string            `json:"commit"`
	ContentDigest string            `json:"content_digest"`
	ToolVersions  map[string]string `json:"tool_versions"`
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
}

// AssertionObservation records the evaluated result of a declared check assertion.
type AssertionObservation struct {
	Kind     AssertionKind `json:"kind"`
	Target   string        `json:"target"`
	Expected any           `json:"expected"`
	Observed any           `json:"observed"`
	Passed   bool          `json:"passed"`
}

// EvidenceArtifact records a retained file artifact and its cryptographic SHA-256 digest.
type EvidenceArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// TaskEvidence encapsulates an immutable, candidate-bound verification receipt
// proving the execution and outcome of a specific acceptance case check.
type TaskEvidence struct {
	SchemaVersion string                 `json:"schema_version"`
	AttemptID     string                 `json:"attempt_id"`
	GoalSlug      string                 `json:"goal_slug"`
	TaskID        string                 `json:"task_id"`
	CaseID        string                 `json:"case_id"`
	CheckID       string                 `json:"check_id"`
	SpecDigest    string                 `json:"spec_digest"`
	PacketDigest  string                 `json:"packet_digest"`
	Candidate     TaskCandidate          `json:"candidate"`
	Scope         Scope                  `json:"scope"`
	Outcome       EvidenceOutcome        `json:"outcome"`
	Origin        EvidenceOrigin         `json:"origin"`
	Executable    string                 `json:"executable"`
	Args          []string               `json:"args"`
	Cwd           string                 `json:"cwd"`
	ExitCode      *int                   `json:"exit_code"`
	Assertions    []AssertionObservation `json:"assertions"`
	Artifacts     []EvidenceArtifact     `json:"artifacts"`
	StartedAt     time.Time              `json:"started_at"`
	EndedAt       time.Time              `json:"ended_at"`
	FailureCode   string                 `json:"failure_code,omitempty"`
	Observation   map[string]any         `json:"observation,omitempty"`
}

// Diagnostic codes for task evidence validation.
const (
	DiagEvidenceScopeMismatch         = "EVIDENCE_SCOPE_MISMATCH"
	DiagEvidenceGoalMismatch          = "EVIDENCE_GOAL_MISMATCH"
	DiagEvidenceTaskUnknown           = "EVIDENCE_TASK_UNKNOWN"
	DiagEvidenceCaseUnknown           = "EVIDENCE_CASE_UNKNOWN"
	DiagEvidenceCheckUnknown          = "EVIDENCE_CHECK_UNKNOWN"
	DiagEvidenceAssertionsEmpty       = "EVIDENCE_ASSERTIONS_EMPTY"
	DiagEvidenceAssertionFailed       = "EVIDENCE_ASSERTION_FAILED"
	DiagEvidenceContradictoryExit     = "EVIDENCE_CONTRADICTORY_EXIT"
	DiagEvidenceCandidateMissing      = "EVIDENCE_CANDIDATE_MISSING"
	DiagEvidenceArtifactDigestInvalid = "EVIDENCE_ARTIFACT_DIGEST_INVALID"
	DiagEvidenceSpecDigestMismatch    = "EVIDENCE_SPEC_DIGEST_MISMATCH"
	DiagEvidencePacketDigestInvalid   = "EVIDENCE_PACKET_DIGEST_INVALID"
	DiagEvidenceOutcomeInvalid        = "EVIDENCE_OUTCOME_INVALID"
	DiagEvidenceOriginInvalid         = "EVIDENCE_ORIGIN_INVALID"
)

var sha256HexRegex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// DecodeTaskEvidence strictly decodes a TaskEvidence JSON payload, enforcing
// duplicate key rejection, no unknown fields, and valid schema version.
func DecodeTaskEvidence(data []byte) (*TaskEvidence, error) {
	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, fmt.Errorf("json token validation: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var ev TaskEvidence
	if err := dec.Decode(&ev); err != nil {
		return nil, fmt.Errorf("decode task evidence: %w", err)
	}

	if ev.SchemaVersion != TaskEvidenceSchemaVersion {
		return nil, fmt.Errorf("unsupported schema version %q: expected %q", ev.SchemaVersion, TaskEvidenceSchemaVersion)
	}

	return &ev, nil
}

// ValidateTaskEvidence validates that an evidence receipt conforms strictly to
// the referenced WorkPackage specifications, case scope, and candidate requirements.
func ValidateTaskEvidence(pkg *WorkPackage, ev *TaskEvidence) []Diagnostic {
	var diags []Diagnostic

	if ev == nil {
		return []Diagnostic{
			{
				Code:     "EVIDENCE_NIL",
				Severity: "error",
				Message:  "task evidence receipt is nil",
			},
		}
	}

	if pkg == nil {
		return []Diagnostic{
			{
				Code:     "WORK_PACKAGE_NIL",
				Severity: "error",
				Message:  "work package is nil",
			},
		}
	}

	// 1. Goal slug alignment
	if ev.GoalSlug != pkg.GoalSlug {
		diags = append(diags, Diagnostic{
			Code:     DiagEvidenceGoalMismatch,
			Severity: "error",
			GoalSlug: pkg.GoalSlug,
			Message:  fmt.Sprintf("evidence goal %q does not match package goal %q", ev.GoalSlug, pkg.GoalSlug),
		})
	}

	// 2. Task lookup and declaration
	var matchedTask *TaskContract
	for i := range pkg.Tasks {
		if pkg.Tasks[i].ID == ev.TaskID {
			matchedTask = &pkg.Tasks[i]
			break
		}
	}
	if matchedTask == nil {
		diags = append(diags, Diagnostic{
			Code:     DiagEvidenceTaskUnknown,
			Severity: "error",
			GoalSlug: pkg.GoalSlug,
			Message:  fmt.Sprintf("task %q not found in work package %s", ev.TaskID, pkg.GoalSlug),
		})
	}

	// 3. Case lookup and declaration
	var matchedCase *AcceptanceCase
	for i := range pkg.Acceptance {
		if pkg.Acceptance[i].ID == ev.CaseID {
			matchedCase = &pkg.Acceptance[i]
			break
		}
	}
	if matchedCase == nil {
		diags = append(diags, Diagnostic{
			Code:     DiagEvidenceCaseUnknown,
			Severity: "error",
			GoalSlug: pkg.GoalSlug,
			Message:  fmt.Sprintf("acceptance case %q not found in work package %s", ev.CaseID, pkg.GoalSlug),
		})
	} else if matchedTask != nil {
		// Verify task declares this acceptance case
		caseDeclared := false
		for _, id := range matchedTask.AcceptanceIDs {
			if id == ev.CaseID {
				caseDeclared = true
				break
			}
		}
		if !caseDeclared {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceCaseUnknown,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  fmt.Sprintf("task %q does not declare acceptance case %q", ev.TaskID, ev.CaseID),
			})
		}
	}

	// 4. Check lookup and declaration
	if matchedCase != nil {
		checkInCase := false
		for _, id := range matchedCase.CheckIDs {
			if id == ev.CheckID {
				checkInCase = true
				break
			}
		}
		if !checkInCase {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceCheckUnknown,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  fmt.Sprintf("acceptance case %q does not declare check %q", ev.CaseID, ev.CheckID),
			})
		}
	}

	if matchedTask != nil {
		checkInTask := false
		for _, c := range matchedTask.Checks {
			if c.ID == ev.CheckID {
				checkInTask = true
				break
			}
		}
		if !checkInTask {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceCheckUnknown,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  fmt.Sprintf("task %q does not declare check %q", ev.TaskID, ev.CheckID),
			})
		}
	}

	// 5. Exact scope equality (AC-002)
	if matchedCase != nil {
		if ev.Scope != matchedCase.RequiredScope {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceScopeMismatch,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  fmt.Sprintf("evidence scope %q does not match required case scope %q", ev.Scope, matchedCase.RequiredScope),
			})
		}
	}

	// 6. Spec digest alignment
	if ev.SpecDigest != "" {
		expectedDigest, err := WorkPackageDigest(pkg)
		if err == nil && ev.SpecDigest != expectedDigest {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceSpecDigestMismatch,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  fmt.Sprintf("evidence spec_digest %q does not match package digest %q", ev.SpecDigest, expectedDigest),
			})
		}
	}

	// 7. Packet digest format check
	if ev.PacketDigest != "" && !sha256HexRegex.MatchString(ev.PacketDigest) {
		diags = append(diags, Diagnostic{
			Code:     DiagEvidencePacketDigestInvalid,
			Severity: "error",
			GoalSlug: pkg.GoalSlug,
			Message:  fmt.Sprintf("evidence packet_digest %q is not a valid 64-char hex SHA-256", ev.PacketDigest),
		})
	}

	// 8. Candidate identity verification (AC-003)
	if strings.TrimSpace(ev.Candidate.Commit) == "" ||
		strings.TrimSpace(ev.Candidate.ContentDigest) == "" ||
		strings.TrimSpace(ev.Candidate.OS) == "" ||
		strings.TrimSpace(ev.Candidate.Arch) == "" {
		diags = append(diags, Diagnostic{
			Code:     DiagEvidenceCandidateMissing,
			Severity: "error",
			GoalSlug: pkg.GoalSlug,
			Message:  "task candidate must include non-empty commit, content_digest, os, and arch",
		})
	}

	// 9. Outcome and Origin validity
	if !ev.Outcome.IsValid() {
		diags = append(diags, Diagnostic{
			Code:     DiagEvidenceOutcomeInvalid,
			Severity: "error",
			GoalSlug: pkg.GoalSlug,
			Message:  fmt.Sprintf("invalid evidence outcome %q", ev.Outcome),
		})
	}
	if !ev.Origin.IsValid() {
		diags = append(diags, Diagnostic{
			Code:     DiagEvidenceOriginInvalid,
			Severity: "error",
			GoalSlug: pkg.GoalSlug,
			Message:  fmt.Sprintf("invalid evidence origin %q", ev.Origin),
		})
	}

	// 10. Pass outcome checks (AC-003)
	if ev.Outcome == OutcomePass {
		// Non-zero exit code contradictory to pass
		if ev.ExitCode != nil && *ev.ExitCode != 0 {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceContradictoryExit,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  fmt.Sprintf("evidence outcome is pass but exit_code is %d", *ev.ExitCode),
			})
		}
		// Runner origin must record an exit code
		if ev.ExitCode == nil && ev.Origin == OriginRunner {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceContradictoryExit,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  "runner check with pass outcome must record an exit code",
			})
		}
		// Empty assertions
		if len(ev.Assertions) == 0 {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceAssertionsEmpty,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  "evidence with pass outcome must have non-empty assertions",
			})
		}
		// All assertions must be passed
		for _, a := range ev.Assertions {
			if !a.Passed {
				diags = append(diags, Diagnostic{
					Code:     DiagEvidenceAssertionFailed,
					Severity: "error",
					GoalSlug: pkg.GoalSlug,
					Message:  fmt.Sprintf("assertion on target %q (%s) did not pass", a.Target, a.Kind),
				})
			}
		}
	}

	// 11. Artifact digest validity (AC-003)
	for _, art := range ev.Artifacts {
		if strings.TrimSpace(art.Path) == "" {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactDigestInvalid,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  "evidence artifact has empty path",
			})
		}
		if !sha256HexRegex.MatchString(art.SHA256) {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactDigestInvalid,
				Severity: "error",
				GoalSlug: pkg.GoalSlug,
				Message:  fmt.Sprintf("artifact %q has invalid SHA-256 digest %q", art.Path, art.SHA256),
			})
		}
	}

	return diags
}
