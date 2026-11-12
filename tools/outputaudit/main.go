package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SurfaceSpec defines one CLI surface capture in the output audit harness.
type SurfaceSpec struct {
	Group        string        `json:"group"`
	Label        string        `json:"label"`
	Command      string        `json:"command"`
	Argv         []string      `json:"argv"`
	Stdin        string        `json:"stdin,omitempty"`
	Mode         string        `json:"mode"` // human|json|json-envelope|toon|porcelain|help
	Mutating     bool          `json:"mutating"`
	ExpectEmpty  bool          `json:"expect_empty,omitempty"`
	ExpectedExit int           `json:"expected_exit"`
	Timeout      time.Duration `json:"timeout"`
	Golden       string        `json:"golden,omitempty"` // relative under testdata/output/surfaces/
}

// CaptureRecord holds the normalized result of executing one SurfaceSpec.
type CaptureRecord struct {
	Group        string `json:"group"`
	Label        string `json:"label"`
	Command      string `json:"command"`
	Argv         string `json:"argv"`
	Mode         string `json:"mode"`
	ExpectedExit int    `json:"expected_exit"`
	ExitCode     int    `json:"exit_code"`
	StdoutBytes  int    `json:"stdout_bytes"`
	StderrBytes  int    `json:"stderr_bytes"`
	GoldenFile   string `json:"golden_file,omitempty"`
	FullFile     string `json:"full_file"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
}

// CoverageSummary summarizes command and mode coverage across the manifest.
type CoverageSummary struct {
	CanonicalCommands []string       `json:"canonical_commands"`
	CoveredCommands   []string       `json:"covered_commands"`
	MissingCommands   []string       `json:"missing_commands"`
	ModesCovered      map[string]int `json:"modes_covered"`
	TotalSurfaces     int            `json:"total_surfaces"`
	GoldenSurfaces    int            `json:"golden_surfaces"`
}

// AuditManifest is the deterministic JSON manifest written by the harness.
type AuditManifest struct {
	Schema   string          `json:"schema"`
	Coverage CoverageSummary `json:"coverage"`
	Surfaces []CaptureRecord `json:"surfaces"`
}

// Denylisted network or destructive invocations that must never run without --help or --dry-run.
var destructiveOrNetworkDenylist = map[string]bool{
	"ui":      true,
	"demo":    true,
	"live":    true,
	"watch":   true,
	"mcp":     true,
	"upgrade": true,
	"clean":   true,
	"vacuum":  true,
	"crew":    true,
	"cloud":   true,
	"gateway": true,
	"sync":    true,
	"ingest":  true,
	"dream":   true,
	"hire":    true,
	"audit":   true,
}

func main() {
	outDir := flag.String("out", "", "Output directory for report, manifest.json, and full captures")
	goldenRoot := flag.String("golden-dir", "testdata/output", "Directory containing manifest.json and surfaces/ goldens")
	update := flag.Bool("update", false, "Write/update golden files under --golden-dir")
	groupFilter := flag.String("group", "", "Filter surfaces by group")
	modeFilter := flag.String("mode", "", "Filter surfaces by mode")
	jsonOut := flag.Bool("json", false, "Emit JSON summary on stdout")
	fullFlag := flag.Bool("full", true, "Always write full stdout/stderr sidecars")
	binFlag := flag.String("bin", "", "Path to compiled sila binary (built automatically if empty)")
	flag.Parse()

	_ = fullFlag
	repoRoot, err := resolveRepoRoot()
	if err != nil {
		fatalf("resolve repo root: %v", err)
	}

	silaBin := *binFlag
	var cleanupBin func()
	if silaBin == "" {
		silaBin, cleanupBin, err = ensureSilaBinary(repoRoot)
		if err != nil {
			fatalf("build sila binary: %v", err)
		}
		if cleanupBin != nil {
			defer cleanupBin()
		}
	}

	manifest, err := RunHarness(HarnessOptions{
		RepoRoot:    repoRoot,
		SilaBin:     silaBin,
		OutDir:      *outDir,
		GoldenRoot:  *goldenRoot,
		Update:      *update,
		GroupFilter: *groupFilter,
		ModeFilter:  *modeFilter,
	})
	if err != nil {
		fatalf("%v", err)
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(manifest.Coverage)
		return
	}
	fmt.Printf("outputaudit: %d surfaces captured (%d goldens, %d canonical commands covered)\n",
		manifest.Coverage.TotalSurfaces,
		manifest.Coverage.GoldenSurfaces,
		len(manifest.Coverage.CoveredCommands))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "outputaudit: "+format+"\n", args...)
	os.Exit(1)
}

func resolveRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	cur := wd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return wd, nil
}

func ensureSilaBinary(repoRoot string) (string, func(), error) {
	prebuilt := filepath.Join(repoRoot, "bin", "sila")
	if info, err := os.Stat(prebuilt); err == nil && !info.IsDir() {
		return prebuilt, nil, nil
	}
	tmpDir, err := os.MkdirTemp("", "sila-outputaudit-bin-*")
	if err != nil {
		return "", nil, err
	}
	binPath := filepath.Join(tmpDir, "sila")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/sila")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", nil, fmt.Errorf("go build ./cmd/sila: %w\n%s", err, string(out))
	}
	return binPath, func() { _ = os.RemoveAll(tmpDir) }, nil
}

// HarnessOptions configures an audit harness execution.
type HarnessOptions struct {
	RepoRoot    string
	SilaBin     string
	OutDir      string
	GoldenRoot  string
	Update      bool
	GroupFilter string
	ModeFilter  string
}

// Sandbox holds the isolated temporary paths for one deterministic audit run.
type Sandbox struct {
	Root    string
	HomeDir string
	RepoDir string
	DBPath  string
	SilaBin string
}

// Close removes the temporary sandbox directory.
func (s *Sandbox) Close() {
	if s != nil && s.Root != "" {
		_ = os.RemoveAll(s.Root)
	}
}

// mkSandbox creates an isolated sandbox with HOME, SILA_DB, a deterministic Git
// repository, and seeded Sila fixtures (goals, memo, lesson, case, feedback,
// handoff, decision, doc).
func mkSandbox(silaBin string) (*Sandbox, error) {
	root, err := os.MkdirTemp("", "sila-audit-sandbox-*")
	if err != nil {
		return nil, err
	}
	sb := &Sandbox{
		Root:    root,
		HomeDir: filepath.Join(root, "home"),
		RepoDir: filepath.Join(root, "repo"),
		DBPath:  filepath.Join(root, "home", ".local", "share", "sila", "sila.db"),
		SilaBin: silaBin,
	}
	if err := os.MkdirAll(filepath.Dir(sb.DBPath), 0o755); err != nil {
		sb.Close()
		return nil, err
	}
	if err := os.MkdirAll(sb.RepoDir, 0o755); err != nil {
		sb.Close()
		return nil, err
	}
	if err := seedSandbox(sb); err != nil {
		sb.Close()
		return nil, err
	}
	return sb, nil
}

func seedSandbox(sb *Sandbox) error {
	// 1. Initialize git repo with deterministic identity and timestamp
	gitEnv := sandboxEnv(sb)
	runGit := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = sb.RepoDir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v: %w\n%s", args, err, string(out))
		}
		return nil
	}
	if err := runGit("init", "-b", "main"); err != nil {
		return err
	}
	if err := runGit("config", "user.name", "Audit Bot"); err != nil {
		return err
	}
	if err := runGit("config", "user.email", "audit@example.com"); err != nil {
		return err
	}

	// Write minimal repo files + 1 ready goal package
	readmePath := filepath.Join(sb.RepoDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Sandbox Repo\n"), 0o644); err != nil {
		return err
	}
	goalDir := filepath.Join(sb.RepoDir, "goals", "alpha-goal")
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		return err
	}
	goalMD := `# Goal: Alpha Sandbox Goal

## Chain of Custody (Isnad)
- **Registered By**: audit-bot
- **Spec Audited By**: Pending Spec Audit
- **Implemented By**: Pending Implementation
- **Goal Slug**: ` + "`alpha-goal`" + `

## Goal Description
Deterministic sandbox goal for output audit harness.

## Dependencies & Execution Order
- **Mode**: Independent
- **Sequence**: 1
- **Shape**: ship
- **Tier**: immediate

## Files to Touch
- README.md

## Acceptance Criteria
- **AC-001**: README exists.
`
	planMD := `# Plan: Alpha Sandbox Goal

## Execution Steps
- [ ] **Phase 1**: Update README.md
`
	factsMD := `# Facts: Alpha Sandbox Goal

## 1. Grounding
- README.md is present at the repository root.
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalMD), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(goalDir, "plan.md"), []byte(planMD), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(goalDir, "facts.md"), []byte(factsMD), 0o644); err != nil {
		return err
	}
	if err := runGit("add", "."); err != nil {
		return err
	}
	if err := runGit("commit", "-m", "chore: initial sandbox commit"); err != nil {
		return err
	}

	// 2. Seed deterministic Sila records (memo, lesson, case, decision)
	seedCmds := [][]string{
		// Keep the seeded memo on the bare derived project id: the onboard
		// failure surface depends on the repository staying unregistered.
		{"memo", "add", "Always normalize volatile sandbox paths before golden comparison", "--project=repo", "--files=README.md", "--agent=audit-bot"},
		{"lesson", "Sandbox path leaked into golden snapshot", "--cause=Temp dir was not normalized", "--fix=Apply longest-path-first replacement", "--files=README.md", "--agent=audit-bot"},
		{"case", "Deterministic CLI output audit harness", "--approach=Run every command in an isolated sandbox and normalize volatile tokens", "--invariant=Never touch real HOME or network", "--files=README.md", "--agent=audit-bot"},
		{"decision", "create", "Output Golden Format", "-o", "A: Plain UTF-8 text files", "-o", "B: Binary blobs", "-r", "A", "--rationale=Human and git diff friendly"},
	}
	for _, argv := range seedCmds {
		cmd := exec.Command(sb.SilaBin, argv...)
		cmd.Dir = sb.RepoDir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("seed sila %v: %w\n%s", argv, err, string(out))
		}
	}
	return nil
}

func sandboxEnv(sb *Sandbox) []string {
	fixedDate := "2026-01-02T03:04:05Z"
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + sb.HomeDir,
		"XDG_CONFIG_HOME=" + filepath.Join(sb.HomeDir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(sb.HomeDir, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(sb.HomeDir, ".cache"),
		"SILA_DB=" + sb.DBPath,
		"SILA_AGENT=audit-bot",
		"SILA_SESSION_ID=sess-audit-0001",
		"SILA_NO_UPDATE_CHECK=1",
		"NO_COLOR=1",
		"TERM=dumb",
		"TZ=UTC",
		"LC_ALL=C",
		"GIT_AUTHOR_NAME=Audit Bot",
		"GIT_AUTHOR_EMAIL=audit@example.com",
		"GIT_COMMITTER_NAME=Audit Bot",
		"GIT_COMMITTER_EMAIL=audit@example.com",
		"GIT_AUTHOR_DATE=" + fixedDate,
		"GIT_COMMITTER_DATE=" + fixedDate,
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
	}
}

// DiscoverCanonicalCommands parses `sila help all` to extract every canonical
// command in the help inventory.
func DiscoverCanonicalCommands(silaBin string) ([]string, error) {
	cmd := exec.Command(silaBin, "help", "all")
	cmd.Env = append(os.Environ(), "NO_COLOR=1", "TERM=dumb")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sila help all: %w\n%s", err, string(out))
	}
	return ParseCanonicalCommandsFromHelpAll(string(out)), nil
}

// ParseCanonicalCommandsFromHelpAll extracts command names from the `sila help all` text.
func ParseCanonicalCommandsFromHelpAll(helpText string) []string {
	seen := make(map[string]bool)
	var cmds []string
	inSection := false
	for _, rawLine := range strings.Split(helpText, "\n") {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasSuffix(trimmed, ":") && !strings.HasPrefix(line, " ") {
			switch trimmed {
			case "Usage:", "Common Options:", "Examples:", "Conversational phrases:", "Learn More:":
				inSection = false
			default:
				inSection = true
			}
			continue
		}
		if !inSection || !strings.HasPrefix(line, "  ") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		first := fields[0]
		// Continuation lines for wrapped descriptions start after wide padding
		if strings.HasPrefix(line, "      ") {
			continue
		}
		if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(first) {
			continue
		}
		if !seen[first] {
			seen[first] = true
			cmds = append(cmds, first)
		}
	}
	return cmds
}

// SurfaceManifest returns the complete list of audited CLI surfaces, covering
// every canonical command and all required R3 golden contracts.
func SurfaceManifest(canonicalCommands []string) []SurfaceSpec {
	specs := []SurfaceSpec{
		// Help surfaces
		{Group: "help", Label: "help__core", Command: "salam", Argv: []string{"--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/core.txt"},
		{Group: "help", Label: "help__all", Command: "salam", Argv: []string{"help", "all"}, Mode: "help", ExpectedExit: 0, Golden: "help/all.txt"},
		{Group: "help", Label: "help__goals", Command: "goals", Argv: []string{"goals", "--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/goals.txt"},
		{Group: "help", Label: "help__gate", Command: "gate", Argv: []string{"gate", "--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/gate.txt"},
		{Group: "help", Label: "help__connect", Command: "connect", Argv: []string{"connect", "--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/connect.txt"},
		{Group: "help", Label: "help__cards", Command: "cards", Argv: []string{"cards", "--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/cards.txt"},
		{Group: "help", Label: "help__daemon", Command: "daemon", Argv: []string{"daemon", "--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/daemon.txt"},
		{Group: "help", Label: "help__man", Command: "man", Argv: []string{"man", "--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/man.txt"},
		{Group: "help", Label: "help__soak", Command: "soak", Argv: []string{"soak", "--help"}, Mode: "help", ExpectedExit: 0, Golden: "help/soak.txt"},

		// Error surfaces
		{Group: "errors", Label: "errors__unknown-command", Command: "salam", Argv: []string{"definitely-not-a-command"}, Mode: "human", ExpectedExit: 2, Golden: "errors/unknown-command.txt"},
		{Group: "errors", Label: "errors__unknown-flag", Command: "decision", Argv: []string{"decision", "create", "--definitely-unknown-flag"}, Mode: "human", ExpectedExit: 2, Golden: "errors/unknown-flag.txt"},
		{Group: "errors", Label: "errors__missing-arg", Command: "memo", Argv: []string{"memo", "show"}, Mode: "human", ExpectedExit: 2, Golden: "errors/missing-arg.txt"},

		// Goals matrix & tasks surfaces
		{Group: "goals", Label: "goals__default", Command: "goals", Argv: []string{"goals"}, Mode: "human", ExpectedExit: 0, Golden: "goals/default.txt"},
		{Group: "goals", Label: "goals__no-sync", Command: "goals", Argv: []string{"goals", "--no-sync"}, Mode: "human", ExpectedExit: 0, Golden: "goals/no-sync.txt"},
		{Group: "goals", Label: "goals__ascii", Command: "goals", Argv: []string{"goals", "--no-sync", "--ascii"}, Mode: "human", ExpectedExit: 0, Golden: "goals/ascii.txt"},
		{Group: "goals", Label: "goals__verbose", Command: "goals", Argv: []string{"goals", "--no-sync", "--verbose"}, Mode: "human", ExpectedExit: 0, Golden: "goals/verbose.txt"},
		{Group: "goals", Label: "goals__json", Command: "goals", Argv: []string{"goals", "--json"}, Mode: "json", ExpectedExit: 0, Golden: "goals/json.txt"},
		{Group: "goals", Label: "goals__json-envelope", Command: "goals", Argv: []string{"goals", "--json-envelope"}, Mode: "json-envelope", ExpectedExit: 0, Golden: "goals/json-envelope.json"},
		{Group: "goals", Label: "goals__toon", Command: "goals", Argv: []string{"goals", "--toon"}, Mode: "toon", ExpectedExit: 0, Golden: "goals/toon.txt"},
		{Group: "goals", Label: "goals__porcelain", Command: "goals", Argv: []string{"goals", "--porcelain"}, Mode: "porcelain", ExpectedExit: 0, Golden: "goals/porcelain.txt"},
		{Group: "goals", Label: "goals__tasks", Command: "goals", Argv: []string{"goals", "tasks", "alpha-goal"}, Mode: "human", ExpectedExit: 0, Golden: "goals/tasks.txt"},

		// Onboard check contracts
		{Group: "onboard", Label: "onboard__fail", Command: "onboard", Argv: []string{"onboard", "check"}, Mode: "human", ExpectedExit: 1, Golden: "onboard/fail.txt"},
		{Group: "onboard", Label: "onboard__advisory", Command: "onboard", Argv: []string{"onboard", "check", "--advisory"}, Mode: "human", ExpectedExit: 0, Golden: "onboard/advisory.txt"},
		{Group: "onboard", Label: "onboard__json-fail", Command: "onboard", Argv: []string{"onboard", "check", "--json"}, Mode: "json-envelope", ExpectedExit: 1, Golden: "onboard/json-fail.json"},

		// Agents check & sync dry-run
		{Group: "agents", Label: "agents__check", Command: "agents", Argv: []string{"agents", "check"}, Mode: "human", ExpectedExit: 1, Golden: "agents/check.txt"},
		{Group: "agents", Label: "agents__sync-dry-run", Command: "agents", Argv: []string{"agents", "sync", "--dry-run"}, Mode: "human", Mutating: true, ExpectedExit: 0, Golden: "agents/sync-dry-run.txt"},

		// Guide & Learn surfaces
		{Group: "guide", Label: "guide__case", Command: "case", Argv: []string{"case", "list"}, Mode: "human", ExpectedExit: 0, Golden: "guide/case.txt"},
		{Group: "guide", Label: "guide__lesson", Command: "lesson", Argv: []string{"lesson", "list"}, Mode: "human", ExpectedExit: 0, Golden: "guide/lesson.txt"},

		// Decision surfaces
		{Group: "decision", Label: "decision__list", Command: "decision", Argv: []string{"decision", "list"}, Mode: "human", ExpectedExit: 0, Golden: "decision/list.txt"},
		{Group: "decision", Label: "decision__view", Command: "decision", Argv: []string{"decision", "view", "D1"}, Mode: "human", ExpectedExit: 0, Golden: "decision/view.txt"},

		// JSON empty & structured surfaces
		{Group: "json", Label: "json__search-empty", Command: "search", Argv: []string{"search", "nonexistent_token_xyz987", "--json"}, Mode: "json", ExpectedExit: 0, Golden: "json/search-empty.json"},
		{Group: "json", Label: "json__handoff-empty", Command: "handoff", Argv: []string{"handoff", "--json"}, Mode: "json", ExpectedExit: 0, Golden: "json/handoff-empty.json"},
	}

	covered := make(map[string]bool)
	for _, s := range specs {
		covered[s.Command] = true
	}

	// Ensure every canonical command in the inventory has a deterministic help capture
	for _, cmdName := range canonicalCommands {
		if !covered[cmdName] {
			specs = append(specs, SurfaceSpec{
				Group:        "help",
				Label:        "help__" + cmdName,
				Command:      cmdName,
				Argv:         []string{cmdName, "--help"},
				Mode:         "help",
				Mutating:     destructiveOrNetworkDenylist[cmdName],
				ExpectedExit: 0,
			})
			covered[cmdName] = true
		}
	}

	for i := range specs {
		if specs[i].Timeout == 0 {
			specs[i].Timeout = 15 * time.Second
		}
	}
	return specs
}

// ValidateSurfaceManifest checks safety rules (denylisted commands must use
// --help or --dry-run) and verifies 100% coverage of canonical commands and
// required output modes.
func ValidateSurfaceManifest(specs []SurfaceSpec, canonicalCommands []string) (CoverageSummary, error) {
	summary := CoverageSummary{
		CanonicalCommands: append([]string(nil), canonicalCommands...),
		ModesCovered:      make(map[string]int),
		TotalSurfaces:     len(specs),
	}
	seenCmd := make(map[string]bool)
	for _, s := range specs {
		if s.Command == "" || len(s.Argv) == 0 {
			return summary, fmt.Errorf("surface %q has empty command or argv", s.Label)
		}
		if destructiveOrNetworkDenylist[s.Command] || s.Mutating {
			safe := false
			for _, a := range s.Argv {
				if a == "--help" || a == "-h" || a == "help" || a == "--dry-run" || a == "-n" {
					safe = true
					break
				}
			}
			if !safe {
				return summary, fmt.Errorf("surface %q invokes mutating/denylisted command %q without --help or --dry-run", s.Label, s.Command)
			}
		}
		seenCmd[s.Command] = true
		summary.ModesCovered[s.Mode]++
		if s.Golden != "" {
			summary.GoldenSurfaces++
		}
	}

	for _, c := range canonicalCommands {
		if seenCmd[c] {
			summary.CoveredCommands = append(summary.CoveredCommands, c)
		} else {
			summary.MissingCommands = append(summary.MissingCommands, c)
		}
	}
	sort.Strings(summary.CoveredCommands)
	sort.Strings(summary.MissingCommands)

	if len(summary.MissingCommands) > 0 {
		return summary, fmt.Errorf("surface manifest missing %d canonical command(s): %s",
			len(summary.MissingCommands), strings.Join(summary.MissingCommands, ", "))
	}

	requiredModes := []string{"human", "json", "json-envelope", "toon", "porcelain", "help"}
	for _, m := range requiredModes {
		if summary.ModesCovered[m] == 0 {
			return summary, fmt.Errorf("surface manifest missing required output mode %q", m)
		}
	}
	return summary, nil
}

var (
	ansiRegex     = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	rfc3339Regex  = regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\b`)
	dateTimeRegex = regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\b`)
	dateOnlyRegex = regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2}\b`)
	durationRegex = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|us|µs|ms|s)\b`)
	idPrefixRegex = regexp.MustCompile(`\b(?:fact|fb|doc|hoff|appr|reb_fact)_[0-9a-f]{6,16}\b`)
	uuidRegex     = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	shaFieldRegex = regexp.MustCompile(`("(?:commit|head|sha|hash|managed_hash|digest)":\s*")[0-9a-f]{7,64}(")`)
	repoHashRegex = regexp.MustCompile(`\brepo-[0-9a-f]{8}\b`)
	hexTokenRegex = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// NormalizeOutput deterministically scrubs sandbox paths, ANSI sequences,
// timestamps, durations, generated IDs, and commit SHAs.
func NormalizeOutput(raw, sandboxRoot, homeDir, repoDir string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = ansiRegex.ReplaceAllString(s, "")

	// Longest-path-first replacement
	type pathPair struct {
		from string
		to   string
	}
	var pairs []pathPair
	if repoDir != "" {
		pairs = append(pairs, pathPair{repoDir, "<sandbox>/repo"})
	}
	if homeDir != "" {
		pairs = append(pairs, pathPair{homeDir, "<sandbox>/home"})
	}
	if sandboxRoot != "" {
		pairs = append(pairs, pathPair{sandboxRoot, "<sandbox>"})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i].from) > len(pairs[j].from)
	})
	for _, p := range pairs {
		if p.from != "" {
			s = strings.ReplaceAll(s, p.from, p.to)
		}
	}

	s = rfc3339Regex.ReplaceAllString(s, "<timestamp>")
	s = dateTimeRegex.ReplaceAllString(s, "<timestamp>")
	s = dateOnlyRegex.ReplaceAllString(s, "<date-iso>")
	s = idPrefixRegex.ReplaceAllString(s, "<id>")
	s = uuidRegex.ReplaceAllString(s, "<id>")
	s = shaFieldRegex.ReplaceAllString(s, "${1}<sha>${2}")
	s = repoHashRegex.ReplaceAllString(s, "repo-<sha>")
	s = durationRegex.ReplaceAllString(s, "<duration>")

	// Normalize standalone 7-40 char hex commit SHAs that contain at least one digit and one a-f letter
	s = hexTokenRegex.ReplaceAllStringFunc(s, func(tok string) string {
		hasDigit := strings.ContainsAny(tok, "0123456789")
		hasHexAlpha := strings.ContainsAny(tok, "abcdef")
		if hasDigit && hasHexAlpha {
			return "<sha>"
		}
		return tok
	})

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	out := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	if out != "" {
		out += "\n"
	}
	return out
}

// FormatCombinedSurface formats normalized stdout, stderr, and exit code into a
// deterministic golden snapshot payload.
func FormatCombinedSurface(rec CaptureRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# surface: %s\n", rec.Label)
	fmt.Fprintf(&b, "# argv: %s\n", rec.Argv)
	fmt.Fprintf(&b, "# mode: %s\n", rec.Mode)
	fmt.Fprintf(&b, "# exit: %d\n", rec.ExitCode)
	b.WriteString("--- stdout ---\n")
	b.WriteString(rec.Stdout)
	if rec.Stderr != "" {
		b.WriteString("--- stderr ---\n")
		b.WriteString(rec.Stderr)
	}
	return b.String()
}

// RunHarness executes the output audit in an isolated sandbox and optionally
// writes the report, full sidecars, and golden files.
func RunHarness(opts HarnessOptions) (*AuditManifest, error) {
	canonical, err := DiscoverCanonicalCommands(opts.SilaBin)
	if err != nil {
		return nil, err
	}
	allSpecs := SurfaceManifest(canonical)
	cov, err := ValidateSurfaceManifest(allSpecs, canonical)
	if err != nil {
		return nil, err
	}

	var specs []SurfaceSpec
	for _, s := range allSpecs {
		if opts.GroupFilter != "" && s.Group != opts.GroupFilter {
			continue
		}
		if opts.ModeFilter != "" && s.Mode != opts.ModeFilter {
			continue
		}
		specs = append(specs, s)
	}

	records := make([]CaptureRecord, 0, len(specs))

	// Surfaces within a group share a sandbox; groups are isolated. A write in
	// one group (for example a memo that registers its project) must not change
	// another group's expected exit code or output.
	var sb *Sandbox
	var env []string
	currentGroup := ""
	defer func() {
		if sb != nil {
			sb.Close()
		}
	}()

	for _, spec := range specs {
		if sb == nil || spec.Group != currentGroup {
			if sb != nil {
				sb.Close()
			}
			sb, err = mkSandbox(opts.SilaBin)
			if err != nil {
				return nil, err
			}
			env = sandboxEnv(sb)
			currentGroup = spec.Group
		}
		ctx, cancel := context.WithTimeout(context.Background(), spec.Timeout)
		cmd := exec.CommandContext(ctx, opts.SilaBin, spec.Argv...)
		cmd.Dir = sb.RepoDir
		cmd.Env = env
		if spec.Stdin != "" {
			cmd.Stdin = strings.NewReader(spec.Stdin)
		}
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf

		runErr := cmd.Run()
		ctxErr := ctx.Err()
		cancel()

		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("surface %q timed out after %s", spec.Label, spec.Timeout)
		}

		exitCode := 0
		if runErr != nil {
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				return nil, fmt.Errorf("surface %q execution failed: %w", spec.Label, runErr)
			}
		}

		if exitCode != spec.ExpectedExit {
			return nil, fmt.Errorf("surface %q exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
				spec.Label, exitCode, spec.ExpectedExit, stdoutBuf.String(), stderrBuf.String())
		}

		normOut := NormalizeOutput(stdoutBuf.String(), sb.Root, sb.HomeDir, sb.RepoDir)
		normErr := NormalizeOutput(stderrBuf.String(), sb.Root, sb.HomeDir, sb.RepoDir)
		if !spec.ExpectEmpty && normOut == "" && normErr == "" {
			return nil, fmt.Errorf("surface %q produced unexpected empty stdout and stderr", spec.Label)
		}

		rec := CaptureRecord{
			Group:        spec.Group,
			Label:        spec.Label,
			Command:      spec.Command,
			Argv:         "sila " + strings.Join(spec.Argv, " "),
			Mode:         spec.Mode,
			ExpectedExit: spec.ExpectedExit,
			ExitCode:     exitCode,
			StdoutBytes:  len(normOut),
			StderrBytes:  len(normErr),
			GoldenFile:   spec.Golden,
			FullFile:     filepath.ToSlash(filepath.Join("full", spec.Label+".txt")),
			Stdout:       normOut,
			Stderr:       normErr,
		}
		records = append(records, rec)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Label < records[j].Label
	})

	manifest := &AuditManifest{
		Schema:   "sila/output-audit-manifest/v1",
		Coverage: cov,
		Surfaces: records,
	}

	if opts.OutDir != "" {
		if err := writeOutputArtifacts(opts.OutDir, manifest); err != nil {
			return nil, err
		}
	}
	if opts.Update && opts.GoldenRoot != "" {
		if err := WriteGoldenSnapshots(opts.GoldenRoot, manifest); err != nil {
			return nil, err
		}
	}
	return manifest, nil
}

func writeOutputArtifacts(outDir string, m *AuditManifest) error {
	fullDir := filepath.Join(outDir, "full")
	if err := os.MkdirAll(fullDir, 0o755); err != nil {
		return err
	}
	for _, rec := range m.Surfaces {
		fullPath := filepath.Join(outDir, filepath.FromSlash(rec.FullFile))
		if err := os.WriteFile(fullPath, []byte(FormatCombinedSurface(rec)), 0o644); err != nil {
			return err
		}
	}
	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), append(manifestBytes, '\n'), 0o644); err != nil {
		return err
	}
	reportMD := RenderMarkdownReport(m)
	return os.WriteFile(filepath.Join(outDir, "report.md"), []byte(reportMD), 0o644)
}

// WriteGoldenSnapshots writes `manifest.json` and every surface with a `GoldenFile`
// under `goldenRoot/surfaces/`.
func WriteGoldenSnapshots(goldenRoot string, m *AuditManifest) error {
	if err := os.MkdirAll(filepath.Join(goldenRoot, "surfaces"), 0o755); err != nil {
		return err
	}
	type goldenEntry struct {
		Label        string `json:"label"`
		Group        string `json:"group"`
		Command      string `json:"command"`
		Argv         string `json:"argv"`
		Mode         string `json:"mode"`
		ExpectedExit int    `json:"expected_exit"`
		GoldenFile   string `json:"golden_file"`
	}
	var entries []goldenEntry
	for _, rec := range m.Surfaces {
		if rec.GoldenFile == "" {
			continue
		}
		target := filepath.Join(goldenRoot, "surfaces", filepath.FromSlash(rec.GoldenFile))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(FormatCombinedSurface(rec)), 0o644); err != nil {
			return err
		}
		entries = append(entries, goldenEntry{
			Label:        rec.Label,
			Group:        rec.Group,
			Command:      rec.Command,
			Argv:         rec.Argv,
			Mode:         rec.Mode,
			ExpectedExit: rec.ExitCode,
			GoldenFile:   filepath.ToSlash(filepath.Join("surfaces", rec.GoldenFile)),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Label < entries[j].Label
	})
	wrapper := struct {
		Schema   string          `json:"schema"`
		Coverage CoverageSummary `json:"coverage"`
		Goldens  []goldenEntry   `json:"goldens"`
	}{
		Schema:   "sila/output-golden-manifest/v1",
		Coverage: m.Coverage,
		Goldens:  entries,
	}
	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(goldenRoot, "manifest.json"), append(data, '\n'), 0o644)
}

// RenderMarkdownReport builds the deterministic Markdown audit report with the
// Capture Completeness table and full-file sidecar links.
func RenderMarkdownReport(m *AuditManifest) string {
	var b strings.Builder
	b.WriteString("# Sila CLI Output Format Audit & Golden Contract Report\n\n")
	b.WriteString("Generated deterministically by `go run ./tools/outputaudit` inside an isolated sandbox.\n\n")
	b.WriteString("## Coverage Summary\n\n")
	fmt.Fprintf(&b, "- **Canonical commands**: %d / %d covered (%d missing)\n",
		len(m.Coverage.CoveredCommands), len(m.Coverage.CanonicalCommands), len(m.Coverage.MissingCommands))
	fmt.Fprintf(&b, "- **Total captured surfaces**: %d\n", m.Coverage.TotalSurfaces)
	fmt.Fprintf(&b, "- **Golden contract snapshots**: %d\n", m.Coverage.GoldenSurfaces)
	b.WriteString("- **Modes represented**: ")
	modes := make([]string, 0, len(m.Coverage.ModesCovered))
	for k := range m.Coverage.ModesCovered {
		modes = append(modes, k)
	}
	sort.Strings(modes)
	modeParts := make([]string, 0, len(modes))
	for _, k := range modes {
		modeParts = append(modeParts, fmt.Sprintf("`%s` (%d)", k, m.Coverage.ModesCovered[k]))
	}
	b.WriteString(strings.Join(modeParts, ", ") + "\n\n")

	b.WriteString("## Capture Completeness\n\n")
	b.WriteString("| Surface Label | Command | Mode |Stdout Bytes | Stderr Bytes | Exit | Full Sidecar | Golden |\n")
	b.WriteString("|---|---|---|---:|---:|---:|---|---|\n")
	for _, rec := range m.Surfaces {
		goldenCell := "—"
		if rec.GoldenFile != "" {
			goldenCell = "`surfaces/" + rec.GoldenFile + "`"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | %d | %d | %d | [`%s`](%s) | %s |\n",
			rec.Label, rec.Argv, rec.Mode, rec.StdoutBytes, rec.StderrBytes, rec.ExitCode, rec.FullFile, rec.FullFile, goldenCell)
	}
	b.WriteString("\n")
	return b.String()
}
