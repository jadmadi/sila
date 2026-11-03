package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectConfigStaysWithinSelectedRoot(t *testing.T) {
	for _, boundary := range []string{"directory", "worktree"} {
		t.Run(boundary, func(t *testing.T) {
			parent := t.TempDir()
			ancestorConfig := filepath.Join(parent, ".sila", "config.json")
			if err := os.MkdirAll(filepath.Dir(ancestorConfig), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte(`{"assist_enabled":true}`)
			if err := os.WriteFile(ancestorConfig, original, 0600); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(parent, "repo")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "directory":
				if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			case "worktree":
				if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../worktrees/repo\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := FindProjectConfig(root); got != "" {
				t.Fatalf("inherited ancestor config: %s", got)
			}
			if got := ProjectConfigDir(root); got != filepath.Join(root, ".sila") {
				t.Fatalf("config escaped selected root: %s", got)
			}
			if err := SaveProjectConfig(root, &Config{AssistEnabled: false}); err != nil {
				t.Fatal(err)
			}
			if got := FindProjectConfig(root); got != filepath.Join(root, ".sila", "config.json") {
				t.Fatalf("wrong saved config: %s", got)
			}
			data, err := os.ReadFile(ancestorConfig)
			if err != nil || string(data) != string(original) {
				t.Fatalf("ancestor changed: %s, %v", data, err)
			}
		})
	}
}

func TestProjectConfigSubdirectoryAndNestedRepo(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveProjectConfig(root, &Config{AssistEnabled: true}); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub", "nested")
	if err := os.MkdirAll(sub, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".sila", "config.json")
	if got := FindProjectConfig(sub); got != want {
		t.Fatalf("subdirectory lost in-repo config: %s", got)
	}
	if got := ProjectConfigDir(sub); got != filepath.Dir(want) {
		t.Fatalf("subdirectory lost in-repo root: %s", got)
	}
	if err := os.WriteFile(filepath.Join(sub, ".git"), []byte("gitdir: ../worktree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectConfig(sub); got != "" {
		t.Fatalf("nested repo inherited parent config: %s", got)
	}
}
