package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Legacy reconciliation states. A legacy receipt cannot prove the content it
// covered, so it is never promoted to verified.
const (
	ReconcileLegacyAudit = "legacy_audit"
	ReconcileLegacyCert  = "legacy_certification"
	ReconcileUnbound     = "unbound"
)

// VerificationClassification is one goal's reconciliation verdict.
type VerificationClassification struct {
	Slug   string `json:"slug"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	Level  string `json:"level,omitempty"`
}

// VerificationReconciliation is the full classification report.
type VerificationReconciliation struct {
	Total       int                          `json:"total"`
	Verified    int                          `json:"verified"`
	Stale       int                          `json:"stale"`
	StaleSpec   int                          `json:"stale_spec"`
	LegacyAudit int                          `json:"legacy_audit"`
	LegacyCert  int                          `json:"legacy_certification"`
	Unbound     int                          `json:"unbound"`
	Goals       []VerificationClassification `json:"goals"`
}

// ClassifyVerification classifies every implemented goal without mutating
// anything. It is safe to run on any repository.
func ClassifyVerification(rootDir string) (*VerificationReconciliation, error) {
	audit, err := ScanGoals(rootDir)
	if err != nil {
		return nil, err
	}
	root := resolveGoalsRootDir(rootDir)
	rep := &VerificationReconciliation{}
	for _, g := range audit.Goals {
		if g.Status != "implemented" {
			continue
		}
		entry := ClassifyGoalVerification(root, g.Slug)
		rep.Total++
		switch entry.State {
		case StateVerified:
			rep.Verified++
		case StateStale:
			rep.Stale++
		case StateStaleSpec:
			rep.StaleSpec++
		case ReconcileLegacyAudit:
			rep.LegacyAudit++
		case ReconcileLegacyCert:
			rep.LegacyCert++
		default:
			rep.Unbound++
		}
		rep.Goals = append(rep.Goals, entry)
	}
	sort.Slice(rep.Goals, func(i, j int) bool {
		if rep.Goals[i].State != rep.Goals[j].State {
			return rep.Goals[i].State < rep.Goals[j].State
		}
		return rep.Goals[i].Slug < rep.Goals[j].Slug
	})
	return rep, nil
}

// ClassifyGoalVerification classifies one goal package.
func ClassifyGoalVerification(rootDir, slug string) VerificationClassification {
	entry := VerificationClassification{Slug: slug}
	rec, err := LoadVerification(rootDir, slug)
	switch {
	case err != nil:
		entry.State = ReconcileUnbound
		entry.Reason = "unreadable verification record: " + err.Error()
	case rec != nil:
		state, changed, serr := ComputeVerificationState(rootDir, slug, rec)
		if serr != nil {
			entry.State = ReconcileUnbound
			entry.Reason = serr.Error()
			break
		}
		entry.State = state
		entry.Level = rec.Level
		if len(changed) > 0 {
			entry.Reason = "changed: " + strings.Join(changed, ", ")
		}
	default:
		dir := filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug)
		switch {
		case fileExists(filepath.Join(dir, "audit-receipt.json")):
			entry.State = ReconcileLegacyAudit
			entry.Reason = "signed audit receipt without content binding; its commit may be absent from history"
		case fileExists(filepath.Join(dir, "certification.json")):
			entry.State = ReconcileLegacyCert
			entry.Reason = "evidence certification without content binding"
		default:
			entry.State = ReconcileUnbound
			entry.Reason = "no verification evidence"
		}
	}
	return entry
}

// ReanchorOutcome is one goal's batch re-anchor result.
type ReanchorOutcome struct {
	Slug       string `json:"slug"`
	State      string `json:"state"`
	Reanchored bool   `json:"reanchored"`
	Reason     string `json:"reason,omitempty"`
}

// ReanchorReport is the batch re-anchor summary.
type ReanchorReport struct {
	Total      int               `json:"total"`
	Reanchored int               `json:"reanchored"`
	Refused    int               `json:"refused"`
	Goals      []ReanchorOutcome `json:"goals"`
}

// ReverifyAll re-anchors every implemented goal whose content state is still
// verified and refuses the rest. A refusal never mutates the goal.
func ReverifyAll(rootDir string) (*ReanchorReport, error) {
	audit, err := ScanGoals(rootDir)
	if err != nil {
		return nil, err
	}
	root := resolveGoalsRootDir(rootDir)
	rep := &ReanchorReport{Goals: []ReanchorOutcome{}}
	for _, g := range audit.Goals {
		if g.Status != "implemented" {
			continue
		}
		rep.Total++
		out := ReanchorOutcome{Slug: g.Slug}
		if _, err := Reverify(root, g.Slug); err != nil {
			out.State = "refused"
			out.Reason = err.Error()
			rep.Refused++
		} else {
			out.State = StateVerified
			out.Reanchored = true
			rep.Reanchored++
		}
		rep.Goals = append(rep.Goals, out)
	}
	sort.Slice(rep.Goals, func(i, j int) bool { return rep.Goals[i].Slug < rep.Goals[j].Slug })
	return rep, nil
}

// AuditQueueVersion is the resumable batch-audit ledger schema.
const AuditQueueVersion = "sila/verification-audit-queue/v1"

// Audit queue states. A conclusive outcome is never retried automatically,
// and an inconclusive one neither reopens the goal nor downgrades it.
const (
	AuditStatePassed       = "passed"
	AuditStateRejected     = "rejected"
	AuditStateInconclusive = "inconclusive"
)

// AuditQueueEntry is one goal's batch-audit attempt.
type AuditQueueEntry struct {
	Slug        string    `json:"slug"`
	State       string    `json:"state"`
	Reason      string    `json:"reason,omitempty"`
	AttemptedAt time.Time `json:"attempted_at,omitempty"`
}

// AuditQueue is the resumable ledger for bounded stale-goal audits.
type AuditQueue struct {
	Version string                     `json:"version"`
	Entries map[string]AuditQueueEntry `json:"entries"`
}

// AuditQueuePath returns the ledger path. It lives under .sila/ so the queue is
// local tool state and never enters the repository.
func AuditQueuePath(rootDir string) string {
	return filepath.Join(resolveGoalsRootDir(rootDir), ".sila", "verification-audit-queue.json")
}

// LoadAuditQueue reads the ledger, returning an empty queue when absent.
func LoadAuditQueue(rootDir string) (*AuditQueue, error) {
	q := &AuditQueue{Version: AuditQueueVersion, Entries: map[string]AuditQueueEntry{}}
	data, err := os.ReadFile(AuditQueuePath(rootDir))
	if os.IsNotExist(err) {
		return q, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, q); err != nil {
		return nil, fmt.Errorf("parse audit queue: %w", err)
	}
	if q.Entries == nil {
		q.Entries = map[string]AuditQueueEntry{}
	}
	return q, nil
}

// SaveAuditQueue writes the ledger atomically.
func SaveAuditQueue(rootDir string, q *AuditQueue) error {
	if q == nil {
		return fmt.Errorf("audit queue is nil")
	}
	q.Version = AuditQueueVersion
	data, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(AuditQueuePath(rootDir), append(data, '\n'), 0o644)
}

// SelectStaleAuditCandidates returns the next bounded batch of goals that need
// a fresh audit: goals with changed content, then goals that only hold a
// legacy audit receipt. Goals already recorded as passed, rejected, or
// inconclusive are skipped so a resumed batch never repeats a dossier.
func SelectStaleAuditCandidates(rootDir string, limit int) ([]VerificationClassification, error) {
	if limit <= 0 {
		limit = 10
	}
	audit, err := ScanGoals(rootDir)
	if err != nil {
		return nil, err
	}
	root := resolveGoalsRootDir(rootDir)
	queue, err := LoadAuditQueue(root)
	if err != nil {
		return nil, err
	}
	var out []VerificationClassification
	for _, g := range audit.Goals {
		if g.Status != "implemented" {
			continue
		}
		entry := ClassifyGoalVerification(root, g.Slug)
		if entry.State != StateStale && entry.State != ReconcileLegacyAudit {
			continue
		}
		if _, done := queue.Entries[g.Slug]; done {
			continue
		}
		out = append(out, entry)
	}
	rank := func(state string) int {
		if state == StateStale {
			return 0
		}
		return 1
	}
	sort.Slice(out, func(i, j int) bool {
		if rank(out[i].State) != rank(out[j].State) {
			return rank(out[i].State) < rank(out[j].State)
		}
		return out[i].Slug < out[j].Slug
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// RecordAuditOutcome appends one attempt to the ledger and returns the updated
// queue.
func RecordAuditOutcome(rootDir, slug, state, reason string) (*AuditQueue, error) {
	if slug == "" {
		return nil, fmt.Errorf("goal slug is required")
	}
	q, err := LoadAuditQueue(rootDir)
	if err != nil {
		return nil, err
	}
	q.Entries[slug] = AuditQueueEntry{
		Slug:        slug,
		State:       state,
		Reason:      reason,
		AttemptedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := SaveAuditQueue(rootDir, q); err != nil {
		return nil, err
	}
	return q, nil
}

// DefaultContractTestPaths returns the declared touch paths that carry tests:
// `_test.go` files and directories that contain them. It is the default test
// binding for `sila goals contract`.
func DefaultContractTestPaths(rootDir, slug string) ([]string, error) {
	paths, err := DeclaredTouchPaths(rootDir, slug)
	if err != nil {
		return nil, err
	}
	root := resolveGoalsRootDir(rootDir)
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
			continue
		}
		if dirHasTestFiles(filepath.Join(root, filepath.FromSlash(p))) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func dirHasTestFiles(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			found = true
		}
		return nil
	})
	return found
}

// RenderReconciliation writes a human summary.
func RenderReconciliation(w *os.File, rep *VerificationReconciliation, all bool) {
	fmt.Fprintf(w, "Verification reconciliation: %d implemented goals\n", rep.Total)
	fmt.Fprintf(w, "  verified             %d\n", rep.Verified)
	fmt.Fprintf(w, "  stale                %d\n", rep.Stale)
	fmt.Fprintf(w, "  stale_spec           %d\n", rep.StaleSpec)
	fmt.Fprintf(w, "  legacy_audit         %d\n", rep.LegacyAudit)
	fmt.Fprintf(w, "  legacy_certification %d\n", rep.LegacyCert)
	fmt.Fprintf(w, "  unbound              %d\n", rep.Unbound)
	if !all {
		return
	}
	for _, g := range rep.Goals {
		line := fmt.Sprintf("  %-22s %s", g.State, g.Slug)
		if g.Level != "" {
			line += " (" + g.Level + ")"
		}
		if g.Reason != "" {
			line += " - " + g.Reason
		}
		fmt.Fprintln(w, line)
	}
}
