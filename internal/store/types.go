package store

// Provenance records source attribution metadata for facts, handoffs, and agent submissions
// to prevent personal PAT vs organizational tool attribution collisions.
type Provenance struct {
	ToolName     string `json:"tool_name,omitempty"`     // e.g. "antigravity", "cursor", "claude_code"
	ClientKind   string `json:"client_kind,omitempty"`   // e.g. "ai_agent", "human", "ci_job"
	OriginEntity string `json:"origin_entity,omitempty"` // e.g. "org:waqftech", "pat:personal", "system"
	SessionRef   string `json:"session_ref,omitempty"`   // local agent session identifier
	OriginRole   string `json:"origin_role,omitempty"`   // user | assistant | subagent:<id>
}

// IsZero reports whether all provenance fields are empty.
func (p Provenance) IsZero() bool {
	return p.ToolName == "" && p.ClientKind == "" && p.OriginEntity == "" && p.SessionRef == ""
}
