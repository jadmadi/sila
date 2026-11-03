package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestPreflightSetAndStatus(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ui := render.New(os.Stdout, true)
	runPreflight(ui, "", "", []string{"set", "enforce"}, false)
	data, err := os.ReadFile(filepath.Join(dir, ".sila", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `preflight_policy = "enforce"`) {
		t.Fatalf("config = %s", data)
	}
	runPreflight(ui, "", "", []string{"set", "warn"}, false)
	data, _ = os.ReadFile(filepath.Join(dir, ".sila", "config"))
	if !strings.Contains(string(data), `preflight_policy = "warn"`) || strings.Count(string(data), "preflight_policy") != 1 {
		t.Fatalf("rewritten config = %s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, ".sila", "config.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	out := captureStdout(t, func() {
		runPreflight(ui, "", "", []string{"status"}, true)
	})
	if !strings.Contains(out, `"policy": "warn"`) {
		t.Fatalf("status = %q", out)
	}
}

func TestPreflightSetUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	ui := render.New(os.Stdout, true)
	runPreflight(ui, "", "", []string{"set", "off", "--user"}, false)
	data, err := os.ReadFile(filepath.Join(home, ".config", "sila", "config"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `preflight_policy = "off"`) {
		t.Fatalf("user config = %s", data)
	}
}
