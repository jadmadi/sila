package goals

import (
	"fmt"
	"time"
)

// GoalState represents the discrete lifecycle state of an isnad goal.
type GoalState string

const (
	StateDraft        GoalState = "draft"
	StateSpecAudited  GoalState = "spec_audited"
	StateClaimed      GoalState = "claimed"
	StatePendingAudit GoalState = "pending_audit"
	StateImplemented  GoalState = "implemented"
	StateRemediation  GoalState = "remediation"
	StateTainted      GoalState = "tainted"
	StateSuperseded   GoalState = "superseded"
)

// Label returns a human-readable explanation of the epistemic state.
func (s GoalState) Label() string {
	switch s {
	case StateDraft:
		return "draft (not yet spec-audited)"
	case StateSpecAudited:
		return "spec audited (ready to claim)"
	case StateClaimed:
		return "claimed (implementation in progress)"
	case StatePendingAudit:
		return "pending audit (implementation complete)"
	case StateImplemented:
		return "implemented (verified)"
	case StateRemediation:
		return "remediation (needs rework)"
	case StateTainted:
		return "tainted (evidence invalidated)"
	case StateSuperseded:
		return "superseded (replaced by a successor)"
	default:
		return string(s)
	}
}

// String returns the string representation of GoalState.
func (s GoalState) String() string {
	return string(s)
}

// IsValid reports whether s is one of the 8 canonical goal states.
func (s GoalState) IsValid() bool {
	switch s {
	case StateDraft, StateSpecAudited, StateClaimed, StatePendingAudit,
		StateImplemented, StateRemediation, StateTainted, StateSuperseded:
		return true
	default:
		return false
	}
}

// ValidateStateTransition checks if transitioning from 'from' to 'to' is allowed
// according to Sila's 14-transition state machine closure.
func ValidateStateTransition(from, to GoalState) error {
	if !from.IsValid() {
		return fmt.Errorf("invalid source state: %q", from)
	}
	if !to.IsValid() {
		return fmt.Errorf("invalid destination state: %q", to)
	}
	if from == to {
		if from == StateDraft {
			return nil // Draft revision loop
		}
		return fmt.Errorf("redundant state transition from %s to itself", from)
	}

	// Superseded can be reached from any non-superseded state
	if to == StateSuperseded {
		return nil
	}

	valid := false
	switch from {
	case StateDraft:
		valid = (to == StateSpecAudited)
	case StateSpecAudited:
		valid = (to == StateClaimed)
	case StateClaimed:
		valid = (to == StateSpecAudited || to == StatePendingAudit || to == StateRemediation)
	case StatePendingAudit:
		valid = (to == StateImplemented || to == StateRemediation)
	case StateImplemented:
		valid = (to == StateRemediation)
	case StateRemediation:
		valid = (to == StateClaimed)
	case StateTainted:
		valid = (to == StatePendingAudit || to == StateRemediation)
	case StateSuperseded:
		valid = false // Terminal state
	}

	if !valid {
		return fmt.Errorf("illegal goal state transition from %s to %s", from, to)
	}
	return nil
}

// IsnadHeader captures the 4-stage Chain of Custody (Isnad) for a goal.
type IsnadHeader struct {
	RegisteredBy          string `json:"registered_by"`
	RegisteredSession     string `json:"registered_session,omitempty"`
	RegisteredDate        string `json:"registered_date,omitempty"`
	SpecAuditedBy         string `json:"spec_audited_by,omitempty"`
	SpecAuditedSession    string `json:"spec_audited_session,omitempty"`
	SpecAuditedDate       string `json:"spec_audited_date,omitempty"`
	ImplementedBy         string `json:"implemented_by,omitempty"`
	ImplementedSession    string `json:"implemented_session,omitempty"`
	ImplementedCommit     string `json:"implemented_commit,omitempty"`
	MysteryAuditedBy      string `json:"mystery_audited_by,omitempty"`
	MysteryAuditedSession string `json:"mystery_audited_session,omitempty"`
	MysteryAuditedReport  string `json:"mystery_audited_report,omitempty"`
}

// ClaimLease represents a time-bounded mutual exclusion lease on a goal.
type ClaimLease struct {
	GoalSlug    string    `json:"goal_slug"`
	AgentID     string    `json:"agent_id"`
	SessionID   string    `json:"session_id"`
	PID         int       `json:"pid"`
	Hostname    string    `json:"hostname"`
	ContainerID string    `json:"container_id,omitempty"`
	ClaimedAt   time.Time `json:"claimed_at"`
	ExpiresAt   time.Time `json:"expires_at"`   // ClaimedAt + 5m hard lease expiration
	HeartbeatAt time.Time `json:"heartbeat_at"` // Renewed every 60s via TouchGoal
	Phase       string    `json:"phase"`        // "implementation" or "remediation"
	LeaseToken  string    `json:"lease_token"`  // UUID v4
	Version     int       `json:"version"`
}

// IsExpired reports whether the lease has expired past now.
func (c *ClaimLease) IsExpired(now time.Time) bool {
	if c == nil || c.ExpiresAt.IsZero() {
		return false
	}
	return now.After(c.ExpiresAt)
}

// AuditReceipt represents the immutable, cryptographic certification envelope.
type AuditReceipt struct {
	Version            string    `json:"version"` // "sila/audit-receipt/v1"
	GoalSlug           string    `json:"goal_slug"`
	CommitSHA          string    `json:"commit_sha"`
	SpecHash           string    `json:"spec_hash"`
	SandboxNonce       string    `json:"sandbox_nonce"`
	CandidateSelection string    `json:"candidate_selection"` // "A" or "B"
	BlindfoldVerified  bool      `json:"blindfold_verified"`
	CanaryPassed       bool      `json:"canary_passed"`
	CCDSIScore         float64   `json:"ccdsi_score"`  // Must be >= 0.40
	ReqCoverage        float64   `json:"req_coverage"` // Must be >= 0.50
	ProbesExecuted     int       `json:"probes_executed"`
	ProbesPassed       int       `json:"probes_passed"`
	ProbeManifestHash  string    `json:"probe_manifest_hash"`
	AuditorPubKey      string    `json:"auditor_pub_key"`
	AuditorSignature   string    `json:"auditor_signature"`
	CertifiedAt        time.Time `json:"certified_at"`
}

// DefectDossier represents structured diagnostic feedback emitted on audit rejection.
type DefectDossier struct {
	GoalSlug          string    `json:"goal_slug"`
	CommitSHA         string    `json:"commit_sha"`
	ReopenEpoch       int       `json:"reopen_epoch"`
	FailedProbeName   string    `json:"failed_probe_name"`
	TriggerConditions string    `json:"trigger_conditions"`
	ExpectedBehavior  string    `json:"expected_behavior"`
	ActualBehavior    string    `json:"actual_behavior"`
	StderrSnippet     string    `json:"stderr_snippet"`
	RemediationAdvice string    `json:"remediation_advice"`
	Timestamp         time.Time `json:"timestamp"`
}
