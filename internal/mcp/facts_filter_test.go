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

func factFilterServer(t *testing.T) (*Server, *store.Store, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dir := t.TempDir()
	project, err := st.ResolveScope(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	insert := func(id string, refs int) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, file_refs_exist, repo_files, tools, session_ref,
			first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, kind)
			VALUES (?, ?, ?, 'grounded', ?, '["a.go"]', '[]', 's', ?, ?, ?, ?, ?, 'code_claim')`,
			id, project.ID, "claim-"+id, refs, now, now, now, "hash-"+id, store.GenesisHash); err != nil {
			t.Fatal(err)
		}
	}
	insert("v1", 1)
	insert("u1", 0)
	var out bytes.Buffer
	return NewServer(st, nil, &out), st, project.ID
}

func TestFactReadSchemasDocumentFlags(t *testing.T) {
	tools := ToolDefinitions()
	byName := map[string]Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"recall", "search_knowledge", "get_project_facts", "omni_search", "prime_context"} {
		tool, ok := byName[name]
		if !ok {
			t.Fatalf("%s missing", name)
		}
		for _, flag := range []string{"include_unverified", "include_stale", "include_disputed"} {
			if _, ok := tool.InputSchema.Properties[flag]; !ok {
				t.Fatalf("%s lacks %s", name, flag)
			}
		}
	}
}

func TestGetProjectFactsDefaultsToVerified(t *testing.T) {
	srv, st, projectID := factFilterServer(t)
	args, _ := json.Marshal(map[string]any{"name": "get_project_facts", "arguments": map[string]any{"project": projectID}})
	var out bytes.Buffer
	srv.out = &out
	srv.handleToolCall(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: args})
	response := out.String()
	if !strings.Contains(response, "hidden: unverified=1") {
		t.Fatalf("response = %s", response)
	}
	if strings.Contains(response, `"id": "u1"`) {
		t.Fatalf("unverified row leaked: %s", response)
	}
	_ = st
}

func TestGetProjectFactsAcceptsDeprecatedFlags(t *testing.T) {
	srv, _, projectID := factFilterServer(t)
	args, _ := json.Marshal(map[string]any{"name": "get_project_facts", "arguments": map[string]any{
		"project": projectID, "verified_only": true, "refs_exist_only": true,
	}})
	var out bytes.Buffer
	srv.out = &out
	srv.handleToolCall(JSONRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: args})
	if strings.Contains(out.String(), `"error"`) {
		t.Fatalf("deprecated flags errored: %s", out.String())
	}
	if !strings.Contains(out.String(), "hidden: unverified=1") {
		t.Fatalf("missing hidden counts: %s", out.String())
	}
}
