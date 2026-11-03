package assess

import (
	"fmt"
	"strings"
)

// AgentRoleClass defines the epistemic classification of an agent.
type AgentRoleClass string

const (
	RoleClassProducer     AgentRoleClass = "producer"     // Task implementer
	RoleClassReviewer     AgentRoleClass = "reviewer"     // Independent auditor
	RoleClassOrchestrator AgentRoleClass = "orchestrator" // Coordinator
)

// ReviewerVerdict represents the outcome of an audit evaluation.
type ReviewerVerdict string

const (
	VerdictApproved ReviewerVerdict = "APPROVED" // Verification passed cleanly
	VerdictRejected ReviewerVerdict = "REJECTED" // Non-blocking issues reported
	VerdictVetoed   ReviewerVerdict = "VETOED"   // Blocking defect; non-overrulable
)

// MutationTools lists tool names that alter state, files, or persistent storage.
var MutationTools = map[string]bool{
	"write_to_file":        true,
	"replace_file_content": true,
	"write_file":           true,
	"edit_file":            true,
	"apply_diff":           true,
	"git_commit":           true,
	"git_push":             true,
	"exec_mutate":          true,
	"save_handoff":         true,
	"pop_handoff":          true,
	"save_memo":            true,
	"save_lesson":          true,
	"save_doc":             true,
	"claim_goal":           true,
	"release_goal":         true,
	"create_goal":          true,
	"reopen_goal":          true,
	"spawn_crewmate":       true,
}

// IsMutationTool returns true if toolName performs any state or disk modification.
func IsMutationTool(toolName string) bool {
	norm := strings.ToLower(strings.TrimSpace(toolName))
	if MutationTools[norm] {
		return true
	}
	for _, prefix := range []string{"write_", "edit_", "create_", "delete_", "remove_", "mutate_", "save_", "update_"} {
		if strings.HasPrefix(norm, prefix) {
			return true
		}
	}
	return false
}

// StripMutationTools filters out any tools that mutate state when role is reviewer.
func StripMutationTools(roleClass AgentRoleClass, tools []string) []string {
	if roleClass != RoleClassReviewer {
		return tools
	}
	var stripped []string
	for _, t := range tools {
		if !IsMutationTool(t) {
			stripped = append(stripped, t)
		}
	}
	return stripped
}

// AgentIdentity represents an agent's name and session ID.
type AgentIdentity struct {
	Agent   string `json:"agent"`
	Session string `json:"session"`
}

// ValidateAuditIndependence checks that producer and reviewer are strictly independent.
func ValidateAuditIndependence(producer, reviewer AgentIdentity) error {
	prodAgent := normalizeAgentName(producer.Agent)
	revAgent := normalizeAgentName(reviewer.Agent)

	if prodAgent != "" && revAgent != "" && prodAgent == revAgent {
		return fmt.Errorf("self-audit prohibited: producer (%s) cannot audit own work", prodAgent)
	}

	prodSess := strings.TrimSpace(producer.Session)
	revSess := strings.TrimSpace(reviewer.Session)
	if prodSess != "" && revSess != "" && prodSess == revSess {
		return fmt.Errorf("session bleed prohibited: producer and reviewer share session (%s)", prodSess)
	}

	return nil
}

func normalizeAgentName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "@")
	return strings.ToLower(name)
}
