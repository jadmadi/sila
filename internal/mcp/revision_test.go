package mcp

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func revisionServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	project, err := st.ResolveScope(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	return NewServer(st, nil, &out), st, project.ID
}

func callTool(t *testing.T, srv *Server, name string, args map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	srv.out = &out
	srv.handleToolCall(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params})
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &response); err != nil {
		t.Fatalf("response: %v\n%s", err, out.String())
	}
	return response
}

func TestRevisionSchemaFields(t *testing.T) {
	want := map[string]string{
		"save_handoff": "expected_revision", "memory_archive": "expected_revision",
		"memory_restore": "expected_revision", "get_latest_handoff": "revision_hash", "get_memory": "revision_hash",
	}
	for _, tool := range ToolDefinitions() {
		field, ok := want[tool.Name]
		if !ok {
			continue
		}
		if _, ok := tool.InputSchema.Properties[field]; !ok {
			t.Fatalf("%s lacks %s", tool.Name, field)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

func TestGetMemoryReturnsRevisionHash(t *testing.T) {
	srv, st, _ := revisionServer(t)
	now := time.Now().UTC().Format(time.RFC3339)
	env := store.MemoryEnvelope{SchemaVersion: store.MemorySchemaVersion, ProjectID: "p", Kind: "memo", Title: "t", Summary: "s"}
	raw, _ := json.Marshal(env)
	if _, err := st.DB.Exec(`INSERT INTO memory_records (id, project_id, fact_id, kind, title, summary, digest, envelope_json, state, created_at, updated_at)
		VALUES ('m1','p','f1','memo','t','s','d',?, 'current', ?, ?)`, string(raw), now, now); err != nil {
		t.Fatal(err)
	}
	response := callTool(t, srv, "get_memory", map[string]any{"id": "m1"})
	text := response["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"revision_hash"`) {
		t.Fatalf("text = %s", text)
	}
}

func TestSaveHandoffRevisionConflict(t *testing.T) {
	srv, st, projectID := revisionServer(t)
	seed := store.Handoff{ID: "h1", ProjectID: projectID, Agent: "a", Branch: "main", Summary: "first", Status: "open"}
	if err := st.InsertHandoff(seed); err != nil {
		t.Fatal(err)
	}
	response := callTool(t, srv, "save_handoff", map[string]any{
		"project": projectID, "summary": "second", "branch": "main", "expected_revision": "deadbeef",
	})
	errObj, ok := response["error"].(map[string]any)
	if !ok || errObj["code"].(float64) != -32001 {
		t.Fatalf("response = %+v", response)
	}
	if data, _ := errObj["data"].(string); !strings.Contains(data, "current_revision") {
		t.Fatalf("data = %v", errObj["data"])
	}
}
