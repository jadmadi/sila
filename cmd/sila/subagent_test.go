package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
)

func TestSubagentCharterAndMemoAttribution(t *testing.T) {
	dir := t.TempDir()
	previous, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	ui := render.New(os.Stdout, true)
	db := filepath.Join(dir, "store.db")

	out := captureStdout(t, func() {
		runSubagent(ui, db, true, false, []string{
			"charter", "--parent-session=p1", "--subagent=s1", "--goal=goal-x",
			"--lane=lane-a", "--paths=cmd/sila/",
		})
	})
	var charter session.SubagentCharter
	if err := json.Unmarshal([]byte(out), &charter); err != nil {
		t.Fatalf("invalid charter JSON: %v\n%s", err, out)
	}
	if charter.ParentSessionID != "p1" || charter.SubagentID != "s1" || charter.Signature == "" {
		t.Fatalf("charter = %+v", charter)
	}
	path := filepath.Join(dir, ".sila", "state", "charters", "s1.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("charter file missing: %v", err)
	}

	t.Setenv("SILA_CHARTER", path)
	captureStdout(t, func() {
		runMemo(ui, db, "proj", "agent", "cmd/sila/hook.go", "", memoOptions{}, []string{"child decision"})
	})

	st, err := store.OpenRO(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	facts, err := st.FactsAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) == 0 {
		t.Fatal("memo not written")
	}
	fact := facts[len(facts)-1]
	if fact.SessionRef != "p1" {
		t.Fatalf("session_ref = %q, want p1", fact.SessionRef)
	}
	if fact.OriginRole != "subagent:s1" {
		t.Fatalf("origin_role = %q, want subagent:s1", fact.OriginRole)
	}
	if !strings.Contains(fact.Claim, "child decision") {
		t.Fatalf("claim = %q", fact.Claim)
	}
}

func TestSubagentVerifyRejectsTamper(t *testing.T) {
	dir := t.TempDir()
	previous, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	key, err := session.LoadOrCreateCharterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	charter, err := session.SignCharter(session.SubagentCharter{
		Schema: session.CharterSchema, CharterID: "sub_s1", ParentSessionID: "p1", SubagentID: "s1",
		ProjectID: "proj", HandoffPolicy: "parent-only",
		CreatedAt: "2026-10-22T00:00:00Z", ExpiresAt: "2099-10-22T00:00:00Z",
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	charter.ParentSessionID = "spoofed"
	data, _ := json.Marshal(charter)
	path := filepath.Join(dir, "tampered.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = session.LoadCharter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.VerifyCharter(charter, key); err == nil || !strings.Contains(err.Error(), "CHARTER_SIGNATURE") {
		t.Fatalf("tamper error = %v", err)
	}
}
