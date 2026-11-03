package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestTraceToolCallIsMetadataOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SILA_MCP_TRACE_ARGS", "")

	st, err := store.Open(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	req := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"recall","arguments":{"query":"super-secret-query","limit":"2"}}}` + "\n"
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(req), &out)
	srv.SetProfile(ProfileContinuity)
	srv.SetTraceArgs(true)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, ".local", "share", "sila", "mcp-trace.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "super-secret-query") {
		t.Fatalf("trace leaked the query value: %s", text)
	}
	if !strings.Contains(text, `"tool":"recall"`) {
		t.Fatalf("trace missing tool: %s", text)
	}
	if !strings.Contains(text, `"limit":"number"`) || !strings.Contains(text, `"query":"string"`) {
		t.Fatalf("trace missing argument types: %s", text)
	}
	var record traceRecord
	if err := json.Unmarshal(bytes.TrimSpace(data), &record); err != nil {
		t.Fatalf("trace line is not JSON: %v", err)
	}
	if record.RawShape != "object" || record.Outcome != "ok" {
		t.Fatalf("unexpected trace record: %+v", record)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("trace mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestTraceSecretLikeKeysAreRedacted(t *testing.T) {
	raw := json.RawMessage(`{"api_key":"abc","query":"x"}`)
	if got := traceKeySummary(raw); got != "query,redacted" {
		t.Fatalf("traceKeySummary = %q, want redacted,query", got)
	}
}
