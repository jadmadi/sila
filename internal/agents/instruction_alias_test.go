package agents

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureInstructionAlias(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		want      string
		link      string
		target    string
		unchanged []string
	}{
		{
			name: "agents only",
			setup: func(t *testing.T, dir string) {
				writeAliasFile(t, filepath.Join(dir, "AGENTS.md"), "# agents\n")
			},
			want:   "CLAUDE.md -> AGENTS.md",
			link:   "CLAUDE.md",
			target: "AGENTS.md",
		},
		{
			name: "claude only",
			setup: func(t *testing.T, dir string) {
				writeAliasFile(t, filepath.Join(dir, "CLAUDE.md"), "# claude\n")
			},
			want:   "AGENTS.md -> CLAUDE.md",
			link:   "AGENTS.md",
			target: "CLAUDE.md",
		},
		{
			name: "both present",
			setup: func(t *testing.T, dir string) {
				writeAliasFile(t, filepath.Join(dir, "AGENTS.md"), "# agents\n")
				writeAliasFile(t, filepath.Join(dir, "CLAUDE.md"), "# claude\n")
			},
			unchanged: []string{"AGENTS.md", "CLAUDE.md"},
		},
		{
			name: "neither present",
		},
		{
			name: "broken reverse symlink",
			setup: func(t *testing.T, dir string) {
				if err := os.Symlink("AGENTS.md", filepath.Join(dir, "CLAUDE.md")); err != nil {
					t.Fatal(err)
				}
			},
			unchanged: []string{"CLAUDE.md"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			before := readAliasFiles(t, dir)
			got, err := EnsureInstructionAlias(dir)
			if err != nil {
				t.Fatalf("EnsureInstructionAlias: %v", err)
			}
			if got != tt.want {
				t.Fatalf("description = %q, want %q", got, tt.want)
			}
			if tt.link != "" {
				fi, err := os.Lstat(filepath.Join(dir, tt.link))
				if err != nil {
					t.Fatal(err)
				}
				if fi.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("%s is not a symlink", tt.link)
				}
				target, err := os.Readlink(filepath.Join(dir, tt.link))
				if err != nil {
					t.Fatal(err)
				}
				if target != tt.target {
					t.Fatalf("link target = %q, want %q", target, tt.target)
				}
			}
			for _, name := range tt.unchanged {
				if readAliasFiles(t, dir)[name] != before[name] {
					t.Fatalf("%s changed", name)
				}
			}
			if _, err := EnsureInstructionAlias(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnsureInstructionAliasSkipsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureInstructionAlias(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("description = %q, want empty", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatalf("CLAUDE.md created for a directory target: %v", err)
	}
}

func TestDeployTargetsLinksExistingClaudeWithoutSecondBody(t *testing.T) {
	dir := t.TempDir()
	const body = "# claude only\n"
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := DeployTargets(dir, CompileOptions{RepoRoot: dir, ProjectName: "demo"}, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) > 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	fi, err := os.Lstat(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("AGENTS.md is a regular file; want a symlink to CLAUDE.md")
	}
	target, err := os.Readlink(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if target != "CLAUDE.md" {
		t.Fatalf("AGENTS.md -> %q, want CLAUDE.md", target)
	}
	got, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("CLAUDE.md rewritten:\n%s", got)
	}
}

func writeAliasFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readAliasFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			out[name] = "symlink:" + target
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(data)
	}
	return out
}
