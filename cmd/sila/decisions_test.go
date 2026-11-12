package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestCLIDecisions_EndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cli_decisions.db")
	ui := render.New(os.Stdout, true)
	project := "proj-cli-dec"

	// 1. Create decision D1
	outCreate := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "arch-goal", true, []string{
			"create",
			"Database Driver Choice",
			"-o", "A: pure-go modernc.org/sqlite (Zero CGO dependency)",
			"-o", "B: cgo mattn/go-sqlite3 (Requires gcc/cgo)",
			"-r", "A",
			"--rationale", "Zero CGO builds stand-alone binaries everywhere",
			"--context", "Cross-platform distribution without toolchain friction",
		})
	})

	var created store.Decision
	if err := json.Unmarshal([]byte(outCreate), &created); err != nil {
		t.Fatalf("unmarshal create output: %v, raw: %s", err, outCreate)
	}
	if created.Address != "D1" {
		t.Fatalf("expected address D1, got %q", created.Address)
	}
	if created.Recommendation != "A" {
		t.Fatalf("expected recommendation A, got %q", created.Recommendation)
	}
	if created.Status != "open" {
		t.Fatalf("expected status open, got %q", created.Status)
	}

	// 2. View decision D1
	outView := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", true, []string{"view", "D1"})
	})
	var viewed store.Decision
	if err := json.Unmarshal([]byte(outView), &viewed); err != nil {
		t.Fatalf("unmarshal view output: %v, raw: %s", err, outView)
	}
	if viewed.ID != created.ID || len(viewed.Options) != 2 {
		t.Fatalf("viewed decision mismatch: %+v", viewed)
	}

	// 3. List decisions
	outList := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", true, []string{"list"})
	})
	var listed []store.Decision
	if err := json.Unmarshal([]byte(outList), &listed); err != nil {
		t.Fatalf("unmarshal list output: %v, raw: %s", err, outList)
	}
	if len(listed) != 1 || listed[0].Address != "D1" {
		t.Fatalf("expected 1 decision D1, got %+v", listed)
	}

	// 4. Resolve decision D1
	outResolve := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "lead-engineer", "", true, []string{
			"resolve",
			"D1",
			"--choice", "A",
			"--notes", "Adopted option A in migration 30",
		})
	})
	var resolved store.Decision
	if err := json.Unmarshal([]byte(outResolve), &resolved); err != nil {
		t.Fatalf("unmarshal resolve output: %v, raw: %s", err, outResolve)
	}
	if resolved.Status != "resolved" || resolved.Resolution != "A" {
		t.Fatalf("expected resolved with A, got %+v", resolved)
	}
	if resolved.ResolvedBy != "lead-engineer" {
		t.Fatalf("expected resolved_by lead-engineer, got %q", resolved.ResolvedBy)
	}

	// 5. Check decisions integrity
	outCheck := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", true, []string{"check"})
	})
	var checkRes map[string]any
	if err := json.Unmarshal([]byte(outCheck), &checkRes); err != nil {
		t.Fatalf("unmarshal check output: %v, raw: %s", err, outCheck)
	}
	if checkRes["clean"] != true || checkRes["open"].(float64) != 0 {
		t.Fatalf("expected clean check with 0 open, got: %+v", checkRes)
	}

	// 6. Connect memo to D1
	outMemo := captureStdout(t, func() {
		runMemo(ui, dbPath, project, "developer", "", "D1", memoOptions{jsonOut: true}, []string{"Pure-Go SQLite is standard across CLI"})
	})
	var memoFact store.Fact
	if err := json.Unmarshal([]byte(outMemo), &memoFact); err != nil {
		t.Fatalf("unmarshal memo output: %v, raw: %s", err, outMemo)
	}
	if !strings.Contains(memoFact.Claim, "[decision: D1]") {
		t.Fatalf("expected claim to reference [decision: D1], got: %s", memoFact.Claim)
	}
}

func TestDecisionCLIDeferAndQuoteResolve(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cli_defer.db")
	ui := render.New(os.Stdout, true)
	project := "proj-cli-defer"

	outCreate := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", true, []string{
			"create", "Worker mode", "-o", "A: Enable", "-o", "B: Disable", "-r", "A",
		})
	})
	var created store.Decision
	if err := json.Unmarshal([]byte(outCreate), &created); err != nil {
		t.Fatalf("unmarshal create: %v (%s)", err, outCreate)
	}

	outDefer := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", true, []string{
			"defer", created.Address, "--notes", "waiting on user", "--by", "user",
		})
	})
	var deferred store.Decision
	if err := json.Unmarshal([]byte(outDefer), &deferred); err != nil {
		t.Fatalf("unmarshal defer: %v (%s)", err, outDefer)
	}
	if deferred.Status != "deferred" || deferred.Resolution != "" {
		t.Fatalf("deferred = %#v", deferred)
	}

	outResolve := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", true, []string{
			"resolve", created.Address, "--choice", "A", "--user-quote", "choose A", "--route-note", "via worker",
		})
	})
	var resolved store.Decision
	if err := json.Unmarshal([]byte(outResolve), &resolved); err != nil {
		t.Fatalf("unmarshal resolve: %v (%s)", err, outResolve)
	}
	if resolved.Status != "resolved" || resolved.UserQuote != "choose A" || resolved.RouteNote != "via worker" {
		t.Fatalf("resolved = %#v", resolved)
	}

	outView := captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", false, []string{"view", created.Address})
	})
	if !strings.Contains(outView, "User Quote") || !strings.Contains(outView, "choose A") {
		t.Fatalf("view missing user quote: %s", outView)
	}
	if !strings.Contains(outView, "Route Note") || !strings.Contains(outView, "via worker") {
		t.Fatalf("view missing route note: %s", outView)
	}
}

func TestDecisionCLIRequireQuoteExit(t *testing.T) {
	if os.Getenv("DECISION_REQUIRE_QUOTE_HELPER") == "1" {
		ui := render.New(os.Stdout, true)
		runDecision(ui, os.Getenv("DECISION_DB"), os.Getenv("DECISION_PROJECT"), "developer", "", false, []string{
			"resolve", "D1", "--choice", "A", "--require-quote",
		})
		return
	}
	dbPath := filepath.Join(t.TempDir(), "cli_require_quote.db")
	ui := render.New(os.Stdout, true)
	project := "proj-cli-require-quote"
	_ = captureStdout(t, func() {
		runDecision(ui, dbPath, project, "developer", "", true, []string{
			"create", "Worker mode", "-o", "A: Enable", "-o", "B: Disable", "-r", "A",
		})
	})

	cmd := exec.Command(os.Args[0], "-test.run=TestDecisionCLIRequireQuoteExit")
	cmd.Env = append(os.Environ(), "DECISION_REQUIRE_QUOTE_HELPER=1", "DECISION_DB="+dbPath, "DECISION_PROJECT="+project)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected non-zero exit for --require-quote without --user-quote")
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	current, err := st.GetDecision(project, "D1")
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "open" {
		t.Fatalf("decision mutated to %q after rejected resolve", current.Status)
	}
}
