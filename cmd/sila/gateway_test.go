package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestGatewayCLI_Unconfigured(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("SILA_GATEWAY_ENABLED", "false")
	t.Setenv("SILA_GATEWAY_DISCORD_WEBHOOK", "")
	t.Setenv("SILA_GATEWAY_TELEGRAM_TOKEN", "")

	ui := render.New(os.Stdout, true)
	dbPath := filepath.Join(tempHome, "test_gateway.db")

	// 1. Status (standard, json, toon)
	runGateway(ui, dbPath, "sila", false, false, []string{"status"})
	runGateway(ui, dbPath, "sila", true, false, []string{"status"})
	runGateway(ui, dbPath, "sila", false, true, []string{"status"})

	// 2. Test unconfigured
	runGateway(ui, dbPath, "sila", false, false, []string{"test"})
	runGateway(ui, dbPath, "sila", true, false, []string{"test"})
	runGateway(ui, dbPath, "sila", false, true, []string{"test"})

	// 3. Help
	runGateway(ui, dbPath, "sila", false, false, []string{"help"})
}

func TestGatewayCLI_Configured(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"jsonrpc": "2.0",
			"result":  "pong",
		})
	}))
	defer srv.Close()

	t.Setenv("SILA_GATEWAY_ENABLED", "true")
	t.Setenv("SILA_GATEWAY_DISCORD_ENABLED", "true")
	t.Setenv("SILA_GATEWAY_DISCORD_WEBHOOK", srv.URL)
	t.Setenv("SILA_GATEWAY_MCP_ENABLED", "true")
	t.Setenv("SILA_GATEWAY_MCP_ENDPOINT", srv.URL)

	ui := render.New(os.Stdout, true)
	dbPath := filepath.Join(tempHome, "test_gateway.db")

	// Status
	runGateway(ui, dbPath, "sila", false, false, []string{"status"})
	runGateway(ui, dbPath, "sila", true, false, []string{"status"})
	runGateway(ui, dbPath, "sila", false, true, []string{"status"})

	// Test all
	runGateway(ui, dbPath, "sila", false, false, []string{"test"})
	runGateway(ui, dbPath, "sila", true, false, []string{"test"})
	runGateway(ui, dbPath, "sila", false, true, []string{"test"})

	// Test specific surface
	runGateway(ui, dbPath, "sila", false, false, []string{"test", "discord"})
	runGateway(ui, dbPath, "sila", true, false, []string{"test", "discord"})

	// Send notification
	runGateway(ui, dbPath, "sila", false, false, []string{"send", "--title=Deploy Succeeded", "--body=Substrate ready", "--level=success"})
	runGateway(ui, dbPath, "sila", true, false, []string{"send", "--title=Deploy Succeeded", "--body=Substrate ready", "--level=success"})
	runGateway(ui, dbPath, "sila", false, true, []string{"send", "--title=Deploy Succeeded", "--body=Substrate ready", "--level=success"})
}
