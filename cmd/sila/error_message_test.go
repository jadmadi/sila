package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderFlagParseError(t *testing.T) {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Int("top", 5, "limit")
	if err := fs.Parse([]string{"--top=abc"}); err == nil {
		t.Fatal("expected a parse error")
	} else {
		msg := renderFlagParseError(fs, "recall", err)
		for _, want := range []string{"--top", `got "abc"`, "a number"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("message missing %q: %s", want, msg)
			}
		}
		for _, forbidden := range []string{"parse error", "Usage", "invalid value"} {
			if strings.Contains(msg, forbidden) {
				t.Fatalf("message leaked %q: %s", forbidden, msg)
			}
		}
	}

	unknown := errors.New("flag provided but not defined: -nope")
	if msg := renderFlagParseError(fs, "recall", unknown); !strings.Contains(msg, "unknown flag --nope") {
		t.Fatalf("unknown flag message = %q", msg)
	}
}

func TestFriendlyStoreError(t *testing.T) {
	busy := errors.New(`migrate: migration 5 failed smoke A/B validation: andon halt [ERR_ANDON_INVARIANT_VIOLATION] ... database is locked (5) (SQLITE_BUSY)`)
	msg := friendlyStoreError(busy)
	if !strings.Contains(msg, "busy") || strings.Contains(msg, "ERR_ANDON") || strings.Contains(msg, "SQLITE_BUSY") {
		t.Fatalf("busy mapping = %q", msg)
	}
	halt := errors.New("andon halt [ERR_ANDON_SCHEMA_CORRUPTION] during migrate")
	if msg := friendlyStoreError(halt); strings.Contains(msg, "ERR_ANDON") || !strings.Contains(msg, "consistency check") {
		t.Fatalf("andon mapping = %q", msg)
	}
	if got := friendlyStoreError(nil); got != "" {
		t.Fatalf("nil mapping = %q", got)
	}
}

func TestCLIErrorContracts(t *testing.T) {
	if os.Getenv("SILA_ERR_HELPER") == "1" {
		args := strings.Split(os.Getenv("SILA_ERR_ARGS"), "\x1f")
		os.Args = append([]string{"sila"}, args...)
		main()
		return
	}

	root := t.TempDir()
	dir := filepath.Join(root, "ingest-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		args      []string
		wantExit  int
		want      []string
		forbidden []string
	}{
		{
			name:      "search expand missing id",
			args:      []string{"search", "expand", "999"},
			wantExit:  1,
			want:      []string{"no message with id 999", "fix:"},
			forbidden: []string{"sql:", "no rows in result set"},
		},
		{
			name:      "doc rejects directory",
			args:      []string{"doc", dir},
			wantExit:  2,
			want:      []string{"is a directory", "fix:"},
			forbidden: []string{"Ingested and indexed 0"},
		},
		{
			name:      "flag value",
			args:      []string{"recall", "--top=abc", "sqlite"},
			wantExit:  2,
			want:      []string{"--top", `got "abc"`, "a number"},
			forbidden: []string{"parse error", "Usage"},
		},
		{
			name:      "version alias",
			args:      []string{"version"},
			wantExit:  0,
			forbidden: []string{"unknown command"},
		},
		{
			name:      "list suggestion",
			args:      []string{"list"},
			wantExit:  2,
			want:      []string{"sila goals list"},
			forbidden: []string{`did you mean "live"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caseRoot := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-"))
			home := filepath.Join(caseRoot, "home")
			repo := filepath.Join(caseRoot, "repo")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(repo, 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command(os.Args[0], "-test.run=TestCLIErrorContracts")
			cmd.Dir = repo
			cmd.Env = append(cleanSilaEnv(),
				"SILA_ERR_HELPER=1",
				"SILA_ERR_ARGS="+strings.Join(tc.args, "\x1f"),
				"HOME="+home,
				"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
				"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
				"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
				"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
			)
			out, err := cmd.CombinedOutput()
			exitCode := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("exec: %v", err)
				}
				exitCode = exitErr.ExitCode()
			}
			if exitCode != tc.wantExit {
				t.Fatalf("exit = %d, want %d\n%s", exitCode, tc.wantExit, out)
			}
			text := string(out)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q in:\n%s", want, text)
				}
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(text, forbidden) {
					t.Fatalf("leaked %q in:\n%s", forbidden, text)
				}
			}
		})
	}
}
