package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestAdoptionReportJSON(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "adoption.db")
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, ev := range []store.ToolEvent{
		{ProjectID: "probe", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "prime_context", Category: "context", CreatedAt: now},
		{ProjectID: "probe", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "recall", Category: "context", CreatedAt: now},
		{ProjectID: "probe", SessionID: "s1", Agent: "command-code", Source: "mcp", ToolName: "save_memo", Category: "write", CreatedAt: now},
	} {
		if err := st.RecordToolEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	out := captureStdout(t, func() {
		runAdoption(render.New(os.Stdout, true), db, "probe", true, false, []string{"report", "--since=7d"})
	})
	var report struct {
		Schema   string `json:"schema"`
		Sessions struct {
			Total  int `json:"total"`
			Prime  int `json:"prime"`
			Recall int `json:"recall"`
			Writes int `json:"writes"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if report.Schema != "sila/adoption-funnel/v1" || report.Sessions.Total != 1 || report.Sessions.Prime != 1 || report.Sessions.Recall != 1 || report.Sessions.Writes != 1 {
		t.Fatalf("report = %+v", report)
	}
}

func TestAdoptionHumanReport(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "adoption.db")
	st, _ := store.Open(db)
	_ = st.RecordToolEvent(store.ToolEvent{
		ProjectID: "probe", SessionID: "s1", Agent: "command-code", Source: "mcp",
		ToolName: "prime", Category: "context", CreatedAt: time.Now().UTC(),
	})
	st.Close()
	out := captureStdout(t, func() {
		runAdoption(render.New(os.Stdout, true), db, "probe", false, false, []string{"report"})
	})
	for _, want := range []string{"sila · adoption", "Sessions: 1", "Coverage:", "Verdict:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("human report missing %q:\n%s", want, out)
		}
	}
}
