package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

func seedSubagentStore(t *testing.T, repo string) (*store.Store, store.Project) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "hook.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "cmd"), 0o755); err != nil {
		st.Close()
		t.Fatal(err)
	}
	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir: repo, Slug: "sub-goal", Title: "Sub Goal",
		Description: "A subagent goal", Independent: &indep,
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	ch, err := goals.CreateCharter(repo, "sub-goal", "agent", "parent-1", []string{"cmd/"}, nil, nil, time.Hour)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := goals.SaveCharter(repo, ch); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if err := st.UpsertAgentSession(store.AgentSession{
		SessionID: "parent-1", ProjectID: p.ID, Harness: "claude",
		ActiveGoal: "sub-goal", ClaimID: "claim-1", Cwd: repo, Source: "hook",
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	return st, p
}

func TestBuildSubagentBrief(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	st, _ := seedSubagentStore(t, repo)
	defer st.Close()

	env := &loaders.HookEnvelope{
		Event: loaders.EventSubagentStart, Agent: "claude", Cwd: repo,
		Payload: map[string]any{
			"parent_session_id": "parent-1",
			"subagent_id":       "sub-1",
			"goal":              "sub-goal",
		},
	}
	out, brief, err := buildSubagentBrief(st, env, "parent-1", "sub-1")
	if err != nil {
		t.Fatalf("buildSubagentBrief: %v", err)
	}
	if brief.ParentSessionID != "parent-1" || brief.SubagentID != "sub-1" {
		t.Fatalf("identity = %+v", brief)
	}
	if brief.Goal != "sub-goal" || brief.ClaimID != "claim-1" {
		t.Fatalf("goal/claim = %+v", brief)
	}
	if len(brief.AllowedPaths) != 1 || brief.AllowedPaths[0] != "cmd" {
		t.Fatalf("allowed paths = %v", brief.AllowedPaths)
	}
	if len(brief.Memory) == 0 {
		t.Fatal("brief must carry the memory contract")
	}
	if brief.Bytes != len(out) || len(out) > 4096 {
		t.Fatalf("brief bytes = %d (payload %d), want <=4096 and matching", brief.Bytes, len(out))
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("brief is not JSON: %v", err)
	}
}

func TestBuildSubagentBriefParentMissing(t *testing.T) {
	repo := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "hook.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	env := &loaders.HookEnvelope{Event: loaders.EventSubagentStart, Agent: "claude", Cwd: repo}
	if _, _, err := buildSubagentBrief(st, env, "missing-parent", "sub-1"); !errors.Is(err, errParentSessionMissing) {
		t.Fatalf("error = %v, want errParentSessionMissing", err)
	}
}

var _ = os.Stdout
