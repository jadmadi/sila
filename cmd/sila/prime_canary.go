package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

type canaryCaseResult struct {
	Harness      string `json:"harness"`
	SessionID    string `json:"session_id"`
	OK           bool   `json:"ok"`
	ExitCode     int    `json:"exit_code"`
	MarkerStatus string `json:"marker_status,omitempty"`
	PrimeStatus  string `json:"prime_status,omitempty"`
	ChecksPrime  bool   `json:"checks_prime"`
	BriefBytes   int    `json:"brief_bytes"`
	LedgerEvents int    `json:"ledger_events"`
	Error        string `json:"error,omitempty"`
}

type canaryReport struct {
	OK               bool               `json:"ok"`
	TempRoot         string             `json:"temp_root,omitempty"`
	Cases            []canaryCaseResult `json:"cases"`
	FailureInjection *canaryCaseResult  `json:"failure_injection,omitempty"`
}

var canaryHarnesses = []string{"claude-code", "cursor", "command-code", "zcode", "antigravity"}

func canaryEnvelope(harness, repo, sessionID string) string {
	if harness == "antigravity" {
		return fmt.Sprintf(`{"event":"PreInvocation","invocationNum":0,"conversationId":%q,"workspacePaths":[%q]}`, sessionID, repo)
	}
	return fmt.Sprintf(`{"hook_event_name":"SessionStart","session_id":%q,"cwd":%q,"source":"canary"}`, sessionID, repo)
}

func runPrimeCanary(ui render.UI, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("prime canary", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	binary := fs.String("binary", "", "sila binary to exercise (default: self)")
	harness := fs.String("harness", "", "run one harness only")
	keep := fs.Bool("keep", false, "keep the temp root and print its path")
	timeout := fs.Duration("timeout", 60*time.Second, "per-case timeout")
	strict := fs.Bool("strict", false, "assert the strict failure exit")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, renderFlagParseError(fs, "prime canary", err))
		return 2
	}
	bin := *binary
	if bin == "" {
		var err error
		bin, err = os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila prime canary: resolve binary: %v\n", err)
			return 1
		}
	}

	cases := canaryHarnesses
	if *harness != "" {
		cases = []string{*harness}
	}

	root, err := os.MkdirTemp("", "sila-prime-canary-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila prime canary: temp root: %v\n", err)
		return 1
	}
	if !*keep {
		defer os.RemoveAll(root)
	}
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	for _, dir := range []string{home, repo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "sila prime canary: mkdir: %v\n", err)
			return 1
		}
	}
	dbPath := filepath.Join(home, ".local", "share", "sila", "sila.db")
	env := canaryEnv(home, dbPath)
	initCanaryRepo(repo, env)

	report := canaryReport{OK: true, Cases: []canaryCaseResult{}}
	if *keep {
		report.TempRoot = root
	}
	for _, h := range cases {
		res := runCanaryCase(bin, repo, dbPath, env, h, *timeout, false)
		if !res.OK {
			report.OK = false
		}
		report.Cases = append(report.Cases, res)
	}
	failure := runCanaryCase(bin, repo, dbPath, env, "claude-code", *timeout, true)
	if !failure.OK {
		report.OK = false
	}
	if *strict {
		failure.ExitCode = runCanaryStrict(bin, repo, env, *timeout)
		if failure.ExitCode != 3 {
			failure.OK = false
			report.OK = false
		}
	}
	report.FailureInjection = &failure

	if jsonOut {
		emitJSON(report)
	} else {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · prime canary")))
		for _, c := range append(report.Cases, *report.FailureInjection) {
			icon := ui.Green("✔")
			if !c.OK {
				icon = ui.Red("✖")
			}
			fmt.Fprintf(os.Stdout, "  %s %-14s exit=%d prime=%s bytes=%d ledger=%d\n",
				icon, c.Harness, c.ExitCode, c.PrimeStatus, c.BriefBytes, c.LedgerEvents)
			if c.Error != "" {
				fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(c.Error))
			}
		}
		if report.TempRoot != "" {
			fmt.Fprintf(os.Stdout, "  %s\n", ui.Dim("temp root: "+report.TempRoot))
		}
	}
	if !report.OK {
		return 1
	}
	return 0
}

func canaryEnv(home, dbPath string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
		"SILA_DB=" + dbPath,
		"SILA_NO_UPDATE_CHECK=1",
		"SILA_AGENT=canary",
	}
}

func initCanaryRepo(repo string, env []string) {
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = env
		_ = cmd.Run()
	}
	run("init", "-b", "main")
	run("config", "user.name", "Canary Bot")
	run("config", "user.email", "canary@example.com")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Canary\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "canary: init")
}

func runCanaryCase(bin, repo, dbPath string, baseEnv []string, harness string, timeout time.Duration, failInjected bool) canaryCaseResult {
	sessionID := fmt.Sprintf("canary-%s-%d", harness, time.Now().UnixNano())
	if failInjected {
		sessionID = fmt.Sprintf("canary-failure-%d", time.Now().UnixNano())
	}
	env := append([]string{}, baseEnv...)
	if failInjected {
		env = append(env, "SILA_PRIME_FAIL=1")
	}

	res := canaryCaseResult{Harness: harness, SessionID: sessionID}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "hook", "session-start")
	cmd.Dir = repo
	cmd.Env = env
	cmd.Stdin = strings.NewReader(canaryEnvelope(harness, repo, sessionID))
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		res.Error = "timeout"
		return res
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
		}
		res.Error = err.Error()
		return res
	}
	trimmed := strings.TrimSpace(string(out))
	if !failInjected {
		if trimmed == "" {
			res.Error = "empty stdout"
			return res
		}
		if strings.HasPrefix(trimmed, "{") && !json.Valid(out) {
			res.Error = "stdout looks like JSON but does not parse"
			return res
		}
	}

	marker, err := loaders.ReadPreflightMarker(loaders.PreflightMarkerPath(repo, sessionID))
	if err != nil {
		res.Error = "marker: " + err.Error()
		return res
	}
	res.MarkerStatus = marker.Status
	res.PrimeStatus = string(marker.PrimeStatus)
	res.ChecksPrime = marker.Checks["prime"]
	res.BriefBytes = marker.BriefBytes
	if failInjected {
		res.OK = marker.Status == "failed" && marker.PrimeStatus == loaders.PrimeStatusFailed && !marker.Checks["prime"]
		if res.OK {
			res.Error = ""
		}
		return res
	}
	if marker.Status != "ok" || marker.PrimeStatus != loaders.PrimeStatusOK || !marker.Checks["prime"] || marker.BriefBytes <= 0 {
		res.Error = fmt.Sprintf("marker status=%s prime=%s checks.prime=%t bytes=%d", marker.Status, marker.PrimeStatus, marker.Checks["prime"], marker.BriefBytes)
		return res
	}

	if st, openErr := store.OpenRO(dbPath); openErr == nil {
		events, _ := st.ListPrimeEvents(store.PrimeEventFilter{SessionID: sessionID, Source: string(loaders.PrimeSourceSessionStart), Limit: 10})
		res.LedgerEvents = len(events)
		_ = st.Close()
	}
	if res.LedgerEvents == 0 {
		res.Error = "prime ledger row missing"
		return res
	}
	res.OK = true
	return res
}

func runCanaryStrict(bin, repo string, baseEnv []string, timeout time.Duration) int {
	sessionID := fmt.Sprintf("canary-strict-%d", time.Now().UnixNano())
	env := append(append([]string{}, baseEnv...), "SILA_PRIME_FAIL=1", "SILA_HOOK_STRICT=1")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "hook", "session-start")
	cmd.Dir = repo
	cmd.Env = env
	cmd.Stdin = strings.NewReader(canaryEnvelope("claude-code", repo, sessionID))
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}

// gatePrimeCanary runs a single-harness canary as a full-gate sentry.
func gatePrimeCanary(cwd string) gateCheck {
	bin, err := exec.LookPath("sila")
	if err != nil {
		return gateCheck{Name: "prime-canary", Status: "skip", Detail: "sila binary not on PATH"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "prime", "canary", "--harness=claude-code", "--json", "--timeout=60s")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return gateCheck{Name: "prime-canary", Status: "fail", Detail: fmt.Sprintf("canary failed: %v", err)}
	}
	var rep canaryReport
	if err := json.Unmarshal(out, &rep); err != nil || !rep.OK {
		return gateCheck{Name: "prime-canary", Status: "fail", Detail: "canary report not OK"}
	}
	return gateCheck{Name: "prime-canary", Status: "pass", Detail: "hermetic session-start canary passed"}
}
