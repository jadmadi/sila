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

func seedPrimeCard(t *testing.T, dbPath, projectID string) {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card := store.SessionCard{
		Schema:     store.SessionCardSchema,
		ProjectID:  projectID,
		SessionID:  "prime-test",
		Agent:      "dsh",
		CreatedAt:  time.Now().UTC(),
		Intent:     "test prime",
		Outcome:    "state block visible",
		NextAction: store.CardNextAction{Text: "ship the state block", Owner: "agent"},
		Source:     store.CardSource{Generator: "deterministic"},
	}
	if _, err := st.SaveSessionCard(t.Context(), card); err != nil {
		t.Fatal(err)
	}
}

func TestPrimeStateBlockSurfaces(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "s.db")
	ui := render.New(os.Stdout, true)

	// Prime resolves the project id from the repository root. Learn it from a
	// first JSON run so the card lands on the same project in every environment.
	probeOut := captureStdout(t, func() {
		runPrime(ui, dbPath, "", true, false, 5, "")
	})
	var probe map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(probeOut)), &probe); err != nil {
		t.Fatalf("prime --json probe: %v\n%s", err, probeOut)
	}
	projectID, _ := probe["project_id"].(string)
	if projectID == "" {
		t.Fatalf("prime did not report a project id: %s", probeOut)
	}
	seedPrimeCard(t, dbPath, projectID)

	out := captureStdout(t, func() {
		runPrime(ui, dbPath, "", false, false, 5, "")
	})
	if !strings.HasPrefix(out, "### Continuity state") {
		t.Fatalf("state block is not first:\n%s", out)
	}
	if !strings.Contains(out, "ship the state block") {
		t.Fatalf("next action missing:\n%s", out)
	}

	jsonOut := captureStdout(t, func() {
		runPrime(ui, dbPath, "", true, false, 5, "")
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &payload); err != nil {
		t.Fatalf("prime --json: %v\n%s", err, jsonOut)
	}
	block, _ := payload["state_block"].(string)
	if !strings.Contains(block, "Continuity state") {
		t.Fatalf("state_block field = %q", block)
	}
}

func TestPrimeNoStateBlockOnFreshStore(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "fresh.db")
	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runPrime(ui, dbPath, "", false, false, 5, "")
	})
	if strings.Contains(out, "### Continuity state") {
		t.Fatalf("fresh store rendered a state block:\n%s", out)
	}
	jsonOut := captureStdout(t, func() {
		runPrime(ui, dbPath, "", true, false, 5, "")
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &payload); err != nil {
		t.Fatalf("prime --json: %v\n%s", err, jsonOut)
	}
	if value, ok := payload["state_block"]; ok && value != "" {
		t.Fatalf("fresh store JSON state_block = %#v", value)
	}
}
