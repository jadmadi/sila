package loaders

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeFixture writes transcripts shaped like the real ones, including every
// trap verified against this machine's Claude Code store.
func claudeFixture(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "projects")
	dir := filepath.Join(root, "-work-atlas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		// bookkeeping types must all be ignored
		`{"type":"mode","mode":"normal","sessionId":"s1"}`,
		`{"type":"permission-mode","permissionMode":"dontAsk","sessionId":"s1"}`,
		`{"type":"file-history-snapshot","isSnapshotUpdate":false,"snapshot":{"messageId":"m0"}}`,
		`{"type":"queue-operation","operation":"enqueue","sessionId":"s1","timestamp":"2026-09-01T10:00:00.000Z"}`,
		// a real typed user prompt, content as a bare string
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:01.000Z","cwd":"/work/atlas","gitBranch":"main","promptSource":"typed","message":{"role":"user","content":"why is the payment worker leaking goroutines?"}}`,
		// the same prompt echoed by last-prompt: must NOT become a second message
		`{"type":"last-prompt","sessionId":"s1","lastPrompt":"why is the payment worker leaking goroutines?"}`,
		// injected context (isMeta) must be skipped
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:02.000Z","cwd":"/work/atlas","isMeta":true,"message":{"role":"user","content":"<system-reminder>tool catalog</system-reminder>"}}`,
		// assistant output as a block list
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-09-01T10:00:03.000Z","cwd":"/work/atlas","message":{"role":"assistant","model":"claude-sonnet-4","content":[{"type":"text","text":"It leaks a goroutine per request."},{"type":"tool_use","name":"Read","input":{}}]}}`,
		// synthetic API-error notice must be skipped
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-09-01T10:00:04.000Z","cwd":"/work/atlas","isApiErrorMessage":true,"message":{"role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"Not logged in · Please run /login"}]}}`,
		// empty content and a tool-only assistant turn must be skipped
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-09-01T10:00:05.000Z","cwd":"/work/atlas","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}`,
	}
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// a second project/session, so project attribution is exercised
	dir2 := filepath.Join(root, "-work-site")
	if err := os.MkdirAll(dir2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "s2.jsonl"), []byte(
		`{"type":"user","sessionId":"s2","timestamp":"2026-09-02T09:00:00.000Z","cwd":"/work/site","message":{"role":"user","content":"deploy question"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLoadClaudeCode(t *testing.T) {
	root := claudeFixture(t)

	msgs, wm, err := loadClaudeCode(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 curated messages, got %d: %+v", len(msgs), msgs)
	}
	for _, m := range msgs {
		if m.Tool != "claude-code" {
			t.Fatalf("tool = %q", m.Tool)
		}
		if m.SessionID == "" || m.ProjectDir == "" {
			t.Fatalf("provenance missing: %+v", m)
		}
		switch {
		case strings.Contains(m.Content, "system-reminder"):
			t.Fatal("isMeta injected context landed as a user message")
		case strings.Contains(m.Content, "Not logged in"):
			t.Fatal("synthetic API-error notice ingested as assistant output")
		case strings.Contains(m.Content, "tool catalog"):
			t.Fatal("injected context ingested")
		}
	}

	// The prompt appears exactly once despite the last-prompt echo.
	count := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "leaking goroutines") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("prompt recorded %d times, want 1 (last-prompt must not duplicate)", count)
	}

	// Assistant output came from the block list.
	var assistant *Message
	for i := range msgs {
		if msgs[i].Role == "assistant" {
			assistant = &msgs[i]
		}
	}
	if assistant == nil {
		t.Fatal("assistant message missing")
	}
	if assistant.Content != "It leaks a goroutine per request." {
		t.Fatalf("assistant content = %q (tool_use block must not leak in)", assistant.Content)
	}

	// Cursor is the newest timestamp, in a fixed-width sortable shape.
	if wm != "2026-09-02T09:00:00.000000000Z" {
		t.Fatalf("watermark = %q", wm)
	}

	n, err := countClaudeCode(root, nil)
	if err != nil || n != 3 {
		t.Fatalf("countClaudeCode = %d (err=%v), want 3", n, err)
	}
}

func TestLoadClaudeCodeIncremental(t *testing.T) {
	root := claudeFixture(t)
	_, wm, err := loadClaudeCode(root, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	// Nothing new: the incoming cursor is preserved.
	msgs, wm2, err := loadClaudeCode(root, nil, wm)
	if err != nil {
		t.Fatal(err)
	}
	// The record sitting exactly on the boundary is re-emitted (dedup absorbs it).
	if len(msgs) != 1 {
		t.Fatalf("incremental pass emitted %d messages, want the single boundary record", len(msgs))
	}
	if wm2 != wm {
		t.Fatalf("watermark moved on an unchanged pass: %q -> %q", wm, wm2)
	}

	// A newer turn is picked up, and older ones stay behind the cursor.
	file := filepath.Join(root, "-work-atlas", "s1.jsonl")
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"user","sessionId":"s1","timestamp":"2026-09-03T08:00:00.000Z","cwd":"/work/atlas","message":{"role":"user","content":"newer turn"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	msgs, wm3, err := loadClaudeCode(root, nil, wm2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range msgs {
		if m.Content == "newer turn" {
			found = true
		}
		// The record sitting exactly on the cursor is re-emitted by design
		// (inclusive boundary); anything strictly older must not be.
		if strings.Contains(m.Content, "leaking goroutines") {
			t.Fatal("record older than the cursor was re-emitted")
		}
	}
	if !found {
		t.Fatalf("appended turn not ingested: %+v", msgs)
	}
	if wm3 != "2026-09-03T08:00:00.000000000Z" {
		t.Fatalf("watermark = %q", wm3)
	}
}

func TestLoadClaudeCodeMissingRoot(t *testing.T) {
	if _, _, err := loadClaudeCode(filepath.Join(t.TempDir(), "absent"), nil, ""); err == nil {
		t.Fatal("expected an error for a missing Claude projects directory")
	}
}

// TestLoadClaudeCodeScanErrorIsReported is the re-eval F01 companion: a
// transcript with an oversized line must error, not return one record and a
// watermark as if the scan were complete.
func TestLoadClaudeCodeScanErrorIsReported(t *testing.T) {
	root := filepath.Join(t.TempDir(), "projects")
	dir := filepath.Join(root, "-work-atlas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.jsonl"), []byte(
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:00.000Z","cwd":"/work/atlas","message":{"role":"user","content":"before"}}`+"\n"+
			`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:01.000Z","cwd":"/work/atlas","message":{"role":"user","content":"`+strings.Repeat("y", 33*1024*1024)+`"}}`+"\n"+
			`{"type":"user","sessionId":"s1","timestamp":"2026-09-01T10:00:02.000Z","cwd":"/work/atlas","message":{"role":"user","content":"after"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, wm, err := loadClaudeCode(root, nil, ""); err == nil {
		t.Fatal("interrupted transcript scan must surface an error")
	} else if wm != "" {
		t.Fatalf("watermark returned alongside a scan error: %q", wm)
	}
	if _, err := countClaudeCode(root, nil); err == nil {
		t.Fatal("count must also report the interrupted scan")
	}
}
