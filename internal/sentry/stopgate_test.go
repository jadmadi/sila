package sentry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func stopFixture(t *testing.T) (string, *store.Store, store.Project, string) {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "goals", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "goals", "demo", "goal.md"), []byte("# Goal: Demo\n\n## Files to Touch\n- cmd/sila/hook.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "stop.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(repo)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	t.Setenv("SILA_DB", dbPath)
	return repo, st, p, dbPath
}

func TestEvaluateStopGateReadOnlyAndActive(t *testing.T) {
	repo := t.TempDir()
	res, err := EvaluateStopGate(context.Background(), nil, StopGateOptions{Event: "stop", Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "skip" {
		t.Fatalf("read-only stop should skip: %+v", res)
	}
	res, _ = EvaluateStopGate(context.Background(), nil, StopGateOptions{Event: "stop", Cwd: repo, Mutated: true, StopHookActive: true})
	if res.Verdict != "skip" || res.Diagnostics == nil {
		t.Fatalf("active stop hook should skip: %+v", res)
	}
}

func TestEvaluateStopGateMutatingWarnAndEnforce(t *testing.T) {
	repo, st, p, _ := stopFixture(t)
	defer st.Close()
	res, err := EvaluateStopGate(context.Background(), st, StopGateOptions{Event: "stop", Policy: "warn", Cwd: repo, ProjectID: p.ID, SessionID: "s1", Mutated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "warn" || len(res.Missing) == 0 {
		t.Fatalf("missing write should warn: %+v", res)
	}
	res, _ = EvaluateStopGate(context.Background(), st, StopGateOptions{Event: "stop", Policy: "enforce", Cwd: repo, ProjectID: p.ID, SessionID: "s2", Mutated: true})
	if res.Verdict != "block" || StopGateExitCode(res) != 2 {
		t.Fatalf("missing write should block under enforce: %+v", res)
	}
}

func TestEvaluateStopGateGroundedWriteAndHandoff(t *testing.T) {
	repo, st, p, _ := stopFixture(t)
	defer st.Close()
	if _, err := st.InsertMemo(p.ID, "demo architecture decision", "tester", repo, []string{"cmd/sila/hook.go"}); err != nil {
		t.Fatal(err)
	}
	res, err := EvaluateStopGate(context.Background(), st, StopGateOptions{Event: "stop", Policy: "enforce", Cwd: repo, ProjectID: p.ID, SessionID: "s3", Mutated: true, GoalSlug: "demo", GoalBound: true})
	if err != nil || res.Verdict != "pass" {
		t.Fatalf("grounded write should pass: %+v %v", res, err)
	}
	if err := st.InsertHandoff(store.Handoff{ProjectID: p.ID, Agent: "test", Summary: "handoff"}); err != nil {
		t.Fatal(err)
	}
	res, err = EvaluateStopGate(context.Background(), st, StopGateOptions{Event: "session-end", Policy: "enforce", Cwd: repo, ProjectID: p.ID, SessionID: "s3", Mutated: true})
	if err != nil || res.Verdict != "pass" || res.HandoffID == "" {
		t.Fatalf("session-end with handoff should pass: %+v %v", res, err)
	}
}

func TestEvaluateStopGateWaiverBypassAndRetryCap(t *testing.T) {
	repo, st, p, _ := stopFixture(t)
	defer st.Close()
	waiverDir := filepath.Join(repo, ".sila")
	if err := os.MkdirAll(waiverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	waiver := `{"schema":"sila/stop-waiver/v1","session_id":"s9","goal":"demo","reason":"read-only investigation with no durable decision","approved_by":"test","expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`
	if err := os.WriteFile(filepath.Join(waiverDir, "stop-waiver.json"), []byte(waiver), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := EvaluateStopGate(context.Background(), st, StopGateOptions{Event: "stop", Policy: "enforce", Cwd: repo, ProjectID: p.ID, SessionID: "s9", Mutated: true})
	if err != nil || res.Verdict != "pass" {
		t.Fatalf("valid waiver should pass: %+v %v", res, err)
	}

	t.Setenv("SILA_STOP_GATE", "off")
	res, _ = EvaluateStopGate(context.Background(), st, StopGateOptions{Event: "stop", Policy: "enforce", Cwd: repo, ProjectID: p.ID, SessionID: "s10", Mutated: true})
	if res.Verdict != "skip" || !res.Bypassed {
		t.Fatalf("env bypass should skip: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, ".sila", "state", "stop", "bypass.jsonl")); err != nil {
		t.Fatalf("bypass record missing: %v", err)
	}
	t.Setenv("SILA_STOP_GATE", "")

	stateDir := filepath.Join(repo, ".sila", "state", "stop")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(map[string]any{"session_id": "s11", "retries": 3})
	if err := os.WriteFile(filepath.Join(stateDir, "s11.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	res, _ = EvaluateStopGate(context.Background(), st, StopGateOptions{Event: "stop", Policy: "enforce", Cwd: repo, ProjectID: p.ID, SessionID: "s11", Mutated: true})
	if res.Verdict != "warn" {
		t.Fatalf("retry cap should warn: %+v", res)
	}
}

func TestEvaluateStopGateCorruptStoreDegraded(t *testing.T) {
	repo, st, _, dbPath := stopFixture(t)
	st.Close()
	if err := os.WriteFile(dbPath, []byte("not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SILA_DB", dbPath)
	res, err := EvaluateStopGate(context.Background(), nil, StopGateOptions{Event: "stop", Policy: "enforce", Cwd: repo, SessionID: "s12", Mutated: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != "degraded" || StopGateExitCode(res) != 3 {
		t.Fatalf("corrupt store should degrade: %+v", res)
	}
}
