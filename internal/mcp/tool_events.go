package mcp

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// recordToolEvent writes one privacy-safe adoption event. It never changes the
// tool-call response and honors SILA_TOOL_LEDGER=off|summary|full.
func (s *Server) recordToolEvent(name string, args json.RawMessage, start time.Time) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("SILA_TOOL_LEDGER")))
	if mode == "off" || s.store == nil {
		return
	}
	s.mu.Lock()
	outcome := "ok"
	if s.lastToolError {
		outcome = "error"
	}
	s.mu.Unlock()
	project := os.Getenv("SILA_PROJECT_ID")
	if project == "" {
		if cwd, err := os.Getwd(); err == nil {
			project = store.ProjectID(cwd)
		}
	}
	session := os.Getenv("SILA_SESSION_ID")
	if session == "" {
		session = os.Getenv("SILA_SESSION")
	}
	agent := s.ledgerAgent()
	shape := ""
	if mode == "full" {
		shape = store.BuildArgsShape(args)
	}
	_ = s.store.RecordToolEvent(store.ToolEvent{
		ProjectID: project, SessionID: session, Agent: agent, Source: "mcp",
		ToolName: name, Category: toolCategories[name], Outcome: outcome,
		DurationMS: time.Since(start).Milliseconds(), ArgsShape: shape, CreatedAt: time.Now().UTC(),
	})
}
