package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// handleMemoryFeedbackTool serves the retrieval-bound relevance feedback
// surface. It is split from handlers_knowledge.go to keep that dispatch file
// from growing past its existing ceiling.
func (s *Server) handleMemoryFeedbackTool(req JSONRPCRequest, name string, rawArgs json.RawMessage) bool {
	switch name {
	case "memory_feedback":
		var args struct {
			MemoryID    string `json:"memory_id"`
			RetrievalID string `json:"retrieval_id"`
			Signal      string `json:"signal"`
			Note        string `json:"note"`
			Agent       string `json:"agent"`
			Project     string `json:"project"`
		}
		if err := decodeStrictMemoryArgs(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		if strings.TrimSpace(args.MemoryID) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "memory_id is required")
			return true
		}
		if strings.TrimSpace(args.RetrievalID) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "retrieval_id is required; call recall or prime_context first")
			return true
		}
		if strings.TrimSpace(args.Signal) == "" {
			s.sendError(req.ID, -32602, "Invalid params", "signal is required")
			return true
		}
		projectID := ""
		if strings.TrimSpace(args.Project) != "" {
			resolved, resolveErr := s.resolveTargetProject(args.Project)
			if resolveErr != nil {
				s.sendError(req.ID, -32602, "Invalid params", resolveErr.Error())
				return true
			}
			projectID = resolved
		}
		outcome, err := s.store.RecordMemoryFeedback(context.Background(), store.MemoryFeedbackInput{
			ProjectID:   projectID,
			MemoryID:    args.MemoryID,
			RetrievalID: args.RetrievalID,
			Signal:      store.MemoryFeedbackSignal(strings.TrimSpace(args.Signal)),
			Note:        args.Note,
			Agent:       args.Agent,
			SessionRef:  s.sessionID,
		})
		if err != nil {
			s.sendMemoryValidationError(req.ID, err)
			return true
		}
		formatted, _ := json.MarshalIndent(outcome, "", "  ")
		summary := fmt.Sprintf("feedback %s for %s: adjustment %+.1f", outcome.Feedback.Signal, outcome.Feedback.MemoryID, outcome.Adjustment.Adjustment)
		if outcome.RecommendedAction != "" {
			summary += " · recommended action: " + outcome.RecommendedAction
		}
		s.sendToolResult(req.ID, outcome, summary+"\n"+string(formatted))
		return true

	case "list_memory_feedback":
		var args struct {
			Project  string `json:"project"`
			MemoryID string `json:"memory_id"`
			Signal   string `json:"signal"`
			Since    string `json:"since"`
			Limit    int    `json:"limit"`
		}
		if err := decodeStrictMemoryArgs(rawArgs, &args); err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		projectID := ""
		if strings.TrimSpace(args.Project) != "" {
			resolved, resolveErr := s.resolveTargetProject(args.Project)
			if resolveErr != nil {
				s.sendError(req.ID, -32602, "Invalid params", resolveErr.Error())
				return true
			}
			projectID = resolved
		}
		signal := store.MemoryFeedbackSignal("")
		if strings.TrimSpace(args.Signal) != "" {
			parsed, ok := store.ParseMemoryFeedbackSignal(args.Signal)
			if !ok {
				s.sendError(req.ID, -32602, "Invalid params", "signal must be one of "+strings.Join(store.MemoryFeedbackSignalValues(), ", "))
				return true
			}
			signal = parsed
		}
		since, err := parseMemorySince(args.Since)
		if err != nil {
			s.sendError(req.ID, -32602, "Invalid params", err.Error())
			return true
		}
		summary, err := s.store.ListMemoryFeedback(store.MemoryFeedbackListOptions{
			ProjectID: projectID,
			MemoryID:  args.MemoryID,
			Signal:    signal,
			Since:     since,
			Limit:     clampLimit(args.Limit, 50, 500),
		})
		if err != nil {
			s.sendError(req.ID, -32603, "Internal error", err.Error())
			return true
		}
		formatted, _ := json.MarshalIndent(summary, "", "  ")
		text := fmt.Sprintf("%d feedback row(s): accepted %d · superseded %d · bound %d · unbound %d",
			summary.Total, summary.Accepted, summary.Superseded, summary.Bound, summary.Unbound)
		s.sendToolResult(req.ID, summary, text+"\n"+string(formatted))
		return true
	}
	return false
}
