package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func resetOutputModes(t *testing.T) {
	t.Helper()
	prev := outModes
	outModes = outputModes{}
	t.Cleanup(func() { outModes = prev })
}

func TestOutputModeConflicts(t *testing.T) {
	cases := []struct {
		name                                      string
		jsonOut, jsonEnvelope, toonOut, porcelain bool
		wantErr                                   bool
	}{
		{name: "json only"},
		{name: "envelope only", jsonEnvelope: true},
		{name: "toon only", toonOut: true},
		{name: "porcelain only", porcelain: true},
		{name: "json and toon", jsonOut: true, toonOut: true, wantErr: true},
		{name: "envelope and toon", jsonEnvelope: true, toonOut: true, wantErr: true},
		{name: "json and porcelain", jsonOut: true, porcelain: true, wantErr: true},
		{name: "envelope and porcelain", jsonEnvelope: true, porcelain: true, wantErr: true},
		{name: "toon and porcelain", toonOut: true, porcelain: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOutputModes(tc.jsonOut, tc.jsonEnvelope, tc.toonOut, tc.porcelain)
			if tc.wantErr && err == nil {
				t.Fatal("expected a conflict error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected conflict: %v", err)
			}
		})
	}
}

func TestSearchJSONEmptyResultsAreArrays(t *testing.T) {
	resetOutputModes(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "search.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	for _, twoPass := range []bool{false, true} {
		name := "raw"
		if twoPass {
			name = "two-pass"
		}
		t.Run(name, func(t *testing.T) {
			out := captureStdout(t, func() {
				runSearch(ui, dbPath, "", "", "", time.Time{}, false, false, twoPass, 3, 0, false, false, false, true, false, false, 0, []string{"zzzz-no-match"})
			})
			var payload any
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("search --json is not valid JSON: %v\n%s", err, out)
			}
			if payload == nil {
				t.Fatal("search --json emitted top-level null")
			}
			if _, ok := payload.([]any); !ok {
				t.Fatalf("search --json emitted %T, want an array", payload)
			}
		})
	}
}

func TestHandoffJSONEmptyCollectionsAreArrays(t *testing.T) {
	resetOutputModes(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "handoff.db")
	ui := render.New(os.Stdout, true)

	out := captureStdout(t, func() {
		runHandoff(ui, dbPath, tmp, "", "agent", "", "", "", true, false, []string{"contract summary"})
	})
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("handoff --json is not valid JSON: %v\n%s", err, out)
	}
	for _, field := range []string{"open_items", "decisions", "repo_files"} {
		raw, ok := payload[field]
		if !ok {
			t.Fatalf("missing %s", field)
		}
		if string(raw) != "[]" {
			t.Fatalf("%s = %s, want []", field, raw)
		}
	}
}

func TestJSONEnvelopeModeWrapsSuccessAndErrors(t *testing.T) {
	resetOutputModes(t)
	outModes.envelope = true

	success := captureStdout(t, func() {
		emitJSON(map[string]any{"items": []string(nil)})
	})
	var envelope struct {
		Schema    string            `json:"schema"`
		OK        bool              `json:"ok"`
		Data      map[string]any    `json:"data"`
		Warnings  []string          `json:"warnings"`
		Error     *render.JSONError `json:"error"`
		Mutations []string          `json:"mutations"`
	}
	if err := json.Unmarshal([]byte(success), &envelope); err != nil {
		t.Fatalf("envelope is not valid JSON: %v\n%s", err, success)
	}
	if envelope.Schema != render.JSONSchemaV1 || !envelope.OK {
		t.Fatalf("bad success envelope: %s", success)
	}
	if envelope.Data["items"] == nil {
		t.Fatal("envelope data.items is null")
	}
	if envelope.Warnings == nil || envelope.Mutations == nil {
		t.Fatal("envelope collections must be [] not null")
	}

	failure := captureStdout(t, func() {
		emitErrorEnvelope("GOAL_NOT_FOUND", "missing", "sila goals")
	})
	var errEnvelope struct {
		OK    bool              `json:"ok"`
		Data  any               `json:"data"`
		Error *render.JSONError `json:"error"`
	}
	if err := json.Unmarshal([]byte(failure), &errEnvelope); err != nil {
		t.Fatalf("error envelope is not valid JSON: %v\n%s", err, failure)
	}
	if errEnvelope.OK || errEnvelope.Error == nil || errEnvelope.Error.Code != "GOAL_NOT_FOUND" {
		t.Fatalf("bad error envelope: %s", failure)
	}
}

func TestGoalsJSONReadOnlyByDefaultAndSyncMutations(t *testing.T) {
	resetOutputModes(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals.db")
	ui := render.New(os.Stdout, true)

	_ = captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "Read Only Target", "Description", "", "internal/readonly/target.go", 1,
			true, false, false, false, false, false, false, false, false, false, false, false, true,
			"", "", "", false, []string{"create", "read-only-target"})
	})

	readmePath := filepath.Join(tmp, "goals", "README.md")
	const sentinel = "# SENTINEL — machine reads must not rewrite this\n"
	if err := os.WriteFile(readmePath, []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}

	listArgs := func() []string { return nil }
	runList := func(jsonOut bool) string {
		return captureStdout(t, func() {
			runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
				false, false, false, false, false, false, false, false, jsonOut, false, false, false, false,
				"", "", "", false, listArgs())
		})
	}

	out := runList(true)
	var payload any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("goals --json is not valid JSON: %v\n%s", err, out)
	}
	got, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("goals --json rewrote goals/README.md:\n%s", got)
	}

	outModes.sync = true
	synced := runList(true)
	var envelope struct {
		Schema    string   `json:"schema"`
		OK        bool     `json:"ok"`
		Mutations []string `json:"mutations"`
	}
	if err := json.Unmarshal([]byte(synced), &envelope); err != nil {
		t.Fatalf("goals --json --sync did not emit an envelope: %v\n%s", err, synced)
	}
	if envelope.Schema != render.JSONSchemaV1 || !envelope.OK {
		t.Fatalf("bad sync envelope: %s", synced)
	}
	found := false
	for _, m := range envelope.Mutations {
		if m == "goals/README.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mutations does not report goals/README.md: %#v", envelope.Mutations)
	}
	got, err = os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == sentinel {
		t.Fatal("goals --json --sync did not reconcile goals/README.md")
	}
}

func TestGoalsPorcelainGolden(t *testing.T) {
	resetOutputModes(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "goals.db")
	ui := render.New(os.Stdout, true)

	_ = captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "Porcelain Target", "Description", "", "internal/porcelain/target.go", 1,
			true, false, false, false, false, false, false, false, false, false, false, false, true,
			"immediate", "", "", false, []string{"create", "porcelain-target"},
			goalsRunOptions{tier: "immediate"})
	})

	outModes.porcelain = true
	outModes.quiet = true
	got := captureStdout(t, func() {
		runGoals(ui, dbPath, tmp, "agent", "", "", "", false, false, "", "", "", "", "", 0,
			false, false, false, false, false, false, false, false, false, false, false, false, false,
			"", "", "", false, nil)
	})

	goldenPath := filepath.Join("testdata", "goals_porcelain.golden")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read porcelain golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("porcelain output drifted from %s\n--- got ---\n%s--- want ---\n%s", goldenPath, got, want)
	}
}

func TestCLIContractSubprocess(t *testing.T) {
	if os.Getenv("GO_WANT_CLI_CONTRACT_SUBPROCESS") != "1" {
		return
	}
	action := os.Getenv("CLI_CONTRACT_ACTION")
	tmp := os.Getenv("CLI_CONTRACT_TMP")
	db := filepath.Join(tmp, "store.db")
	ui := render.New(os.Stdout, false)

	switch action {
	case "packet_missing":
		gc := &goalsContext{jsonOut: true}
		gc.handlePacket(tmp, []string{"--json"})
	case "decision_missing":
		st, err := store.Open(db)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
		defer st.Close()
		viewDecision(ui, st, "contract-project", "D99", true)
	case "readiness_missing_slug":
		gc := &goalsContext{jsonOut: true}
		gc.handleReadiness(tmp, []string{"--json"})
	case "onboard_check":
		runOnboardCheck(ui, db, "contract-project", false, false, true)
	case "onboard_advisory":
		outModes.advisory = true
		runOnboardCheck(ui, db, "contract-project", false, false, true)
	}
	os.Exit(0)
}

func runCLIContractSubprocess(t *testing.T, action, dir string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestCLIContractSubprocess")
	cmd.Env = append(os.Environ(),
		"GO_WANT_CLI_CONTRACT_SUBPROCESS=1",
		"CLI_CONTRACT_ACTION="+action,
		"CLI_CONTRACT_TMP="+dir,
	)
	cmd.Dir = dir
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("subprocess failed: %v", err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), code
}

func TestJSONErrorExitCodes(t *testing.T) {
	resetOutputModes(t)
	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	cases := []struct {
		action   string
		wantCode int
		wantKey  string
	}{
		{action: "packet_missing", wantCode: 2, wantKey: "USAGE"},
		{action: "decision_missing", wantCode: 1, wantKey: "DECISION_NOT_FOUND"},
		{action: "readiness_missing_slug", wantCode: 2, wantKey: "USAGE"},
		{action: "onboard_check", wantCode: 1, wantKey: "ready"},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			out, code := runCLIContractSubprocess(t, tc.action, tmp)
			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\n%s", code, tc.wantCode, out)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(out), &payload); err != nil {
				t.Fatalf("stdout is not a single JSON document: %v\n%s", err, out)
			}
			if tc.wantKey == "ready" {
				data, ok := payload["data"].(map[string]any)
				if !ok || data["ready"] != false {
					t.Fatalf("onboard payload lacks data.ready:false: %s", out)
				}
				errObj, _ := payload["error"].(map[string]any)
				if errObj["code"] != "ONBOARDING_INCOMPLETE" {
					t.Fatalf("onboard error.code = %v, want ONBOARDING_INCOMPLETE", errObj["code"])
				}
				return
			}
			errObj, ok := payload["error"].(map[string]any)
			if !ok {
				t.Fatalf("missing error object: %s", out)
			}
			if errObj["code"] != tc.wantKey {
				t.Fatalf("error.code = %v, want %s", errObj["code"], tc.wantKey)
			}
			if payload["schema"] != render.JSONSchemaV1 {
				t.Fatalf("schema = %v", payload["schema"])
			}
		})
	}
}

func TestOnboardAdvisoryExitsZero(t *testing.T) {
	resetOutputModes(t)
	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	out, code := runCLIContractSubprocess(t, "onboard_advisory", tmp)
	if code != 0 {
		t.Fatalf("advisory exit code = %d, want 0\n%s", code, out)
	}
	var payload struct {
		Schema string `json:"schema"`
		Data   struct {
			Advisory bool `json:"advisory"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if payload.Schema != "sila/json/v1" || !payload.Data.Advisory {
		t.Fatalf("advisory envelope missing: %s", out)
	}
}
