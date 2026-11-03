package main

import (
	"os"

	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
)

// recordMemoryEvent is best-effort: adoption telemetry must never fail or slow
// the user command. It stores no query, claim, or path content.
func recordMemoryEvent(st *store.Store, project, agent, verb, outcome, goal, source string, matched int) {
	if st == nil {
		return
	}
	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}
	sessionID := ""
	if id, err := session.ResolveIdentity("", project, cwd); err == nil {
		sessionID = id.SessionID
		if agent == "" {
			agent = id.Agent
		}
	}
	_ = st.RecordMemoryEvent(store.MemoryEvent{
		ProjectID: project, SessionID: sessionID, Agent: agent,
		Verb: verb, Outcome: outcome, GoalSlug: goal, Source: source, Matched: matched,
	})
}
