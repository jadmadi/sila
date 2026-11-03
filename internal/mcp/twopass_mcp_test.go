package mcp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestMCPSearchMessagesTwoPass(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "twopass_mcp.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject("/workspace/test-mcp-twopass")
	if err != nil {
		t.Fatal(err)
	}

	sess := "mcp-sess-1"
	msgs := []store.LoadMessage{
		{
			ProjectID:   p.ID,
			ContentHash: "hash-u1",
			TS:          "2026-09-01T10:00:00Z",
			Role:        "user",
			Content:     "how do we rotate API tokens safely?",
			Tool:        "devin",
			SessionID:   sess,
			SourceOrder: 1,
		},
		{
			ProjectID:   p.ID,
			ContentHash: "hash-a1",
			TS:          "2026-09-01T10:01:00Z",
			Role:        "assistant",
			Content:     "Use the CLI command: `vault token rotate`.\nAlways audit downstream services.",
			Tool:        "devin",
			SessionID:   sess,
			SourceOrder: 2,
		},
	}
	if _, _, err := st.WriteRun("", "devin", sess, msgs); err != nil {
		t.Fatal(err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_messages","arguments":{"query":"rotate API tokens","project":"test-mcp-twopass"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_messages","arguments":{"query":"rotate API tokens","project":"test-mcp-twopass","two_pass":true}}}`,
	}

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer

	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d:\n%s", len(lines), out.String())
	}

	// 1. Single-pass response
	var respSingle JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &respSingle); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respSingle.Error != nil {
		t.Fatalf("single pass error: %+v", respSingle.Error)
	}
	resM := respSingle.Result.(map[string]any)
	content := resM["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "rotate API tokens") {
		t.Fatalf("unexpected single-pass content: %s", text)
	}

	// 2. Two-pass response
	var respTwoPass JSONRPCResponse
	if err := json.Unmarshal([]byte(lines[1]), &respTwoPass); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if respTwoPass.Error != nil {
		t.Fatalf("two pass error: %+v", respTwoPass.Error)
	}
	resM2 := respTwoPass.Result.(map[string]any)
	content2 := resM2["content"].([]any)
	text2 := content2[0].(map[string]any)["text"].(string)
	if !strings.Contains(text2, "session_id") || !strings.Contains(text2, "user_prompt") || !strings.Contains(text2, "assistant_fix") {
		t.Fatalf("expected ResolvedExchange fields in two_pass output, got: %s", text2)
	}
	if !strings.Contains(text2, "how do we rotate API tokens safely?") {
		t.Fatalf("expected UserPrompt in output, got: %s", text2)
	}
	if !strings.Contains(text2, "vault token rotate") {
		t.Fatalf("expected AssistantFix in output, got: %s", text2)
	}
}
