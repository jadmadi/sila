package mcp

import (
	"io"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// NewServer creates a new MCP server connected to the knowledge store. It
// adopts SILA_SESSION_ID when present; otherwise it generates a process-local
// identifier that is never written to the project current pointer.
func (s *Server) recordMemoryEvent(project, verb, outcome, goal string, matched int) {
	if s == nil || s.store == nil {
		return
	}
	_ = s.store.RecordMemoryEvent(store.MemoryEvent{
		ProjectID: project, SessionID: s.sessionID, Agent: "mcp",
		Verb: verb, Outcome: outcome, GoalSlug: goal, Source: "mcp", Matched: matched,
	})
}

func NewServer(st *store.Store, in io.Reader, out io.Writer) *Server {
	sessionID := strings.TrimSpace(os.Getenv("SILA_SESSION_ID"))
	source := "env"
	if sessionID == "" {
		sessionID = GenerateSessionID()
		source = "mcp-process"
	}
	return &Server{store: st, in: in, out: out, sessionID: sessionID, sessionSource: source}
}
