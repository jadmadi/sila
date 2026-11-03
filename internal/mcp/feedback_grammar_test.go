package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestRecordFeedback_ExplicitIDAndEpistemicGrammar(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// 1. Test explicit ID
	req1 := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"record_feedback","arguments":{"id":"dialectic-subshell-paths","content":"Explicit ticket id test","category":"idea","project":"sila","agent":"antigravity"}}}`
	in1 := strings.NewReader(req1 + "\n")
	var out1 bytes.Buffer
	srv1 := NewServer(st, in1, &out1)
	if err := srv1.Run(); err != nil {
		t.Fatal(err)
	}

	fbCustom, err := st.GetFeedback("fb_dialectic-subshell-paths")
	if err != nil {
		t.Fatalf("expected fb_dialectic-subshell-paths in store: %v", err)
	}
	if fbCustom.Category != "idea" {
		t.Errorf("expected category idea, got %s", fbCustom.Category)
	}

	// 2. Test epistemic grammar derived ID
	req2 := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"record_feedback","arguments":{"content":"Sentry toolchain integrity in pre-push hook","category":"issue","project":"sila","agent":"devin"}}}`
	in2 := strings.NewReader(req2 + "\n")
	var out2 bytes.Buffer
	srv2 := NewServer(st, in2, &out2)
	if err := srv2.Run(); err != nil {
		t.Fatal(err)
	}

	var resp2 JSONRPCResponse
	if err := json.Unmarshal(out2.Bytes(), &resp2); err != nil {
		t.Fatalf("unmarshal resp2: %v", err)
	}
	if resp2.Error != nil {
		t.Fatalf("resp2 returned error: %+v", resp2.Error)
	}

	items, err := st.ListFeedback("sila", "issue", 10)
	if err != nil {
		t.Fatalf("ListFeedback: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(items))
	}
	if !strings.HasPrefix(items[0].ID, "fb_sentry-") {
		t.Errorf("expected ID prefix fb_sentry-, got %s", items[0].ID)
	}

	// 3. Test record_feedback with share: true
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "fb_remote_mcp_123",
			"status":  "pending",
			"message": "Received",
		})
	}))
	defer ts.Close()
	t.Setenv("SILA_FEEDBACK_URL", ts.URL)

	req3 := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"record_feedback","arguments":{"content":"Share from MCP tool test","category":"tip","project":"sila","share":true}}}`
	in3 := strings.NewReader(req3 + "\n")
	var out3 bytes.Buffer
	srv3 := NewServer(st, in3, &out3)
	if err := srv3.Run(); err != nil {
		t.Fatal(err)
	}

	var resp3 JSONRPCResponse
	if err := json.Unmarshal(out3.Bytes(), &resp3); err != nil {
		t.Fatalf("unmarshal resp3: %v", err)
	}
	if resp3.Error != nil {
		t.Fatalf("resp3 error: %+v", resp3.Error)
	}
	tipItems, err := st.ListFeedback("sila", "tip", 10)
	if err != nil || len(tipItems) != 1 {
		t.Fatalf("expected tip feedback saved in store: %v", err)
	}
	if !strings.Contains(tipItems[0].ResolutionNote, "fb_remote_mcp_123") {
		t.Errorf("expected ResolutionNote to record remote edge ID: %s", tipItems[0].ResolutionNote)
	}
}
