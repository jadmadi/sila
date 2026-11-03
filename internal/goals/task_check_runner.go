package goals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Failure codes for check execution.
const (
	FailureCodeTimeout              = "TIMEOUT"
	FailureCodeCanceled             = "CANCELED"
	FailureCodeLaunchFailed         = "LAUNCH_FAILED"
	FailureCodeOutputOverflow       = "OUTPUT_OVERFLOW"
	FailureCodeAssertionFailed      = "ASSERTION_FAILED"
	FailureCodeAssertionNotObserved = "ASSERTION_NOT_OBSERVED"
	FailureCodeExitCodeMismatch     = "EXIT_CODE_MISMATCH"
	FailureCodeUnmetCapability      = "UNMET_CAPABILITY"
	FailureCodeDependentTaskBlocker = "DEPENDENT_TASKS_UNSUPPORTED"
)

// maxArtifactBytes bounds stdout/stderr captures (5MB ceiling).
const maxArtifactBytes = 5 * 1024 * 1024

// resolveJSONPointer resolves an RFC 6901 JSON pointer against a decoded JSON value.
func resolveJSONPointer(doc any, pointer string) (any, error) {
	if pointer == "" {
		return doc, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON pointer must start with /: %q", pointer)
	}

	tokens := strings.Split(pointer[1:], "/")
	current := doc

	for _, token := range tokens {
		// RFC 6901 unescaping
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")

		switch v := current.(type) {
		case map[string]any:
			val, ok := v[token]
			if !ok {
				return nil, fmt.Errorf("property %q not found in object", token)
			}
			current = val
		case []any:
			idx, err := strconv.Atoi(token)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil, fmt.Errorf("array index %q out of bounds", token)
			}
			current = v[idx]
		default:
			return nil, fmt.Errorf("cannot navigate %q into scalar type %T", token, current)
		}
	}

	return current, nil
}

// GoTestJSONEvent captures a single go test -json output event.
type GoTestJSONEvent struct {
	Time    *time.Time `json:"Time,omitempty"`
	Action  string     `json:"Action"`
	Package string     `json:"Package,omitempty"`
	Test    string     `json:"Test,omitempty"`
	Elapsed *float64   `json:"Elapsed,omitempty"`
	Output  string     `json:"Output,omitempty"`
}

// evaluateAssertion evaluates a single declared Assertion against stdout/stderr observations.
func evaluateAssertion(a Assertion, stdoutBytes []byte, exitCode int) (AssertionObservation, string) {
	obs := AssertionObservation{
		Kind:     a.Kind,
		Target:   a.Target,
		Expected: a.Expected,
		Passed:   false,
	}

	switch a.Kind {
	case AssertionGoTestPass:
		// Target format: "package/path/TestName" or "TestName"
		targetTest := a.Target
		if idx := strings.LastIndex(a.Target, "/"); idx != -1 && idx < len(a.Target)-1 {
			if strings.HasPrefix(a.Target[idx+1:], "Test") {
				targetTest = a.Target[idx+1:]
			}
		}

		lines := strings.Split(string(stdoutBytes), "\n")
		foundTestEvent := false
		testPassed := false

		for _, l := range lines {
			line := strings.TrimSpace(l)
			if line == "" {
				continue
			}

			// Try parsing as go test -json event
			if strings.HasPrefix(line, "{") && strings.HasSuffix(line, "}") {
				var ev GoTestJSONEvent
				if err := json.Unmarshal([]byte(line), &ev); err == nil && ev.Test != "" {
					if ev.Test == targetTest {
						foundTestEvent = true
						if ev.Action == "pass" {
							testPassed = true
						}
					}
				}
			}

			// Fallback: plain go test output (--- PASS: TestName)
			if strings.Contains(line, "--- PASS: "+targetTest) {
				foundTestEvent = true
				testPassed = true
			}
		}

		if !foundTestEvent {
			obs.Observed = "no_events_observed"
			return obs, FailureCodeAssertionNotObserved
		}

		obs.Observed = testPassed
		obs.Passed = testPassed
		if !testPassed {
			return obs, FailureCodeAssertionFailed
		}
		return obs, ""

	case AssertionStdoutExact:
		actual := strings.TrimSpace(string(stdoutBytes))
		expected := fmt.Sprint(a.Expected)
		obs.Observed = actual
		if actual == expected {
			obs.Passed = true
			return obs, ""
		}
		return obs, FailureCodeAssertionFailed

	case AssertionJSONPointerEquals:
		var parsed any
		if err := json.Unmarshal(stdoutBytes, &parsed); err != nil {
			obs.Observed = fmt.Sprintf("invalid_json: %v", err)
			return obs, FailureCodeAssertionFailed
		}

		val, err := resolveJSONPointer(parsed, a.Target)
		if err != nil {
			obs.Observed = fmt.Sprintf("pointer_error: %v", err)
			return obs, FailureCodeAssertionFailed
		}

		obs.Observed = val
		// Compare JSON representations of observed vs expected
		vJSON, _ := json.Marshal(val)
		eJSON, _ := json.Marshal(a.Expected)
		if string(vJSON) == string(eJSON) {
			obs.Passed = true
			return obs, ""
		}
		return obs, FailureCodeAssertionFailed

	default:
		obs.Observed = fmt.Sprintf("unknown_assertion_kind: %s", a.Kind)
		return obs, FailureCodeAssertionFailed
	}
}

// RunTaskChecks executes all declared local checks for taskID under goal slug,
// collecting cryptographic candidate-bound evidence receipts and saving execution logs.
func RunTaskChecks(ctx context.Context, rootDir, slug, taskID string) ([]TaskEvidence, error) {
	// 1. Load work package
	pkg, err := LoadWorkPackage(rootDir, slug)
	if err != nil {
		return nil, fmt.Errorf("load work package: %w", err)
	}

	// 2. Validate dispatch and pins via TaskPacket API
	packet, err := BuildTaskPacket(rootDir, slug, taskID)
	if err != nil {
		return nil, fmt.Errorf("build task packet: %w", err)
	}
	diags := ValidateTaskPacket(rootDir, packet)
	for _, d := range diags {
		if d.Severity == "error" {
			return nil, fmt.Errorf("dispatch validation failure: %s", d.Message)
		}
	}

	// Find task
	var task *TaskContract
	for i := range pkg.Tasks {
		if pkg.Tasks[i].ID == taskID {
			task = &pkg.Tasks[i]
			break
		}
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found in work package", taskID)
	}

	// 3. Stage 1 check: dependent tasks return explicit blocker
	if len(task.DependsOn) > 0 {
		var blockedResults []TaskEvidence
		specDigest, _ := WorkPackageDigest(pkg)
		for _, caseID := range task.AcceptanceIDs {
			blockedResults = append(blockedResults, TaskEvidence{
				SchemaVersion: TaskEvidenceSchemaVersion,
				AttemptID:     fmt.Sprintf("att-%s-%s", taskID, caseID),
				GoalSlug:      slug,
				TaskID:        taskID,
				CaseID:        caseID,
				SpecDigest:    specDigest,
				PacketDigest:  packet.PacketDigest,
				Scope:         ScopeUnit,
				Outcome:       OutcomeBlocked,
				Origin:        OriginRunner,
				StartedAt:     time.Now().UTC(),
				EndedAt:       time.Now().UTC(),
				FailureCode:   FailureCodeDependentTaskBlocker,
			})
		}
		return blockedResults, nil
	}

	// 4. Capture TaskCandidate
	candidate, err := CaptureTaskCandidate(rootDir, slug)
	if err != nil {
		return nil, fmt.Errorf("capture task candidate: %w", err)
	}

	specDigest, _ := WorkPackageDigest(pkg)
	evidenceDir := filepath.Join(rootDir, "goals", slug, "evidence", taskID)
	if err := os.MkdirAll(evidenceDir, 0755); err != nil {
		return nil, fmt.Errorf("create evidence directory: %w", err)
	}

	var results []TaskEvidence

	// 5. Execute checks for each declared acceptance case
	for _, caseID := range task.AcceptanceIDs {
		var ac *AcceptanceCase
		for i := range pkg.Acceptance {
			if pkg.Acceptance[i].ID == caseID {
				ac = &pkg.Acceptance[i]
				break
			}
		}
		if ac == nil {
			continue
		}

		for _, checkID := range ac.CheckIDs {
			var chk *Check
			for i := range task.Checks {
				if task.Checks[i].ID == checkID {
					chk = &task.Checks[i]
					break
				}
			}
			if chk == nil {
				continue
			}

			attemptID := fmt.Sprintf("att-%s-%s-%d", taskID, checkID, time.Now().UnixNano())
			startedAt := time.Now().UTC()

			// Check capability: non-local capability returns blocked
			if chk.Capability != CapabilityLocal {
				results = append(results, TaskEvidence{
					SchemaVersion: TaskEvidenceSchemaVersion,
					AttemptID:     attemptID,
					GoalSlug:      slug,
					TaskID:        taskID,
					CaseID:        caseID,
					CheckID:       checkID,
					SpecDigest:    specDigest,
					PacketDigest:  packet.PacketDigest,
					Candidate:     *candidate,
					Scope:         ac.RequiredScope,
					Outcome:       OutcomeBlocked,
					Origin:        OriginRunner,
					Executable:    chk.Executable,
					Args:          chk.Args,
					Cwd:           chk.Cwd,
					StartedAt:     startedAt,
					EndedAt:       time.Now().UTC(),
					FailureCode:   FailureCodeUnmetCapability,
				})
				continue
			}

			// Execution directory
			targetCwd := rootDir
			if chk.Cwd != "" && chk.Cwd != "." {
				targetCwd = filepath.Join(rootDir, chk.Cwd)
			}

			timeoutSec := chk.TimeoutSeconds
			if timeoutSec <= 0 {
				timeoutSec = 30
			}

			runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)

			cmd := exec.CommandContext(runCtx, chk.Executable, chk.Args...)
			cmd.Dir = targetCwd

			// Environment configuration
			cmd.Env = os.Environ()
			for k, v := range chk.Environment {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}

			var stdoutBuf bytes.Buffer
			cmd.Stdout = &stdoutBuf
			cmd.Stderr = &stdoutBuf

			runErr := cmd.Run()
			endedAt := time.Now().UTC()
			ctxErr := runCtx.Err()
			cancel()

			rawOutput := stdoutBuf.Bytes()
			overflow := false
			if len(rawOutput) > maxArtifactBytes {
				rawOutput = rawOutput[:maxArtifactBytes]
				overflow = true
			}

			// Write output artifact
			artifactRelPath := filepath.Join("goals", slug, "evidence", taskID, fmt.Sprintf("%s.log", checkID))
			artifactDiskPath := filepath.Join(rootDir, artifactRelPath)
			_ = os.WriteFile(artifactDiskPath, rawOutput, 0644)
			artSha := sha256.Sum256(rawOutput)

			artifacts := []EvidenceArtifact{
				{
					Path:   artifactRelPath,
					SHA256: hex.EncodeToString(artSha[:]),
				},
			}

			var exitCode *int
			outcome := OutcomePass
			failureCode := ""

			if runErr != nil {
				if ctxErr == context.DeadlineExceeded {
					failureCode = FailureCodeTimeout
					outcome = OutcomeFail
				} else if ctxErr == context.Canceled {
					failureCode = FailureCodeCanceled
					outcome = OutcomeBlocked
				} else if exitErr, ok := runErr.(*exec.ExitError); ok {
					code := exitErr.ExitCode()
					exitCode = &code
				} else {
					failureCode = FailureCodeLaunchFailed
					outcome = OutcomeFail
				}
			} else {
				code0 := 0
				exitCode = &code0
			}

			if overflow && outcome == OutcomePass {
				outcome = OutcomeFail
				failureCode = FailureCodeOutputOverflow
			}

			// Evaluate exit code against expected_exit_code (AC-008)
			if exitCode != nil && *exitCode != chk.ExpectedExitCode {
				outcome = OutcomeFail
				if failureCode == "" {
					failureCode = FailureCodeExitCodeMismatch
				}
			}

			// Evaluate assertions
			var evaluatedAssertions []AssertionObservation
			for _, a := range chk.Assertions {
				obs, aFailCode := evaluateAssertion(a, rawOutput, chk.ExpectedExitCode)
				evaluatedAssertions = append(evaluatedAssertions, obs)
				if !obs.Passed && outcome == OutcomePass {
					outcome = OutcomeFail
					if failureCode == "" {
						failureCode = aFailCode
					}
				}
			}

			results = append(results, TaskEvidence{
				SchemaVersion: TaskEvidenceSchemaVersion,
				AttemptID:     attemptID,
				GoalSlug:      slug,
				TaskID:        taskID,
				CaseID:        caseID,
				CheckID:       checkID,
				SpecDigest:    specDigest,
				PacketDigest:  packet.PacketDigest,
				Candidate:     *candidate,
				Scope:         ac.RequiredScope,
				Outcome:       outcome,
				Origin:        OriginRunner,
				Executable:    chk.Executable,
				Args:          chk.Args,
				Cwd:           chk.Cwd,
				ExitCode:      exitCode,
				Assertions:    evaluatedAssertions,
				Artifacts:     artifacts,
				StartedAt:     startedAt,
				EndedAt:       endedAt,
				FailureCode:   failureCode,
			})
		}
	}

	return results, nil
}
