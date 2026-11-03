package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestFactsCLI_VerifyAndRebaseline(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "facts_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SaveFact(store.Fact{ID: "f_cli_1", ProjectID: "p", Claim: "Authentic claim"}); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)

	// 1. Verify JSON on clean store
	out := captureStdout(t, func() {
		runFactsVerify(ui, st, "p", true, false, nil)
	})
	var audit store.ChainAuditResult
	if err := json.Unmarshal([]byte(out), &audit); err != nil {
		t.Fatalf("expected JSON audit, got: %s (err: %v)", out, err)
	}
	if !audit.Intact || audit.TotalBlocks != 1 {
		t.Fatalf("expected 1 intact block, got %+v", audit)
	}

	// 2. Tamper directly in SQLite
	if _, err := st.DB.Exec(`UPDATE facts SET claim = 'Tampered claim' WHERE id = 'f_cli_1'`); err != nil {
		t.Fatal(err)
	}

	// 3. Rebaseline with reason
	rebOut := captureStdout(t, func() {
		runFactsRebaseline(ui, st, true, "", []string{"--reason=operator approved migration", "--yes", "--allow-no-backup"})
	})
	var rebResult map[string]any
	if err := json.Unmarshal([]byte(rebOut), &rebResult); err != nil {
		t.Fatalf("expected JSON rebaseline result, got: %s (err: %v)", rebOut, err)
	}
	if rebResult["reason"] != "operator approved migration" {
		t.Errorf("expected reason 'operator approved migration', got %v", rebResult["reason"])
	}

	// 4. Human text rebaseline output contains security warning
	textOut := captureStdout(t, func() {
		runFactsRebaseline(ui, st, false, "", []string{"--reason=manual test", "--yes", "--allow-no-backup"})
	})
	if !strings.Contains(textOut, "DOES NOT recover original truth") {
		t.Errorf("expected security notice in rebaseline text output, got: %s", textOut)
	}
}

// TestFactsCLI_RootReasonFlagReachesRebaseline is the regression for the
// silently ignored --reason: the root flagset defines it (goals reopen), so
// Go's flag package consumes `sila facts rebaseline --reason=...` — the syntax
// the ledger auditor prints — before the facts subcommand sees it. The dispatch
// layer now hands the value over; without that, the ledger records the default
// reason and the operator's explanation is lost.
func TestFactsCLI_RootReasonFlagReachesRebaseline(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "facts_reason.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveFact(store.Fact{ID: "f_reason_1", ProjectID: "p", Claim: "Authentic claim"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runFacts(ui, dbPath, "", true, false, false, "authorized: project rename example-client.com->github", []string{"rebaseline", "--yes", "--allow-no-backup"})
	})
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("expected JSON rebaseline result, got: %s (err: %v)", out, err)
	}
	if res["reason"] != "authorized: project rename example-client.com->github" {
		t.Errorf("root --reason did not reach the rebaseline, got %v", res["reason"])
	}
}
