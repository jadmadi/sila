package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestConnector_QueryD1_MockAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-cf-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/accounts/test-account-id/d1/database/test-db-id/query" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		resp := map[string]any{
			"success": true,
			"errors":  []any{},
			"result": []map[string]any{
				{
					"success": true,
					"meta": map[string]any{
						"duration":     1.5,
						"rows_read":    2,
						"rows_written": 0,
					},
					"results": []map[string]any{
						{"id": "msg_001", "role": "user", "content": "hello world"},
						{"id": "msg_002", "role": "assistant", "content": "hi there"},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	conn := NewConnector("test-cf-token", "test-account-id", server.Client())
	conn.SetBaseURL(server.URL)

	res, err := conn.QueryD1(context.Background(), "test-db-id", "SELECT id, role, content FROM messages LIMIT 2", nil, "")
	if err != nil {
		t.Fatalf("QueryD1 failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success=true, got false")
	}
	if res.RowsCount != 2 {
		t.Errorf("expected 2 rows, got %d", res.RowsCount)
	}
	if res.Provider != "cloudflare-api" {
		t.Errorf("expected provider 'cloudflare-api', got %q", res.Provider)
	}
}

func TestConnector_QueryD1_DestructiveQueryBlocked(t *testing.T) {
	conn := NewConnector("token", "account", nil)
	_, err := conn.QueryD1(context.Background(), "db", "DROP DATABASE production;", nil, "")
	if err == nil {
		t.Errorf("expected DROP DATABASE to be rejected by sentry, got nil error")
	}
}

func TestConnector_QueryD1_EmptyQuery(t *testing.T) {
	conn := NewConnector("token", "account", nil)
	_, err := conn.QueryD1(context.Background(), "db", "   ", nil, "")
	if err == nil {
		t.Errorf("expected empty query to return error")
	}
}

func TestConnector_QueryD1_OfflineFallback(t *testing.T) {
	tempDir := t.TempDir()
	drizzleDir := filepath.Join(tempDir, "drizzle")
	_ = os.MkdirAll(drizzleDir, 0755)
	migrationSQL := `
CREATE TABLE IF NOT EXISTS "conversations" (
	"id" text PRIMARY KEY NOT NULL,
	"created_at" integer NOT NULL
);
CREATE TABLE IF NOT EXISTS "messages" (
	"id" text PRIMARY KEY NOT NULL,
	"role" text NOT NULL
);
`
	_ = os.WriteFile(filepath.Join(drizzleDir, "0000_init.sql"), []byte(migrationSQL), 0644)

	// Connector without credentials triggers offline fallback
	conn := NewConnector("", "", nil)
	res, err := conn.QueryD1(context.Background(), "local-db", "SELECT name FROM sqlite_master WHERE type='table'", nil, tempDir)
	if err != nil {
		t.Fatalf("offline QueryD1 failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success=true")
	}
	if res.Provider != "local-wrangler-schema" {
		t.Errorf("expected local-wrangler-schema, got %s", res.Provider)
	}
	if res.RowsCount < 2 {
		t.Errorf("expected at least 2 tables, got %d", res.RowsCount)
	}
}

func TestConnector_GetWorkerStatus_MockAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		resp := map[string]any{
			"success": true,
			"result": map[string]any{
				"id":                 "sila-edge-bridge",
				"tag":                "v1.4.2",
				"compatibility_date": "2026-09-01",
				"routes":             []string{"api.sila.dev/*"},
				"modified_on":        "2026-09-23T12:00:00Z",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	conn := NewConnector("test-token", "test-acc", server.Client())
	conn.SetBaseURL(server.URL)

	status, err := conn.GetWorkerStatus(context.Background(), "sila-edge-bridge", "")
	if err != nil {
		t.Fatalf("GetWorkerStatus failed: %v", err)
	}
	if !status.Deployed {
		t.Errorf("expected deployed=true")
	}
	if status.ActiveVersion != "v1.4.2" {
		t.Errorf("expected version v1.4.2, got %s", status.ActiveVersion)
	}
	if status.Provider != "cloudflare-api" {
		t.Errorf("expected provider cloudflare-api, got %s", status.Provider)
	}
}

func TestConnector_GetWorkerStatus_Offline(t *testing.T) {
	tempDir := t.TempDir()
	wranglerContent := `{
	"name": "test-worker",
	"main": "src/index.ts",
	"compatibility_date": "2026-09-15",
	"d1_databases": [
		{
			"binding": "DB",
			"database_name": "test_db",
			"database_id": "11111111-2222-3333-4444-555555555555"
		}
	]
}`
	_ = os.WriteFile(filepath.Join(tempDir, "wrangler.jsonc"), []byte(wranglerContent), 0644)

	conn := NewConnector("", "", nil)
	status, err := conn.GetWorkerStatus(context.Background(), "", tempDir)
	if err != nil {
		t.Fatalf("GetWorkerStatus offline failed: %v", err)
	}
	if status.ScriptName != "test-worker" {
		t.Errorf("expected script name 'test-worker', got %s", status.ScriptName)
	}
	if status.CompatibilityDate != "2026-09-15" {
		t.Errorf("expected compatibility date 2026-09-15, got %s", status.CompatibilityDate)
	}
	if len(status.Bindings) != 1 || status.Bindings[0].Name != "DB" {
		t.Errorf("expected 1 binding with Name DB, got %+v", status.Bindings)
	}
}

func TestConnector_GetEdgeTelemetry(t *testing.T) {
	conn := NewConnector("", "", nil)
	telem, err := conn.GetEdgeTelemetry(context.Background(), "my-worker", "24h", "")
	if err != nil {
		t.Fatalf("GetEdgeTelemetry failed: %v", err)
	}
	if telem.ScriptName != "my-worker" {
		t.Errorf("expected script name my-worker, got %s", telem.ScriptName)
	}
	if telem.RequestsTotal <= 0 {
		t.Errorf("expected positive request count, got %d", telem.RequestsTotal)
	}
	if telem.CPUTimeP50Ms <= 0 {
		t.Errorf("expected valid CPU time metric, got %f", telem.CPUTimeP50Ms)
	}
}

func TestResolveCredentials(t *testing.T) {
	os.Setenv("CLOUDFLARE_API_TOKEN", "env-cf-token")
	os.Setenv("CLOUDFLARE_ACCOUNT_ID", "env-cf-acc")
	defer func() {
		os.Unsetenv("CLOUDFLARE_API_TOKEN")
		os.Unsetenv("CLOUDFLARE_ACCOUNT_ID")
	}()

	tok, acc, _ := ResolveCredentials("")
	if tok != "env-cf-token" {
		t.Errorf("expected env-cf-token, got %s", tok)
	}
	if acc != "env-cf-acc" {
		t.Errorf("expected env-cf-acc, got %s", acc)
	}
}
