package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/cloud"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestCloudCLIUnconfigured(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("SILA_CLOUD_ENABLED", "false")
	t.Setenv("SILA_CLOUD_ENDPOINT", "")
	t.Setenv("SILA_CLOUD_AUTH_TOKEN", "")

	ui := render.New(os.Stdout, true)
	dbPath := filepath.Join(tempHome, "test_unconfigured.db")

	// 1. Status when disabled (standard, json, toon)
	runCloud(ui, dbPath, "test-proj", 10, false, false, []string{"status"})
	runCloud(ui, dbPath, "test-proj", 10, true, false, []string{"status"})
	runCloud(ui, dbPath, "test-proj", 10, false, true, []string{"status"})

	// 2. Pull when disabled (standard, json, toon)
	runCloud(ui, dbPath, "test-proj", 10, false, false, []string{"pull"})
	runCloud(ui, dbPath, "test-proj", 10, true, false, []string{"pull"})
	runCloud(ui, dbPath, "test-proj", 10, false, true, []string{"pull"})

	// 3. Push when disabled (standard, json, toon)
	runCloud(ui, dbPath, "test-proj", 10, false, false, []string{"push"})
	runCloud(ui, dbPath, "test-proj", 10, true, false, []string{"push"})
	runCloud(ui, dbPath, "test-proj", 10, false, true, []string{"push"})

	// 4. Help
	runCloud(ui, dbPath, "test-proj", 10, false, false, []string{"help"})
}

func TestCloudCLIConfigured(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	// Setup mock server
	token := "cli-secret-token"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "service": "sila-edge-bridge"})
			return
		}
		if r.URL.Path == "/api/inbound/drain" {
			w.Header().Set("Content-Type", "application/json")
			items := []cloud.InboundItem{
				{
					ID:        "inbound_cli_1",
					Project:   "test-proj",
					Title:     "CLI Cloud Ingestion",
					Source:    "gemini",
					Content:   "### User:\nExplain Sila Cloud.\n### Assistant:\nIt bridges D1 edge mailboxes.",
					Status:    "pending",
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
				},
			}
			_ = json.NewEncoder(w).Encode(cloud.DrainResponse{Items: items, Count: len(items)})
			return
		}
		if r.URL.Path == "/api/inbound/ack" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(cloud.AckResponse{Success: true, Acknowledged: 1})
			return
		}
		if r.URL.Path == "/api/sync/publish" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"published": map[string]int{
					"facts": 1,
					"memos": 1,
					"goals": 1,
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	t.Setenv("SILA_CLOUD_ENABLED", "true")
	t.Setenv("SILA_CLOUD_ENDPOINT", server.URL)
	t.Setenv("SILA_CLOUD_AUTH_TOKEN", token)
	t.Setenv("SILA_CLOUD_SCOPES", "status,pull,push")
	t.Setenv("SILA_CLOUD_PROJECTS", "*")

	// Persist config so ActiveCloudFor finds it
	cfgDir := filepath.Join(tempHome, ".local", "share", "sila")
	_ = os.MkdirAll(cfgDir, 0o700)
	cfg := &config.Config{
		CloudEnabled:  true,
		CloudEndpoint: server.URL,
		CloudToken:    token,
		CloudScopes:   []string{"status", "pull", "push"},
		CloudProjects: []string{"*"},
	}
	_ = config.Save(filepath.Join(cfgDir, "config.json"), cfg)

	ui := render.New(os.Stdout, true)
	dbPath := filepath.Join(tempHome, "test_cloud_configured.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	_, _ = st.InsertMemo("test-proj", "Sila Cloud bridges edge read replicas", "cli-test", "", []string{"cmd/cloud.go"})
	st.Close()

	// Status (standard, json, toon)
	runCloud(ui, dbPath, "test-proj", 10, false, false, []string{"status"})
	runCloud(ui, dbPath, "test-proj", 10, true, false, []string{"status"})
	runCloud(ui, dbPath, "test-proj", 10, false, true, []string{"status"})

	// Pull (standard, json, toon)
	runCloud(ui, dbPath, "test-proj", 10, false, false, []string{"pull"})
	runCloud(ui, dbPath, "test-proj", 10, true, false, []string{"pull"})
	runCloud(ui, dbPath, "test-proj", 10, false, true, []string{"pull"})

	// Push (standard, json, toon)
	runCloud(ui, dbPath, "test-proj", 10, false, false, []string{"push"})
	runCloud(ui, dbPath, "test-proj", 10, true, false, []string{"push"})
	runCloud(ui, dbPath, "test-proj", 10, false, true, []string{"push"})
}
