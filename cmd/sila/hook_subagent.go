package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

var errParentSessionMissing = errors.New("parent session not found")

// subagentBrief is the bounded, structured brief emitted on SubagentStart.
type subagentBrief struct {
	Schema                string   `json:"schema"`
	ParentSessionID       string   `json:"parent_session_id"`
	SubagentID            string   `json:"subagent_id"`
	Project               string   `json:"project,omitempty"`
	Goal                  string   `json:"active_goal,omitempty"`
	ClaimID               string   `json:"claim_id,omitempty"`
	AllowedPaths          []string `json:"allowed_paths,omitempty"`
	DenyPaths             []string `json:"deny_paths,omitempty"`
	OpenItems             []string `json:"open_items,omitempty"`
	HandoffPolicy         string   `json:"handoff_policy"`
	Memory                []string `json:"memory"`
	Warnings              []string `json:"warnings,omitempty"`
	MemoryContractVersion string   `json:"memory_contract_version"`
	Bytes                 int      `json:"bytes"`
}

// buildSubagentBrief builds the bounded charter/brief for a child session.
// It never mutates the parent marker or parent session state.
func buildSubagentBrief(st *store.Store, env *loaders.HookEnvelope, parentSession, subagentID string) ([]byte, *subagentBrief, error) {
	if st == nil {
		return nil, nil, errors.New("knowledge store unavailable")
	}
	parent, err := st.GetAgentSession(parentSession)
	if err != nil || parent == nil {
		return nil, nil, errParentSessionMissing
	}

	goalSlug, _ := env.Payload["goal"].(string)
	if goalSlug == "" {
		goalSlug = parent.ActiveGoal
	}

	sessionBrief, err := dream.BuildSessionStartBrief(st, "", env.Cwd, subagentID, 512)
	if err != nil {
		return nil, nil, fmt.Errorf("build subagent brief: %w", err)
	}

	sb := &subagentBrief{
		Schema:                "sila/subagent-brief/v1",
		ParentSessionID:       parentSession,
		SubagentID:            subagentID,
		Project:               sessionBrief.Project,
		Goal:                  goalSlug,
		ClaimID:               parent.ClaimID,
		OpenItems:             sessionBrief.OpenItems,
		Memory:                sessionBrief.Memory,
		MemoryContractVersion: sessionBrief.MemoryContractVersion,
		HandoffPolicy:         "record sila handoff before exit; never claim or close the parent goal",
	}

	if charters, cerr := goals.ListActiveCharters(env.Cwd); cerr == nil {
		for _, c := range charters {
			if (goalSlug != "" && c.GoalSlug == goalSlug) || (goalSlug == "" && c.SessionID == parentSession) {
				sb.Goal = c.GoalSlug
				sb.AllowedPaths = c.AllowPaths
				sb.DenyPaths = c.DenyPaths
				break
			}
		}
	}
	if sb.Goal == "" {
		sb.Warnings = append(sb.Warnings, "goal_unbound")
	}

	b, err := json.Marshal(sb)
	if err != nil {
		return nil, nil, err
	}
	for i := 0; i < 4; i++ {
		if sb.Bytes == len(b) {
			break
		}
		sb.Bytes = len(b)
		b, err = json.Marshal(sb)
		if err != nil {
			return nil, nil, err
		}
	}
	return b, sb, nil
}
