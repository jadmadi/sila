package contract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePolicyPrecedence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SILA_GOAL_CONTRACT", "")
	if got := ResolvePolicy(root); got.Policy != PolicyWarn || got.Source != SourceDefault {
		t.Fatalf("default: %+v", got)
	}
	user := filepath.Join(os.Getenv("HOME"), ".sila")
	if err := os.MkdirAll(user, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, "config"), []byte("[harness.goal_contract]\npolicy = \"enforce\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolvePolicy(root); got.Policy != PolicyEnforce || got.Source != SourceUser {
		t.Fatalf("user: %+v", got)
	}
	proj := filepath.Join(root, ".sila")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "config"), []byte("[harness.goal_contract]\npolicy = \"off\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ResolvePolicy(root); got.Policy != PolicyOff || got.Source != SourceProject {
		t.Fatalf("project: %+v", got)
	}
	t.Setenv("SILA_GOAL_CONTRACT", "warn")
	if got := ResolvePolicy(root); got.Policy != PolicyWarn || got.Source != SourceEnv {
		t.Fatalf("env: %+v", got)
	}
}

func TestResolvePolicyBypass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SILA_GOAL_CONTRACT", "enforce")
	t.Setenv("SILA_GOAL_CONTRACT_BYPASS", "1")
	got := ResolvePolicy(t.TempDir())
	if !got.Bypass || got.Policy != PolicyEnforce {
		t.Fatalf("bypass: %+v", got)
	}
}
