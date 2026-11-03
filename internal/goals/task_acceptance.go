package goals

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Schema versions for task acceptance protocol.
const (
	ReceiptSelectionSchemaVersion = "sila/receipt-selection/v1"
	AcceptanceReviewSchemaVersion = "sila/acceptance-review/v1"
	AcceptanceResultSchemaVersion = "sila/acceptance-result/v1"
)

// NewerFailureDisposition records a reviewer explanation for superseding a newer failing receipt.
type NewerFailureDisposition struct {
	ReceiptPath string `json:"receipt_path"`
	Reason      string `json:"reason"`
	Reviewer    string `json:"reviewer"`
}

// ReceiptSelection identifies the explicit set of receipts selected to satisfy task acceptance.
type ReceiptSelection struct {
	SchemaVersion            string                    `json:"schema_version"`
	GoalSlug                 string                    `json:"goal_slug"`
	TaskID                   string                    `json:"task_id"`
	SpecDigest               string                    `json:"spec_digest"`
	CandidateDigest          string                    `json:"candidate_digest"`
	ReceiptPaths             []string                  `json:"receipt_paths"`
	NewerFailureDispositions []NewerFailureDisposition `json:"newer_failure_dispositions"`
}

// CaseDisposition records the reviewer's verdict on an individual acceptance case.
type CaseDisposition struct {
	CaseID   string `json:"case_id"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

// AcceptanceReview captures an attested or authenticated review of a task candidate.
type AcceptanceReview struct {
	SchemaVersion          string            `json:"schema_version"`
	Reviewer               string            `json:"reviewer"`
	Producer               string            `json:"producer"`
	CandidateDigest        string            `json:"candidate_digest"`
	SpecDigest             string            `json:"spec_digest"`
	Disposition            string            `json:"disposition"` // accept | request_changes
	CaseDispositions       []CaseDisposition `json:"case_dispositions"`
	ReviewedAt             string            `json:"reviewed_at"`
	AuthenticatedAuditPath string            `json:"authenticated_audit_path,omitempty"`
}

// EvidenceStatus represents the evaluation state of acceptance evidence.
type EvidenceStatus string

const (
	EvidenceStatusNotRun     EvidenceStatus = "not_run"
	EvidenceStatusIncomplete EvidenceStatus = "incomplete"
	EvidenceStatusFailed     EvidenceStatus = "failed"
	EvidenceStatusStale      EvidenceStatus = "stale"
	EvidenceStatusAccepted   EvidenceStatus = "accepted"
)

// AcceptanceResult records the authoritative evaluation result of a task or goal acceptance check.
type AcceptanceResult struct {
	SchemaVersion   string         `json:"schema_version"`
	GoalSlug        string         `json:"goal_slug"`
	TaskID          string         `json:"task_id,omitempty"` // empty for goal
	Accepted        bool           `json:"accepted"`
	EvidenceStatus  EvidenceStatus `json:"evidence_status"` // not_run | incomplete | failed | stale | accepted
	SpecDigest      string         `json:"spec_digest"`
	CandidateDigest string         `json:"candidate_digest"`
	ReceiptPaths    []string       `json:"receipt_paths"`
	MissingCaseIDs  []string       `json:"missing_case_ids"`
	FailedCaseIDs   []string       `json:"failed_case_ids"`
	Diagnostics     []Diagnostic   `json:"diagnostics"`
	ReviewedAt      string         `json:"reviewed_at"`
}

// DecodeReceiptSelection strictly parses a ReceiptSelection JSON payload.
func DecodeReceiptSelection(data []byte) (*ReceiptSelection, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("empty receipt selection data")
	}
	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, fmt.Errorf("receipt selection duplicate keys or trailing bytes: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var sel ReceiptSelection
	if err := dec.Decode(&sel); err != nil {
		return nil, fmt.Errorf("decode receipt selection: %w", err)
	}
	if sel.SchemaVersion != ReceiptSelectionSchemaVersion {
		return nil, fmt.Errorf("unsupported receipt selection schema version %q (expected %q)", sel.SchemaVersion, ReceiptSelectionSchemaVersion)
	}
	return &sel, nil
}

// DecodeAcceptanceReview strictly parses an AcceptanceReview JSON payload.
func DecodeAcceptanceReview(data []byte) (*AcceptanceReview, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("empty acceptance review data")
	}
	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, fmt.Errorf("acceptance review duplicate keys or trailing bytes: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var rev AcceptanceReview
	if err := dec.Decode(&rev); err != nil {
		return nil, fmt.Errorf("decode acceptance review: %w", err)
	}
	if rev.SchemaVersion != AcceptanceReviewSchemaVersion {
		return nil, fmt.Errorf("unsupported acceptance review schema version %q (expected %q)", rev.SchemaVersion, AcceptanceReviewSchemaVersion)
	}
	return &rev, nil
}

// DecodeAcceptanceResult strictly parses an AcceptanceResult JSON payload.
func DecodeAcceptanceResult(data []byte) (*AcceptanceResult, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("empty acceptance result data")
	}
	if err := checkDuplicateKeysAndEOF(data); err != nil {
		return nil, fmt.Errorf("acceptance result duplicate keys or trailing bytes: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var res AcceptanceResult
	if err := dec.Decode(&res); err != nil {
		return nil, fmt.Errorf("decode acceptance result: %w", err)
	}
	if res.SchemaVersion != AcceptanceResultSchemaVersion {
		return nil, fmt.Errorf("unsupported acceptance result schema version %q (expected %q)", res.SchemaVersion, AcceptanceResultSchemaVersion)
	}
	return &res, nil
}

func resolveReceiptPath(rootDir, slug string, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	clean := filepath.Clean(p)
	candidates := []string{
		filepath.Join(rootDir, clean),
		filepath.Join(rootDir, "goals", slug, clean),
		filepath.Join(rootDir, "goals", slug, "evidence", clean),
	}
	for _, c := range candidates {
		if fileExists(c) {
			return c
		}
	}
	return filepath.Join(rootDir, clean)
}

// TaskCandidateDigest returns the candidate content digest or fallback commit.
func TaskCandidateDigest(cand TaskCandidate) string {
	if strings.TrimSpace(cand.ContentDigest) != "" {
		return cand.ContentDigest
	}
	return cand.Commit
}

// EvaluateTaskAcceptance computes complete task acceptance over selected receipts and reviewer attestations.
func EvaluateTaskAcceptance(rootDir, slug, taskID string, selection ReceiptSelection, review AcceptanceReview) (*AcceptanceResult, error) {
	if strings.TrimSpace(slug) == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	if strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("task ID is required")
	}
	rootDir = resolveGoalsRootDir(rootDir)
	slug = filepath.Clean(slug)
	taskID = strings.TrimSpace(taskID)

	res := &AcceptanceResult{
		SchemaVersion:   AcceptanceResultSchemaVersion,
		GoalSlug:        slug,
		TaskID:          taskID,
		Accepted:        false,
		EvidenceStatus:  EvidenceStatusIncomplete,
		SpecDigest:      selection.SpecDigest,
		CandidateDigest: selection.CandidateDigest,
		ReceiptPaths:    []string{},
		MissingCaseIDs:  []string{},
		FailedCaseIDs:   []string{},
		Diagnostics:     []Diagnostic{},
		ReviewedAt:      review.ReviewedAt,
	}
	if res.ReviewedAt == "" {
		res.ReviewedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// 1. Load WorkPackage and compute specDigest
	wp, err := LoadWorkPackage(rootDir, slug)
	if err != nil {
		return nil, fmt.Errorf("load work package for %s: %w", slug, err)
	}
	actualSpecDigest, err := WorkPackageDigest(wp)
	if err != nil {
		return nil, fmt.Errorf("compute work package digest: %w", err)
	}
	res.SpecDigest = actualSpecDigest

	// Find the task contract
	var task *TaskContract
	for i := range wp.Tasks {
		if wp.Tasks[i].ID == taskID {
			task = &wp.Tasks[i]
			break
		}
	}
	if task == nil {
		return nil, fmt.Errorf("task %q not found in work package for goal %q", taskID, slug)
	}

	// 2. Identify all mandatory acceptance cases for this task
	declaredCaseIDs := make(map[string]bool, len(task.AcceptanceIDs))
	for _, id := range task.AcceptanceIDs {
		declaredCaseIDs[id] = true
	}
	var mandatoryCaseIDs []string
	for i := range wp.Acceptance {
		c := &wp.Acceptance[i]
		if declaredCaseIDs[c.ID] && c.Mandatory {
			mandatoryCaseIDs = append(mandatoryCaseIDs, c.ID)
		}
	}
	sort.Strings(mandatoryCaseIDs)

	// AC-004: zero cases never pass
	if len(mandatoryCaseIDs) == 0 {
		res.EvidenceStatus = EvidenceStatusIncomplete
		res.MissingCaseIDs = []string{"NO_CASES_DEFINED"}
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Code:     "NO_CASES_DEFINED",
			Severity: "error",
			GoalSlug: slug,
			Message:  fmt.Sprintf("task %s defines zero mandatory acceptance cases; zero cases never pass", taskID),
		})
		return res, nil
	}

	// 3. Spec digest validation
	if selection.SpecDigest == "" || selection.SpecDigest != actualSpecDigest {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Code:     "SPEC_DIGEST_MISMATCH",
			Severity: "error",
			GoalSlug: slug,
			Message:  fmt.Sprintf("selection spec_digest %q does not match actual spec_digest %q", selection.SpecDigest, actualSpecDigest),
		})
	}
	if review.SpecDigest == "" || review.SpecDigest != actualSpecDigest {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Code:     "SPEC_DIGEST_MISMATCH",
			Severity: "error",
			GoalSlug: slug,
			Message:  fmt.Sprintf("review spec_digest %q does not match actual spec_digest %q", review.SpecDigest, actualSpecDigest),
		})
	}

	// 4. Candidate digest matching
	if selection.CandidateDigest == "" || review.CandidateDigest == "" || selection.CandidateDigest != review.CandidateDigest {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Code:     "CANDIDATE_DIGEST_MISMATCH",
			Severity: "error",
			GoalSlug: slug,
			Message:  fmt.Sprintf("candidate digest mismatch between selection (%q) and review (%q)", selection.CandidateDigest, review.CandidateDigest),
		})
	}
	res.CandidateDigest = selection.CandidateDigest

	// 5. Reviewer-class independence guard
	prod := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(review.Producer), "@"))
	rev := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(review.Reviewer), "@"))
	if prod != "" && rev != "" && prod == rev {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Code:     "SELF_REVIEW_PROHIBITED",
			Severity: "error",
			GoalSlug: slug,
			Message:  fmt.Sprintf("self-audit prohibited: producer %q cannot review own task", prod),
		})
	}
	if review.Disposition != "accept" {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Code:     "REVIEW_DISPOSITION_REJECTED",
			Severity: "error",
			GoalSlug: slug,
			Message:  fmt.Sprintf("review disposition is %q (expected 'accept')", review.Disposition),
		})
	}

	// Authenticated audit receipt verification if specified
	if review.AuthenticatedAuditPath != "" {
		auditFullPath := resolveReceiptPath(rootDir, slug, review.AuthenticatedAuditPath)
		if aData, err := os.ReadFile(auditFullPath); err != nil {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "AUTHENTICATED_AUDIT_MISSING",
				Severity: "error",
				GoalSlug: slug,
				Path:     review.AuthenticatedAuditPath,
				Message:  fmt.Sprintf("authenticated audit receipt missing: %v", err),
			})
		} else {
			var rec AuditReceipt
			if err := json.Unmarshal(aData, &rec); err != nil {
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Code:     "AUTHENTICATED_AUDIT_INVALID",
					Severity: "error",
					GoalSlug: slug,
					Path:     review.AuthenticatedAuditPath,
					Message:  fmt.Sprintf("authenticated audit receipt invalid: %v", err),
				})
			} else if err := VerifyAuditReceipt(&rec); err != nil {
				res.Diagnostics = append(res.Diagnostics, Diagnostic{
					Code:     "AUTHENTICATED_AUDIT_INVALID",
					Severity: "error",
					GoalSlug: slug,
					Path:     review.AuthenticatedAuditPath,
					Message:  fmt.Sprintf("authenticated audit verification failed: %v", err),
				})
			}
		}
	}

	reviewCaseDisp := make(map[string]CaseDisposition, len(review.CaseDispositions))
	for _, cd := range review.CaseDispositions {
		reviewCaseDisp[cd.CaseID] = cd
	}

	// 6. Process selected receipts
	if len(selection.ReceiptPaths) == 0 {
		res.EvidenceStatus = EvidenceStatusNotRun
		res.MissingCaseIDs = mandatoryCaseIDs
		return res, nil
	}

	coveredCases := make(map[string]*TaskEvidence)
	selectedReceiptPaths := make([]string, 0, len(selection.ReceiptPaths))

	for _, relPath := range selection.ReceiptPaths {
		fullPath := resolveReceiptPath(rootDir, slug, relPath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "RECEIPT_FILE_MISSING",
				Severity: "error",
				GoalSlug: slug,
				Path:     relPath,
				Message:  fmt.Sprintf("receipt file not found: %s (%v)", relPath, err),
			})
			continue
		}

		ev, err := DecodeTaskEvidence(data)
		if err != nil {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "RECEIPT_DECODE_FAILED",
				Severity: "error",
				GoalSlug: slug,
				Path:     relPath,
				Message:  fmt.Sprintf("failed to decode receipt %s: %v", relPath, err),
			})
			continue
		}

		selectedReceiptPaths = append(selectedReceiptPaths, relPath)

		if ev.GoalSlug != slug || ev.TaskID != taskID {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "RECEIPT_TASK_MISMATCH",
				Severity: "error",
				GoalSlug: slug,
				Path:     relPath,
				Message:  fmt.Sprintf("receipt belongs to %s/%s, expected %s/%s", ev.GoalSlug, ev.TaskID, slug, taskID),
			})
			continue
		}

		if ev.SpecDigest != actualSpecDigest {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "SPEC_DIGEST_MISMATCH",
				Severity: "error",
				GoalSlug: slug,
				Path:     relPath,
				Message:  fmt.Sprintf("receipt spec_digest %q does not match actual spec_digest %q", ev.SpecDigest, actualSpecDigest),
			})
		}

		evCandDigest := TaskCandidateDigest(ev.Candidate)
		if evCandDigest != selection.CandidateDigest && ev.Candidate.Commit != selection.CandidateDigest {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "CANDIDATE_DIGEST_MISMATCH",
				Severity: "error",
				GoalSlug: slug,
				Path:     relPath,
				Message:  fmt.Sprintf("receipt candidate digest %q does not match selection candidate digest %q", evCandDigest, selection.CandidateDigest),
			})
		}

		artDiags := ValidateEvidenceArtifacts(rootDir, slug, ev)
		if len(artDiags) > 0 {
			res.Diagnostics = append(res.Diagnostics, artDiags...)
		}

		if existing, dup := coveredCases[ev.CaseID]; dup {
			res.Diagnostics = append(res.Diagnostics, Diagnostic{
				Code:     "DUPLICATE_CASE_COVERAGE",
				Severity: "error",
				GoalSlug: slug,
				Path:     relPath,
				Message:  fmt.Sprintf("case %s covered multiple times (receipts %s and %s)", ev.CaseID, existing.AttemptID, ev.AttemptID),
			})
		}
		coveredCases[ev.CaseID] = ev

		// Stale review check
		if review.ReviewedAt != "" && !ev.EndedAt.IsZero() {
			if revTime, err := time.Parse(time.RFC3339, review.ReviewedAt); err == nil {
				if revTime.Before(ev.EndedAt) {
					res.Diagnostics = append(res.Diagnostics, Diagnostic{
						Code:     "REVIEW_STALE",
						Severity: "error",
						GoalSlug: slug,
						Message:  fmt.Sprintf("review timestamp %s is stale against evidence ended at %s", review.ReviewedAt, ev.EndedAt.Format(time.RFC3339)),
					})
				}
			}
		}

		casePassed := ev.Outcome == OutcomePass
		if casePassed {
			for _, a := range ev.Assertions {
				if !a.Passed {
					casePassed = false
					break
				}
			}
		}

		if cd, ok := reviewCaseDisp[ev.CaseID]; ok {
			if !cd.Accepted {
				casePassed = false
			}
		}

		if !casePassed {
			res.FailedCaseIDs = append(res.FailedCaseIDs, ev.CaseID)
		}
	}

	res.ReceiptPaths = selectedReceiptPaths

	// 7. Check coverage of all mandatory cases
	for _, cid := range mandatoryCaseIDs {
		if _, ok := coveredCases[cid]; !ok {
			res.MissingCaseIDs = append(res.MissingCaseIDs, cid)
		}
	}
	sort.Strings(res.MissingCaseIDs)
	sort.Strings(res.FailedCaseIDs)

	// 8. Undisposed newer failure check
	disposedReceipts := make(map[string]bool, len(selection.NewerFailureDispositions))
	for _, nfd := range selection.NewerFailureDispositions {
		if strings.TrimSpace(nfd.Reason) != "" && strings.TrimSpace(nfd.Reviewer) != "" {
			disposedReceipts[filepath.Clean(nfd.ReceiptPath)] = true
			disposedReceipts[filepath.Base(filepath.Clean(nfd.ReceiptPath))] = true
		}
	}

	evidenceDir := filepath.Join(rootDir, "goals", slug, "evidence", taskID)
	if entries, err := os.ReadDir(evidenceDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			rPath := filepath.Join(evidenceDir, e.Name(), "receipt.json")
			rData, err := os.ReadFile(rPath)
			if err != nil {
				continue
			}
			otherEv, err := DecodeTaskEvidence(rData)
			if err != nil {
				continue
			}
			isFail := otherEv.Outcome == OutcomeFail
			if !isFail {
				for _, a := range otherEv.Assertions {
					if !a.Passed {
						isFail = true
						break
					}
				}
			}
			if isFail {
				if selectedEv, ok := coveredCases[otherEv.CaseID]; ok {
					if otherEv.EndedAt.After(selectedEv.EndedAt) {
						relRPath := filepath.Join("goals", slug, "evidence", taskID, e.Name(), "receipt.json")
						if !disposedReceipts[relRPath] && !disposedReceipts[rPath] && !disposedReceipts[e.Name()] {
							res.Diagnostics = append(res.Diagnostics, Diagnostic{
								Code:     "UNDISPOSED_NEWER_FAILURE",
								Severity: "error",
								GoalSlug: slug,
								Path:     relRPath,
								Message:  fmt.Sprintf("newer failing receipt for case %s (ended at %s) requires explicit reviewer disposition", otherEv.CaseID, otherEv.EndedAt.Format(time.RFC3339)),
							})
						}
					}
				}
			}
		}
	}

	// 9. Determine final EvidenceStatus and Accepted
	hasErrorDiag := false
	for _, d := range res.Diagnostics {
		if d.Severity == "error" {
			hasErrorDiag = true
			break
		}
	}

	if len(res.MissingCaseIDs) > 0 {
		res.EvidenceStatus = EvidenceStatusIncomplete
		res.Accepted = false
	} else if len(res.FailedCaseIDs) > 0 || hasErrorDiag {
		res.EvidenceStatus = EvidenceStatusFailed
		res.Accepted = false
	} else {
		res.EvidenceStatus = EvidenceStatusAccepted
		res.Accepted = true
	}

	return res, nil
}
