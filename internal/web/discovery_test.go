package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
)

func TestWebMCPDiscoveryEndpoints(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	cfg := Config{
		Host:       "127.0.0.1",
		Port:       0,
		ProjectDir: tmpDir,
		ProjectID:  "test-project",
		NoOpen:     true,
		Store:      st,
	}

	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	handler := srv.Handler()

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	// 1. Verify /.well-known/mcp/server-card.json
	t.Run("ServerCard", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/.well-known/mcp/server-card.json", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET server-card failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		linkHeader := resp.Header.Get("Link")
		if !strings.Contains(linkHeader, "mcp-server-card") {
			t.Errorf("expected mcp-server-card in Link header, got: %s", linkHeader)
		}

		var card mcp.ServerCard
		if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
			t.Fatalf("decode server card failed: %v", err)
		}
		if card.Name != "sila" {
			t.Errorf("card.Name = %s, want sila", card.Name)
		}
	})

	// 2. Verify RFC 9727 linkset
	t.Run("Linkset", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/.well-known/linkset.json", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET linkset.json failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}

		var linkset mcp.LinksetDocument
		if err := json.NewDecoder(resp.Body).Decode(&linkset); err != nil {
			t.Fatalf("decode linkset failed: %v", err)
		}
		if len(linkset.Linkset) == 0 {
			t.Fatal("expected at least one linkset entry")
		}
	})

	// 3. Verify /llms.txt
	t.Run("LLMsTxt", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/llms.txt", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET llms.txt failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/markdown") {
			t.Errorf("Content-Type = %s, want text/markdown", ct)
		}
	})

	// 4. Verify Link header in /api/status
	t.Run("StatusLinkHeader", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/status failed: %v", err)
		}
		defer resp.Body.Close()

		linkHeader := resp.Header.Get("Link")
		if !strings.Contains(linkHeader, "rel=\"mcp-server-card\"") {
			t.Errorf("expected rel=\"mcp-server-card\" on /api/status, got: %s", linkHeader)
		}
	})
}
