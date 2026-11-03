package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func runCompatRequests(t *testing.T, st *store.Store, profile string, requests []string) []JSONRPCResponse {
	t.Helper()
	var out bytes.Buffer
	srv := NewServer(st, strings.NewReader(strings.Join(requests, "\n")+"\n"), &out)
	if profile != "" {
		srv.SetProfile(profile)
	}
	if err := srv.Run(); err != nil {
		t.Fatalf("server run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("expected %d responses, got %d:\n%s", len(requests), len(lines), out.String())
	}
	responses := make([]JSONRPCResponse, 0, len(lines))
	for _, line := range lines {
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("unmarshal response: %v (%s)", err, line)
		}
		responses = append(responses, resp)
	}
	return responses
}

func TestMCPCompatibilityStringTypedArguments(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "compat.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject("/repo/compat"); err != nil {
		t.Fatal(err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"compatibility","limit":"3","semantic":"true"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":"compat","max_facts":"2","max_tokens":"2000","disable_codebase_map":"true"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"save_memo","arguments":{"project":"compat","content":"compatibility memo","files":"[\"cmd/sila/workspace.go\"]"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"get_status","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"recall","arguments":{"query":"compatibility","limit":null}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"not_a_tool","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"recall","arguments":"{\"query\":\"compatibility\",\"limit\":\"2\"}"}}`,
	}
	responses := runCompatRequests(t, st, ProfileContinuity, requests)

	for _, idx := range []int{1, 2, 3, 4, 7} {
		if responses[idx].Error != nil {
			t.Fatalf("request %d failed: %+v", idx, responses[idx].Error)
		}
	}
	if responses[5].Error == nil || responses[5].Error.Code != -32602 {
		t.Fatalf("null argument should be -32602, got %+v", responses[5].Error)
	}
	if responses[6].Error == nil || responses[6].Error.Code != -32601 {
		t.Fatalf("unknown tool should be -32601, got %+v", responses[6].Error)
	}

	initResult, ok := responses[0].Result.(map[string]any)
	if !ok {
		t.Fatalf("initialize result is not an object: %T", responses[0].Result)
	}
	caps, _ := initResult["capabilities"].(map[string]any)
	experimental, _ := caps["experimental"].(map[string]any)
	silaCaps, _ := experimental["sila"].(map[string]any)
	if silaCaps["schema_digest"] == "" || silaCaps["profile"] != ProfileContinuity {
		t.Fatalf("missing initialize capabilities: %+v", silaCaps)
	}

	statusResult, ok := responses[4].Result.(map[string]any)
	if !ok {
		t.Fatalf("get_status result is not an object: %T", responses[4].Result)
	}
	status, _ := statusResult["structuredData"].(map[string]any)
	if status["version"] == "" || status["profile"] != ProfileContinuity || status["schema_digest"] == "" {
		t.Fatalf("missing get_status handshake: %+v", status)
	}
}

func TestCommandCodeGoldenFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/commandcode/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 6 {
		t.Fatalf("expected at least 6 fixtures, got %d", len(files))
	}
	sort.Strings(files)
	st, err := store.Open(filepath.Join(t.TempDir(), "fixtures.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject("/repo/compat"); err != nil {
		t.Fatal(err)
	}

	requests := make([]string, 0, len(files))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		var fixture struct {
			Source  string          `json:"source"`
			Reason  string          `json:"reason"`
			Request json.RawMessage `json:"request"`
		}
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		if fixture.Source == "" || fixture.Reason == "" || len(fixture.Request) == 0 {
			t.Fatalf("%s is missing source, reason, or request", file)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, fixture.Request); err != nil {
			t.Fatalf("%s request is not valid JSON: %v", file, err)
		}
		requests = append(requests, compact.String())
	}
	responses := runCompatRequests(t, st, ProfileContinuity, requests)
	for i, resp := range responses {
		if resp.Error != nil {
			t.Fatalf("fixture %s failed: %+v", files[i], resp.Error)
		}
	}
}
