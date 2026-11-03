package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/ccli"
)

func TestCCLIChildHelper(t *testing.T) {
	if os.Getenv("SILA_CCLI_CHILD") != "1" {
		return
	}
	raw := os.Getenv("SILA_CCLI_CHILD_ARGS")
	args := []string{"sila", "--no-ccli"}
	if raw != "" {
		args = append(args, strings.Split(raw, "\x1f")...)
	}
	os.Args = args
	main()
	os.Exit(0)
}

func TestCCLIDryRunMachinePayloadsAndNoExecution(t *testing.T) {
	t.Setenv("SILA_CCLI", "on")
	t.Setenv("SILA_CCLI_DENYLIST", "")

	// 1. Human dry-run across all 4 truthy spellings; --dry-run=false executes hostRunner
	for _, tc := range []struct {
		flag       string
		wantCalled bool
	}{
		{"--dry-run", false},
		{"--dry-run=true", false},
		{"--preview", false},
		{"-n", false},
		{"--dry-run=false", true},
	} {
		var out, errBuf bytes.Buffer
		streams := ccli.NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)
		called := false
		var gotCanon []string
		canon, handled, code := executeCCLIDispatch(
			[]string{"save", "memo", "use WAL", tc.flag},
			ccli.RewriteAuto,
			false,
			streams,
			func(canonicalArgs []string, _ *ccli.Production, _ *ccli.Streams) int {
				called = true
				gotCanon = canonicalArgs
				return 0
			},
		)
		if !handled || code != 0 {
			t.Fatalf("flag %s: expected handled=true code=0, got handled=%v code=%d (stderr=%s)", tc.flag, handled, code, errBuf.String())
		}
		if called != tc.wantCalled {
			t.Fatalf("flag %s: hostRunner called = %v, want %v", tc.flag, called, tc.wantCalled)
		}
		if !tc.wantCalled {
			if out.Len() != 0 {
				t.Errorf("flag %s: expected empty stdout in human dry-run, got %q", tc.flag, out.String())
			}
			if !strings.Contains(errBuf.String(), "sila · ccli dry-run") || !strings.Contains(errBuf.String(), "no changes applied") {
				t.Errorf("flag %s: expected dry-run preview on stderr, got %q", tc.flag, errBuf.String())
			}
		} else {
			want := []string{"memo", "use WAL"}
			if !reflect.DeepEqual(gotCanon, want) || !reflect.DeepEqual(canon, want) {
				t.Errorf("flag %s: expected stripped canonical %v, got %v", tc.flag, want, gotCanon)
			}
		}
	}

	// 2. Machine dry-run payloads (--json, --json-envelope, --toon, --porcelain)
	// --json
	var out, errBuf bytes.Buffer
	streams := ccli.NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)
	_, handled, code := executeCCLIDispatch(
		[]string{"save", "memo", "use WAL", "--dry-run", "--json"},
		ccli.RewriteAuto, false, streams,
		func(_ []string, _ *ccli.Production, _ *ccli.Streams) int {
			t.Fatal("hostRunner must never run during dry-run")
			return 0
		},
	)
	if !handled || code != 0 {
		t.Fatalf("--json dry-run: got handled=%v code=%d", handled, code)
	}
	var jsonPayload map[string]any
	if err := json.Unmarshal(out.Bytes(), &jsonPayload); err != nil {
		t.Fatalf("unmarshal --json dry-run stdout: %v (raw=%s)", err, out.String())
	}
	if jsonPayload["ok"] != true || jsonPayload["dry_run"] != true || jsonPayload["would_execute"] != true {
		t.Errorf("unexpected --json dry-run payload: %v", jsonPayload)
	}
	if !strings.Contains(errBuf.String(), "sila · ccli dry-run") {
		t.Errorf("expected dry-run preview on stderr in --json mode, got %q", errBuf.String())
	}

	// --json-envelope
	out.Reset()
	errBuf.Reset()
	_, handled, code = executeCCLIDispatch(
		[]string{"save", "memo", "use WAL", "--preview", "--json-envelope"},
		ccli.RewriteDo, false, streams,
		func(_ []string, _ *ccli.Production, _ *ccli.Streams) int {
			t.Fatal("hostRunner must never run during dry-run")
			return 0
		},
	)
	if !handled || code != 0 {
		t.Fatalf("--json-envelope dry-run: got handled=%v code=%d", handled, code)
	}
	var envPayload map[string]any
	if err := json.Unmarshal(out.Bytes(), &envPayload); err != nil {
		t.Fatalf("unmarshal --json-envelope dry-run stdout: %v", err)
	}
	if envPayload["schema"] != "sila/json/v1" || envPayload["ok"] != true {
		t.Errorf("unexpected --json-envelope dry-run header: %v", envPayload)
	}
	dataMap, _ := envPayload["data"].(map[string]any)
	if dataMap["dry_run"] != true || dataMap["would_execute"] != true {
		t.Errorf("unexpected --json-envelope dry-run data: %v", dataMap)
	}

	// --toon
	out.Reset()
	errBuf.Reset()
	_, handled, code = executeCCLIDispatch(
		[]string{"save", "memo", "use WAL", "-n", "--toon"},
		ccli.RewriteAuto, false, streams, nil,
	)
	if !handled || code != 0 || !strings.Contains(out.String(), "ccli_dry_run") || !strings.Contains(out.String(), "would_execute: true") {
		t.Errorf("unexpected --toon dry-run output (code=%d): %s", code, out.String())
	}

	// --porcelain
	out.Reset()
	errBuf.Reset()
	_, handled, code = executeCCLIDispatch(
		[]string{"save", "memo", "use WAL", "--dry-run", "--porcelain"},
		ccli.RewriteAuto, false, streams, nil,
	)
	if !handled || code != 0 || !strings.HasPrefix(out.String(), "dry-run\tmemo \"use WAL\"") {
		t.Errorf("unexpected --porcelain dry-run output (code=%d): %q", code, out.String())
	}
}

func TestCCLITier3ConfirmationJSONEnvelopesAndControlFlagStripping(t *testing.T) {
	t.Setenv("SILA_CCLI", "on")
	t.Setenv("SILA_CCLI_DENYLIST", "")

	// 1. Non-TTY Tier 3 in --json-envelope mode never prompts and emits CCLI_CONFIRMATION_REQUIRED envelope with exit 3
	var out, errBuf bytes.Buffer
	ttyStreams := ccli.NewStreamsWithTTY(strings.NewReader("yes\n"), &out, &errBuf, true, true)
	_, handled, code := executeCCLIDispatch(
		[]string{"drop", "goal", "old-goal", "--json-envelope"},
		ccli.RewriteAuto, false, ttyStreams,
		func(_ []string, _ *ccli.Production, _ *ccli.Streams) int {
			t.Fatal("hostRunner must not run when confirmation is refused")
			return 0
		},
	)
	if !handled || code != 3 {
		t.Fatalf("expected exit 3 in --json-envelope Tier 3 without --yes, got handled=%v code=%d", handled, code)
	}
	var errEnv map[string]any
	if err := json.Unmarshal(out.Bytes(), &errEnv); err != nil {
		t.Fatalf("unmarshal error envelope: %v (raw=%s)", err, out.String())
	}
	errObj, _ := errEnv["error"].(map[string]any)
	if errEnv["ok"] != false || errObj["code"] != ccli.ErrCodeConfirmationRequired {
		t.Errorf("expected CCLI_CONFIRMATION_REQUIRED error envelope, got %v", errEnv)
	}

	// 2. Secret refusal in --json-envelope mode emits CCLI_SECRET and never leaks the secret
	out.Reset()
	errBuf.Reset()
	secret := "ghp_SUPERSECRETVALUE123456"
	_, handled, code = executeCCLIDispatch(
		[]string{"save", "memo", secret, "--json-envelope"},
		ccli.RewriteAuto, false, ttyStreams, nil,
	)
	if !handled || code != 3 {
		t.Fatalf("expected secret refusal exit 3, got handled=%v code=%d", handled, code)
	}
	if strings.Contains(out.String()+errBuf.String(), secret) {
		t.Fatalf("secret leaked in output: stdout=%s stderr=%s", out.String(), errBuf.String())
	}
	var secEnv map[string]any
	if err := json.Unmarshal(out.Bytes(), &secEnv); err != nil {
		t.Fatalf("unmarshal secret error envelope: %v", err)
	}
	secErrObj, _ := secEnv["error"].(map[string]any)
	if secErrObj["code"] != ccli.ErrCodeSecret {
		t.Errorf("expected CCLI_SECRET error code, got %v", secErrObj)
	}

	// 3. Control flags (--no-input, --confirm, --no-ccli, --ccli-strict-exit, user --yes/--force) are stripped before hostRunner,
	// while static_flags: ["--force"] on "force vacuum database" is preserved once!
	out.Reset()
	errBuf.Reset()
	var dispatched []string
	_, handled, code = executeCCLIDispatch(
		[]string{"drop", "goal", "old-goal", "--confirm=old-goal", "--no-input", "--ccli-strict-exit", "--yes"},
		ccli.RewriteAuto, false, ttyStreams,
		func(canonicalArgs []string, _ *ccli.Production, _ *ccli.Streams) int {
			dispatched = canonicalArgs
			return 0
		},
	)
	if !handled || code != 0 {
		t.Fatalf("expected confirm+yes dispatch to succeed, got handled=%v code=%d (stderr=%s)", handled, code, errBuf.String())
	}
	wantDispatched := []string{"goals", "release", "old-goal"}
	if !reflect.DeepEqual(dispatched, wantDispatched) {
		t.Errorf("dispatched canonical argv = %v, want %v (all CCLI control flags stripped)", dispatched, wantDispatched)
	}

	// Production with static_flags: ["--force"] ("force vacuum database") keeps one "--force"
	out.Reset()
	errBuf.Reset()
	dispatched = nil
	_, handled, code = executeCCLIDispatch(
		[]string{"force", "vacuum", "database", "--yes"},
		ccli.RewriteAuto, false, ttyStreams,
		func(canonicalArgs []string, _ *ccli.Production, _ *ccli.Streams) int {
			dispatched = canonicalArgs
			return 0
		},
	)
	if !handled || code != 0 {
		t.Fatalf("expected force vacuum database --yes to succeed, got handled=%v code=%d", handled, code)
	}
	wantVacuum := []string{"vacuum", "--force"}
	if !reflect.DeepEqual(dispatched, wantVacuum) {
		t.Errorf("dispatched canonical argv = %v, want %v", dispatched, wantVacuum)
	}
}

func TestCCLIHostExitCodeMatrixAndPanicRecovery(t *testing.T) {
	t.Setenv("SILA_CCLI", "on")
	t.Setenv("SILA_CCLI_DENYLIST", "")

	// 1. Standard production (sila_memo_save, preserve_exit=0): host 1 maps to 4; 0, 2, 3, 4, 5, 130 pass through
	for _, tc := range []struct {
		hostExit int
		wantExit int
	}{
		{0, 0},
		{1, 4},
		{2, 2},
		{3, 3},
		{4, 4},
		{5, 5},
		{130, 130},
	} {
		var out, errBuf bytes.Buffer
		streams := ccli.NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)
		_, handled, got := executeCCLIDispatch(
			[]string{"save", "memo", "use WAL"},
			ccli.RewriteAuto, false, streams,
			func(_ []string, _ *ccli.Production, _ *ccli.Streams) int {
				return tc.hostExit
			},
		)
		if !handled || got != tc.wantExit {
			t.Errorf("standard prod hostExit=%d: got (handled=%v, exit=%d), want (true, %d)", tc.hostExit, handled, got, tc.wantExit)
		}
	}

	// 2. Production with preserve_exit: 1 ("run gate" -> sila_gate_run): host 1 is preserved as 1!
	var out, errBuf bytes.Buffer
	streams := ccli.NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)
	_, handled, got := executeCCLIDispatch(
		[]string{"run", "gate"},
		ccli.RewriteAuto, false, streams,
		func(_ []string, _ *ccli.Production, _ *ccli.Streams) int {
			return 1
		},
	)
	if !handled || got != 1 {
		t.Errorf("preserve_exit:1 prod hostExit=1: got (handled=%v, exit=%d), want (true, 1)", handled, got)
	}

	// 3. Panic recovery at CCLI dispatch boundary exits 5 without stack trace unless --verbose
	out.Reset()
	errBuf.Reset()
	_, handled, got = executeCCLIDispatch(
		[]string{"save", "memo", "use WAL"},
		ccli.RewriteAuto, false, streams,
		func(_ []string, _ *ccli.Production, _ *ccli.Streams) int {
			panic("unexpected nil pointer in host handler")
		},
	)
	if !handled || got != 5 {
		t.Fatalf("expected recovered panic to exit 5, got handled=%v code=%d", handled, got)
	}
	if !strings.Contains(errBuf.String(), "CCLI internal error: unexpected nil pointer in host handler") {
		t.Errorf("expected short error on stderr, got %q", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "goroutine ") {
		t.Errorf("expected no stack trace without --verbose, got:\n%s", errBuf.String())
	}

	// With --verbose, stack trace IS included
	out.Reset()
	errBuf.Reset()
	_, handled, got = executeCCLIDispatch(
		[]string{"save", "memo", "use WAL", "--verbose"},
		ccli.RewriteAuto, false, streams,
		func(_ []string, _ *ccli.Production, _ *ccli.Streams) int {
			panic("boom with verbose")
		},
	)
	if !handled || got != 5 || !strings.Contains(errBuf.String(), "goroutine ") {
		t.Errorf("expected exit 5 with stack trace under --verbose, got code=%d stderr=%s", got, errBuf.String())
	}
}

func TestCCLIMutationSentinelsOnRefusalAndDryRun(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sentinel.db")
	t.Setenv("SILA_DB", dbPath)

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var out, errBuf bytes.Buffer
	nonTTY := ccli.NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)

	// 1. Dry-run on Tier 2 mutation
	_, _, code := executeCCLIDispatch([]string{"save", "memo", "use WAL", "--dry-run"}, ccli.RewriteAuto, false, nonTTY, defaultCCLIHostRunner)
	if code != 0 {
		t.Fatalf("expected dry-run exit 0, got %d", code)
	}
	// 2. Non-TTY refusal on Tier 3 destructive
	_, _, code = executeCCLIDispatch([]string{"drop", "goal", "my-goal"}, ccli.RewriteAuto, false, nonTTY, defaultCCLIHostRunner)
	if code != 3 {
		t.Fatalf("expected Tier 3 non-TTY refusal exit 3, got %d", code)
	}
	// 3. Secret refusal
	_, _, code = executeCCLIDispatch([]string{"save", "memo", "ghp_1234567890abcdef"}, ccli.RewriteAuto, false, nonTTY, defaultCCLIHostRunner)
	if code != 3 {
		t.Fatalf("expected secret refusal exit 3, got %d", code)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected zero filesystem/DB mutations on dry-run and refusals, found %d entries", len(entries))
	}
}
