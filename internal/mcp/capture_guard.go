package mcp

import (
	"encoding/json"
	"strings"
)

// captureWriteTools are the memory and knowledge writes a pause blocks.
var captureWriteTools = map[string]bool{
	"save_memo":            true,
	"save_lesson":          true,
	"save_case":            true,
	"save_handoff":         true,
	"save_memory":          true,
	"save_domain_learning": true,
	"create_decision":      true,
	"record_feedback":      true,
	"memory_feedback":      true,
}

func isCaptureWriteTool(name string) bool { return captureWriteTools[name] }

// capturePausedFor resolves the target project and reports the pause state.
func (s *Server) capturePausedFor(raw json.RawMessage) bool {
	if s == nil || s.store == nil {
		return false
	}
	var args struct {
		Project string `json:"project"`
	}
	_ = json.Unmarshal(raw, &args)
	projectID := strings.TrimSpace(args.Project)
	if projectID == "" {
		projectID, _ = s.resolveTargetProject("")
	} else if resolved, err := s.resolveTargetProject(projectID); err == nil {
		projectID = resolved
	}
	return s.capturePausedForProject(projectID)
}

// capturePausedForProject is the store check once a project id is known.
func (s *Server) capturePausedForProject(projectID string) bool {
	if s == nil || s.store == nil || projectID == "" {
		return false
	}
	state, err := s.store.CaptureState(projectID)
	return err == nil && (state.Paused || state.Forgotten)
}
