package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

func TestGateCompactModeSelection(t *testing.T) {
	cases := []struct {
		name             string
		compact, verbose bool
		tty              bool
		env              string
		want             bool
		wantErr          bool
	}{
		{"both", true, true, false, "", false, true},
		{"explicit compact", true, false, true, "", true, false},
		{"explicit verbose", false, true, false, "", false, false},
		{"env detail", false, false, false, "0", false, false},
		{"piped", false, false, false, "", true, false},
		{"tty", false, false, true, "", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SILA_GATE_COMPACT", tc.env)
			got, err := gateCompactMode(tc.compact, tc.verbose, tc.tty)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %v err=%v, want %v err=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestGateCompactPassAtMostTwelveLines(t *testing.T) {
	ui := render.New(os.Stdout, true)
	checks := []gateCheck{
		{Name: "tests", Status: "pass", DurationMS: 12},
		{Name: "format", Status: "pass"},
		{Name: "changelog", Status: "pass"},
		{Name: "sentry", Status: "pass"},
		{Name: "arch", Status: "pass"},
	}
	out := captureStdout(t, func() {
		renderGateCompact(ui, checks, gateVerdict{Clean: true}, "/tmp/receipt.log", 0)
	})
	if lines := strings.Count(out, "\n"); lines > 12 {
		t.Fatalf("%d lines:\n%s", lines, out)
	}
	if !strings.Contains(out, "PASS tests 12ms") {
		t.Fatalf("missing pass line:\n%s", out)
	}
	if !strings.Contains(out, "PASS 5 checks") {
		t.Fatalf("missing summary:\n%s", out)
	}
}

func TestGateCompactFailPrintsOutput(t *testing.T) {
	ui := render.New(os.Stdout, true)
	checks := []gateCheck{
		{Name: "tests", Status: "pass"},
		{Name: "gate", Status: "fail", Detail: "two blockers", Output: "boom: assertion failed"},
	}
	verdict := gateVerdict{Clean: false, Blockers: []gateCheck{checks[1]}}
	out := captureStdout(t, func() {
		renderGateCompact(ui, checks, verdict, "/tmp/r.log", 0)
	})
	for _, want := range []string{"FAIL gate", "two blockers", "boom: assertion failed", "FAIL 1 of 2 checks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestGateRunReceiptWritesAndCopies(t *testing.T) {
	cwd := t.TempDir()
	checks := []gateCheck{{Name: "tests", Status: "pass", Detail: "ok", Output: "out"}}
	path := writeGateRunReceipt(cwd, checks, gateVerdict{Clean: true, ExitCode: 0})
	if path == "" {
		t.Fatal("no receipt path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tests") || !strings.Contains(string(data), "out") {
		t.Fatalf("receipt = %s", data)
	}
	last, err := os.ReadFile(filepath.Join(cwd, ".sila", "gate", "last-run.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(last) != string(data) {
		t.Fatal("last-run.log differs from the newest run")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestGateReceiptPrunesToFive(t *testing.T) {
	cwd := t.TempDir()
	for i := 0; i < 11; i++ {
		writeGateRunReceipt(cwd, []gateCheck{{Name: "tests", Status: "pass"}}, gateVerdict{Clean: true})
		time.Sleep(time.Millisecond)
	}
	matches, _ := filepath.Glob(filepath.Join(cwd, ".sila", "gate", "run-*.log"))
	if len(matches) != 5 {
		t.Fatalf("kept %d receipts", len(matches))
	}
}

func TestGateReceiptTruncatesLargeOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("large output")
	}
	cwd := t.TempDir()
	big := strings.Repeat("x", gateReceiptMaxBytes+1024)
	path := writeGateRunReceipt(cwd, []gateCheck{{Name: "tests", Status: "fail", Output: big}}, gateVerdict{Clean: false})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[truncated at 10 MiB]") {
		t.Fatal("missing truncation marker")
	}
	if len(data) > gateReceiptMaxBytes+(1<<20) {
		t.Fatalf("receipt is %d bytes", len(data))
	}
}

func TestGateReceiptReadOnlyFailsOpen(t *testing.T) {
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".sila")
	if err := os.MkdirAll(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if path := writeGateRunReceipt(cwd, []gateCheck{{Name: "tests", Status: "pass"}}, gateVerdict{Clean: true}); path != "" {
		t.Fatalf("read-only write returned %q", path)
	}
}

func TestGateReceiptRedactsSecrets(t *testing.T) {
	cwd := t.TempDir()
	checks := []gateCheck{{Name: "tests", Status: "fail", Output: "postgres://user:receiptpass@db.local/app"}}
	path := writeGateRunReceipt(cwd, checks, gateVerdict{Clean: false})
	if path == "" {
		t.Fatal("no receipt")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "receiptpass") {
		t.Fatalf("receipt leaked the secret: %s", data)
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("receipt missing redaction marker: %s", data)
	}
}
