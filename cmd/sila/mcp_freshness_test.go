package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
)

// TestAppendMCPFreshnessCheckContributesAtMostOneCheck pins the wiring between
// the command layer and the doctor report: the check is added (or the platform
// is skipped), and the summary counts always match the checks.
func TestAppendMCPFreshnessCheckContributesAtMostOneCheck(t *testing.T) {
	rep := &store.DoctorReport{
		Timestamp: time.Now().UTC(),
		Checks: []store.CheckResult{
			{Category: "Database", Name: "SQLite Integrity Check", Status: store.StatusPass},
		},
	}
	before := len(rep.Checks)

	appendMCPFreshnessCheck(rep)

	if rep.MCP == nil {
		t.Fatal("expected structured MCP freshness report")
	}
	added := len(rep.Checks) - before
	if added > 1 {
		t.Fatalf("expected at most one Runtime check, got %d", added)
	}
	if added == 1 {
		c := rep.Checks[len(rep.Checks)-1]
		if c.Category != "Runtime" || c.Name != "Running MCP Servers" {
			t.Fatalf("unexpected check: %+v", c)
		}
		switch c.Status {
		case store.StatusPass, store.StatusWarn:
		default:
			t.Fatalf("running-server check must be PASS or WARN, got %q", c.Status)
		}
	}

	// Counters must reflect whatever is in Checks, including the new one.
	pass, warn, fail := 0, 0, 0
	for _, c := range rep.Checks {
		switch c.Status {
		case store.StatusPass:
			pass++
		case store.StatusWarn:
			warn++
		case store.StatusFail:
			fail++
		}
	}
	if rep.Passed != pass || rep.Warnings != warn || rep.Failures != fail {
		t.Fatalf("summary counts stale: report=%d/%d/%d actual=%d/%d/%d",
			rep.Passed, rep.Warnings, rep.Failures, pass, warn, fail)
	}
}

// TestDoctorFinalizeRecomputesStatus covers the exported summary helper the
// command layer relies on after appending its own check.
func TestDoctorFinalizeRecomputesStatus(t *testing.T) {
	rep := &store.DoctorReport{}
	rep.AddCheck("Runtime", "a", store.StatusPass, "ok", "", "")
	rep.Finalize()
	if rep.OverallStatus != store.StatusPass || rep.Passed != 1 {
		t.Fatalf("single pass not summarized: %+v", rep)
	}

	rep.AddCheck("Runtime", "b", store.StatusWarn, "hmm", "", "")
	rep.Finalize()
	if rep.OverallStatus != store.StatusWarn || rep.Passed != 1 || rep.Warnings != 1 {
		t.Fatalf("warning not reflected: %+v", rep)
	}

	rep.AddCheck("Runtime", "c", store.StatusFail, "bad", "", "")
	rep.Finalize()
	if rep.OverallStatus != store.StatusFail || rep.Failures != 1 {
		t.Fatalf("failure not reflected: %+v", rep)
	}

	// Finalize must be idempotent rather than accumulating counts.
	rep.Finalize()
	if rep.Passed != 1 || rep.Warnings != 1 || rep.Failures != 1 {
		t.Fatalf("Finalize accumulated counts: %+v", rep)
	}
}

// TestIsSilaMCPServerRecognisesThisProcessShape documents the detection
// predicate: argv[0] basename "sila" plus argv[1] "mcp".
func TestIsSilaMCPServerRejectsNonServers(t *testing.T) {
	if isSilaMCPServer(1) {
		t.Fatal("pid 1 must not be classified as a sila MCP server")
	}
	if isSilaMCPServer(-1) {
		t.Fatal("invalid pid must not be classified as a sila MCP server")
	}
}

func TestBuildMCPBridgeDiagnostic(t *testing.T) {
	home := t.TempDir()
	diag := buildMCPBridgeDiagnostic(home)
	if diag.ContinuityTools != mcp.ContinuityToolCount {
		t.Fatalf("continuity tools = %d, want %d", diag.ContinuityTools, mcp.ContinuityToolCount)
	}
	if diag.SchemaDigest == "" || diag.Profile != mcp.ProfileContinuity {
		t.Fatalf("missing schema/profile: %+v", diag)
	}
	if !strings.Contains(diag.ExpectedEnvelope, `"params"`) || !strings.Contains(diag.ExpectedEnvelope, `"name"`) {
		t.Fatalf("unexpected expected envelope: %q", diag.ExpectedEnvelope)
	}
	if diag.LastProxyError != "none recorded" {
		t.Fatalf("missing log should report none recorded, got %q", diag.LastProxyError)
	}

	logDir := filepath.Join(home, ".commandcode", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logBody := strings.Join([]string{
		"2026-10-22T00:00:00Z info startup",
		`2026-10-22T00:00:01Z error mcp__sila__recall Error: Invalid params {"query":"x"}`,
		`2026-10-22T00:00:02Z error mcp__sila__prime_context Error: Invalid params {"project":"p"}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(logDir, "command.log"), []byte(logBody), 0o644); err != nil {
		t.Fatal(err)
	}
	diag = buildMCPBridgeDiagnostic(home)
	if !strings.Contains(diag.LastProxyError, "mcp__sila__prime_context") || !strings.Contains(diag.LastProxyError, "Invalid params") {
		t.Fatalf("last proxy error = %q", diag.LastProxyError)
	}
}
