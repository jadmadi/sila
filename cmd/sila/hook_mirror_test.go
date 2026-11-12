package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

func mirrorHookEnv(projectDir, session, tool, input string) *loaders.HookEnvelope {
	return &loaders.HookEnvelope{
		Event:     loaders.EventPostToolUse,
		Agent:     "claude",
		ProjectID: "p",
		Cwd:       projectDir,
		SessionID: session,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Payload: map[string]interface{}{
			"tool_name":  tool,
			"tool_input": input,
		},
	}
}

func countMemory(t *testing.T, st *store.Store) int {
	t.Helper()
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: "p", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(records)
}

func TestHookMirrorWritesAndDebounces(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude", "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".claude", "memory", "note.md")
	if err := os.WriteFile(path, []byte("## Decisions\n\n- keep the ten-tool surface\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	env := mirrorHookEnv(dir, "sess-1", "Write", `{"file_path":"`+path+`"}`)
	out := captureStdout(t, func() {
		runHookMemoryMirror(st, env, dir, false)
	})
	if !strings.Contains(out, "1 mirrored") {
		t.Fatalf("output = %q", out)
	}
	if got := countMemory(t, st); got != 1 {
		t.Fatalf("records = %d", got)
	}
	out = captureStdout(t, func() {
		runHookMemoryMirror(st, env, dir, false)
	})
	if out != "" {
		t.Fatalf("debounced mirror emitted %q", out)
	}
	if got := countMemory(t, st); got != 1 {
		t.Fatalf("debounce wrote a record: %d", got)
	}

	// A new session bypasses the debounce and sees a duplicate.
	env2 := mirrorHookEnv(dir, "sess-2", "Write", `{"file_path":"`+path+`"}`)
	out = captureStdout(t, func() {
		runHookMemoryMirror(st, env2, dir, false)
	})
	if !strings.Contains(out, "0 mirrored") || !strings.Contains(out, "1 duplicates") {
		t.Fatalf("duplicate output = %q", out)
	}
}

func TestHookMirrorIgnoresOutsideAndSilaPaths(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("## Decisions\n\n- outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := mirrorHookEnv(dir, "sess-1", "Write", `{"file_path":"`+outside+`"}`)
	if out := captureStdout(t, func() { runHookMemoryMirror(st, env, dir, false) }); out != "" {
		t.Fatalf("outside path produced %q", out)
	}
	if got := countMemory(t, st); got != 0 {
		t.Fatalf("outside path wrote %d", got)
	}

	inside := filepath.Join(dir, ".sila", "state", "note.md")
	_ = os.MkdirAll(filepath.Dir(inside), 0o755)
	_ = os.WriteFile(inside, []byte("## Decisions\n\n- internal\n"), 0o644)
	env = mirrorHookEnv(dir, "sess-3", "Write", `{"file_path":"`+inside+`"}`)
	if out := captureStdout(t, func() { runHookMemoryMirror(st, env, dir, false) }); out != "" {
		t.Fatalf(".sila path produced %q", out)
	}
	if got := countMemory(t, st); got != 0 {
		t.Fatalf(".sila path wrote %d", got)
	}
}

func TestHookMirrorDisabledByEnv(t *testing.T) {
	t.Setenv("SILA_MEMORY_MIRROR", "off")
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".claude", "memory"), 0o755)
	path := filepath.Join(dir, ".claude", "memory", "note.md")
	_ = os.WriteFile(path, []byte("## Decisions\n\n- disabled\n"), 0o644)
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	env := mirrorHookEnv(dir, "sess-1", "Write", `{"file_path":"`+path+`"}`)
	if out := captureStdout(t, func() { runHookMemoryMirror(st, env, dir, false) }); out != "" {
		t.Fatalf("disabled mirror produced %q", out)
	}
	if got := countMemory(t, st); got != 0 {
		t.Fatalf("disabled mirror wrote %d", got)
	}
	_ = context.Background()
}
