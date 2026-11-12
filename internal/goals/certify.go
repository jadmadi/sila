package goals

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// CertificationMemory is the memory-contract outcome recorded on certification.
type CertificationMemory struct {
	Required     bool     `json:"required"`
	Status       string   `json:"status"` // verified | waived | legacy_skipped
	Prime        bool     `json:"prime"`
	Recall       bool     `json:"recall"`
	Writes       bool     `json:"writes"`
	Facts        bool     `json:"facts"`
	Handoff      bool     `json:"handoff"`
	Verified     bool     `json:"verified,omitempty"`
	Waived       bool     `json:"waived,omitempty"`
	WaiverReason string   `json:"waiver_reason,omitempty"`
	Diagnostics  []string `json:"diagnostics,omitempty"`
}

// CertifyOptions controls optional memory-contract and session-independence enforcement.
type CertifyOptions struct {
	NoMemory  bool
	Reason    string
	SessionID string
}

// CertificationRecord captures an evidence-based goal certification.
type CertificationRecord struct {
	GoalSlug           string               `json:"goal_slug"`
	CertifiedAt        time.Time            `json:"certified_at"`
	CertifiedBy        string               `json:"certified_by"`
	EvidenceFile       string               `json:"evidence_file"`
	Note               string               `json:"note"`
	Verified           bool                 `json:"verified"`
	FactsPassed        int                  `json:"facts_passed,omitempty"`
	FactsTotal         int                  `json:"facts_total,omitempty"`
	EvidenceKind       string               `json:"evidence_kind,omitempty"`
	CommitSHA          string               `json:"commit_sha,omitempty"`
	MemoryRequired     bool                 `json:"memory_required,omitempty"`
	MemoryVerified     bool                 `json:"memory_verified,omitempty"`
	MemoryWaived       bool                 `json:"memory_waived,omitempty"`
	MemoryWaiverReason string               `json:"memory_waiver_reason,omitempty"`
	MemoryDiagnostics  []string             `json:"memory_diagnostics,omitempty"`
	Memory             *CertificationMemory `json:"memory,omitempty"`
}

// MaxMarkdownEvidenceSize bounds markdown evidence accepted by CertifyGoal.
const MaxMarkdownEvidenceSize = 1024 * 1024

// MarkdownEvidenceVerifier validates a markdown evidence document before
// CertifyGoal records certification. The CLI wires this to internal/assess at
// init so CLI and MCP callers share one certification path without introducing
// an internal/goals -> internal/assess import cycle.
type MarkdownEvidenceVerifier func(rootDir, slug, evidencePath string, data []byte) error

var markdownEvidenceVerifier MarkdownEvidenceVerifier

// SetMarkdownEvidenceVerifier installs the process-wide markdown verifier.
func SetMarkdownEvidenceVerifier(v MarkdownEvidenceVerifier) {
	markdownEvidenceVerifier = v
}

func normalizeAgentName(agent string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(agent), "@"))
}

// normalizeReceiptEvidencePath scrubs host-local absolute paths so certification.json
// stores a portable repository-relative path (e.g., "goals/<slug>/evidence.md").
func normalizeReceiptEvidencePath(rootDir, evPath string) string {
	if evPath == "" {
		return ""
	}
	if rootDir != "" {
		absRoot, rErr := filepath.Abs(rootDir)
		absEv, eErr := filepath.Abs(evPath)
		if rErr == nil && eErr == nil {
			if rel, relErr := filepath.Rel(absRoot, absEv); relErr == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
				return filepath.ToSlash(rel)
			}
		}
	}
	slashed := filepath.ToSlash(filepath.Clean(evPath))
	if idx := strings.Index(slashed, "/goals/"); idx >= 0 {
		return slashed[idx+1:]
	}
	if idx := strings.Index(slashed, "/.sila/"); idx >= 0 {
		return slashed[idx+1:]
	}
	if home := os.Getenv("HOME"); home != "" {
		cleanHome := filepath.ToSlash(filepath.Clean(home))
		if strings.HasPrefix(slashed, cleanHome+"/") {
			return "~" + strings.TrimPrefix(slashed, cleanHome)
		}
	}
	if filepath.IsAbs(evPath) {
		return filepath.Base(evPath)
	}
	return slashed
}

// enforceProducerIndependence rejects certification or auditing by the agent
// that implemented or claimed the goal.
func enforceProducerIndependence(goalDir, agent, action string) error {
	return enforceProducerSessionIndependence(goalDir, agent, "", action)
}

// enforceProducerSessionIndependence rejects certification or auditing when the
// reviewer matches the producer agent or shares the same active session ID with
// a named producer (blocking same-session --agent alias spoofing).
func enforceProducerSessionIndependence(goalDir, agent, sessionID, action string) error {
	cert := normalizeAgentName(agent)
	if cert == "" {
		return nil
	}

	type producerBinding struct {
		agent   string
		session string
	}
	var bindings []producerBinding
	if data, err := os.ReadFile(filepath.Join(goalDir, "goal.md")); err == nil {
		if isnad := ParseIsnadHeader(string(data)); isnad != nil && isnad.ImplementedBy != "" {
			bindings = append(bindings, producerBinding{
				agent:   isnad.ImplementedBy,
				session: strings.TrimSpace(isnad.ImplementedSession),
			})
		}
	}
	if claim, err := readClaim(filepath.Join(goalDir, "claim.json")); err == nil && claim != nil {
		cSess := strings.TrimSpace(claim.SessionID)
		bindings = append(bindings,
			producerBinding{agent: claim.Agent, session: cSess},
			producerBinding{agent: claim.ClaimedBy, session: cSess},
		)
	}

	for _, b := range bindings {
		prod := normalizeAgentName(b.agent)
		if prod == "" || prod == "agent" {
			continue
		}
		if prod == cert {
			return fmt.Errorf("self-audit prohibited: producer (%s) cannot %s own goal", prod, action)
		}
	}

	sess := strings.TrimSpace(sessionID)
	if sess != "" && cert != "agent" {
		for _, b := range bindings {
			prod := normalizeAgentName(b.agent)
			if prod == "" || prod == "agent" {
				continue
			}
			if b.session != "" && b.session == sess {
				return fmt.Errorf("self-audit prohibited: session bleed detected: reviewer (%s) shares session (%s) with producer (%s)", cert, sess, prod)
			}
		}
	}
	return nil
}

// CertifyGoal certifies a goal from an evidence file (e.g. test log, verification report, facts-result.json).
func CertifyGoal(rootDir, slug, evidenceFile, agent, note string) (*CertificationRecord, error) {
	return CertifyGoalWithOptions(rootDir, slug, evidenceFile, agent, note, CertifyOptions{})
}

// CertifyGoalWithOptions certifies with an explicit memory waiver policy.
func CertifyGoalWithOptions(rootDir, slug, evidenceFile, agent, note string, opts CertifyOptions) (*CertificationRecord, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	goalMdPath := filepath.Join(goalDir, "goal.md")
	if !fileExists(goalMdPath) {
		return nil, fmt.Errorf("goal package %q does not exist in goals/%s", slug, slug)
	}
	goalData, _ := os.ReadFile(goalMdPath)
	memoryProtocol := ParseMemoryProtocol(string(goalData))
	memory := &CertificationMemory{Required: memoryProtocol == "required", Status: "legacy_skipped"}
	if memoryProtocol != "required" {
		memory.Diagnostics = []string{fmt.Sprintf("memory protocol not required for legacy goals (missing '- **Memory Protocol**: required' in goals/%s/goal.md)", slug)}
	}

	// 1. Enforce non-overrulable reviewer veto
	vetoPath := filepath.Join(goalDir, "veto.json")
	if fileExists(vetoPath) {
		if data, err := os.ReadFile(vetoPath); err == nil {
			var vr struct {
				Active      bool   `json:"active"`
				AuditorName string `json:"auditor_name"`
				Reason      string `json:"reason"`
			}
			if json.Unmarshal(data, &vr) == nil && vr.Active {
				return nil, fmt.Errorf("cannot certify goal %q: active non-overrulable reviewer veto by %s: %s", slug, vr.AuditorName, vr.Reason)
			}
		}
	}

	// 2. Enforce reviewer-class independence (producer cannot certify own work)
	if err := enforceProducerSessionIndependence(goalDir, agent, opts.SessionID, "certify"); err != nil {
		return nil, err
	}

	// 3. For structured goals with work-package.json, dispatch to structured acceptance.
	wpPath := filepath.Join(goalDir, "work-package.json")
	if fileExists(wpPath) {
		if evidenceFile != "" && !strings.Contains(evidenceFile, "-contract.json") {
			return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: structured goal %q requires task acceptance receipts; legacy facts certification is unsupported for work packages (use 'sila goals accept <slug> <task-id>')", slug)
		}

		if active, err := GetActiveCertification(rootDir, slug); err == nil && active != nil {
			return active, nil
		}

		eval, err := EvaluateGoalAcceptance(rootDir, slug)
		if err != nil {
			return nil, fmt.Errorf("evaluate structured goal acceptance: %w", err)
		}
		if eval == nil || !eval.Accepted {
			missingStr := ""
			if eval != nil && len(eval.MissingCaseIDs) > 0 {
				missingStr = fmt.Sprintf(" (missing cases: %s)", strings.Join(eval.MissingCaseIDs, ", "))
			}
			failedStr := ""
			if eval != nil && len(eval.FailedCaseIDs) > 0 {
				failedStr = fmt.Sprintf(" (failed cases: %s)", strings.Join(eval.FailedCaseIDs, ", "))
			}
			return nil, fmt.Errorf("cannot certify structured goal %q: acceptance criteria incomplete%s%s", slug, missingStr, failedStr)
		}

		if active, err := GetActiveCertification(rootDir, slug); err == nil && active != nil {
			return active, nil
		}

		cert := &CertificationRecord{
			GoalSlug:           slug,
			CertifiedAt:        time.Now().UTC(),
			CertifiedBy:        agent,
			EvidenceFile:       "work-package.json",
			Note:               note,
			Verified:           true,
			EvidenceKind:       "structured_acceptance",
			MemoryRequired:     memory.Required,
			MemoryVerified:     memory.Verified,
			MemoryWaived:       memory.Waived,
			MemoryWaiverReason: memory.WaiverReason,
			MemoryDiagnostics:  memory.Diagnostics,
			Memory:             memory,
		}
		if cert.CertifiedBy == "" {
			cert.CertifiedBy = "agent"
		}
		if evidenceFile != "" {
			cert.EvidenceFile = normalizeReceiptEvidencePath(rootDir, evidenceFile)
		}
		if headOut, err := exec.Command("git", "-C", rootDir, "rev-parse", "HEAD").Output(); err == nil {
			cert.CommitSHA = strings.TrimSpace(string(headOut))
		}

		certBytes, err := json.MarshalIndent(cert, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal certification.json: %w", err)
		}

		targetFile := filepath.Join(goalDir, "certification.json")
		if err := AtomicWriteFile(targetFile, certBytes, 0644); err != nil {
			return nil, fmt.Errorf("write certification.json: %w", err)
		}

		// Bind the certification to the content it covered.
		_, _ = RecordVerification(rootDir, slug, LevelL2, "verify")

		// Release active claim only if verified closed in git history; otherwise retain claim.json and charter
		releaseClaimLockIfClosed(rootDir, goalDir, slug)

		// Auto-sync README
		if audit, err := ScanGoals(rootDir); err == nil {
			_ = SyncReadme(rootDir, audit)
		}

		return cert, nil
	}

	if evidenceFile == "" {
		return nil, fmt.Errorf("evidence file path is required (--evidence=<file>)")
	}

	// Resolve evidence file relative to rootDir or cwd
	evPath := evidenceFile
	if !filepath.IsAbs(evPath) {
		if fileExists(filepath.Join(rootDir, evidenceFile)) {
			evPath = filepath.Join(rootDir, evidenceFile)
		} else if fileExists(filepath.Join(goalDir, evidenceFile)) {
			evPath = filepath.Join(goalDir, evidenceFile)
		}
	}
	if !fileExists(evPath) {
		return nil, fmt.Errorf("evidence file not found: %s", evidenceFile)
	}

	evData, err := os.ReadFile(evPath)
	if err != nil {
		return nil, fmt.Errorf("read evidence file: %w", err)
	}

	if memoryProtocol == "required" {
		memory.Required = true
		if opts.NoMemory {
			if strings.TrimSpace(opts.Reason) == "" {
				return nil, fmt.Errorf("MEMORY_WAIVER_REASON_REQUIRED: --no-memory requires a non-empty --reason")
			}
			memory.Status = "waived"
			memory.Waived = true
			memory.WaiverReason = opts.Reason
		} else {
			diags := ValidateMemoryEvidence(slug, string(evData))
			if len(diags) > 0 {
				memory.Diagnostics = diags
				return nil, fmt.Errorf("MEMORY_CERTIFICATION_BLOCKED: %s", strings.Join(diags, "; "))
			}
			memory.Status = "verified"
			memory.Verified = true
			if mw, _ := ParseMemoryWrites(string(evData)); mw != nil {
				memory.Prime = mw.Prime.Ran
				memory.Recall = len(mw.Recall) > 0
				memory.Writes = len(mw.Memos)+len(mw.Lessons)+len(mw.Cases) > 0
				memory.Facts = mw.Facts.ExitCode == 0
				memory.Handoff = strings.TrimSpace(mw.Handoff.ID) != ""
			}
		}
	}

	if isMarkdownEvidenceFile(evPath) && !(filepath.Base(evPath) == "goal.md" && fileExists(filepath.Join(goalDir, "contract.json"))) {
		return certifyFromMarkdownEvidence(rootDir, goalDir, slug, evPath, evData, agent, note, memory)
	}

	trimmed := strings.TrimSpace(string(evData))
	if trimmed == "" {
		return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: evidence file is empty; expected valid JSON. Migration guidance: provide a valid facts JSON or run structured acceptance")
	}

	// Strict parsing for legacy facts/results JSON
	type factItem struct {
		ID                    string `json:"id"`
		Fact                  string `json:"fact"`
		Passed                *bool  `json:"passed"`
		Verified              *bool  `json:"verified"`
		AutomatedVerification bool   `json:"automatedVerification"`
	}
	type factsResult struct {
		Facts   []factItem `json:"facts"`
		Results []factItem `json:"results"`
	}

	var items []factItem

	// Plain-text reports or non-JSON are rejected
	if !json.Valid([]byte(trimmed)) {
		// Legacy compatibility: if evidenceFile points to the goal specification itself (goal.md)
		// and the goal directory contains an established verification contract, accept it as legacy contract evidence.
		contractPath := filepath.Join(goalDir, "contract.json")
		if filepath.Base(evPath) == "goal.md" && fileExists(contractPath) {
			tVal := true
			items = []factItem{
				{
					ID:       "contract",
					Fact:     "verification contract passed",
					Passed:   &tVal,
					Verified: &tVal,
				},
			}
		} else {
			return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: evidence file is not valid JSON; plain-text reports are not supported. Migration guidance: provide a valid facts JSON or run structured acceptance")
		}
	} else {
		trimmedBytes := bytes.TrimSpace([]byte(trimmed))
		if !bytes.HasPrefix(trimmedBytes, []byte("{")) && !bytes.HasPrefix(trimmedBytes, []byte("[")) {
			return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: evidence must be a JSON object or array; scalar primitives are not supported")
		}

		if bytes.HasPrefix(trimmedBytes, []byte("[")) {
			if err := json.Unmarshal(trimmedBytes, &items); err != nil {
				return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: invalid facts array: %w", err)
			}
		} else {
			var parsed factsResult
			if err := json.Unmarshal(trimmedBytes, &parsed); err != nil {
				return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: invalid facts object: %w", err)
			}
			items = parsed.Facts
			if len(items) == 0 {
				items = parsed.Results
			}
		}

		if len(items) == 0 {
			// Check if it is a verification contract result JSON (e.g. from AutoCertifyGoal)
			var contractRes struct {
				ContractCommand string `json:"contract_command"`
				ExitCode        *int   `json:"exit_code"`
			}
			if err := json.Unmarshal(trimmedBytes, &contractRes); err == nil && contractRes.ContractCommand != "" && contractRes.ExitCode != nil {
				if *contractRes.ExitCode != 0 {
					return nil, fmt.Errorf("verification contract %q failed with exit code %d; cannot certify", contractRes.ContractCommand, *contractRes.ExitCode)
				}
				tVal := true
				items = []factItem{
					{
						ID:       "contract",
						Fact:     fmt.Sprintf("contract %s passed", contractRes.ContractCommand),
						Passed:   &tVal,
						Verified: &tVal,
					},
				}
			} else {
				return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: evidence contains zero facts or results; at least one passed fact is required")
			}
		}
	}

	seenIDs := make(map[string]bool, len(items))
	factsPassed := 0
	factsTotal := 0

	for i, f := range items {
		id := strings.TrimSpace(f.ID)
		if id == "" {
			return nil, fmt.Errorf("empty fact ID at index %d", i)
		}
		if seenIDs[id] {
			return nil, fmt.Errorf("duplicate fact ID %q", id)
		}
		seenIDs[id] = true

		if f.Passed != nil && f.Verified != nil {
			if *f.Passed != *f.Verified {
				return nil, fmt.Errorf("fact %q has contradictory passed (%v) and verified (%v) values", id, *f.Passed, *f.Verified)
			}
		}

		hasExplicitTrue := (f.Passed != nil && *f.Passed) || (f.Verified != nil && *f.Verified)
		if !hasExplicitTrue {
			if (f.Passed != nil && !*f.Passed) || (f.Verified != nil && !*f.Verified) {
				return nil, fmt.Errorf("fact %q has passed=false; all facts must pass to certify", id)
			}
			return nil, fmt.Errorf("fact %q has neither 'passed' nor 'verified' set to true", id)
		}

		factsTotal++
		factsPassed++
	}

	agent = strings.TrimPrefix(agent, "@")
	if agent == "" {
		agent = "agent"
	}
	if note == "" {
		note = fmt.Sprintf("certified from legacy evidence: %s", filepath.Base(evPath))
	}

	cert := &CertificationRecord{
		GoalSlug:           slug,
		CertifiedAt:        time.Now().UTC(),
		CertifiedBy:        agent,
		EvidenceFile:       normalizeReceiptEvidencePath(rootDir, evPath),
		Note:               note,
		Verified:           true,
		FactsPassed:        factsPassed,
		FactsTotal:         factsTotal,
		EvidenceKind:       "legacy_facts",
		MemoryRequired:     memory.Required,
		MemoryVerified:     memory.Verified,
		MemoryWaived:       memory.Waived,
		MemoryWaiverReason: memory.WaiverReason,
		MemoryDiagnostics:  memory.Diagnostics,
		Memory:             memory,
	}
	if headOut, err := exec.Command("git", "-C", rootDir, "rev-parse", "HEAD").Output(); err == nil {
		cert.CommitSHA = strings.TrimSpace(string(headOut))
	}

	certBytes, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal certification.json: %w", err)
	}

	targetFile := filepath.Join(goalDir, "certification.json")
	if err := AtomicWriteFile(targetFile, certBytes, 0644); err != nil {
		return nil, fmt.Errorf("write certification.json: %w", err)
	}

	// Bind the evidence review to the content it covered.
	_, _ = RecordVerification(rootDir, slug, LevelL1, "evidence")

	// Release active claim only if verified closed in git history; otherwise retain claim.json and charter
	releaseClaimLockIfClosed(rootDir, goalDir, slug)

	// Auto-sync README
	if audit, err := ScanGoals(rootDir); err == nil {
		_ = SyncReadme(rootDir, audit)
	}

	return cert, nil
}

func isMarkdownEvidenceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

// certifyFromMarkdownEvidence validates a markdown review document through the
// injected verifier, binds it to HEAD, and persists certification.json.
func certifyFromMarkdownEvidence(rootDir, goalDir, slug, evPath string, data []byte, agent, note string, memory *CertificationMemory) (*CertificationRecord, error) {
	if len(data) > MaxMarkdownEvidenceSize {
		return nil, fmt.Errorf("EVIDENCE_TOO_LARGE: evidence input exceeds 1MiB limit (%d bytes)", len(data))
	}
	if markdownEvidenceVerifier == nil {
		return nil, fmt.Errorf("EVIDENCE_FORMAT_UNSUPPORTED: markdown evidence verification is not configured")
	}
	if err := markdownEvidenceVerifier(rootDir, slug, evPath, data); err != nil {
		return nil, err
	}

	agent = strings.TrimPrefix(agent, "@")
	if agent == "" {
		agent = "agent"
	}
	if note == "" {
		note = fmt.Sprintf("certified from review document: %s", filepath.Base(evPath))
	}

	cert := &CertificationRecord{
		GoalSlug:           slug,
		CertifiedAt:        time.Now().UTC(),
		CertifiedBy:        agent,
		EvidenceFile:       normalizeReceiptEvidencePath(rootDir, evPath),
		Note:               note,
		Verified:           true,
		FactsPassed:        1,
		FactsTotal:         1,
		EvidenceKind:       "review_document",
		MemoryRequired:     memory.Required,
		MemoryVerified:     memory.Verified,
		MemoryWaived:       memory.Waived,
		MemoryWaiverReason: memory.WaiverReason,
		MemoryDiagnostics:  memory.Diagnostics,
		Memory:             memory,
	}
	if headOut, err := exec.Command("git", "-C", rootDir, "rev-parse", "HEAD").Output(); err == nil {
		cert.CommitSHA = strings.TrimSpace(string(headOut))
	}

	certBytes, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal certification.json: %w", err)
	}
	if err := AtomicWriteFile(filepath.Join(goalDir, "certification.json"), certBytes, 0644); err != nil {
		return nil, fmt.Errorf("write certification.json: %w", err)
	}

	// Bind the evidence review to the content it covered.
	_, _ = RecordVerification(rootDir, slug, LevelL1, "evidence")

	// Release active claim only if verified closed in git history; otherwise retain claim.json and charter.
	releaseClaimLockIfClosed(rootDir, goalDir, slug)

	if audit, err := ScanGoals(rootDir); err == nil {
		_ = SyncReadme(rootDir, audit)
	}

	return cert, nil
}

// GetActiveCertification reads certification.json if present and valid.
func GetActiveCertification(rootDir, slug string) (*CertificationRecord, error) {
	if slug == "" {
		return nil, nil
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	certPath := filepath.Join(rootDir, "goals", slug, "certification.json")
	if !fileExists(certPath) {
		return nil, nil
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	var cert CertificationRecord
	if err := json.Unmarshal(data, &cert); err != nil {
		return nil, err
	}
	if !cert.Verified {
		return nil, nil
	}
	return &cert, nil
}

// AutoCertifyGoal executes the immutable verification contract for a goal and,
// upon passing, automatically writes certification evidence and marks the goal verified.
func AutoCertifyGoal(ctx context.Context, rootDir, slug, agent, note string) (*CertificationRecord, *ContractResult, error) {
	if slug == "" {
		return nil, nil, fmt.Errorf("goal slug is required")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)

	// 0. For structured goals with work-package.json, enforce acceptance first
	wpPath := filepath.Join(rootDir, "goals", slug, "work-package.json")
	if fileExists(wpPath) {
		eval, err := EvaluateGoalAcceptance(rootDir, slug)
		if err != nil {
			return nil, nil, fmt.Errorf("evaluate goal acceptance: %w", err)
		}
		if eval == nil || !eval.Accepted {
			return nil, nil, fmt.Errorf("cannot auto-certify structured goal %q: acceptance criteria incomplete", slug)
		}
	}

	// 1. Run the verification contract
	res, err := RunVerificationContract(ctx, rootDir, slug)
	if err != nil {
		_, _, _ = RecordClaimIteration(rootDir, slug, false, err.Error())
		return nil, res, fmt.Errorf("verification contract error: %w", err)
	}

	// 2. Track iteration count in claim
	_, tripped, _ := RecordClaimIteration(rootDir, slug, res.Passed, res.FailureReason)
	if !res.Passed {
		msg := res.FailureReason
		if tripped {
			msg = fmt.Sprintf("%s (circuit breaker tripped: claim lock released)", msg)
		}
		return nil, res, fmt.Errorf("verification contract failed: %s", msg)
	}

	// 3. Save passed output as verification evidence
	evidenceDir := filepath.Join(rootDir, ".sila", "evidence")
	_ = os.MkdirAll(evidenceDir, 0755)
	evPath := filepath.Join(evidenceDir, fmt.Sprintf("%s-contract.json", slug))
	evObj := map[string]any{
		"contract_command": slug,
		"exit_code":        res.ExitCode,
		"duration_ms":      res.DurationMs,
		"date":             time.Now().UTC().Format(time.RFC3339),
		"output_snippet":   res.OutputSnippet,
		"facts": []map[string]any{
			{
				"id":                    fmt.Sprintf("%s-contract", slug),
				"fact":                  fmt.Sprintf("Contract execution for goal %s passed", slug),
				"passed":                true,
				"verified":              true,
				"automatedVerification": true,
			},
		},
	}
	evBytes, err := json.MarshalIndent(evObj, "", "  ")
	if err != nil {
		return nil, res, fmt.Errorf("marshal contract evidence: %w", err)
	}
	_ = AtomicWriteFile(evPath, evBytes, 0644)

	if note == "" {
		note = fmt.Sprintf("auto-certified via immutable contract in %dms", res.DurationMs)
	}

	// 4. Certify goal
	cert, cErr := CertifyGoal(rootDir, slug, evPath, agent, note)
	if cErr != nil {
		return nil, res, cErr
	}
	return cert, res, nil
}

// FormatReceiptDigest produces the canonical preimage bytes for cryptographic isnad signing.
func FormatReceiptDigest(receipt *AuditReceipt) []byte {
	if receipt == nil {
		return nil
	}
	ts := ""
	if !receipt.CertifiedAt.IsZero() {
		ts = receipt.CertifiedAt.UTC().Format(time.RFC3339)
	}
	payload := fmt.Sprintf("%s:%s:%s:%s:%s:%s:%.4f:%.4f:%d:%d:%t:%t:%s",
		receipt.GoalSlug,
		receipt.CommitSHA,
		receipt.SpecHash,
		receipt.ProbeManifestHash,
		receipt.SandboxNonce,
		receipt.CandidateSelection,
		receipt.CCDSIScore,
		receipt.ReqCoverage,
		receipt.ProbesExecuted,
		receipt.ProbesPassed,
		receipt.CanaryPassed,
		receipt.BlindfoldVerified,
		ts,
	)
	if receipt.WaiverSummary != "" {
		payload += ":" + receipt.WaiverSummary
	}
	return []byte(payload)
}

// VerifyAuditReceipt validates cryptographic signatures and sensitivity thresholds.
func VerifyAuditReceipt(receipt *AuditReceipt) error {
	if receipt == nil {
		return fmt.Errorf("audit receipt cannot be nil")
	}
	if receipt.Version != "sila/audit-receipt/v1" {
		return fmt.Errorf("unsupported receipt version %q, expected %q", receipt.Version, "sila/audit-receipt/v1")
	}
	if strings.TrimSpace(receipt.GoalSlug) == "" {
		return fmt.Errorf("goal slug is required in audit receipt")
	}
	if !receipt.BlindfoldVerified {
		return fmt.Errorf("blindfold isolation was not verified")
	}
	if !receipt.CanaryPassed {
		return fmt.Errorf("canary mutant was not detected")
	}
	if receipt.CCDSIScore < 0.40 {
		return fmt.Errorf("confirmation bias error: CCDSI score %0.3f < 0.40", receipt.CCDSIScore)
	}
	if receipt.ReqCoverage < 0.50 {
		return fmt.Errorf("insufficient requirement coverage %0.3f < 0.50", receipt.ReqCoverage)
	}
	if receipt.ProbesExecuted < 6 {
		return fmt.Errorf("probe suite floor violated: %d probes executed, minimum is 6", receipt.ProbesExecuted)
	}
	if receipt.ProbesPassed != receipt.ProbesExecuted {
		return fmt.Errorf("not all probes passed: %d/%d passed", receipt.ProbesPassed, receipt.ProbesExecuted)
	}

	// Enforce mandatory non-empty Ed25519 public key (32 bytes) and signature (64 bytes)
	if strings.TrimSpace(receipt.AuditorPubKey) == "" || strings.TrimSpace(receipt.AuditorSignature) == "" {
		return fmt.Errorf("audit receipt missing mandatory cryptographic public key or signature")
	}

	pubBytes, err := base64.StdEncoding.DecodeString(receipt.AuditorPubKey)
	if err != nil {
		return fmt.Errorf("decode auditor public key: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid ed25519 public key length %d, expected %d", len(pubBytes), ed25519.PublicKeySize)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(receipt.AuditorSignature)
	if err != nil {
		return fmt.Errorf("decode auditor signature: %w", err)
	}
	if len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("invalid ed25519 signature length %d, expected %d", len(sigBytes), ed25519.SignatureSize)
	}

	digest := FormatReceiptDigest(receipt)
	if !ed25519.Verify(pubBytes, digest, sigBytes) {
		legacyPayload := fmt.Sprintf("%s:%s:%s:%s:%s",
			receipt.SpecHash,
			receipt.CommitSHA,
			receipt.ProbeManifestHash,
			receipt.SandboxNonce,
			receipt.CandidateSelection,
		)
		if !ed25519.Verify(pubBytes, []byte(legacyPayload), sigBytes) {
			return fmt.Errorf("cryptographic audit receipt signature verification failed")
		}
	}
	return nil
}

// SaveAuditReceipt validates and writes an audit receipt, records it in SQLite, and transitions goal to implemented.
func SaveAuditReceipt(rootDir, slug string, receipt *AuditReceipt, agent, session string, st *store.Store) error {
	if err := VerifyAuditReceipt(receipt); err != nil {
		return fmt.Errorf("verify receipt: %w", err)
	}

	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return fmt.Errorf("goal %q not found in %s", slug, goalDir)
	}

	// Verify independence: auditor cannot be producer (isnad or active claim)
	goalMdPath := filepath.Join(goalDir, "goal.md")
	if err := enforceProducerIndependence(goalDir, agent, "audit"); err != nil {
		return err
	}
	if data, err := os.ReadFile(goalMdPath); err == nil {
		isnad := ParseIsnadHeader(string(data))
		if isnad != nil && session != "" && isnad.ImplementedSession != "" && strings.TrimSpace(session) == strings.TrimSpace(isnad.ImplementedSession) {
			return fmt.Errorf("session bleed prohibited: producer and auditor share session (%s)", session)
		}
	}

	receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal audit receipt: %w", err)
	}
	targetFile := filepath.Join(goalDir, "audit-receipt.json")
	if err := AtomicWriteFile(targetFile, receiptBytes, 0644); err != nil {
		return fmt.Errorf("write audit-receipt.json: %w", err)
	}

	// Resolve any active reviewer veto upon verified receipt
	vetoPath := filepath.Join(goalDir, "veto.json")
	if fileExists(vetoPath) {
		if data, err := os.ReadFile(vetoPath); err == nil {
			var vr map[string]any
			if json.Unmarshal(data, &vr) == nil {
				vr["active"] = false
				vr["resolved_at"] = time.Now().UTC().Format(time.RFC3339)
				vr["resolved_by"] = agent
				vr["receipt_path"] = "audit-receipt.json"
				if updated, err := json.MarshalIndent(vr, "", "  "); err == nil {
					_ = AtomicWriteFile(vetoPath, updated, 0644)
				}
			}
		}
	}

	// Update goal.md Isnad
	if data, err := os.ReadFile(goalMdPath); err == nil {
		isnad := ParseIsnadHeader(string(data))
		if isnad == nil {
			isnad = &IsnadHeader{}
		}
		auditorName := agent
		if auditorName == "" {
			auditorName = "@mystery-auditor"
		}
		if !strings.HasPrefix(auditorName, "@") {
			auditorName = "@" + auditorName
		}
		isnad.MysteryAuditedBy = auditorName
		isnad.MysteryAuditedSession = session
		isnad.MysteryAuditedReport = fmt.Sprintf("receipt_%s_%d", slug, receipt.CertifiedAt.Unix())
		_ = UpdateGoalMdIsnad(goalMdPath, isnad)
	}

	// Record in store if available
	if st != nil && st.DB != nil {
		blindInt := 0
		if receipt.BlindfoldVerified {
			blindInt = 1
		}
		canaryInt := 0
		if receipt.CanaryPassed {
			canaryInt = 1
		}
		_, _ = st.DB.Exec(`
			INSERT INTO goal_audit_receipts (
				project_id, goal_slug, commit_sha, spec_hash, sandbox_nonce,
				candidate_selection, blindfold_verified, canary_passed,
				ccdsi_score, req_coverage, probes_executed, probes_passed,
				probe_manifest_hash, auditor_pub_key, auditor_signature, certified_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rootDir, slug, receipt.CommitSHA, receipt.SpecHash, receipt.SandboxNonce,
			receipt.CandidateSelection, blindInt, canaryInt,
			receipt.CCDSIScore, receipt.ReqCoverage, receipt.ProbesExecuted, receipt.ProbesPassed,
			receipt.ProbeManifestHash, receipt.AuditorPubKey, receipt.AuditorSignature,
			receipt.CertifiedAt.UTC().Format(time.RFC3339),
		)
	}

	// Bind the signed audit receipt to the content it covered.
	_, _ = RecordVerification(rootDir, slug, LevelL3, "audit")

	// Release active claim only if verified closed in git history; otherwise retain claim.json and charter
	releaseClaimLockIfClosed(rootDir, goalDir, slug)
	_ = ResolveReopen(rootDir, slug, receipt.CommitSHA)

	// Sync README
	if audit, err := ScanGoals(rootDir); err == nil {
		_ = SyncReadme(rootDir, audit)
	}

	return nil
}

// GetActiveAuditReceipt reads audit-receipt.json from the goal package.
func GetActiveAuditReceipt(rootDir, slug string) (*AuditReceipt, error) {
	if slug == "" {
		return nil, nil
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	receiptPath := filepath.Join(rootDir, "goals", slug, "audit-receipt.json")
	if !fileExists(receiptPath) {
		return nil, nil
	}
	data, err := os.ReadFile(receiptPath)
	if err != nil {
		return nil, err
	}
	var receipt AuditReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// SaveDefectDossier saves a defect dossier, generates remediation.md, and reopens the goal.
func SaveDefectDossier(rootDir, slug string, dossier *DefectDossier, agent, session string, st *store.Store) error {
	if dossier == nil {
		return fmt.Errorf("defect dossier cannot be nil")
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	goalDir := filepath.Join(rootDir, "goals", slug)
	if !fileExists(filepath.Join(goalDir, "goal.md")) {
		return fmt.Errorf("goal %q not found in %s", slug, goalDir)
	}

	dossierBytes, err := json.MarshalIndent(dossier, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal defect dossier: %w", err)
	}
	dossierPath := filepath.Join(goalDir, "defect-dossier.json")
	if err := AtomicWriteFile(dossierPath, dossierBytes, 0644); err != nil {
		return fmt.Errorf("write defect-dossier.json: %w", err)
	}

	// Generate remediation.md
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Remediation Required: %s\n\n", slug))
	sb.WriteString("## Defect Overview\n")
	sb.WriteString(fmt.Sprintf("- **Failed Probe**: `%s`\n", dossier.FailedProbeName))
	sb.WriteString(fmt.Sprintf("- **Commit SHA**: `%s`\n", dossier.CommitSHA))
	sb.WriteString(fmt.Sprintf("- **Timestamp**: `%s`\n\n", dossier.Timestamp.UTC().Format(time.RFC3339)))

	sb.WriteString("## Trigger Conditions\n")
	sb.WriteString(dossier.TriggerConditions)
	sb.WriteString("\n\n")

	sb.WriteString("## Expected Behavior\n")
	sb.WriteString(dossier.ExpectedBehavior)
	sb.WriteString("\n\n")

	sb.WriteString("## Actual Behavior\n")
	sb.WriteString(dossier.ActualBehavior)
	sb.WriteString("\n\n")

	if dossier.StderrSnippet != "" {
		sb.WriteString("## Stderr Snippet\n```\n")
		sb.WriteString(dossier.StderrSnippet)
		sb.WriteString("\n```\n\n")
	}
	if dossier.StdoutSnippet != "" {
		sb.WriteString("## Stdout Snippet\n```\n")
		sb.WriteString(dossier.StdoutSnippet)
		sb.WriteString("\n```\n\n")
	}

	sb.WriteString("## Actionable Remediation Guidance\n")
	sb.WriteString(dossier.RemediationAdvice)
	sb.WriteString("\n")

	remediationPath := filepath.Join(goalDir, "remediation.md")
	if err := AtomicWriteFile(remediationPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("write remediation.md: %w", err)
	}

	// Reopen the goal under the identical slug with cascading taint sweep (if already implemented)
	reason := fmt.Sprintf("mystery audit failed probe: %s (%s)", dossier.FailedProbeName, dossier.RemediationAdvice)
	if _, _, err := ReopenGoal(rootDir, slug, agent, reason, false); err != nil && !strings.Contains(err.Error(), "is not currently implemented") {
		return fmt.Errorf("reopen goal: %w", err)
	}

	return nil
}

// GetActiveDefectDossier reads defect-dossier.json from the goal package.
func GetActiveDefectDossier(rootDir, slug string) (*DefectDossier, error) {
	if slug == "" {
		return nil, nil
	}
	slug = filepath.Clean(slug)
	rootDir = resolveGoalsRootDir(rootDir)
	dossierPath := filepath.Join(rootDir, "goals", slug, "defect-dossier.json")
	if !fileExists(dossierPath) {
		return nil, nil
	}
	data, err := os.ReadFile(dossierPath)
	if err != nil {
		return nil, err
	}
	var dossier DefectDossier
	if err := json.Unmarshal(data, &dossier); err != nil {
		return nil, err
	}
	return &dossier, nil
}

// HasVerifiedCommitClosure checks if git history contains a valid commit verifying the goal slug.
func HasVerifiedCommitClosure(rootDir, slug string) bool {
	commits := scanGitCommitsRaw(rootDir, []string{slug})
	_, ok := commits[slug]
	return ok
}

// releaseClaimLockIfClosed releases .claim.lock immediately, but only removes claim.json and the
// charter if commit reconciliation proves the goal has closed in git history.
func releaseClaimLockIfClosed(rootDir, goalDir, slug string) {
	lockPath := filepath.Join(goalDir, ".claim.lock")
	if fileExists(lockPath) {
		_ = os.Remove(lockPath)
	}

	if HasVerifiedCommitClosure(rootDir, slug) {
		claimPath := filepath.Join(goalDir, "claim.json")
		if fileExists(claimPath) {
			_ = os.Remove(claimPath)
		}
		_ = RemoveCharter(rootDir, slug)
	}
}
