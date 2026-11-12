package mcp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func writeRequest(t *testing.T, project, content string) JSONRPCRequest {
	t.Helper()
	args, err := json.Marshal(map[string]any{"name": "save_memo", "arguments": map[string]any{
		"project": project, "content": content,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: args}
}

func TestAnonymousWriteRejected(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t.Setenv("SILA_AGENT", "")
	t.Setenv("SILA_ALLOW_ANONYMOUS", "")
	var out bytes.Buffer
	srv := NewServer(st, nil, &out)
	srv.handleToolCall(writeRequest(t, "", "anonymous attempt"))
	if !strings.Contains(out.String(), "agent identity required: set SILA_AGENT") {
		t.Fatalf("response = %s", out.String())
	}
	records, _ := st.ListMemoryRecords(store.MemoryListOptions{Limit: 10})
	if len(records) != 0 {
		t.Fatalf("anonymous write created %d records", len(records))
	}
}

func TestAnonymousWriteAllowedByEnv(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	project, err := st.ResolveScope(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SILA_AGENT", "")
	t.Setenv("SILA_ALLOW_ANONYMOUS", "1")
	var out bytes.Buffer
	srv := NewServer(st, nil, &out)
	srv.handleToolCall(writeRequest(t, project.ID, "allowed anonymous directive"))
	if strings.Contains(out.String(), "agent identity required") {
		t.Fatalf("guard still fired: %s", out.String())
	}
	records, _ := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: project.ID, Limit: 10})
	if len(records) == 0 {
		t.Fatalf("allowed write created no records: %s", out.String())
	}
}
