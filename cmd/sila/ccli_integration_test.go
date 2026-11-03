package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

var (
	ccliTestBinOnce sync.Once
	ccliTestBinPath string
	ccliTestBinErr  error
)

func ensureCCLITestBinary(t *testing.T) string {
	t.Helper()
	ccliTestBinOnce.Do(func() {
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			ccliTestBinErr = err
			return
		}
		preBuilt := filepath.Join(repoRoot, "bin", "sila")
		if info, err := os.Stat(preBuilt); err == nil && !info.IsDir() {
			ccliTestBinPath = preBuilt
			return
		}
		tmpDir, err := os.MkdirTemp("", "sila-ccli-bin-*")
		if err != nil {
			ccliTestBinErr = err
			return
		}
		binOut := filepath.Join(tmpDir, "sila")
		cmd := exec.Command("go", "build", "-o", binOut, "./cmd/sila")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			ccliTestBinErr = fmt.Errorf("go build ./cmd/sila: %w\n%s", err, string(out))
			return
		}
		ccliTestBinPath = binOut
	})
	if ccliTestBinErr != nil {
		t.Fatalf("ensureCCLITestBinary: %v", ccliTestBinErr)
	}
	return ccliTestBinPath
}

type ccliSandbox struct {
	RootDir string
	HomeDir string
	RepoDir string
	DBPath  string
	SilaBin string
}

func newCCLISandbox(t *testing.T) *ccliSandbox {
	t.Helper()
	bin := ensureCCLITestBinary(t)
	root := t.TempDir()
	sb := &ccliSandbox{
		RootDir: root,
		HomeDir: filepath.Join(root, "home"),
		RepoDir: filepath.Join(root, "repo"),
		DBPath:  filepath.Join(root, "home", ".local", "share", "sila", "sila.db"),
		SilaBin: bin,
	}
	for _, dir := range []string{
		sb.HomeDir,
		sb.RepoDir,
		filepath.Dir(sb.DBPath),
		filepath.Join(sb.HomeDir, ".config"),
		filepath.Join(sb.HomeDir, ".cache"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	gitEnv := sb.env()
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = sb.RepoDir
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.name", "CCLI Sandbox")
	runGit("config", "user.email", "ccli@example.com")

	if err := os.WriteFile(filepath.Join(sb.RepoDir, "README.md"), []byte("# CCLI Sandbox\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

	// Seed two fixture goals: alpha-goal (ready) and beta-goal (ready)
	for idx, slug := range []string{"alpha-goal", "beta-goal"} {
		gDir := filepath.Join(sb.RepoDir, "goals", slug)
		if err := os.MkdirAll(gDir, 0o755); err != nil {
			t.Fatalf("mkdir goal %s: %v", slug, err)
		}
		goalMD := fmt.Sprintf(`# Goal: Fixture %s

## Chain of Custody (Isnad)
- **Registered By**: ccli-bot
- **Spec Audited By**: Pending Spec Audit
- **Implemented By**: Pending Implementation
- **Goal Slug**: `+"`%s`"+`

## Goal Description
Deterministic fixture goal for CCLI subprocess integration tests.

## Dependencies & Execution Order
- **Mode**: Independent
- **Sequence**: %d
- **Shape**: ship
- **Tier**: immediate

## Files to Touch
- README.md

## Acceptance Criteria
- **AC-001**: README exists.
`, slug, slug, idx+1)
		planMD := fmt.Sprintf("# Plan: Fixture %s\n\n## Execution Steps\n- [ ] **Phase 1**: Update README.md\n", slug)
		factsMD := fmt.Sprintf("# Facts: Fixture %s\n\n## 1. Grounding\n- README.md exists.\n", slug)
		_ = os.WriteFile(filepath.Join(gDir, "goal.md"), []byte(goalMD), 0o644)
		_ = os.WriteFile(filepath.Join(gDir, "plan.md"), []byte(planMD), 0o644)
		_ = os.WriteFile(filepath.Join(gDir, "facts.md"), []byte(factsMD), 0o644)
	}
	runGit("add", ".")
	runGit("commit", "-m", "chore: seed ccli sandbox")

	// Seed one lesson and one memo so `remember error` and `find` return deterministic hits
	for _, argv := range [][]string{
		{"lesson", "database is locked under concurrent writers", "--cause=Missing busy_timeout", "--fix=Enable WAL and busy_timeout=5000", "--files=README.md", "--agent=ccli-bot"},
		{"memo", "add", "Use pure Go SQLite driver", "--files=README.md", "--agent=ccli-bot"},
	} {
		code, _, stderr := sb.run(argv...)
		if code != 0 {
			t.Fatalf("seed %v failed (exit %d): %s", argv, code, stderr)
		}
	}
	return sb
}

func (sb *ccliSandbox) env() []string {
	fixedDate := "2026-01-02T03:04:05Z"
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + sb.HomeDir,
		"XDG_CONFIG_HOME=" + filepath.Join(sb.HomeDir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(sb.HomeDir, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(sb.HomeDir, ".cache"),
		"SILA_DB=" + sb.DBPath,
		"SILA_AGENT=ccli-bot",
		"SILA_SESSION_ID=sess-ccli-0001",
		"SILA_NO_UPDATE_CHECK=1",
		"SILA_CCLI=on",
		"SILA_CCLI_DENYLIST=",
		"NO_COLOR=1",
		"TERM=dumb",
		"TZ=UTC",
		"LC_ALL=C",
		"GIT_AUTHOR_NAME=CCLI Sandbox",
		"GIT_AUTHOR_EMAIL=ccli@example.com",
		"GIT_COMMITTER_NAME=CCLI Sandbox",
		"GIT_COMMITTER_EMAIL=ccli@example.com",
		"GIT_AUTHOR_DATE=" + fixedDate,
		"GIT_COMMITTER_DATE=" + fixedDate,
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
	}
}

func (sb *ccliSandbox) run(args ...string) (int, string, string) {
	cmd := exec.Command(sb.SilaBin, args...)
	cmd.Dir = sb.RepoDir
	cmd.Env = sb.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return exitCode, stdout.String(), stderr.String()
}

var (
	ccliAnsiRe     = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	ccliRFC3339Re  = regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\b`)
	ccliDateTimeRe = regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2} \d{2}:\d{2}(?::\d{2})?\b`)
	ccliDurationRe = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ns|us|µs|ms|s)\b`)
	ccliIDPrefixRe = regexp.MustCompile(`\b(?:fact|fb|doc|hoff|appr|reb_fact)_[0-9a-f]{6,16}\b`)
	ccliUUIDRe     = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	ccliSHARe      = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
)

// normalizeCCLISandboxOutput applies Facts §5 normalization rules before golden comparison.
func normalizeCCLISandboxOutput(raw string, sb *ccliSandbox) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = ccliAnsiRe.ReplaceAllString(s, "")

	type pair struct {
		from string
		to   string
	}
	var pairs []pair
	if sb != nil {
		if sb.DBPath != "" {
			pairs = append(pairs, pair{sb.DBPath, "<db>"})
		}
		if sb.HomeDir != "" {
			pairs = append(pairs, pair{sb.HomeDir, "<home>"})
		}
		if sb.RepoDir != "" {
			pairs = append(pairs, pair{sb.RepoDir, "<sandbox>"})
		}
		if sb.RootDir != "" {
			pairs = append(pairs, pair{sb.RootDir, "<sandbox>"})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i].from) > len(pairs[j].from)
	})
	for _, p := range pairs {
		if p.from != "" {
			s = strings.ReplaceAll(s, p.from, p.to)
		}
	}

	s = ccliRFC3339Re.ReplaceAllString(s, "<timestamp>")
	s = ccliDateTimeRe.ReplaceAllString(s, "<timestamp>")
	s = ccliIDPrefixRe.ReplaceAllString(s, "<id>")
	s = ccliUUIDRe.ReplaceAllString(s, "<id>")
	s = ccliDurationRe.ReplaceAllString(s, "<duration>")
	s = ccliSHARe.ReplaceAllString(s, "<sha>")
	s = strings.ReplaceAll(s, " (repo)", " (<project-id>)")

	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func TestCCLISubprocessLivePhrasesAndSentinels(t *testing.T) {
	sb := newCCLISandbox(t)

	// Record host git status before running subprocesses to assert zero host mutation
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	hostGitBefore, _ := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()

	cases := []struct {
		name       string
		argv       []string
		wantExit   int
		wantStdout bool // true = non-empty data on stdout
		wantStderr bool // true = non-empty diagnostics/preview on stderr
		stdoutSub  string
		stderrSub  string
		noSecret   string
	}{
		{
			name:       "say_hello",
			argv:       []string{"say", "hello"},
			wantExit:   0,
			wantStdout: true,
			stdoutSub:  "sila",
		},
		{
			name:       "remember_error_database_is_locked",
			argv:       []string{"remember", "error", "database is locked"},
			wantExit:   0,
			wantStdout: true,
			stdoutSub:  "database is locked",
		},
		{
			name:       "save_memo_use_WAL",
			argv:       []string{"save", "memo", "use WAL"},
			wantExit:   0,
			wantStdout: true,
			stdoutSub:  "Recorded memo",
		},
		{
			name:       "claim_goal_alpha_goal",
			argv:       []string{"claim", "goal", "alpha-goal"},
			wantExit:   0,
			wantStdout: true,
			stdoutSub:  "alpha-goal",
		},
		{
			name:       "find_query",
			argv:       []string{"find", "WAL"},
			wantExit:   0,
			wantStdout: true,
			stdoutSub:  "WAL",
		},
		{
			name:       "refresh_memory_dry_run",
			argv:       []string{"refresh", "memory", "--dry-run"},
			wantExit:   0,
			wantStdout: false,
			wantStderr: true,
			stderrSub:  "sila · ccli dry-run",
		},
		{
			name:       "tier3_refusal_without_yes",
			argv:       []string{"drop", "goal", "alpha-goal"},
			wantExit:   3,
			wantStdout: false,
			wantStderr: true,
			stderrSub:  "refusing destructive action without --yes",
		},
		{
			name:       "tier3_drop_goal_with_yes",
			argv:       []string{"drop", "goal", "alpha-goal", "--yes"},
			wantExit:   0,
			wantStdout: true,
			stdoutSub:  "released",
		},
		{
			name:       "do_sync_rules_dry_run",
			argv:       []string{"do", "sync rules", "--dry-run"},
			wantExit:   0,
			wantStdout: false,
			wantStderr: true,
			stderrSub:  "sila · ccli dry-run",
		},
		{
			name:       "ccli_explain_save_memo",
			argv:       []string{"ccli", "explain", "save", "memo", "use WAL"},
			wantExit:   0,
			wantStdout: true,
			stdoutSub:  "Production:  sila_memo_save",
		},
		{
			name:       "forbidden_secret_phrase",
			argv:       []string{"set", "config", "github_token", "to", "SYNTHETIC_SECRET_SENTINEL_99X"},
			wantExit:   3,
			wantStdout: false,
			wantStderr: true,
			stderrSub:  "Policy violation",
			noSecret:   "SYNTHETIC_SECRET_SENTINEL_99X",
		},
		{
			name:       "machine_dry_run_json",
			argv:       []string{"save", "memo", "use WAL", "--dry-run", "--json"},
			wantExit:   0,
			wantStdout: true,
			wantStderr: true,
			stdoutSub:  `"dry_run": true`,
			stderrSub:  "sila · ccli dry-run",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := sb.run(tc.argv...)
			if code != tc.wantExit {
				t.Fatalf("argv=%v exit=%d, want %d\nstdout:\n%s\nstderr:\n%s", tc.argv, code, tc.wantExit, stdout, stderr)
			}
			if tc.wantStdout && strings.TrimSpace(stdout) == "" {
				t.Errorf("argv=%v expected stdout data, got empty stdout (stderr=%s)", tc.argv, stderr)
			}
			if !tc.wantStdout && strings.TrimSpace(stdout) != "" {
				t.Errorf("argv=%v expected empty stdout, got:\n%s", tc.argv, stdout)
			}
			if tc.wantStderr && strings.TrimSpace(stderr) == "" {
				t.Errorf("argv=%v expected stderr diagnostic, got empty stderr", tc.argv)
			}
			if tc.stdoutSub != "" && !strings.Contains(stdout, tc.stdoutSub) {
				t.Errorf("argv=%v stdout missing %q in:\n%s", tc.argv, tc.stdoutSub, stdout)
			}
			if tc.stderrSub != "" && !strings.Contains(stderr, tc.stderrSub) {
				t.Errorf("argv=%v stderr missing %q in:\n%s", tc.argv, tc.stderrSub, stderr)
			}
			if tc.noSecret != "" && strings.Contains(stdout+stderr, tc.noSecret) {
				t.Fatalf("argv=%v leaked secret %q in output", tc.argv, tc.noSecret)
			}
			if tc.name == "machine_dry_run_json" {
				var payload map[string]any
				if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
					t.Fatalf("unmarshal machine dry-run JSON: %v\nraw:\n%s", err, stdout)
				}
				if payload["ok"] != true || payload["dry_run"] != true || payload["would_execute"] != true {
					t.Errorf("unexpected machine dry-run JSON payload: %v", payload)
				}
			}
		})
	}

	// R4.6: Sentinel check — refusal and dry-run leave sandbox DB, goals, and git unchanged
	t.Run("MutationFreeSentinelOnRefusalAndDryRun", func(t *testing.T) {
		// Claim beta-goal first so we can verify that a refused Tier 3 drop does NOT release it
		if code, _, stderr := sb.run("claim", "goal", "beta-goal"); code != 0 {
			t.Fatalf("claim beta-goal: %d (%s)", code, stderr)
		}
		dbBefore, err := os.ReadFile(sb.DBPath)
		if err != nil {
			t.Fatalf("read sandbox db: %v", err)
		}
		gitBefore, _ := exec.Command("git", "-C", sb.RepoDir, "status", "--porcelain").Output()

		// 1. Tier 3 refusal (no --yes)
		if code, _, _ := sb.run("drop", "goal", "beta-goal"); code != 3 {
			t.Fatalf("expected exit 3 on Tier 3 refusal, got %d", code)
		}
		// 2. Dry-run on Tier 2 mutation
		if code, _, _ := sb.run("save", "memo", "should not be written", "--dry-run"); code != 0 {
			t.Fatalf("expected exit 0 on dry-run, got %d", code)
		}
		// 3. Explain
		if code, _, _ := sb.run("ccli", "explain", "save", "memo", "should not be written"); code != 0 {
			t.Fatalf("expected exit 0 on explain, got %d", code)
		}
		// 4. Forbidden secret refusal
		if code, _, _ := sb.run("set", "config", "brave_api_key", "to", "SYNTHETIC_SECRET_SENTINEL_99X"); code != 3 {
			t.Fatalf("expected exit 3 on forbidden secret, got %d", code)
		}

		dbAfter, err := os.ReadFile(sb.DBPath)
		if err != nil {
			t.Fatalf("read sandbox db after: %v", err)
		}
		if !bytes.Equal(dbBefore, dbAfter) {
			t.Errorf("sandbox SQLite DB mutated during refusal/dry-run/explain")
		}
		gitAfter, _ := exec.Command("git", "-C", sb.RepoDir, "status", "--porcelain").Output()
		if !bytes.Equal(gitBefore, gitAfter) {
			t.Errorf("sandbox git status changed during refusal/dry-run/explain:\nbefore:\n%s\nafter:\n%s", gitBefore, gitAfter)
		}
	})

	hostGitAfter, _ := exec.Command("git", "-C", repoRoot, "status", "--porcelain").Output()
	if !bytes.Equal(hostGitBefore, hostGitAfter) {
		t.Fatalf("host git repository was mutated by subprocess integration test:\nbefore:\n%s\nafter:\n%s", hostGitBefore, hostGitAfter)
	}
}
