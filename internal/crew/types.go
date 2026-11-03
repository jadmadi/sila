package crew

import "time"

// AgentRoleClass represents the epistemic classification of an agent in a crew.
type AgentRoleClass string

const (
	RoleClassProducer     AgentRoleClass = "producer"     // Task implementer with mutation capabilities
	RoleClassReviewer     AgentRoleClass = "reviewer"     // Auditor/evaluator with read-only inspection capabilities
	RoleClassOrchestrator AgentRoleClass = "orchestrator" // Coordinator/dispatcher
)

// ReviewerVerdict is an alias for AuditorVerdict.
type ReviewerVerdict = AuditorVerdict

// VetoRecord represents a non-overrulable veto issued by a reviewer-class agent.
type VetoRecord struct {
	VetoID      string     `json:"veto_id"`
	GoalSlug    string     `json:"goal_slug"`
	AuditorName string     `json:"auditor_name"`
	SessionID   string     `json:"session_id"`
	Reason      string     `json:"reason"`
	DefectFile  string     `json:"defect_file,omitempty"`
	VetoedAt    time.Time  `json:"vetoed_at"`
	Active      bool       `json:"active"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy  string     `json:"resolved_by,omitempty"`
	ReceiptPath string     `json:"receipt_path,omitempty"`
}
