package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/man"
)

func runResourceRequests(t *testing.T, srv *Server, requests []string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	srv.in = in
	srv.out = &out
	if err := srv.Run(); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("run: %v", err)
	}
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var response map[string]any
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("response line: %v\n%s", err, line)
		}
		responses = append(responses, response)
	}
	return responses
}

func TestMCPResources(t *testing.T) {
	srv, _, _ := revisionServer(t)
	srv.SetProfile(ProfileContinuity)
	responses := runResourceRequests(t, srv, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"sila://man/gate"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"sila://man/does-not-exist"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/list"}`,
	})
	if len(responses) != 5 {
		t.Fatalf("responses = %d", len(responses))
	}
	initResult := responses[0]["result"].(map[string]any)
	capabilities := initResult["capabilities"].(map[string]any)
	resources, ok := capabilities["resources"].(map[string]any)
	if !ok || resources["listChanged"] != false {
		t.Fatalf("capabilities = %+v", capabilities)
	}
	listResult := responses[1]["result"].(map[string]any)
	entries := listResult["resources"].([]any)
	if len(entries) != len(man.List())+1 {
		t.Fatalf("resources = %d", len(entries))
	}
	readResult := responses[2]["result"].(map[string]any)
	contents := readResult["contents"].([]any)[0].(map[string]any)
	page, _ := man.Lookup("gate")
	if contents["text"] != page.Body {
		t.Fatalf("read text mismatch")
	}
	unknown := responses[3]["error"].(map[string]any)
	if unknown["code"].(float64) != -32602 || !strings.Contains(unknown["message"].(string), "unknown man topic") {
		t.Fatalf("unknown = %+v", unknown)
	}
	tools := responses[4]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 10 {
		t.Fatalf("continuity tools = %d", len(tools))
	}
}
