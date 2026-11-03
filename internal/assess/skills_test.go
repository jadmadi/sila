package assess

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestEstimateTokens(t *testing.T) {
	if tok := EstimateTokens(0); tok != 35 {
		t.Errorf("EstimateTokens(0) = %d; want 35", tok)
	}
	if tok := EstimateTokens(-10); tok != 35 {
		t.Errorf("EstimateTokens(-10) = %d; want 35", tok)
	}
	// 370 bytes should yield ~100 tokens
	if tok := EstimateTokens(370); tok != 100 {
		t.Errorf("EstimateTokens(370) = %d; want 100", tok)
	}
}

func TestDiscoverRegisteredTools(t *testing.T) {
	tempDir := t.TempDir()
	mockMcp := filepath.Join(tempDir, "mcp")

	// Server 1: alphaxiv with 2 tools
	alphaDir := filepath.Join(mockMcp, "alphaxiv")
	if err := os.MkdirAll(alphaDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "search_papers.json"), []byte(`{"name":"search_papers","description":"Search arXiv"}`), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "get_paper.json"), []byte(`{"name":"get_paper"}`), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	opts := AuditOptions{
		MCPConfigDirs: []string{mockMcp},
	}

	tools, err := DiscoverRegisteredTools(opts)
	if err != nil {
		t.Fatalf("DiscoverRegisteredTools: %v", err)
	}

	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].ServerName != "alphaxiv" {
		t.Errorf("expected server alphaxiv, got %s", tools[0].ServerName)
	}
}

func TestAuditSkillBloat(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "audit_test.db")
	mockMcp := filepath.Join(tempDir, "mcp")

	// Create mock MCP schemas: server "weather" with 2 tools
	weatherDir := filepath.Join(mockMcp, "weather")
	if err := os.MkdirAll(weatherDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Tool 1: get_forecast (will be called)
	if err := os.WriteFile(filepath.Join(weatherDir, "get_forecast.json"), []byte(`{"name":"get_forecast","description":"Get 7-day weather forecast for city"}`), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	// Tool 2: get_historical_radar (dormant, never called)
	if err := os.WriteFile(filepath.Join(weatherDir, "get_historical_radar.json"), []byte(`{"name":"get_historical_radar","description":"Get Doppler radar archives with dense geometry coordinates"}`), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	// Server 2: "unused_server" with 1 tool (100% dormant)
	unusedDir := filepath.Join(mockMcp, "unused_server")
	if err := os.MkdirAll(unusedDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(unusedDir, "heavy_action.json"), []byte(`{"name":"heavy_action","description":"A very heavy action that wastes lots of tokens in system prompt"}`), 0644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}

	// Initialize knowledge store
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	t1 := now.Add(-5 * 24 * time.Hour).Format(time.RFC3339)

	// Insert message where assistant invoked "get_forecast"
	if _, err := st.DB.Exec(`INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES ('testproj', 'h1', ?, 'assistant', '<invoke name="get_forecast">Seattle</invoke>', '["claude-code"]', ?)`, t1, t1); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	ctx := context.Background()
	opts := AuditOptions{
		ProjectID:     "testproj",
		AuditPeriod:   30 * 24 * time.Hour,
		MCPConfigDirs: []string{mockMcp},
	}

	audit, err := AuditSkillBloat(ctx, st, opts)
	if err != nil {
		t.Fatalf("AuditSkillBloat: %v", err)
	}

	if audit.TotalRegistered != 3 {
		t.Errorf("TotalRegistered = %d, want 3", audit.TotalRegistered)
	}
	if audit.ActiveCount != 1 {
		t.Errorf("ActiveCount = %d, want 1", audit.ActiveCount)
	}
	if audit.DormantCount != 2 {
		t.Errorf("DormantCount = %d, want 2", audit.DormantCount)
	}
	if audit.WastedTokensTurn <= 0 {
		t.Errorf("WastedTokensTurn = %d, want > 0", audit.WastedTokensTurn)
	}

	// Verify active tool details
	if audit.ActiveTools[0].ToolName != "get_forecast" {
		t.Errorf("Active tool = %s, want get_forecast", audit.ActiveTools[0].ToolName)
	}
	if audit.ActiveTools[0].CallCount != 1 {
		t.Errorf("Active tool call count = %d, want 1", audit.ActiveTools[0].CallCount)
	}

	// Verify recommendations contain server unused_server notice
	foundUnusedRec := false
	for _, rec := range audit.Recommendations {
		if rec != "" && (filepath.Base(rec) == "unused_server" || rec != "") {
			foundUnusedRec = true
			break
		}
	}
	if !foundUnusedRec || len(audit.Recommendations) == 0 {
		t.Errorf("expected recommendations for dormant servers, got %v", audit.Recommendations)
	}
}
