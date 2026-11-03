package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestOnboardCheckGlobalCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "onboard_cli.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runOnboardCheck(ui, dbPath, "", false, true, true)
	})

	var env struct {
		Schema string `json:"schema"`
		OK     bool   `json:"ok"`
		Data   struct {
			Global   bool `json:"global"`
			Count    int  `json:"count"`
			Projects []struct {
				Project string `json:"project"`
				Report  struct {
					RepoRoot string `json:"repo_root"`
					Items    []struct {
						Name   string `json:"name"`
						Status string `json:"status"`
						Fix    string `json:"fix"`
					} `json:"items"`
				} `json:"report"`
			} `json:"projects"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("onboard check output is not JSON: %q: %v", out, err)
	}
	if env.Schema != "sila/json/v1" || !env.OK {
		t.Fatalf("envelope = %+v, want valid success envelope", env)
	}
	res := env.Data
	if !res.Global || res.Count != 1 || len(res.Projects) != 1 {
		t.Fatalf("payload = %+v, want one global project report", res)
	}
	rep := res.Projects[0].Report
	if rep.RepoRoot != repo {
		t.Fatalf("repo root = %q, want %q", rep.RepoRoot, repo)
	}
	names := map[string]bool{}
	for _, item := range rep.Items {
		names[item.Name] = true
	}
	for _, want := range []string{"Registered project", "Canonical project id", "Goals", "Legacy inventory", "Assistance consent"} {
		if !names[want] {
			t.Fatalf("check report missing item %q: %+v", want, rep.Items)
		}
	}
}

func TestOnboardDeferCLI(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "defer_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject(tmp); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origDir) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile("TODO.md", []byte("- [ ] test task\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runOnboard(ui, dbPath, "test-proj", false, "", false, false, false, false, false, true, "Migrated to backlog", "test-agent", []string{"defer", "TODO.md"})
	})

	var rec onboard.Deferral
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("failed to unmarshal JSON deferral output %q: %v", out, err)
	}
	if rec.Surface != "TODO.md" || rec.Reason != "Migrated to backlog" {
		t.Fatalf("unexpected deferral record: %+v", rec)
	}

	// Verify .sila/deferrals.json exists
	defs, err := onboard.LoadDeferrals(tmp)
	if err != nil {
		t.Fatalf("LoadDeferrals failed: %v", err)
	}
	if len(defs) != 1 || defs[0].Surface != "TODO.md" {
		t.Fatalf("expected 1 deferral in file, got %+v", defs)
	}
}

func TestOnboardExitCodeContract(t *testing.T) {
	cases := []struct {
		ready, advisory bool
		want            int
	}{
		{true, false, 0},
		{true, true, 0},
		{false, false, 1},
		{false, true, 0},
	}
	for _, tc := range cases {
		if got := onboardExitCode(tc.ready, tc.advisory); got != tc.want {
			t.Errorf("onboardExitCode(ready=%v, advisory=%v) = %d, want %d", tc.ready, tc.advisory, got, tc.want)
		}
	}
}

func TestOnboardHeaderFallbackAndLegend(t *testing.T) {
	prev := outModes
	outModes = outputModes{}
	t.Cleanup(func() { outModes = prev })

	rep := &onboard.CheckReport{
		RepoRoot: "/tmp/acme-repo",
		Ready:    false,
		Fail:     1,
		Items: []onboard.CheckItem{
			{Name: "Registered project", Status: "fail", Critical: true, Fix: "sila heal"},
		},
	}
	out := captureStdout(t, func() { printOnboardCheck(render.New(os.Stdout, true), rep) })
	if !strings.Contains(out, "sila · onboard check · acme-repo") {
		t.Fatalf("header did not fall back to the repo basename:\n%s", out)
	}
	if !strings.Contains(out, "fail *") {
		t.Fatalf("critical row lost its marker:\n%s", out)
	}
	if !strings.Contains(out, "* critical - must be resolved before onboarding is complete") {
		t.Fatalf("critical legend missing:\n%s", out)
	}

	ready := &onboard.CheckReport{
		RepoRoot: "/tmp/acme-repo",
		Project:  "acme",
		Ready:    true,
		Pass:     1,
		Items:    []onboard.CheckItem{{Name: "Registered project", Status: "pass"}},
	}
	out = captureStdout(t, func() { printOnboardCheck(render.New(os.Stdout, true), ready) })
	if strings.Contains(out, "* critical") {
		t.Fatalf("legend shown without a critical item:\n%s", out)
	}
	if !strings.Contains(out, "sila · onboard check · acme") {
		t.Fatalf("explicit project scope missing:\n%s", out)
	}
}

func TestOnboardJSONEnvelope(t *testing.T) {
	rep := &onboard.CheckReport{
		RepoRoot: "/tmp/acme-repo",
		Project:  "acme",
		Ready:    false,
		Fail:     1,
		Items:    []onboard.CheckItem{{Name: "Registered project", Status: "fail", Critical: true}},
	}
	out := captureStdout(t, func() { emitOnboardJSON(onboardJSONData(rep, false), false, false) })
	var env struct {
		Schema string `json:"schema"`
		OK     bool   `json:"ok"`
		Data   struct {
			Ready    bool `json:"ready"`
			Advisory bool `json:"advisory"`
			Items    []struct {
				Critical bool `json:"critical"`
			} `json:"items"`
		} `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if env.Schema != "sila/json/v1" || env.OK || env.Error == nil || env.Error.Code != "ONBOARDING_INCOMPLETE" {
		t.Fatalf("bad error envelope: %+v", env)
	}
	if env.Data.Ready || len(env.Data.Items) != 1 || !env.Data.Items[0].Critical {
		t.Fatalf("report data lost: %+v", env.Data)
	}

	out = captureStdout(t, func() { emitOnboardJSON(onboardJSONData(rep, true), false, true) })
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid advisory JSON: %v", err)
	}
	if !env.Data.Advisory {
		t.Fatal("advisory flag missing from data")
	}
}

func TestOnboardCheckSubprocessExit(t *testing.T) {
	if probe := os.Getenv("SILA_ONBOARD_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		os.Exit(0)
	}
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "onboard.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	repo := filepath.Join(tmp, "unregistered-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		extra    []string
		wantCode int
		wantJSON bool
	}{
		{"human-not-ready", nil, 1, false},
		{"human-advisory", []string{"--advisory"}, 0, false},
		{"json-not-ready", []string{"--json"}, 1, true},
		{"json-advisory", []string{"--json", "--advisory"}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"sila", "onboard", "check", "--db=" + dbPath}
			args = append(args, tc.extra...)
			cmd := exec.Command(os.Args[0], "-test.run=TestOnboardCheckSubprocessExit")
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "SILA_ONBOARD_PROBE="+strings.Join(args, "\x1f"))
			var stdout, stderr strings.Builder
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("run failed: %v", err)
				}
				code = exitErr.ExitCode()
			}
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, tc.wantCode, stdout.String(), stderr.String())
			}
			if tc.wantJSON {
				var env struct {
					Schema string `json:"schema"`
					OK     bool   `json:"ok"`
					Error  *struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(stdout.String()), &env); err != nil {
					t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
				}
				if env.Schema != "sila/json/v1" || env.OK || env.Error == nil || env.Error.Code != "ONBOARDING_INCOMPLETE" {
					t.Fatalf("bad envelope: %+v", env)
				}
			} else if !strings.Contains(stdout.String(), "Onboarding incomplete") {
				t.Fatalf("human output missing failure summary: %q", stdout.String())
			}
		})
	}
}

func TestE2E_SevenStepOnboardingLoop(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	dbPath := filepath.Join(tmp, "e2e_onboard.db")

	// 1. Setup parent repo with "*" in .gitignore and uninitialized child "telemetry"
	parent := filepath.Join(tmp, "lab")
	child := filepath.Join(parent, "telemetry")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	runGit(parent, "init", "-b", "main")
	runGit(parent, "config", "user.name", "Test")
	runGit(parent, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("*\n!.gitignore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(parent, "add", ".gitignore")
	runGit(parent, "commit", "-m", "chore: init parent")

	runSila := func(dir string, args ...string) (int, string, string) {
		t.Helper()
		fullArgs := append(append([]string{"sila"}, args...), "--db="+dbPath)
		cmd := exec.Command(os.Args[0], "-test.run=TestOnboardCheckSubprocessExit")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "SILA_ONBOARD_PROBE="+strings.Join(fullArgs, "\x1f"))
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				t.Fatalf("runSila %v failed: %v", args, err)
			}
		}
		return code, stdout.String(), stderr.String()
	}

	// 2. Running "sila heal" in uninitialized gitignored subdir must block
	code, _, stderr := runSila(child, "heal", "--no-color")
	if code == 0 || !strings.Contains(stderr, "gitignored by parent repository") {
		t.Fatalf("expected sila heal to block on gitignored subdir; exit=%d stderr=%q", code, stderr)
	}

	// 3. Initialize child repo, run "sila heal", and claim "sila-onboarding"
	runGit(child, "init", "-b", "main")
	runGit(child, "config", "user.name", "Test")
	runGit(child, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(child, "README.md"), []byte("# Telemetry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(child, "add", "README.md")
	runGit(child, "commit", "-m", "chore: initial commit")

	code, stdout, stderr := runSila(child, "heal", "--no-color")
	if code != 0 {
		t.Fatalf("sila heal in initialized child failed: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	runGit(child, "add", "-A")
	runGit(child, "commit", "-m", "chore: provision sila")

	code, stdout, stderr = runSila(child, "goals", "claim", "sila-onboarding", "--agent=@tester")
	if code != 0 {
		t.Fatalf("claim sila-onboarding failed: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	// 4. Verify .sila/charters/ does not dirty git status
	if status := runGit(child, "status", "--porcelain"); strings.Contains(status, ".sila/charters") || strings.Contains(status, ".sila/locks") {
		t.Fatalf("ephemeral charter/lock leaked into git status:\n%s", status)
	}

	// 5. Verify default sila-onboarding charter allows glob paths in both charter-sentry and rule-of-five
	charter, err := goals.LoadCharter(child, "sila-onboarding")
	if err != nil {
		t.Fatalf("LoadCharter failed: %v", err)
	}
	onboardingFiles := []string{"AGENTS.md", ".gitignore", "goals/sila-onboarding/plan.md", "tests/test_telemetry.py"}
	if err := goals.ValidateStagedFilesAgainstCharter(charter, onboardingFiles); err != nil {
		t.Fatalf("charter-sentry rejected onboarding files: %v", err)
	}
	blast := assess.AuditBlastRadius(child, "sila-onboarding", onboardingFiles)
	if !blast.Passed {
		t.Fatalf("rule-of-five AuditBlastRadius rejected onboarding glob paths: %+v", blast)
	}

	// 6. Verify python3 subdir runbook detection from AGENTS.md
	if err := os.MkdirAll(filepath.Join(child, "opencode-telemetry"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "opencode-telemetry", "audit.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	agentsContent := "# AGENTS.md\n\n## Runbook\n- `cd opencode-telemetry && python3 audit.py`\n"
	if err := os.WriteFile(filepath.Join(child, "AGENTS.md"), []byte(agentsContent), 0o644); err != nil {
		t.Fatal(err)
	}
	rb := dream.DetectRunbook(child)
	foundPy := false
	if rb != nil {
		for _, e := range rb.Entries {
			if e.Command == "python3 opencode-telemetry/audit.py" {
				foundPy = true
			}
		}
	}
	if !foundPy {
		t.Fatalf("expected normalized python3 test command, got %+v", rb)
	}
}

func TestHealScaffoldedGoalRequiresMemoryAndLegacyCertifyStdout(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "legacy_cert.db")
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	runSila := func(dir string, args ...string) (int, string, string) {
		t.Helper()
		fullArgs := append(append([]string{"sila"}, args...), "--db="+dbPath)
		cmd := exec.Command(os.Args[0], "-test.run=TestOnboardCheckSubprocessExit")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "SILA_ONBOARD_PROBE="+strings.Join(fullArgs, "\x1f"))
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				t.Fatalf("runSila %v failed: %v", args, err)
			}
		}
		return code, stdout.String(), stderr.String()
	}
	runGit(repo, "init", "-b", "main")
	runGit(repo, "config", "user.name", "Test")
	runGit(repo, "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(repo, "add", "README.md")
	runGit(repo, "commit", "-m", "chore: init")

	code, stdout, stderr := runSila(repo, "heal", "--no-color")
	if code != 0 {
		t.Fatalf("heal failed: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	goalMd, err := os.ReadFile(filepath.Join(repo, "goals", "sila-onboarding", "goal.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goalMd), "- **Memory Protocol**: required") {
		t.Fatalf("expected scaffolded sila-onboarding goal.md to carry Memory Protocol: required, got:\n%s", string(goalMd))
	}

	// Also verify legacy goal certification prints the legacy message on stdout and sila goals --json matches certification.json.
	legacyDir := filepath.Join(repo, "goals", "legacy-demo")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "goal.md"), []byte("# Goal: Legacy Demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "plan.md"), []byte("# Plan\n\n- [x] step\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "facts.md"), []byte("# Facts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evPath := filepath.Join(legacyDir, "evidence.json")
	if err := os.WriteFile(evPath, []byte(`{"facts":[{"id":"f1","fact":"ok","passed":true,"verified":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runSila(repo, "goals", "certify", "legacy-demo", "--evidence="+evPath, "--no-color")
	if code != 0 {
		t.Fatalf("certify legacy-demo failed: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "memory protocol not required for legacy goals") {
		t.Fatalf("expected legacy memory notice on stdout, got: %q", stdout)
	}

	code, jsonStdout, stderr := runSila(repo, "goals", "--json")
	if code != 0 {
		t.Fatalf("goals --json failed: exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(jsonStdout, `"memory_status": "legacy_skipped"`) {
		t.Fatalf("expected sila goals --json to expose memory_status: legacy_skipped, got:\n%s", jsonStdout)
	}
}
