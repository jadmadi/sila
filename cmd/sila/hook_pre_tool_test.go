package main

import (
	"os"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
)

func TestPreToolUseWebSearchWarningAndAudit(t *testing.T) {
	dir := t.TempDir()
	env := &loaders.HookEnvelope{
		Event:     loaders.EventPreToolUse,
		Agent:     "command-code",
		Cwd:       dir,
		SessionID: "s1",
		Payload:   map[string]any{"tool_name": "web_search", "query": "x"},
	}
	errOut := captureStderr(t, func() {
		runHookPreToolUse(render.New(os.Stdout, true), nil, env, false)
	})
	if !strings.Contains(errOut, "web search before recall") {
		t.Fatalf("missing advisory warning: %q", errOut)
	}
	entries, err := loaders.ReadHookAudit(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Summary != "web search before recall" {
		t.Fatalf("audit: %+v", entries)
	}
}
