package contract

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SignalSchema   = "sila/harness-signal/v1"
	DecisionSchema = "sila/harness-goal-contract/v1"
	BindingSchema  = "sila/harness-goal-binding/v1"
	WaiverSchema   = "sila/harness-goal-waiver/v1"
	ReceiptSchema  = "sila/harness-goal-receipt/v1"
)

// HarnessID is the canonical identifier of an agent harness.
type HarnessID string

// Phase is a normalized lifecycle phase.
type Phase string

const (
	PhaseSessionStart  Phase = "session-start"
	PhaseUserPrompt    Phase = "user-prompt"
	PhaseToolBefore    Phase = "tool-before"
	PhaseToolAfter     Phase = "tool-after"
	PhaseTurnEnd       Phase = "turn-end"
	PhaseStop          Phase = "stop"
	PhaseSessionEnd    Phase = "session-end"
	PhaseSubagentStart Phase = "subagent-start"
	PhaseSubagentStop  Phase = "subagent-stop"
	PhaseCompactPre    Phase = "compact-pre"
	PhaseCompactPost   Phase = "compact-post"
	PhaseHandoff       Phase = "handoff"
	PhaseCertify       Phase = "certify"
)

// Capability is a harness feature relevant to the contract.
type Capability string

const (
	CapSessionStart   Capability = "session-start"
	CapSessionEnd     Capability = "session-end"
	CapStop           Capability = "stop"
	CapStopBlock      Capability = "stop-block"
	CapToolBefore     Capability = "tool-before"
	CapToolAfter      Capability = "tool-after"
	CapPermissionDeny Capability = "permission-deny"
	CapContextInject  Capability = "context-inject"
	CapSubagent       Capability = "subagent"
	CapCompaction     Capability = "compaction"
)

// Decision values.
const (
	DecisionAllow       = "allow"
	DecisionWarn        = "warn"
	DecisionDeny        = "deny"
	DecisionUnsupported = "unsupported"
)

// Check statuses.
const (
	CheckPass = "pass"
	CheckWarn = "warn"
	CheckFail = "fail"
	CheckSkip = "skip"
)

// Stable reason codes.
const (
	ReasonSignalInvalid    = "SIGNAL_INVALID"
	ReasonGoalUnbound      = "GOAL_UNBOUND"
	ReasonBindingExpired   = "GOAL_BINDING_EXPIRED"
	ReasonGoalMismatch     = "GOAL_MISMATCH"
	ReasonClaimMissing     = "CLAIM_MISSING"
	ReasonClaimMismatch    = "CLAIM_MISMATCH"
	ReasonClaimDuplicate   = "CLAIM_DUPLICATE"
	ReasonTasksIncomplete  = "TASKS_INCOMPLETE"
	ReasonEvidenceMissing  = "EVIDENCE_MISSING"
	ReasonEvidenceInvalid  = "EVIDENCE_INVALID"
	ReasonFactsDirty       = "FACTS_DIRTY"
	ReasonHandoffMissing   = "HANDOFF_MISSING"
	ReasonMemoryMissing    = "MEMORY_WRITE_MISSING"
	ReasonStoreBusy        = "STORE_BUSY"
	ReasonHarnessUnsupport = "HARNESS_UNSUPPORTED"
	ReasonBypassed         = "CONTRACT_BYPASSED"
	ReasonPolicyOff        = "POLICY_OFF"
)

// Signal is the harness-neutral input to the contract.
type Signal struct {
	SchemaVersion   string    `json:"schema_version"`
	Harness         HarnessID `json:"harness"`
	SessionID       string    `json:"session_id"`
	ParentSessionID string    `json:"parent_session_id,omitempty"`
	ProjectID       string    `json:"project_id"`
	Cwd             string    `json:"cwd"`
	Phase           Phase     `json:"phase"`
	HarnessGoalID   string    `json:"harness_goal_id,omitempty"`
	SilaGoalSlug    string    `json:"sila_goal_slug,omitempty"`
	ClaimID         string    `json:"claim_id,omitempty"`
	CommitSHA       string    `json:"commit_sha,omitempty"`
	EvidencePaths   []string  `json:"evidence_paths,omitempty"`
	PermissionMode  string    `json:"permission_mode,omitempty"`
	ToolName        string    `json:"tool_name,omitempty"`
	ToolShape       string    `json:"tool_shape,omitempty"`
	Sequence        string    `json:"sequence,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
}

// CheckResult is one deterministic contract check.
type CheckResult struct {
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Code     string `json:"code,omitempty"`
	Recovery string `json:"recovery,omitempty"`
}

// Decision is the contract output.
type Decision struct {
	Schema      string                 `json:"schema"`
	Decision    string                 `json:"decision"`
	ReasonCode  string                 `json:"reason_code,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	Recovery    string                 `json:"recovery,omitempty"`
	Checks      map[string]CheckResult `json:"checks"`
	ReceiptPath string                 `json:"receipt_path,omitempty"`
	Bypassed    bool                   `json:"bypassed,omitempty"`
	DurationMS  int64                  `json:"duration_ms,omitempty"`
}

// Validate rejects signals that cannot be evaluated.
func (s Signal) Validate() error {
	if strings.TrimSpace(s.SessionID) == "" {
		return errors.New("session id is required")
	}
	if strings.TrimSpace(s.ProjectID) == "" {
		return errors.New("project id is required")
	}
	if strings.TrimSpace(string(s.Harness)) == "" {
		return errors.New("harness is required")
	}
	switch s.Phase {
	case PhaseSessionStart, PhaseUserPrompt, PhaseToolBefore, PhaseToolAfter,
		PhaseTurnEnd, PhaseStop, PhaseSessionEnd, PhaseSubagentStart,
		PhaseSubagentStop, PhaseCompactPre, PhaseCompactPost, PhaseHandoff,
		PhaseCertify:
	default:
		return fmt.Errorf("unknown phase %q", s.Phase)
	}
	return nil
}

// NormalizeSignal fills schema/timestamp and trims identity fields.
func NormalizeSignal(s Signal) Signal {
	if s.SchemaVersion == "" {
		s.SchemaVersion = SignalSchema
	}
	if s.Timestamp.IsZero() {
		s.Timestamp = time.Now().UTC()
	}
	s.SessionID = strings.TrimSpace(s.SessionID)
	s.ParentSessionID = strings.TrimSpace(s.ParentSessionID)
	s.ProjectID = strings.TrimSpace(s.ProjectID)
	s.SilaGoalSlug = strings.TrimSpace(s.SilaGoalSlug)
	return s
}

// Fail creates a deterministic failure decision without opening the store.
func Fail(code, reason, recovery string) Decision {
	return Decision{
		Schema:     DecisionSchema,
		Decision:   DecisionDeny,
		ReasonCode: code,
		Reason:     reason,
		Recovery:   recovery,
		Checks:     map[string]CheckResult{},
	}
}

// BuildToolShape returns a privacy-safe key:type shape for a raw tool input.
func BuildToolShape(raw []byte) string {
	return contractStoreBuildArgsShape(raw)
}
