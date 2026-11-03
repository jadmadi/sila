package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

func TestParseConversationTurns(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []Turn
	}{
		{
			name:    "single unannotated block",
			content: "Here is an explanation of the architecture.",
			expected: []Turn{
				{Role: "assistant", Content: "Here is an explanation of the architecture."},
			},
		},
		{
			name: "multi-turn markdown headers",
			content: `### User:
How do I run sila gate?

### Assistant:
Run './build.sh' followed by './sila gate'.

### User:
Thank you!`,
			expected: []Turn{
				{Role: "user", Content: "How do I run sila gate?"},
				{Role: "assistant", Content: "Run './build.sh' followed by './sila gate'."},
				{Role: "user", Content: "Thank you!"},
			},
		},
		{
			name: "bold markdown markers",
			content: `**User**: Explain SQLite WAL mode.
**Model**: WAL allows concurrent readers and one writer.`,
			expected: []Turn{
				{Role: "user", Content: "Explain SQLite WAL mode."},
				{Role: "assistant", Content: "WAL allows concurrent readers and one writer."},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			turns := ParseConversationTurns(tc.content)
			if len(turns) != len(tc.expected) {
				t.Fatalf("expected %d turns, got %d", len(tc.expected), len(turns))
			}
			for i, exp := range tc.expected {
				if turns[i].Role != exp.Role {
					t.Errorf("turn %d: expected role %q, got %q", i, exp.Role, turns[i].Role)
				}
				if turns[i].Content != exp.Content {
					t.Errorf("turn %d: expected content %q, got %q", i, exp.Content, turns[i].Content)
				}
			}
		})
	}
}

func setupMockMailboxServer(t *testing.T, token string) (*httptest.Server, *[]string) {
	t.Helper()
	var ackedIDs []string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health check
		if r.URL.Path == "/health" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "sila-edge-bridge"})
			return
		}

		// Auth check for /api/
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
			return
		}

		if r.URL.Path == "/api/inbound/drain" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			items := []InboundItem{
				{
					ID:        "inbound_test_1",
					Project:   "test-proj",
					Title:     "Debugging SQLite WAL",
					Source:    "gemini",
					Content:   "### User:\nHow to optimize SQLite?\n### Assistant:\nUse PRAGMA mmap_size.",
					Status:    "pending",
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
				},
			}
			_ = json.NewEncoder(w).Encode(DrainResponse{
				Items:        items,
				Count:        len(items),
				SessionToken: "test_drain_session_xyz",
			})
			return
		}

		if r.URL.Path == "/api/inbound/ack" && r.Method == http.MethodPost {
			var payload struct {
				IDs          []string `json:"ids"`
				SessionToken string   `json:"session_token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			ackedIDs = append(ackedIDs, payload.IDs...)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(AckResponse{Success: true, Acknowledged: len(payload.IDs)})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	})

	server := httptest.NewTestServer(t, handler)
	return server, &ackedIDs
}

func TestClientStatusAndDrain(t *testing.T) {
	token := "test-secret-token"
	server, acked := setupMockMailboxServer(t, token)
	defer server.Close()

	client := NewClient(server.URL, token, server.Client())

	// Test Status
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.Status != "ok" || status.Service != "sila-edge-bridge" {
		t.Errorf("unexpected status response: %+v", status)
	}
	if status.PendingCount != 1 {
		t.Errorf("expected 1 pending item, got %d", status.PendingCount)
	}

	// Test DrainInbound
	items, err := client.DrainInbound(context.Background(), "test-proj", 10)
	if err != nil {
		t.Fatalf("DrainInbound failed: %v", err)
	}
	if len(items) != 1 || items[0].ID != "inbound_test_1" {
		t.Fatalf("unexpected drain items: %+v", items)
	}

	// Test DrainInboundWithSession
	itemsWithSess, sessionToken, err := client.DrainInboundWithSession(context.Background(), "test-proj", 10)
	if err != nil {
		t.Fatalf("DrainInboundWithSession failed: %v", err)
	}
	if sessionToken != "test_drain_session_xyz" {
		t.Errorf("expected session token 'test_drain_session_xyz', got %q", sessionToken)
	}
	if len(itemsWithSess) != 1 {
		t.Errorf("expected 1 item, got %d", len(itemsWithSess))
	}

	// Test AckInboundWithSession
	ackedCount, err := client.AckInboundWithSession(context.Background(), []string{items[0].ID}, sessionToken)
	if err != nil {
		t.Fatalf("AckInboundWithSession failed: %v", err)
	}
	if ackedCount != 1 {
		t.Errorf("expected 1 acknowledged item, got %d", ackedCount)
	}
	if len(*acked) != 1 || (*acked)[0] != "inbound_test_1" {
		t.Errorf("server did not record ack for item: %+v", *acked)
	}
}

func TestDigesterEndToEnd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_cloud_digest.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer st.Close()

	token := "valid-token"
	server, acked := setupMockMailboxServer(t, token)
	defer server.Close()

	client := NewClient(server.URL, token, server.Client())
	digester := NewDigester(st)

	// First pass: drains, ingests into messages, and acks
	summary, err := digester.DrainAndDigest(context.Background(), client, "test-proj", 10)
	if err != nil {
		t.Fatalf("DrainAndDigest failed: %v", err)
	}
	if summary.TotalFetched != 1 || summary.IngestedMessages != 2 || summary.AckedItems != 1 {
		t.Fatalf("unexpected summary on first pass: %+v", summary)
	}
	if len(*acked) != 1 {
		t.Errorf("expected 1 acked item, got %d", len(*acked))
	}

	// Verify message in SQLite store
	messages, err := st.RecentMessages("test-proj", 10)
	if err != nil {
		t.Fatalf("RecentMessages failed: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages in SQLite, got %d", len(messages))
	}

	// Second pass: idempotent re-ingest of same content yields 0 new messages
	summary2, err := digester.DrainAndDigest(context.Background(), client, "test-proj", 10)
	if err != nil {
		t.Fatalf("Second DrainAndDigest failed: %v", err)
	}
	if summary2.IngestedMessages != 0 {
		t.Errorf("expected 0 new messages on re-ingest, got %d", summary2.IngestedMessages)
	}
	if summary2.SkippedMessages != 2 {
		t.Errorf("expected 2 skipped/merged messages, got %d", summary2.SkippedMessages)
	}
}

func TestActiveCloudConsentGate(t *testing.T) {
	// 1. When config is nil or disabled -> ActiveCloudWithConfigFor returns nil
	if c := ActiveCloudWithConfigFor(nil, "pull", "my-project"); c != nil {
		t.Fatal("ActiveCloudWithConfigFor must return nil when cfg is nil")
	}

	cfg := &config.Config{
		CloudEnabled:  false,
		CloudEndpoint: "https://edge.example.com",
		CloudToken:    "secret",
	}
	if c := ActiveCloudWithConfigFor(cfg, "pull", "my-project"); c != nil {
		t.Fatal("ActiveCloudWithConfigFor must return nil when CloudEnabled=false")
	}

	// 2. When enabled but scope missing -> returns nil
	cfg.CloudEnabled = true
	cfg.CloudScopes = []string{"push"} // only push consented
	if c := ActiveCloudWithConfigFor(cfg, "pull", "my-project"); c != nil {
		t.Fatal("ActiveCloudWithConfigFor must return nil when requested scope is not consented")
	}

	// 3. When scope consented but project not allowed -> returns nil
	cfg.CloudScopes = []string{"pull"}
	cfg.CloudProjects = []string{"other-project"}
	if c := ActiveCloudWithConfigFor(cfg, "pull", "my-project"); c != nil {
		t.Fatal("ActiveCloudWithConfigFor must return nil when project is not in allowed projects")
	}

	// 4. When missing token or endpoint -> returns nil
	cfg.CloudProjects = []string{"my-project"}
	cfg.CloudToken = ""
	if c := ActiveCloudWithConfigFor(cfg, "pull", "my-project"); c != nil {
		t.Fatal("ActiveCloudWithConfigFor must return nil when CloudToken is empty")
	}

	// 5. Fully consented -> returns Client
	cfg.CloudToken = "valid-token"
	client := ActiveCloudWithConfigFor(cfg, "pull", "my-project")
	if client == nil {
		t.Fatal("ActiveCloudWithConfigFor must return valid Client when fully consented")
	}
	if client.endpoint != "https://edge.example.com" {
		t.Errorf("expected endpoint %q, got %q", "https://edge.example.com", client.endpoint)
	}
}

func TestClientTimeoutWithSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(10 * time.Second)
			w.WriteHeader(http.StatusOK)
		})
		server := httptest.NewTestServer(t, handler)
		client := NewClient(server.URL, "token", server.Client())

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		_, err := client.Status(ctx)
		if err == nil {
			t.Fatal("expected timeout error in synctest bubble, got nil")
		}
	})
}
