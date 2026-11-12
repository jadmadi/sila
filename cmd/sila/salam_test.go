package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/store"
)

func TestSalamBriefsAnAgent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dbPath := filepath.Join(tmp, "salam.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	indep := true
	if _, err := goals.CreateGoal(goals.CreateGoalParams{
		RootDir:     repo,
		Slug:        "salam-target",
		Title:       "Salam Target",
		Description: "A ready goal",
		Independent: &indep,
	}); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, true)
	out := captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, true, []string{"--full"})
	})

	var brief struct {
		RepoRoot  string `json:"repo_root"`
		Project   string `json:"project"`
		Connected bool   `json:"connected"`
		Wiring    struct {
			State        string   `json:"state"`
			NextCommand  string   `json:"next_command"`
			MissingPaths []string `json:"missing_paths"`
		} `json:"wiring"`
		HealAutomatic int      `json:"heal_automatic"`
		Loop          []string `json:"loop"`
		Ready         []struct {
			Slug string `json:"slug"`
		} `json:"ready_goals"`
		Assistance struct {
			Status string `json:"status"`
		} `json:"assistance"`
	}
	if err := json.Unmarshal([]byte(out), &brief); err != nil {
		t.Fatalf("salam output is not JSON: %q: %v", out, err)
	}
	if brief.RepoRoot != repo {
		t.Fatalf("repo_root = %q, want %q", brief.RepoRoot, repo)
	}
	if len(brief.Loop) != 7 {
		t.Fatalf("loop = %d steps, want 7", len(brief.Loop))
	}
	if brief.HealAutomatic == 0 {
		t.Fatal("a fresh repo must report pending heal fixes")
	}
	if brief.Connected {
		t.Fatal("a fresh repo must not be reported as connected")
	}
	if brief.Wiring.State != "unwired" {
		t.Fatalf("wiring state = %q, want unwired", brief.Wiring.State)
	}
	if brief.Wiring.NextCommand != "sila heal" {
		t.Fatalf("wiring next_command = %q, want sila heal", brief.Wiring.NextCommand)
	}
	if len(brief.Ready) != 1 || brief.Ready[0].Slug != "salam-target" {
		t.Fatalf("ready goals = %+v, want salam-target", brief.Ready)
	}
	if brief.Assistance.Status != "unconfigured" {
		t.Fatalf("assistance status = %q, want unconfigured", brief.Assistance.Status)
	}
}

func TestSalamCompactJSONAndBudget(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("COLUMNS", "80")
	dbPath := filepath.Join(tmp, "salam.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, true)
	compact := captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, true, nil)
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(compact), &payload); err != nil {
		t.Fatalf("compact JSON: %v\n%s", err, compact)
	}
	if _, hasLoop := payload["loop"]; hasLoop {
		t.Fatal("compact JSON must not include the full loop")
	}
	if payload["next"] == nil {
		t.Fatal("compact JSON must include the next command")
	}
	if len(compact) > 4096 {
		t.Fatalf("compact JSON = %d bytes, want <= 4096", len(compact))
	}

	full := captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, true, []string{"--full"})
	})
	var fullPayload map[string]any
	if err := json.Unmarshal([]byte(full), &fullPayload); err != nil {
		t.Fatalf("full JSON: %v", err)
	}
	if loop, ok := fullPayload["loop"].([]any); !ok || len(loop) != 7 {
		t.Fatalf("full JSON loop = %#v, want 7 steps", fullPayload["loop"])
	}

	text := captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, false, nil)
	})
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > 12 {
		t.Fatalf("default salam = %d lines, want <=12:\n%s", len(lines), text)
	}
	for _, line := range lines {
		measured := strings.ReplaceAll(line, repo, "<repo>")
		if render.VisibleWidth(measured) > 80 {
			t.Fatalf("line exceeds 80 columns: %q", line)
		}
	}
	if strings.Contains(text, "Working loop:") {
		t.Fatal("default salam must not print the full loop")
	}
}

func TestStripPrimeContract(t *testing.T) {
	md := "### Memory\n\n**Working Contract (9-Verb Memory Loop)**:\n1. do things\n_Note: If `sila` CLI is not in `$PATH` (subshell exit 127), invoke via `~/.local/bin/sila` or call Sila MCP tools directly._\n\n**Active Session Handoff**\nstate"
	got := stripPrimeContract(md)
	if strings.Contains(got, "Working Contract") || strings.Contains(got, "Capability Triggers") {
		t.Fatalf("contract not removed:\n%s", got)
	}
	if !strings.Contains(got, "Active Session Handoff") {
		t.Fatalf("state removed:\n%s", got)
	}
}

func TestSalamPrintTextOutput(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dbPath := filepath.Join(tmp, "salam.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		st.Close()
		t.Fatal(err)
	}
	if _, err := st.EnsureProject(repo); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, false)
	out := captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, false, []string{"--full"})
	})

	if !strings.Contains(out, "sila · salam") {
		t.Errorf("expected header 'sila · salam' in output: %s", out)
	}
	if !strings.Contains(out, "Live state:") {
		t.Errorf("expected 'Live state:' in output: %s", out)
	}
	if !strings.Contains(out, "Assistance (JEV):") {
		t.Errorf("expected 'Assistance (JEV):' in output: %s", out)
	}
}

func TestSalamLoopCommandsAreDiscoverable(t *testing.T) {
	known := map[string]bool{}
	for _, group := range commandGroups {
		for _, cmd := range group.Commands {
			known[cmd[0]] = true
		}
	}
	re := regexp.MustCompile("`sila ([a-z0-9-]+)")
	for _, line := range salam.SalamLoop {
		for _, match := range re.FindAllStringSubmatch(line, -1) {
			if !known[match[1]] {
				t.Errorf("loop names undiscoverable command %q: %s", match[1], line)
			}
		}
	}
}

func TestPrintSalamWiringCopy(t *testing.T) {
	cases := []struct {
		name      string
		wiring    salam.SalamWiring
		connected bool
		want      string
		forbidden string
	}{
		{name: "wired", wiring: salam.SalamWiring{State: "wired"}, connected: true, want: "sila-connected repository"},
		{name: "unwired", wiring: salam.SalamWiring{State: "unwired", NextCommand: "sila heal"}, want: "no sila wiring yet", forbidden: "sila-connected repository"},
		{name: "unknown", wiring: salam.SalamWiring{State: "unknown"}, want: "could not verify", forbidden: "sila-connected repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ui := render.New(os.Stdout, true)
			out := captureStdout(t, func() {
				printSalam(ui, &salam.SalamBrief{
					Project:   "demo",
					RepoRoot:  "/tmp/demo",
					Wiring:    tc.wiring,
					Connected: tc.connected,
					Loop:      []string{"step one"},
				})
			})
			if !strings.Contains(out, tc.want) {
				t.Fatalf("output missing %q: %s", tc.want, out)
			}
			if tc.forbidden != "" && strings.Contains(out, tc.forbidden) {
				t.Fatalf("output must not contain %q: %s", tc.forbidden, out)
			}
		})
	}
}

func TestPromptAssistanceHandshakeAccept(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "my-repo")
	initSalamTestRepository(t, repo)

	ui := render.New(os.Stdout, false)
	var out bytes.Buffer
	in := strings.NewReader("y\n")

	ok := promptAssistanceHandshake(ui, repo, "my-repo", HandshakeOptions{
		In:          in,
		Out:         &out,
		ForcePrompt: true,
		Timeout:     2 * time.Second,
	})

	if !ok {
		t.Fatal("expected promptAssistanceHandshake to return true on 'y'")
	}
	if !strings.Contains(out.String(), "JEV semantic diff sentry enabled") {
		t.Errorf("expected confirmation output, got: %s", out.String())
	}

	// Verify .sila/config.json exists and has assist_enabled = true
	pCfgPath := filepath.Join(repo, ".sila", "config.json")
	data, err := os.ReadFile(pCfgPath)
	if err != nil {
		t.Fatalf("expected project config to exist: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("malformed project config: %v", err)
	}
	if !cfg.AssistEnabled {
		t.Error("expected assist_enabled = true")
	}
	if len(cfg.AssistScopes) != 2 || cfg.AssistScopes[0] != "diff" || cfg.AssistScopes[1] != "gate" {
		t.Errorf("unexpected scopes: %+v", cfg.AssistScopes)
	}
}

func TestPromptAssistanceHandshakeDecline(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "my-repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}

	ui := render.New(os.Stdout, false)
	var out bytes.Buffer
	in := strings.NewReader("n\n")

	ok := promptAssistanceHandshake(ui, repo, "my-repo", HandshakeOptions{
		In:          in,
		Out:         &out,
		ForcePrompt: true,
		Timeout:     2 * time.Second,
	})

	if ok {
		t.Fatal("expected promptAssistanceHandshake to return false on 'n'")
	}
	if !strings.Contains(out.String(), "Skipped") {
		t.Errorf("expected skipped message, got: %s", out.String())
	}

	pCfgPath := filepath.Join(repo, ".sila", "config.json")
	if _, err := os.Stat(pCfgPath); err == nil {
		t.Fatal(".sila/config.json should not have been created on decline")
	}
}

func TestSalamAutoEnableFlag(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	dbPath := filepath.Join(tmp, "salam.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(tmp, "repo")
	initSalamTestRepository(t, repo)
	if _, err := st.EnsureProject(repo); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(os.Stdout, false)
	captureStdout(t, func() {
		runSalam(ui, dbPath, repo, false, false, []string{"--assist"})
	})

	pCfgPath := filepath.Join(repo, ".sila", "config.json")
	data, err := os.ReadFile(pCfgPath)
	if err != nil {
		t.Fatalf("expected project config to exist with --assist flag: %v", err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("malformed project config: %v", err)
	}
	if !cfg.AssistEnabled {
		t.Error("expected assist_enabled = true from --assist flag")
	}
}

func initSalamTestRepository(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("initialize isolated repository: %v: %s", err, out)
	}
	ancestor := filepath.Join(filepath.Dir(dir), ".sila", "config.json")
	if err := os.MkdirAll(filepath.Dir(ancestor), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"assist_enabled":true,"assist_scopes":["recap"]}`)
	if err := os.WriteFile(ancestor, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(ancestor)
		if err != nil || !bytes.Equal(data, original) {
			t.Errorf("ancestor configuration changed: %v", err)
		}
	})
}
