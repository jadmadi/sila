package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestSalamAndPrimeContextOnUnregisteredProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0"}}}`

	newStore := func(t *testing.T) *store.Store {
		t.Helper()
		st, err := store.Open(filepath.Join(t.TempDir(), "unregistered.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}

	newRepo := func(t *testing.T) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "unregistered-project")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "-C", dir, "init", "-q")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		return dir
	}

	run := func(t *testing.T, st *store.Store, requests ...string) []string {
		t.Helper()
		var out bytes.Buffer
		srv := NewServer(st, strings.NewReader(strings.Join(requests, "\n")+"\n"), &out)
		if err := srv.Run(); err != nil {
			t.Fatalf("Server.Run: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != len(requests) {
			t.Fatalf("expected %d responses, got %d:\n%s", len(requests), len(lines), out.String())
		}
		return lines
	}

	assertToolResult := func(t *testing.T, raw string) {
		t.Helper()
		var resp struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("unmarshal response: %v (%s)", err, raw)
		}
		if resp.Error != nil {
			t.Fatalf("unexpected JSON-RPC error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if len(resp.Result) == 0 {
			t.Fatalf("expected tool result, got %s", raw)
		}
	}

	t.Run("salam_with_unregistered_path", func(t *testing.T) {
		st := newStore(t)
		repo := newRepo(t)
		req := fmt.Sprintf(
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"salam","arguments":{"project":%q}}}`,
			repo,
		)
		responses := run(t, st, initialize, req)
		assertToolResult(t, responses[1])
	})

	t.Run("salam_with_bare_slug_from_cwd", func(t *testing.T) {
		st := newStore(t)
		repo := newRepo(t)
		t.Chdir(repo)
		slug := store.ProjectID(repo)
		req := fmt.Sprintf(
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"salam","arguments":{"project":%q}}}`,
			slug,
		)
		responses := run(t, st, initialize, req)
		assertToolResult(t, responses[1])
	})

	t.Run("prime_context_with_empty_project", func(t *testing.T) {
		st := newStore(t)
		repo := newRepo(t)
		t.Chdir(repo)
		req := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"prime_context","arguments":{}}}`
		responses := run(t, st, initialize, req)
		assertToolResult(t, responses[1])
	})

	t.Run("prime_context_with_bare_slug_from_cwd", func(t *testing.T) {
		st := newStore(t)
		repo := newRepo(t)
		t.Chdir(repo)
		slug := store.ProjectID(repo)
		req := fmt.Sprintf(
			`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":%q}}}`,
			slug,
		)
		responses := run(t, st, initialize, req)
		assertToolResult(t, responses[1])
	})
}
