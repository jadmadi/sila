package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

func TestDefaultUpstreamToolDefinitions(t *testing.T) {
	tools := DefaultUpstreamToolDefinitions()
	if len(tools) == 0 {
		t.Fatal("expected DefaultUpstreamToolDefinitions to return tools, got 0")
	}

	coreTools := ToolDefinitions()
	coreMap := make(map[string]bool, len(coreTools))
	for _, ct := range coreTools {
		coreMap[ct.Name] = true
	}

	seen := make(map[string]bool, len(tools))
	expectedPrefixes := map[string]bool{
		"uicraft": false,
		"drizzle": false,
		"cf":      false,
		"gopls":   false,
		"astro":   false,
	}

	for _, tool := range tools {
		if tool.Name == "" {
			t.Error("found tool with empty Name")
		}
		if seen[tool.Name] {
			t.Errorf("duplicate upstream tool name: %s", tool.Name)
		}
		seen[tool.Name] = true

		// Invariant: Upstream tools must never collide with Sila's 64 core tools
		if coreMap[tool.Name] {
			t.Errorf("upstream tool %q collides with core Sila tool", tool.Name)
		}

		// Check prefix matches one of the 4 defaults
		parts := strings.SplitN(tool.Name, "_", 2)
		if len(parts) < 2 {
			t.Errorf("upstream tool %q missing prefix separator '_'", tool.Name)
		} else {
			if _, ok := expectedPrefixes[parts[0]]; ok {
				expectedPrefixes[parts[0]] = true
			}
		}

		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
	}

	for pfx, found := range expectedPrefixes {
		if !found {
			t.Errorf("expected default upstream prefix %q was not found in tool definitions", pfx)
		}
	}
}

func TestUpstreamManager_HasToolAndListTools(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Startup default: disabled
	mgr := NewUpstreamManager(nil, nil)
	if mgr.Enabled() {
		t.Fatal("expected UpstreamManager to be disabled by default (mcp_upstreams_enabled=false)")
	}
	if len(mgr.ListTools()) != 0 {
		t.Fatalf("expected ListTools to return 0 tools when disabled, got %d", len(mgr.ListTools()))
	}
	if mgr.HasTool("uicraft_route_task") {
		t.Error("expected HasTool to return false when manager is disabled")
	}

	// Enable manager
	mgr.SetEnabled(true)
	if !mgr.Enabled() {
		t.Fatal("expected UpstreamManager to be enabled after SetEnabled(true)")
	}

	tools := mgr.ListTools()
	if len(tools) == 0 {
		t.Fatal("expected ListTools to return tools, got 0")
	}

	// Known default tools
	expectedTools := []string{
		"uicraft_route_task",
		"uicraft_check_anti_slop",
		"drizzle_search_docs",
		"drizzle_fetch_page",
		"cf_search",
		"gopls_diagnostics",
		"astro_search_astro_docs",
	}

	for _, name := range expectedTools {
		if !mgr.HasTool(name) {
			t.Errorf("expected mgr.HasTool(%q) == true, got false", name)
		}
	}

	// Core tools or unknown tools should return false
	if mgr.HasTool("salam") {
		t.Error("mgr.HasTool('salam') should be false (it is a core tool, not an upstream tool)")
	}
	if mgr.HasTool("prime_context") {
		t.Error("mgr.HasTool('prime_context') should be false")
	}
	if mgr.HasTool("unknown_tool_name") {
		t.Error("mgr.HasTool('unknown_tool_name') should be false")
	}

	// Toggling enabled back to false
	mgr.SetEnabled(false)
	if mgr.Enabled() {
		t.Error("expected mgr.Enabled() == false after SetEnabled(false)")
	}
	if mgr.HasTool("uicraft_check_anti_slop") {
		t.Error("mgr.HasTool should return false when manager is disabled")
	}
	if len(mgr.ListTools()) != 0 {
		t.Error("mgr.ListTools should return nil when manager is disabled")
	}
}

func TestServer_ToolsListWithUpstreams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// 1. Without upstream manager: exactly PublicToolCount (63)
	inWithout := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	var outWithout bytes.Buffer
	srvWithout := NewServer(nil, inWithout, &outWithout)
	if err := srvWithout.Run(); err != nil {
		t.Fatalf("srvWithout.Run() error: %v", err)
	}

	var respWithout JSONRPCResponse
	if err := json.Unmarshal(outWithout.Bytes(), &respWithout); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	resMapWithout := respWithout.Result.(map[string]any)
	toolsWithout := resMapWithout["tools"].([]any)
	if len(toolsWithout) != PublicToolCount {
		t.Fatalf("expected srvWithout to return %d tools, got %d", PublicToolCount, len(toolsWithout))
	}

	// 2. With upstream manager attached but disabled (default): exactly PublicToolCount (63)
	inDisabled := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var outDisabled bytes.Buffer
	srvDisabled := NewServer(nil, inDisabled, &outDisabled)
	upstreamMgrDisabled := NewUpstreamManager(nil, nil)
	srvDisabled.SetUpstreamManager(upstreamMgrDisabled)

	if err := srvDisabled.Run(); err != nil {
		t.Fatalf("srvDisabled.Run() error: %v", err)
	}

	var respDisabled JSONRPCResponse
	if err := json.Unmarshal(outDisabled.Bytes(), &respDisabled); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	resMapDisabled := respDisabled.Result.(map[string]any)
	toolsDisabled := resMapDisabled["tools"].([]any)
	if len(toolsDisabled) != PublicToolCount {
		t.Fatalf("expected disabled srv to return %d tools, got %d", PublicToolCount, len(toolsDisabled))
	}

	// 3. With upstream manager attached and enabled: PublicToolCount (63) + upstream tools
	inWith := strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}` + "\n")
	var outWith bytes.Buffer
	srvWith := NewServer(nil, inWith, &outWith)
	upstreamMgr := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: true})
	srvWith.SetUpstreamManager(upstreamMgr)

	if err := srvWith.Run(); err != nil {
		t.Fatalf("srvWith.Run() error: %v", err)
	}

	var respWith JSONRPCResponse
	if err := json.Unmarshal(outWith.Bytes(), &respWith); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	resMapWith := respWith.Result.(map[string]any)
	toolsWith := resMapWith["tools"].([]any)

	expectedTotal := PublicToolCount + len(upstreamMgr.ListTools())
	if len(toolsWith) != expectedTotal {
		t.Fatalf("expected srvWith to return %d tools (%d core + %d upstream), got %d",
			expectedTotal, PublicToolCount, len(upstreamMgr.ListTools()), len(toolsWith))
	}
}

func TestUpstreamManager_AutoIngest(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_ingest.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open error: %v", err)
	}
	defer st.Close()

	mgr := NewUpstreamManager(st, nil)

	// Simulate drizzle_fetch_page result
	result := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "# Drizzle ORM Migrations Guide\n\nDrizzle migrations are SQL files generated by drizzle-kit generate and applied with drizzle-kit migrate.\nEnsure batch operations use db.batch() when targeting Cloudflare D1 databases for maximum performance.",
			},
		},
	}

	args := map[string]any{
		"slug": "docs/migrations",
	}

	mgr.interceptAutoIngest("drizzle", "fetch_page", args, result, "test_proj")

	// Query store docs to verify auto-ingestion
	docs, err := st.SearchDocsFTS("Drizzle", "test_proj", 10)
	if err != nil {
		t.Fatalf("SearchDocsFTS error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected auto-ingested doc to be found in SQLite FTS5, got 0")
	}

	if !strings.Contains(docs[0].Title, "docs/migrations") {
		t.Errorf("expected doc title to mention slug, got %q", docs[0].Title)
	}
	if !strings.Contains(docs[0].Content, "Cloudflare D1") {
		t.Errorf("expected doc content to contain 'Cloudflare D1', got %q", docs[0].Content)
	}
}

func TestUpstreamManager_Concurrency(t *testing.T) {
	mgr := NewUpstreamManager(nil, nil)

	var wg sync.WaitGroup
	const routines = 20
	const iterations = 50

	for i := 0; i < routines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = mgr.Enabled()
				_ = mgr.HasTool("uicraft_check_anti_slop")
				_ = mgr.HasTool("drizzle_search_docs")
				_ = mgr.HasTool("cf_search")
				_ = mgr.HasTool("salam")
				_ = mgr.ListTools()
			}
		}(i)
	}

	wg.Wait()
}

func TestStdioUpstreamClient_Lifecycle(t *testing.T) {
	// Test creating and closing an unstarted client
	client := NewStdioUpstreamClient("test", "test", "cat", nil, nil, false)
	if client.Name() != "test" {
		t.Errorf("expected Name() == 'test', got %s", client.Name())
	}
	if client.Prefix() != "test" {
		t.Errorf("expected Prefix() == 'test', got %s", client.Prefix())
	}
	if client.IsRunning() {
		t.Error("client should not be running before first call")
	}
	if err := client.Close(); err != nil {
		t.Errorf("unexpected error on Close: %v", err)
	}
}

func TestHTTPUpstreamClient_CallToolUnauthorized(t *testing.T) {
	if conn, derr := net.DialTimeout("tcp", "mcp.cloudflare.com:443", 2*time.Second); derr != nil {
		t.Skipf("network unavailable: %v", derr)
	} else {
		_ = conn.Close()
	}
	// Verify that HTTP client handles 401 cleanly with a helpful error message
	client := NewHTTPUpstreamClient("cloudflare", "cf", "https://mcp.cloudflare.com/mcp", nil, "", false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.CallTool(ctx, "search", map[string]any{"query": "workers"})
	if err == nil {
		t.Fatal("expected error from unauthenticated Cloudflare MCP call, got nil")
	}
	if strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "connection refused") {
		t.Skipf("network unavailable: %v", err)
	}

	if !strings.Contains(err.Error(), "401 Unauthorized") && !strings.Contains(err.Error(), "API token") {
		t.Errorf("expected error to explain 401 Unauthorized or API token, got %q", err.Error())
	}
}

func TestUpstreamManager_OnDemand_StatusAndToggle(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		AlphaXivAPIToken:    "mock-alphaxiv-token",
		StitchAPIKey:        "mock-stitch-key",
	}
	mgr := NewUpstreamManager(nil, cfg)

	statuses := mgr.GetStatuses()
	if len(statuses) < 6 {
		t.Fatalf("expected at least 6 upstreams (4 defaults + 2 on-demand), got %d", len(statuses))
	}

	statusMap := make(map[string]UpstreamStatus)
	for _, s := range statuses {
		statusMap[s.Name] = s
	}

	alphaStatus, ok := statusMap["alphaxiv"]
	if !ok {
		t.Fatal("expected alphaxiv upstream status, not found")
	}
	if !alphaStatus.OnDemand {
		t.Error("expected alphaxiv to be OnDemand=true")
	}
	if alphaStatus.Enabled {
		t.Error("expected alphaxiv to be disabled by default before toggle")
	}

	stitchStatus, ok := statusMap["stitch"]
	if !ok {
		t.Fatal("expected stitch upstream status, not found")
	}
	if !stitchStatus.OnDemand {
		t.Error("expected stitch to be OnDemand=true")
	}
	if stitchStatus.Enabled {
		t.Error("expected stitch to be disabled by default before toggle")
	}

	// Gateways must be present
	if !mgr.HasTool("alphaxiv_query") {
		t.Error("expected HasTool('alphaxiv_query') == true")
	}
	if !mgr.HasTool("stitch_query") {
		t.Error("expected HasTool('stitch_query') == true")
	}
	if !mgr.HasTool("mcp_upstream_status") {
		t.Error("expected HasTool('mcp_upstream_status') == true")
	}
	if !mgr.HasTool("mcp_upstream_toggle") {
		t.Error("expected HasTool('mcp_upstream_toggle') == true")
	}

	// Granular tools should NOT be in ListTools before toggle
	toolsBefore := mgr.ListTools()
	for _, tool := range toolsBefore {
		if tool.Name == "alphaxiv_discover_papers" || tool.Name == "stitch_generate_screen_from_text" {
			t.Errorf("found granular on-demand tool %q in ListTools before toggle", tool.Name)
		}
	}

	// Toggle alphaxiv ON
	enabled, err := mgr.ToggleServer("alphaxiv", true)
	if err != nil || !enabled {
		t.Fatalf("ToggleServer('alphaxiv', true) failed: %v", err)
	}

	// Verify granular tools now appear
	toolsAfterAlpha := mgr.ListTools()
	foundAlpha := false
	for _, tool := range toolsAfterAlpha {
		if tool.Name == "alphaxiv_discover_papers" {
			foundAlpha = true
			break
		}
	}
	if !foundAlpha {
		t.Error("expected alphaxiv_discover_papers in ListTools after toggle ON")
	}

	// Toggle stitch ON
	enabledStitch, err := mgr.ToggleServer("stitch", true)
	if err != nil || !enabledStitch {
		t.Fatalf("ToggleServer('stitch', true) failed: %v", err)
	}

	toolsAfterBoth := mgr.ListTools()
	foundStitch := false
	for _, tool := range toolsAfterBoth {
		if tool.Name == "stitch_generate_screen_from_text" {
			foundStitch = true
			break
		}
	}
	if !foundStitch {
		t.Error("expected stitch_generate_screen_from_text in ListTools after toggle ON")
	}

	// Toggle alphaxiv OFF
	enabledOff, err := mgr.ToggleServer("alphaxiv", false)
	if err != nil || enabledOff {
		t.Fatalf("ToggleServer('alphaxiv', false) failed: %v", err)
	}
	toolsAfterAlphaOff := mgr.ListTools()
	for _, tool := range toolsAfterAlphaOff {
		if tool.Name == "alphaxiv_discover_papers" {
			t.Error("alphaxiv_discover_papers still in ListTools after toggle OFF")
		}
	}
}

func TestUpstreamManager_CallGateway_Validation(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		AlphaXivAPIToken:    "mock-token",
		StitchAPIKey:        "mock-key",
	}
	mgr := NewUpstreamManager(nil, cfg)

	ctx := context.Background()

	// Missing action
	_, err := mgr.CallGateway(ctx, "alphaxiv", map[string]any{}, "proj")
	if err == nil || !strings.Contains(err.Error(), "missing required parameter 'action'") {
		t.Errorf("expected error for missing action, got %v", err)
	}

	// Missing URL or paper_id for get_paper
	_, err = mgr.CallGateway(ctx, "alphaxiv", map[string]any{"action": "get_paper"}, "proj")
	if err == nil || !strings.Contains(err.Error(), "requires 'url' or 'paper_id'") {
		t.Errorf("expected error for missing url/paper_id, got %v", err)
	}

	// Unknown action for stitch
	_, err = mgr.CallGateway(ctx, "stitch", map[string]any{"action": "nonexistent_action"}, "proj")
	if err == nil || !strings.Contains(err.Error(), "unknown stitch action") {
		t.Errorf("expected error for unknown action, got %v", err)
	}

	// Unconfigured gateway when manager is enabled
	unconfMgr := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: true})
	_, err = unconfMgr.CallGateway(ctx, "alphaxiv", map[string]any{"action": "discover"}, "proj")
	if err == nil || !strings.Contains(err.Error(), "alphaxiv is not configured") {
		t.Errorf("expected error for unconfigured gateway, got %v", err)
	}

	// Globally disabled gateway
	disabledMgr := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: false})
	_, err = disabledMgr.CallGateway(ctx, "alphaxiv", map[string]any{"action": "discover"}, "proj")
	if err == nil || !strings.Contains(err.Error(), "upstream manager is disabled") {
		t.Errorf("expected disabled error for disabled gateway, got %v", err)
	}
}

func TestUpstreamManager_AutoIngest_AlphaXiv(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_sila.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		AlphaXivAPIToken:    "mock-token",
	}
	mgr := NewUpstreamManager(st, cfg)

	mockResult := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Deep residual learning for image recognition presents a residual learning framework to ease the training of networks that are substantially deeper than those used previously.",
			},
		},
	}

	mgr.interceptAutoIngest("alphaxiv", "get_paper_content", map[string]any{"paper_id": "1512.03385"}, mockResult, "research-proj")

	docs, err := st.SearchDocsFTS("residual", "research-proj", 10)
	if err != nil {
		t.Fatalf("SearchDocsFTS failed: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected auto-ingested AlphaXiv paper to be found in SQLite FTS5, got 0")
	}

	if !strings.Contains(docs[0].Title, "1512.03385") {
		t.Errorf("expected doc title to contain paper_id, got %q", docs[0].Title)
	}
	if !strings.Contains(docs[0].Content, "residual learning") {
		t.Errorf("expected doc content to contain 'residual learning', got %q", docs[0].Content)
	}
	if docs[0].URL != "https://alphaxiv.org/abs/1512.03385" {
		t.Errorf("expected URL 'https://alphaxiv.org/abs/1512.03385', got %q", docs[0].URL)
	}
}

type recordingUpstreamClient struct {
	mu         sync.Mutex
	name       string
	prefix     string
	calls      []string
	callArgs   []map[string]any
	closeCount int
}

func newRecordingClient(name, prefix string) *recordingUpstreamClient {
	return &recordingUpstreamClient{
		name:   name,
		prefix: prefix,
	}
}

func (r *recordingUpstreamClient) Name() string    { return r.name }
func (r *recordingUpstreamClient) Prefix() string  { return r.prefix }
func (r *recordingUpstreamClient) IsRunning() bool { return true }
func (r *recordingUpstreamClient) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeCount++
	return nil
}

func (r *recordingUpstreamClient) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
	r.callArgs = append(r.callArgs, args)
	return map[string]any{"status": "ok", "tool": name}, nil
}

func (r *recordingUpstreamClient) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestUpstreamManager_ZeroSideEffects_DisabledAndCollisions(t *testing.T) {
	ctx := context.Background()

	// 1. Zero side effects when globally disabled
	t.Run("GloballyDisabled", func(t *testing.T) {
		cfg := &config.Config{
			MCPUpstreamsEnabled: false,
			MCPUpstreams: map[string]config.UpstreamMCPServer{
				"mock1": {Prefix: "mock1", Command: "dummy"},
			},
		}
		mgr := NewUpstreamManager(nil, cfg)
		rec := newRecordingClient("mock1", "mock1")
		mgr.SetClient("mock1", rec)
		mgr.RegisterServerTools("mock1", []Tool{{Name: "mock1_do_work"}})

		_, err := mgr.CallTool(ctx, "mock1_do_work", nil, "")
		if err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("expected disabled error, got: %v", err)
		}
		if rec.CallCount() != 0 {
			t.Fatalf("expected 0 calls when globally disabled, got %d", rec.CallCount())
		}
	})

	// 2. Zero side effects when server is disabled
	t.Run("ServerDisabled", func(t *testing.T) {
		cfg := &config.Config{
			MCPUpstreamsEnabled: true,
			MCPUpstreams: map[string]config.UpstreamMCPServer{
				"mock2": {Prefix: "mock2", Command: "dummy", Disabled: true},
			},
		}
		mgr := NewUpstreamManager(nil, cfg)
		rec := newRecordingClient("mock2", "mock2")
		mgr.SetClient("mock2", rec)
		mgr.RegisterServerTools("mock2", []Tool{{Name: "mock2_do_work"}})

		_, err := mgr.CallTool(ctx, "mock2_do_work", nil, "")
		if err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("expected disabled error, got: %v", err)
		}
		if rec.CallCount() != 0 {
			t.Fatalf("expected 0 calls when server is disabled, got %d", rec.CallCount())
		}
	})

	// 3. Zero side effects on unknown suffixes
	t.Run("UnknownSuffix", func(t *testing.T) {
		cfg := &config.Config{
			MCPUpstreamsEnabled: true,
			MCPUpstreams: map[string]config.UpstreamMCPServer{
				"mock3": {Prefix: "mock3", Command: "dummy"},
			},
		}
		mgr := NewUpstreamManager(nil, cfg)
		rec := newRecordingClient("mock3", "mock3")
		mgr.SetClient("mock3", rec)
		mgr.RegisterServerTools("mock3", []Tool{{Name: "mock3_valid_tool"}})

		_, err := mgr.CallTool(ctx, "mock3_arbitrary_nonexistent_suffix", nil, "")
		if err == nil || !strings.Contains(err.Error(), "unknown upstream tool") {
			t.Fatalf("expected unknown tool error, got: %v", err)
		}
		if rec.CallCount() != 0 {
			t.Fatalf("expected 0 calls on unknown suffix, got %d", rec.CallCount())
		}
	})

	// 4. Zero side effects on duplicate tool name collision
	t.Run("DuplicateCollision", func(t *testing.T) {
		cfg := &config.Config{
			MCPUpstreamsEnabled: true,
			MCPUpstreams: map[string]config.UpstreamMCPServer{
				"srvA": {Prefix: "a", Command: "dummy"},
				"srvB": {Prefix: "b", Command: "dummy"},
			},
		}
		mgr := NewUpstreamManager(nil, cfg)
		recA := newRecordingClient("srvA", "a")
		recB := newRecordingClient("srvB", "b")
		mgr.SetClient("srvA", recA)
		mgr.SetClient("srvB", recB)

		mgr.RegisterServerTools("srvA", []Tool{{Name: "shared_tool"}})
		mgr.RegisterServerTools("srvB", []Tool{{Name: "shared_tool"}})

		_, err := mgr.CallTool(ctx, "shared_tool", nil, "")
		if err == nil || !strings.Contains(err.Error(), "denied: duplicate tool collision") {
			t.Fatalf("expected duplicate collision error, got: %v", err)
		}
		if recA.CallCount() != 0 || recB.CallCount() != 0 {
			t.Fatalf("expected 0 calls on collision, got recA=%d, recB=%d", recA.CallCount(), recB.CallCount())
		}
	})

	// 5. Zero side effects on reserved core tool names
	t.Run("ReservedCoreTool", func(t *testing.T) {
		cfg := &config.Config{
			MCPUpstreamsEnabled: true,
			MCPUpstreams: map[string]config.UpstreamMCPServer{
				"srvR": {Prefix: "r", Command: "dummy"},
			},
		}
		mgr := NewUpstreamManager(nil, cfg)
		recR := newRecordingClient("srvR", "r")
		mgr.SetClient("srvR", recR)

		mgr.RegisterServerTools("srvR", []Tool{
			{Name: "salam"},
			{Name: "search_knowledge"},
			{Name: "prime_context"},
		})

		for _, reserved := range []string{"salam", "search_knowledge", "prime_context"} {
			_, err := mgr.CallTool(ctx, reserved, nil, "")
			if err == nil || !strings.Contains(err.Error(), "reserved core tool") {
				t.Fatalf("expected reserved core tool error for %q, got: %v", reserved, err)
			}
			if recR.CallCount() != 0 {
				t.Fatalf("expected 0 calls for reserved tool %q, got %d", reserved, recR.CallCount())
			}
		}
	})

	// 6. Zero side effects on ambiguous prefix
	t.Run("AmbiguousPrefix", func(t *testing.T) {
		cfg := &config.Config{
			MCPUpstreamsEnabled: true,
			MCPUpstreams: map[string]config.UpstreamMCPServer{
				"serverOne": {Prefix: "sharedpfx", Command: "dummy"},
				"serverTwo": {Prefix: "sharedpfx", Command: "dummy"},
			},
		}
		mgr := NewUpstreamManager(nil, cfg)
		rec1 := newRecordingClient("serverOne", "sharedpfx")
		rec2 := newRecordingClient("serverTwo", "sharedpfx")
		mgr.SetClient("serverOne", rec1)
		mgr.SetClient("serverTwo", rec2)

		_, err := mgr.CallTool(ctx, "sharedpfx_action", nil, "")
		if err == nil || !strings.Contains(err.Error(), "ambiguous prefix") {
			t.Fatalf("expected ambiguous prefix error, got: %v", err)
		}
		if rec1.CallCount() != 0 || rec2.CallCount() != 0 {
			t.Fatalf("expected 0 calls on ambiguous prefix, got rec1=%d, rec2=%d", rec1.CallCount(), rec2.CallCount())
		}
	})
}

func TestServer_CoreSearchNoUpstreamInterference(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_core.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open error: %v", err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmpDir, "test-repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}
	if _, err := st.EnsureProject(repoDir); err != nil {
		t.Fatalf("ensure project error: %v", err)
	}
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd error: %v", err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatalf("chdir error: %v", err)
	}
	defer os.Chdir(origWd)

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"mock_upstream": {Prefix: "mock", Command: "dummy"},
		},
	}
	upstreamMgr := NewUpstreamManager(st, cfg)
	rec := newRecordingClient("mock_upstream", "mock")
	upstreamMgr.SetClient("mock_upstream", rec)

	// Call core tools: salam, search_knowledge, prime_context via Server stdio
	coreCalls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"salam","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search_knowledge","arguments":{"query":"test"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"prime_context","arguments":{}}}`,
	}

	for i, reqStr := range coreCalls {
		in := strings.NewReader(reqStr + "\n")
		var out bytes.Buffer
		srv := NewServer(st, in, &out)
		srv.SetUpstreamManager(upstreamMgr)

		if err := srv.Run(); err != nil {
			t.Fatalf("call %d: srv.Run error: %v", i+1, err)
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("call %d: unmarshal error: %v, raw: %s", i+1, err, out.String())
		}
		if resp.Error != nil {
			t.Fatalf("call %d: unexpected error response: %s", i+1, resp.Error.Message)
		}
		if resp.Result == nil {
			t.Fatalf("call %d: expected non-nil result", i+1)
		}
	}

	// Verify upstream recording client experienced 0 calls
	if rec.CallCount() != 0 {
		t.Fatalf("expected 0 upstream calls during core tool execution, got %d", rec.CallCount())
	}
}

func TestUpstreamManager_ReEnablingDoesNotResurrectDenied(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"srv1": {Prefix: "p", Command: "dummy"},
			"srv2": {Prefix: "p", Command: "dummy"}, // ambiguous prefix
		},
	}
	mgr := NewUpstreamManager(nil, cfg)

	// Verify ambiguous prefix tool is denied
	_, err := mgr.CallTool(context.Background(), "p_tool", nil, "")
	if err == nil || !strings.Contains(err.Error(), "ambiguous prefix") {
		t.Fatalf("expected ambiguous prefix error, got: %v", err)
	}

	// Toggle or re-enable
	_, _ = mgr.ToggleServer("srv1", false)
	_, _ = mgr.ToggleServer("srv1", true)
	mgr.SetEnabled(false)
	mgr.SetEnabled(true)

	// Verify p_tool remains denied and cannot be called
	_, err = mgr.CallTool(context.Background(), "p_tool", nil, "")
	if err == nil || !strings.Contains(err.Error(), "ambiguous prefix") {
		t.Fatalf("expected ambiguous prefix error to persist after re-enabling, got: %v", err)
	}
}

func TestUpstreamManager_ToggleConcurrency(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		AlphaXivAPIToken:    "mock-token",
		StitchAPIKey:        "mock-key",
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"uicraft": {Prefix: "uicraft", Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(nil, cfg)
	rec := newRecordingClient("uicraft", "uicraft")
	mgr.SetClient("uicraft", rec)

	var wg sync.WaitGroup
	const routines = 16
	const iterations = 50

	for i := 0; i < routines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if id%2 == 0 {
					_, _ = mgr.ToggleServer("alphaxiv", j%2 == 0)
					_, _ = mgr.ToggleServer("stitch", j%3 == 0)
				} else {
					mgr.SetEnabled(j%2 == 0)
				}
				_ = mgr.Enabled()
				_ = mgr.GetStatuses()
				_ = mgr.ListTools()
				_ = mgr.HasTool("alphaxiv_query")
				_ = mgr.HasTool("uicraft_route_task")
				_ = mgr.HasTool("salam")
			}
		}(i)
	}

	wg.Wait()
}

func TestSanitizeEgressArgs_SafeScrub(t *testing.T) {
	args := map[string]any{
		"prompt":      "Investigate leak in /home/alice/secrets with sk-1234567890123456789012345 and Bearer mysecrettoken12345678901234567890",
		"question":    "Why does /Users/bob/project use ghp_123456789012345678901234567890123456?",
		"url":         "https://example.com/api?api_key=secretkey1234567890&token=supersecrettoken",
		"description": "-----" + "BEGIN RSA PRIVATE KEY-----\n" + "MIIEowIBAAKCAQEA00000000\n" + "-----" + "END RSA PRIVATE KEY-----",
		"keywords": []any{
			"/home/charlie/data",
			"glpat-0123456789abcdefghij",
			"xoxb-inert",
			"harmless-keyword",
		},
		"metadata": map[string]any{
			"nested_path": "/home/dave/repo",
			"count":       42,
			"active":      true,
		},
	}

	sanitized, err := SanitizeEgressArgs("any_tool", args)
	if err != nil {
		t.Fatalf("unexpected error in SanitizeEgressArgs: %v", err)
	}

	// 1. prompt scrubbed
	prompt, _ := sanitized["prompt"].(string)
	if strings.Contains(prompt, "/home/alice") {
		t.Errorf("prompt leaked home path: %s", prompt)
	}
	if strings.Contains(prompt, "sk-1234567890123456789012345") {
		t.Errorf("prompt leaked api key: %s", prompt)
	}
	if strings.Contains(prompt, "mysecrettoken12345678901234567890") {
		t.Errorf("prompt leaked bearer token: %s", prompt)
	}
	if !strings.Contains(prompt, "~") || !strings.Contains(prompt, "[REDACTED_API_KEY]") {
		t.Errorf("prompt missing redaction placeholders: %s", prompt)
	}

	// 2. question scrubbed
	q, _ := sanitized["question"].(string)
	if strings.Contains(q, "/Users/bob") || strings.Contains(q, "ghp_") {
		t.Errorf("question leaked sensitive info: %s", q)
	}
	if !strings.Contains(q, "[REDACTED_GH_TOKEN]") {
		t.Errorf("question missing REDACTED_GH_TOKEN: %s", q)
	}

	// 3. description PEM key scrubbed
	desc, _ := sanitized["description"].(string)
	if strings.Contains(desc, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("description leaked PEM private key: %s", desc)
	}
	if !strings.Contains(desc, "[REDACTED_PRIVATE_KEY]") {
		t.Errorf("description missing REDACTED_PRIVATE_KEY: %s", desc)
	}

	// 4. keywords array scrubbed
	kw, ok := sanitized["keywords"].([]any)
	if !ok || len(kw) != 4 {
		t.Fatalf("expected 4 keywords, got %+v", kw)
	}
	if strings.Contains(kw[0].(string), "/home/charlie") {
		t.Errorf("keyword 0 leaked home path: %v", kw[0])
	}
	if strings.Contains(kw[1].(string), "glpat-") {
		t.Errorf("keyword 1 leaked gitlab token: %v", kw[1])
	}
	if strings.Contains(kw[2].(string), "xoxb-") {
		t.Errorf("keyword 2 leaked slack token: %v", kw[2])
	}
	if kw[3].(string) != "harmless-keyword" {
		t.Errorf("keyword 3 was modified unexpectedly: %v", kw[3])
	}

	// 5. nested map scrubbed and harmless values retained
	meta, ok := sanitized["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %+v", sanitized["metadata"])
	}
	if strings.Contains(meta["nested_path"].(string), "/home/dave") {
		t.Errorf("metadata leaked nested home path: %v", meta["nested_path"])
	}
	if meta["count"] != 42 || meta["active"] != true {
		t.Errorf("harmless metadata mutated: %+v", meta)
	}
}

func TestSanitizeEgressArgs_ExecutableArguments_DenyAndOptIn(t *testing.T) {
	// 1. Harmless code passes intact without opt-in
	harmlessArgs := map[string]any{
		"code": "const sum = (a, b) => a + b;\nconsole.log(sum(1, 2));",
	}
	sanitizedHarmless, err := SanitizeEgressArgs("cf_execute", harmlessArgs)
	if err != nil {
		t.Fatalf("unexpected error on harmless code: %v", err)
	}
	if sanitizedHarmless["code"] != harmlessArgs["code"] {
		t.Errorf("harmless code was modified: got %q", sanitizedHarmless["code"])
	}

	// 2. Sensitive code WITHOUT opt-in is denied
	sensitiveArgs := map[string]any{
		"code": "const secretToken = 'sk-1234567890123456789012345';\nfetch('/api', { headers: { token: secretToken } });",
	}
	_, err = SanitizeEgressArgs("cf_execute", sensitiveArgs)
	if err == nil {
		t.Fatal("expected error on sensitive code without opt-in, got nil")
	}
	if !strings.Contains(err.Error(), "Rewriting executable code is disabled for safety") {
		t.Errorf("expected safety explanation in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "allow_sensitive=true") {
		t.Errorf("expected actionable guidance to pass allow_sensitive=true, got: %v", err)
	}

	// 3. Sensitive code WITH allow_sensitive=true passes intact (preserves syntax/AST)
	optInArgs := map[string]any{
		"code":            sensitiveArgs["code"],
		"allow_sensitive": true,
	}
	sanitizedOptIn, err := SanitizeEgressArgs("cf_execute", optInArgs)
	if err != nil {
		t.Fatalf("unexpected error with allow_sensitive=true: %v", err)
	}
	if sanitizedOptIn["code"] != sensitiveArgs["code"] {
		t.Errorf("opt-in code was altered: got %q, want %q", sanitizedOptIn["code"], sensitiveArgs["code"])
	}
	if _, exists := sanitizedOptIn["allow_sensitive"]; exists {
		t.Errorf("allow_sensitive should be stripped from outbound args")
	}

	// 4. Sensitive code WITH opt_in_privacy=true passes intact
	optInPrivacyArgs := map[string]any{
		"code":           sensitiveArgs["code"],
		"opt_in_privacy": true,
	}
	sanitizedOptInPriv, err := SanitizeEgressArgs("cf_execute", optInPrivacyArgs)
	if err != nil {
		t.Fatalf("unexpected error with opt_in_privacy=true: %v", err)
	}
	if sanitizedOptInPriv["code"] != sensitiveArgs["code"] {
		t.Errorf("opt-in code was altered: got %q, want %q", sanitizedOptInPriv["code"], sensitiveArgs["code"])
	}
	if _, exists := sanitizedOptInPriv["opt_in_privacy"]; exists {
		t.Errorf("opt_in_privacy should be stripped from outbound args")
	}

	// 5. Sensitive nested code parameter (e.g. raw_tool arguments) denied without opt-in
	nestedSensitive := map[string]any{
		"tool_name": "cf_execute",
		"arguments": map[string]any{
			"code": "const key = 'ghp_123456789012345678901234567890123456';",
		},
	}
	_, err = SanitizeEgressArgs("stitch_query", nestedSensitive)
	if err == nil {
		t.Fatal("expected error on nested sensitive code without opt-in, got nil")
	}

	// 6. Sensitive nested code parameter WITH allow_sensitive=true passes intact
	nestedOptIn := map[string]any{
		"tool_name":       "cf_execute",
		"allow_sensitive": true,
		"arguments": map[string]any{
			"code": "const key = 'ghp_123456789012345678901234567890123456';",
		},
	}
	sanitizedNested, err := SanitizeEgressArgs("stitch_query", nestedOptIn)
	if err != nil {
		t.Fatalf("unexpected error on nested opt-in: %v", err)
	}
	nestedArgs := sanitizedNested["arguments"].(map[string]any)
	if nestedArgs["code"] != "const key = 'ghp_123456789012345678901234567890123456';" {
		t.Errorf("nested opt-in code was altered: %v", nestedArgs["code"])
	}
}

func TestValidateStdioArgs(t *testing.T) {
	// 1. Safe arguments
	safe := []string{"-y", "drizzle-docs-mcp", "--dialect=sqlite"}
	if err := ValidateStdioArgs(safe); err != nil {
		t.Errorf("expected safe args to pass, got: %v", err)
	}

	// 2. Secret-bearing --header flag (space separated)
	leakSpace := []string{"-y", "mcp-remote", "https://api.example.com", "--header", "Authorization: Bearer secret-token-12345"}
	if err := ValidateStdioArgs(leakSpace); err == nil {
		t.Error("expected error for secret-bearing --header flag in argv")
	}

	// 3. Secret-bearing --header= flag
	leakEqual := []string{"-y", "mcp-remote", "https://api.example.com", "--header=X-Api-Key: secret-key-xyz"}
	if err := ValidateStdioArgs(leakEqual); err == nil {
		t.Error("expected error for secret-bearing --header= flag in argv")
	}

	// 4. Raw token or home path directly in argv
	leakToken := []string{"run", "ghp_123456789012345678901234567890123456"}
	if err := ValidateStdioArgs(leakToken); err == nil {
		t.Error("expected error for raw github token in argv")
	}

	leakPath := []string{"run", "/home/developer/secret.js"}
	if err := ValidateStdioArgs(leakPath); err == nil {
		t.Error("expected error for raw home path in argv")
	}
}

func TestUpstreamManager_CallTool_EgressPrivacyAndZeroSideEffects(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"mock": {Prefix: "mock", Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(nil, cfg)
	rec := newRecordingClient("mock", "mock")
	mgr.SetClient("mock", rec)

	// Register tools: one executable (mock_execute) and one non-executable (mock_prompt)
	mgr.RegisterServerTools("mock", []Tool{
		{
			Name: "mock_execute",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"code": {Type: "string"},
				},
			},
		},
		{
			Name: "mock_prompt",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"prompt": {Type: "string"},
				},
			},
		},
	})

	// Case 1: Executable call with sensitive code WITHOUT opt-in
	// MUST fail with error and cause ZERO transport calls (CallCount == 0)
	sensitiveCall := map[string]any{
		"code": "const secretKey = 'sk-1234567890123456789012345';",
	}
	_, err := mgr.CallTool(context.Background(), "mock_execute", sensitiveCall, "")
	if err == nil {
		t.Fatal("expected error on sensitive mock_execute call without opt-in, got nil")
	}
	if rec.CallCount() != 0 {
		t.Fatalf("SECURITY VIOLATION: denied executable call caused %d transport calls; expected 0 side effects", rec.CallCount())
	}

	// Case 2: Executable call WITH allow_sensitive=true
	// MUST succeed and reach transport with intact code
	optInCall := map[string]any{
		"code":            sensitiveCall["code"],
		"allow_sensitive": true,
	}
	_, err = mgr.CallTool(context.Background(), "mock_execute", optInCall, "")
	if err != nil {
		t.Fatalf("unexpected error on opt-in mock_execute call: %v", err)
	}
	if rec.CallCount() != 1 {
		t.Fatalf("expected exactly 1 transport call for opt-in call, got %d", rec.CallCount())
	}
	sentArgs := rec.callArgs[0]
	if sentArgs["code"] != sensitiveCall["code"] {
		t.Errorf("opt-in code was altered in transport: got %v", sentArgs["code"])
	}
	if _, exists := sentArgs["allow_sensitive"]; exists {
		t.Errorf("allow_sensitive should not be forwarded to upstream transport")
	}

	// Case 3: Non-executable call with sensitive prompt
	// MUST safely scrub and reach transport with scrubbed arguments
	promptCall := map[string]any{
		"prompt": "Hello from /home/alice with token ghp_123456789012345678901234567890123456",
	}
	_, err = mgr.CallTool(context.Background(), "mock_prompt", promptCall, "")
	if err != nil {
		t.Fatalf("unexpected error on mock_prompt call: %v", err)
	}
	if rec.CallCount() != 2 {
		t.Fatalf("expected 2 total transport calls, got %d", rec.CallCount())
	}
	scrubbedSent := rec.callArgs[1]
	sentPrompt := scrubbedSent["prompt"].(string)
	if strings.Contains(sentPrompt, "/home/alice") || strings.Contains(sentPrompt, "ghp_") {
		t.Errorf("transport received unscrubbed prompt: %s", sentPrompt)
	}
	if !strings.Contains(sentPrompt, "~") || !strings.Contains(sentPrompt, "[REDACTED_GH_TOKEN]") {
		t.Errorf("transport missing redaction placeholders: %s", sentPrompt)
	}
}

func TestUpstreamManager_CallGateway_EgressPrivacy(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		AlphaXivAPIToken:    "dummy-alpha-token",
		StitchAPIKey:        "dummy-stitch-key",
	}
	mgr := NewUpstreamManager(nil, cfg)
	recAlpha := newRecordingClient("alphaxiv", "alphaxiv")
	recStitch := newRecordingClient("stitch", "stitch")
	mgr.SetClient("alphaxiv", recAlpha)
	mgr.SetClient("stitch", recStitch)

	// 1. Compact gateway alphaxiv_query safely scrubs non-executable query
	alphaArgs := map[string]any{
		"action":   "discover",
		"question": "Research paper located at /home/charlie/data with sk-1234567890123456789012345",
	}
	_, err := mgr.CallGateway(context.Background(), "alphaxiv", alphaArgs, "")
	if err != nil {
		t.Fatalf("unexpected error on alphaxiv_query discover: %v", err)
	}
	if recAlpha.CallCount() != 1 {
		t.Fatalf("expected 1 call to alphaxiv client, got %d", recAlpha.CallCount())
	}
	sentQ := recAlpha.callArgs[0]["question"].(string)
	if strings.Contains(sentQ, "/home/charlie") || strings.Contains(sentQ, "sk-1234567890123456789012345") {
		t.Errorf("alphaxiv gateway leaked sensitive data: %s", sentQ)
	}
	if !strings.Contains(sentQ, "~") || !strings.Contains(sentQ, "[REDACTED_API_KEY]") {
		t.Errorf("alphaxiv gateway missing redaction placeholder: %s", sentQ)
	}

	// 2. Stitch raw_tool with sensitive executable code WITHOUT opt-in is denied with 0 side effects
	stitchSensitive := map[string]any{
		"action":    "raw_tool",
		"tool_name": "cf_execute",
		"arguments": map[string]any{
			"code": "const token = 'ghp_123456789012345678901234567890123456';",
		},
	}
	_, err = mgr.CallGateway(context.Background(), "stitch", stitchSensitive, "")
	if err == nil {
		t.Fatal("expected error on stitch raw_tool sensitive code without opt-in, got nil")
	}
	if recStitch.CallCount() != 0 {
		t.Fatalf("SECURITY VIOLATION: stitch raw_tool executable deny caused %d transport calls", recStitch.CallCount())
	}

	// 3. Stitch raw_tool with allow_sensitive=true passes intact
	stitchOptIn := map[string]any{
		"action":          "raw_tool",
		"tool_name":       "cf_execute",
		"allow_sensitive": true,
		"arguments": map[string]any{
			"code": "const token = 'ghp_123456789012345678901234567890123456';",
		},
	}
	_, err = mgr.CallGateway(context.Background(), "stitch", stitchOptIn, "")
	if err != nil {
		t.Fatalf("unexpected error on stitch raw_tool opt-in: %v", err)
	}
	if recStitch.CallCount() != 1 {
		t.Fatalf("expected 1 call to stitch client, got %d", recStitch.CallCount())
	}
	sentCode := recStitch.callArgs[0]["code"].(string)
	if sentCode != "const token = 'ghp_123456789012345678901234567890123456';" {
		t.Errorf("stitch opt-in code was altered: %s", sentCode)
	}
}

func TestHTTPUpstreamClient_RecordingFixture_AndCredentialRotation(t *testing.T) {
	var mu sync.Mutex
	var receivedHeaders []http.Header
	var receivedBodies [][]byte
	responseStatus := http.StatusOK
	responseBody := `{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		receivedHeaders = append(receivedHeaders, r.Header.Clone())
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBodies = append(receivedBodies, bodyBytes)

		if responseStatus != http.StatusOK {
			w.WriteHeader(responseStatus)
			_, _ = w.Write([]byte(responseBody))
			return
		}

		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "initialize":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"status": "ok"},
			})
		}
	}))
	defer ts.Close()

	// 1. Initial client with initial credentials
	client := NewHTTPUpstreamClient("test_remote", "test", ts.URL, map[string]string{"X-Test-Header": "v1"}, "token-v1", false)
	client.SetHTTPClient(ts.Client())

	res, err := client.CallTool(context.Background(), "test_tool", map[string]any{
		"prompt": "Harmless prompt",
	})
	if err != nil {
		t.Fatalf("unexpected error on CallTool: %v", err)
	}
	if resMap, ok := res.(map[string]any); !ok || resMap["status"] != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}

	mu.Lock()
	if len(receivedHeaders) != 3 {
		t.Fatalf("expected 3 requests (initialize, notifications/initialized, tools/call), got %d", len(receivedHeaders))
	}
	for i, hdr := range receivedHeaders {
		if hdr.Get("Authorization") != "Bearer token-v1" {
			t.Errorf("req[%d]: expected Authorization Bearer token-v1, got %q", i, hdr.Get("Authorization"))
		}
		if hdr.Get("X-Test-Header") != "v1" {
			t.Errorf("req[%d]: expected X-Test-Header v1, got %q", i, hdr.Get("X-Test-Header"))
		}
		if !strings.Contains(hdr.Get("Accept"), "application/json") {
			t.Errorf("req[%d]: expected Accept header containing application/json, got %q", i, hdr.Get("Accept"))
		}
	}
	mu.Unlock()

	// 2. Credential Rotation
	client.UpdateCredentials("token-v2-rotated", map[string]string{"X-Test-Header": "v2-rotated"})
	gotToken, gotHeaders := client.GetCredentials()
	if gotToken != "token-v2-rotated" || gotHeaders["X-Test-Header"] != "v2-rotated" {
		t.Fatalf("GetCredentials mismatch after rotation: token=%q, headers=%+v", gotToken, gotHeaders)
	}

	// 3. Second call with rotated credentials
	_, err = client.CallTool(context.Background(), "test_tool", map[string]any{
		"prompt": "Second call",
	})
	if err != nil {
		t.Fatalf("unexpected error on second CallTool: %v", err)
	}

	mu.Lock()
	if len(receivedHeaders) != 6 {
		t.Fatalf("expected 6 requests (3 initial + 3 rotated), got %d", len(receivedHeaders))
	}
	for i := 3; i < 6; i++ {
		hdr := receivedHeaders[i]
		if hdr.Get("Authorization") != "Bearer token-v2-rotated" {
			t.Errorf("req[%d]: expected rotated Authorization Bearer token-v2-rotated, got %q", i, hdr.Get("Authorization"))
		}
		if hdr.Get("X-Test-Header") != "v2-rotated" {
			t.Errorf("req[%d]: expected rotated X-Test-Header v2-rotated, got %q", i, hdr.Get("X-Test-Header"))
		}
	}
	mu.Unlock()

	// 4. Server returns HTTP 500 error body containing simulated tokens and paths
	// Verify CallTool redacts diagnostics
	mu.Lock()
	responseStatus = http.StatusInternalServerError
	responseBody = `Internal error at /home/admin/server.go token sk-1234567890123456789012345 failed`
	mu.Unlock()

	_, err = client.CallTool(context.Background(), "test_tool", map[string]any{"prompt": "error test"})
	if err == nil {
		t.Fatal("expected error from 500 response, got nil")
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, "/home/admin") || strings.Contains(errMsg, "sk-1234567890123456789012345") {
		t.Errorf("error diagnostics leaked raw credentials or home paths: %s", errMsg)
	}
	if !strings.Contains(errMsg, "~") || !strings.Contains(errMsg, "[REDACTED_API_KEY]") {
		t.Errorf("error diagnostics missing redaction placeholders: %s", errMsg)
	}
}

func TestInterceptAutoIngest_Redaction(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open error: %v", err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	proj, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatalf("EnsureProject error: %v", err)
	}

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"drizzle": {Prefix: "drizzle", AutoIngest: true, Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(st, cfg)

	// Simulate interceptAutoIngest with sensitive doc content
	rawResult := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Drizzle migration runbook from /home/developer/secrets with API token sk-1234567890123456789012345. Ensure tables are indexed properly and keep database safe.",
			},
		},
	}
	mgr.interceptAutoIngest("drizzle", "drizzle_fetch_page", map[string]any{"slug": "/home/developer/docs"}, rawResult, proj.ID)

	docs, err := st.DocsByProject(proj.ID, 5)
	if err != nil {
		t.Fatalf("DocsByProject error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected intercepted doc in store, got 0")
	}

	doc := docs[0]
	if strings.Contains(doc.Title, "/home/developer") {
		t.Errorf("doc title leaked home path: %s", doc.Title)
	}
	if strings.Contains(doc.Content, "/home/developer") || strings.Contains(doc.Content, "sk-1234567890123456789012345") {
		t.Errorf("doc content leaked credentials: %s", doc.Content)
	}
	if !strings.Contains(doc.Content, "~") || !strings.Contains(doc.Content, "[REDACTED_API_KEY]") {
		t.Errorf("doc content missing expected redaction: %s", doc.Content)
	}
}

func TestHTTPUpstreamClient_NegotiatedSession_HandshakeHeaders(t *testing.T) {
	var mu sync.Mutex
	var receivedMethods []string
	var receivedSessionIDs []string
	var receivedAcceptHeaders []string

	sessionID := "test-session-proto-123"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		receivedMethods = append(receivedMethods, rpcReq.Method)
		receivedSessionIDs = append(receivedSessionIDs, r.Header.Get("Mcp-Session-Id"))
		receivedAcceptHeaders = append(receivedAcceptHeaders, r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", sessionID)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"tools": map[string]any{"listChanged": true},
					},
					"serverInfo": map[string]any{
						"name":    "test-proto-server",
						"version": "1.0.0",
					},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/call":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result": map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "tool success"},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer ts.Close()

	client := NewHTTPUpstreamClient("proto_server", "proto", ts.URL, nil, "auth-token", false)
	client.SetHTTPClient(ts.Client())

	res, err := client.CallTool(context.Background(), "greet", map[string]any{"prompt": "hello"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	resMap, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T: %+v", res, res)
	}
	content := resMap["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content in result")
	}

	mu.Lock()
	defer mu.Unlock()

	// Verify the 3-step sequence: initialize -> notifications/initialized -> tools/call
	expectedMethods := []string{"initialize", "notifications/initialized", "tools/call"}
	if len(receivedMethods) != len(expectedMethods) {
		t.Fatalf("expected %d requests, got %d: %v", len(expectedMethods), len(receivedMethods), receivedMethods)
	}
	for i, expected := range expectedMethods {
		if receivedMethods[i] != expected {
			t.Errorf("step %d: expected method %q, got %q", i, expected, receivedMethods[i])
		}
		if !strings.Contains(receivedAcceptHeaders[i], "application/json") || !strings.Contains(receivedAcceptHeaders[i], "text/event-stream") {
			t.Errorf("step %d: expected Accept with json and sse, got %q", i, receivedAcceptHeaders[i])
		}
	}

	// Step 0 (initialize) had no session yet. Steps 1 and 2 must carry the captured session ID.
	if receivedSessionIDs[0] != "" {
		t.Errorf("step 0: expected empty session ID before initialize, got %q", receivedSessionIDs[0])
	}
	if receivedSessionIDs[1] != sessionID {
		t.Errorf("step 1: expected session ID %q in initialized notification, got %q", sessionID, receivedSessionIDs[1])
	}
	if receivedSessionIDs[2] != sessionID {
		t.Errorf("step 2: expected session ID %q in tools/call, got %q", sessionID, receivedSessionIDs[2])
	}

	if client.SessionID() != sessionID {
		t.Errorf("client SessionID mismatch: expected %q, got %q", sessionID, client.SessionID())
	}
	if client.ProtocolVersion() != "2024-11-05" {
		t.Errorf("client ProtocolVersion mismatch: expected '2024-11-05', got %q", client.ProtocolVersion())
	}
	if !client.IsInitialized() {
		t.Error("expected client to be initialized")
	}
}

func TestHTTPUpstreamClient_SSE_ResponseDecoding(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		switch rpcReq.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if ok {
				flusher.Flush()
			}

			// Send SSE frame containing JSON-RPC result
			resJSON, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result": map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "sse stream result verified"},
					},
					"isError": false,
				},
			})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", resJSON)
			if ok {
				flusher.Flush()
			}
		}
	}))
	defer ts.Close()

	client := NewHTTPUpstreamClient("sse_server", "sse", ts.URL, nil, "", false)
	client.SetHTTPClient(ts.Client())

	res, err := client.CallTool(context.Background(), "query", map[string]any{"prompt": "sse test"})
	if err != nil {
		t.Fatalf("CallTool over SSE failed: %v", err)
	}

	resMap, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T: %+v", res, res)
	}
	content := resMap["content"].([]any)
	if len(content) == 0 {
		t.Fatal("expected content from SSE frame")
	}
	txt := content[0].(map[string]any)["text"].(string)
	if txt != "sse stream result verified" {
		t.Errorf("unexpected content text: %s", txt)
	}
}

func TestHTTPUpstreamClient_SSE_ErrorEvent(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		switch rpcReq.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if ok {
				flusher.Flush()
			}
			fmt.Fprintf(w, "event: error\ndata: cluster node disconnected\n\n")
			if ok {
				flusher.Flush()
			}
		}
	}))
	defer ts.Close()

	client := NewHTTPUpstreamClient("sse_err_server", "sse_err", ts.URL, nil, "", false)
	client.SetHTTPClient(ts.Client())

	_, err := client.CallTool(context.Background(), "query", map[string]any{"prompt": "error test"})
	if err == nil {
		t.Fatal("expected error from SSE error event, got nil")
	}
	if !strings.Contains(err.Error(), "sse error event received") || !strings.Contains(err.Error(), "cluster node disconnected") {
		t.Errorf("expected explicit sse error event message, got: %v", err)
	}
}

func TestHTTPUpstreamClient_MismatchedResponseID(t *testing.T) {
	// 1. JSON response with mismatched ID
	tsJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		w.Header().Set("Content-Type", "application/json")
		if rpcReq.Method == "initialize" {
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
			return
		}
		if rpcReq.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Return wrong ID
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      999999, // Mismatched ID!
			"result":  map[string]any{"status": "ok"},
		})
	}))
	defer tsJSON.Close()

	clientJSON := NewHTTPUpstreamClient("mismatch_json", "mm", tsJSON.URL, nil, "", false)
	clientJSON.SetHTTPClient(tsJSON.Client())

	_, err := clientJSON.CallTool(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error on mismatched JSON response ID, got nil")
	}
	if !strings.Contains(err.Error(), "mismatched response ID") {
		t.Errorf("expected 'mismatched response ID' error, got %v", err)
	}

	// 2. SSE response with mismatched ID
	tsSSE := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		if rpcReq.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
			return
		}
		if rpcReq.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}
		fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":888888,\"result\":{}}\n\n")
		if ok {
			flusher.Flush()
		}
	}))
	defer tsSSE.Close()

	clientSSE := NewHTTPUpstreamClient("mismatch_sse", "mm", tsSSE.URL, nil, "", false)
	clientSSE.SetHTTPClient(tsSSE.Client())

	_, err = clientSSE.CallTool(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error on mismatched SSE response ID, got nil")
	}
	if !strings.Contains(err.Error(), "mismatched response ID") {
		t.Errorf("expected 'mismatched response ID' error from SSE, got %v", err)
	}
}

func TestHTTPUpstreamClient_MalformedSSE(t *testing.T) {
	// 1. Malformed SSE line without colon
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		if rpcReq.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
			return
		}
		if rpcReq.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "this_is_an_invalid_sse_line_without_colon\n\n")
	}))
	defer ts1.Close()

	client1 := NewHTTPUpstreamClient("malformed_sse1", "mm", ts1.URL, nil, "", false)
	client1.SetHTTPClient(ts1.Client())
	_, err := client1.CallTool(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error on malformed SSE line, got nil")
	}
	if !strings.Contains(err.Error(), "malformed sse line") {
		t.Errorf("expected malformed sse line error, got: %v", err)
	}

	// 2. Malformed JSON inside data frame
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		if rpcReq.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
			return
		}
		if rpcReq.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "event: message\ndata: {not-valid-json\n\n")
	}))
	defer ts2.Close()

	client2 := NewHTTPUpstreamClient("malformed_sse2", "mm", ts2.URL, nil, "", false)
	client2.SetHTTPClient(ts2.Client())
	_, err = client2.CallTool(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("expected error on malformed JSON in SSE, got nil")
	}
	if !strings.Contains(err.Error(), "malformed sse data frame") {
		t.Errorf("expected malformed sse data frame error, got: %v", err)
	}
}

func TestHTTPUpstreamClient_SessionExpiry_AndReinitialization(t *testing.T) {
	var mu sync.Mutex
	initCount := 0
	sessionExpired := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		switch rpcReq.Method {
		case "initialize":
			initCount++
			sessID := fmt.Sprintf("session-epoch-%d", initCount)
			w.Header().Set("Mcp-Session-Id", sessID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/call":
			// If session-epoch-1 is used after expiration, return 404
			sessHeader := r.Header.Get("Mcp-Session-Id")
			if sessHeader == "session-epoch-1" && sessionExpired {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"session not found or expired"}`))
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"status": "success", "session": sessHeader},
			})
		}
	}))
	defer ts.Close()

	client := NewHTTPUpstreamClient("reinit_server", "reinit", ts.URL, nil, "", false)
	client.SetHTTPClient(ts.Client())

	// First call establishes session-epoch-1
	res1, err := client.CallTool(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("first CallTool failed: %v", err)
	}
	if client.SessionID() != "session-epoch-1" {
		t.Fatalf("expected session-epoch-1, got %q", client.SessionID())
	}
	if resMap, ok := res1.(map[string]any); !ok || resMap["session"] != "session-epoch-1" {
		t.Fatalf("unexpected res1: %+v", res1)
	}

	// Trigger server-side session expiration
	mu.Lock()
	sessionExpired = true
	mu.Unlock()

	// Second call should encounter HTTP 404, reinitialize (acquiring session-epoch-2), and succeed!
	res2, err := client.CallTool(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("second CallTool after expiry failed: %v", err)
	}
	if client.SessionID() != "session-epoch-2" {
		t.Fatalf("expected session-epoch-2 after reinitialization, got %q", client.SessionID())
	}
	if resMap, ok := res2.(map[string]any); !ok || resMap["session"] != "session-epoch-2" {
		t.Fatalf("unexpected res2: %+v", res2)
	}

	mu.Lock()
	if initCount != 2 {
		t.Errorf("expected exactly 2 initialize handshakes, got %d", initCount)
	}
	mu.Unlock()
}

func TestHTTPUpstreamClient_ConcurrentFirstCalls_SafeInitialization(t *testing.T) {
	var mu sync.Mutex
	initCount := 0

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "initialize":
			mu.Lock()
			initCount++
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // Simulate handshake network latency
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/call":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"ok": true},
			})
		}
	}))
	defer ts.Close()

	client := NewHTTPUpstreamClient("concurrent_server", "concur", ts.URL, nil, "", false)
	client.SetHTTPClient(ts.Client())

	concurrency := 10
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := client.CallTool(context.Background(), "task", map[string]any{"idx": idx})
			if err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}

	mu.Lock()
	if initCount != 1 {
		t.Errorf("expected exactly 1 initialize call under concurrent invocation, got %d", initCount)
	}
	mu.Unlock()
}

func TestHTTPUpstreamClient_DiscoverTools(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "initialize":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/list":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result": map[string]any{
					"tools": []any{
						map[string]any{
							"name":        "search_nodes",
							"description": "Search remote cluster nodes",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"pattern": map[string]any{"type": "string", "description": "regex pattern"},
								},
								"required": []any{"pattern"},
							},
						},
					},
				},
			})
		}
	}))
	defer ts.Close()

	client := NewHTTPUpstreamClient("remote_disc", "disc", ts.URL, nil, "", false)
	client.SetHTTPClient(ts.Client())

	tools, err := client.DiscoverTools(context.Background())
	if err != nil {
		t.Fatalf("DiscoverTools failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 discovered tool, got %d", len(tools))
	}
	if tools[0].Name != "search_nodes" {
		t.Errorf("expected tool name 'search_nodes', got %q", tools[0].Name)
	}

	// Test registration via UpstreamManager.DiscoverServerTools
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"remote_disc": {Prefix: "disc", Command: "http"},
		},
	}
	mgr := NewUpstreamManager(nil, cfg)
	mgr.SetClient("remote_disc", client)

	discTools, err := mgr.DiscoverServerTools(context.Background(), "remote_disc")
	if err != nil {
		t.Fatalf("DiscoverServerTools failed: %v", err)
	}
	if len(discTools) != 1 {
		t.Fatalf("expected 1 discovered tool registered, got %d", len(discTools))
	}
	if !mgr.HasTool("disc_search_nodes") {
		t.Error("expected tool 'disc_search_nodes' to be registered in UpstreamManager")
	}

	// Test discovery failure reporting
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		w.Header().Set("Content-Type", "application/json")
		switch rpcReq.Method {
		case "initialize":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
		case "tools/list":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("500 internal server error"))
		}
	}))
	defer tsErr.Close()

	clientErr := NewHTTPUpstreamClient("fail_disc", "fail", tsErr.URL, nil, "", false)
	clientErr.SetHTTPClient(tsErr.Client())

	cfgErr := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"fail_disc": {Prefix: "fail", Command: "http"},
		},
	}
	mgrErr := NewUpstreamManager(nil, cfgErr)
	mgrErr.SetClient("fail_disc", clientErr)

	_, err = mgrErr.DiscoverServerTools(context.Background(), "fail_disc")
	if err == nil {
		t.Fatal("expected error on failed tool discovery, got nil")
	}
	if !strings.Contains(err.Error(), "tool discovery failed") {
		t.Errorf("expected explicit tool discovery failed error, got: %v", err)
	}
}

func TestHTTPUpstreamClient_StalledStream_Cancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		var rpcReq JSONRPCRequest
		_ = json.Unmarshal(bodyBytes, &rpcReq)

		if rpcReq.Method == "initialize" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      rpcReq.ID,
				"result":  map[string]any{"protocolVersion": "2024-11-05"},
			})
			return
		}
		if rpcReq.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Stall indefinitely on tools/call
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(2 * time.Second)
	}))
	defer ts.Close()

	client := NewHTTPUpstreamClient("stall_server", "stall", ts.URL, nil, "", false)
	client.SetHTTPClient(ts.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := client.CallTool(ctx, "slow_query", nil)
	if err == nil {
		t.Fatal("expected timeout error on stalled stream, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("expected context deadline exceeded error, got: %v", err)
	}
}

func TestStitchGateway_GenerateVariants_ActionDispatch(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		StitchAPIKey:        "test-stitch-key",
	}
	mgr := NewUpstreamManager(nil, cfg)

	recordingClient := newRecordingClient("stitch", "stitch")
	mgr.SetClient("stitch", recordingClient)

	// 1. Advertised action "generate_variants"
	res, err := mgr.CallGateway(context.Background(), "stitch", map[string]any{
		"action":     "generate_variants",
		"project_id": "proj-xyz",
		"prompt":     "modern dark theme variant",
		"screen_id":  "scr-123",
	}, "")
	if err != nil {
		t.Fatalf("CallGateway with generate_variants failed: %v", err)
	}
	if resMap, ok := res.(map[string]any); !ok || resMap["status"] != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}

	recordingClient.mu.Lock()
	if len(recordingClient.calls) != 1 {
		recordingClient.mu.Unlock()
		t.Fatalf("expected 1 call, got %d", len(recordingClient.calls))
	}
	toolName := recordingClient.calls[0]
	toolArgs := recordingClient.callArgs[0]
	recordingClient.mu.Unlock()

	if toolName != "generate_variants" {
		t.Errorf("expected dispatched tool name 'generate_variants', got %q", toolName)
	}
	if toolArgs["projectId"] != "proj-xyz" || toolArgs["prompt"] != "modern dark theme variant" {
		t.Errorf("unexpected arguments: %+v", toolArgs)
	}
	screenIDs, ok := toolArgs["selectedScreenIds"].([]string)
	if !ok || len(screenIDs) != 1 || screenIDs[0] != "scr-123" {
		t.Errorf("expected selectedScreenIds ['scr-123'], got %+v", toolArgs["selectedScreenIds"])
	}

	// 2. Legacy alias "variants"
	_, err = mgr.CallGateway(context.Background(), "stitch", map[string]any{
		"action":     "variants",
		"project_id": "proj-xyz",
		"prompt":     "legacy alias",
	}, "")
	if err != nil {
		t.Fatalf("CallGateway with legacy 'variants' failed: %v", err)
	}

	// 3. Unknown action reporting truthful supported list
	_, err = mgr.CallGateway(context.Background(), "stitch", map[string]any{
		"action": "nonexistent_action",
	}, "")
	if err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
	if !strings.Contains(err.Error(), "generate_variants") {
		t.Errorf("expected error message to mention 'generate_variants', got: %v", err)
	}
}

func TestInterceptAutoIngest_IsError_NotPersisted(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open error: %v", err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	proj, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatalf("EnsureProject error: %v", err)
	}

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"drizzle": {Prefix: "drizzle", AutoIngest: true, Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(st, cfg)

	// 1. Tool result with isError: true must NEVER be persisted
	errResult := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Detailed error log from remote Drizzle ORM endpoint describing broken table migration and schema mismatch in full detail.",
			},
		},
		"isError": true,
	}
	mgr.interceptAutoIngest("drizzle", "drizzle_fetch_page", map[string]any{"slug": "schema-declaration"}, errResult, proj.ID)

	docs, err := st.DocsByProject(proj.ID, 10)
	if err != nil {
		t.Fatalf("DocsByProject error: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs persisted when isError is true, got %d", len(docs))
	}

	// 2. Successful result with isError: false should be persisted
	successResult := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Valid Drizzle ORM documentation describing schema declaration, relations, and type inference for TypeScript and SQLite.",
			},
		},
		"isError": false,
	}
	mgr.interceptAutoIngest("drizzle", "drizzle_fetch_page", map[string]any{"slug": "schema-declaration"}, successResult, proj.ID)

	docs, err = st.DocsByProject(proj.ID, 10)
	if err != nil {
		t.Fatalf("DocsByProject error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc persisted for successful tool result, got %d", len(docs))
	}
	if !strings.Contains(docs[0].Title, "Drizzle ORM: schema-declaration") {
		t.Errorf("unexpected doc title: %s", docs[0].Title)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	mode := os.Getenv("GO_HELPER_MODE")

	switch mode {
	case "fail_init":
		os.Exit(2)

	case "silent_init":
		time.Sleep(10 * time.Minute)
		os.Exit(0)

	case "delayed_init":
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var req map[string]any
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			method, _ := req["method"].(string)
			id := req["id"]

			switch method {
			case "initialize":
				time.Sleep(300 * time.Millisecond)
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]any{},
						"serverInfo": map[string]any{
							"name":    "delayed-server",
							"version": "1.0.0",
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
			case "notifications/initialized":
				// no response needed
			case "tools/call":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "echo from delayed server",
							},
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
			}
		}
		os.Exit(0)

	case "silent_call":
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var req map[string]any
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			method, _ := req["method"].(string)
			id := req["id"]

			switch method {
			case "initialize":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]any{},
						"serverInfo": map[string]any{
							"name":    "silent-call-server",
							"version": "1.0.0",
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
			case "tools/call":
				// Hang forever until reaped/killed
				time.Sleep(10 * time.Minute)
			}
		}
		os.Exit(0)

	case "early_exit":
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var req map[string]any
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			method, _ := req["method"].(string)
			id := req["id"]

			if method == "initialize" {
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]any{},
						"serverInfo": map[string]any{
							"name":    "early-exit-server",
							"version": "1.0.0",
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
				os.Exit(0)
			}
		}
		os.Exit(0)

	case "malformed_output":
		fmt.Println("INFO: server booting up")
		fmt.Println("[DEBUG] unparsed raw log line 1")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var req map[string]any
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			method, _ := req["method"].(string)
			id := req["id"]

			switch method {
			case "initialize":
				fmt.Println("{malformed json line 123}")
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]any{},
						"serverInfo": map[string]any{
							"name":    "malformed-server",
							"version": "1.0.0",
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
				fmt.Println("NOTICE: handshake complete")
			case "tools/call":
				fmt.Println("DEBUG: processing tool call")
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "clean result despite noisy stdout",
							},
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
				fmt.Println("DEBUG: tool call done")
			}
		}
		os.Exit(0)

	default: // "echo_server"
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var req map[string]any
			if err := json.Unmarshal([]byte(line), &req); err != nil {
				continue
			}
			method, _ := req["method"].(string)
			id := req["id"]

			switch method {
			case "initialize":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]any{},
						"serverInfo": map[string]any{
							"name":    "echo-server",
							"version": "1.0.0",
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
			case "notifications/initialized":
				// no response needed
			case "tools/list":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"tools": []any{
							map[string]any{
								"name":        "echo",
								"description": "Echo tool",
								"inputSchema": map[string]any{
									"type": "object",
								},
							},
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
			case "tools/call":
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "echoed result",
							},
						},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Println(string(data))
			}
		}
		os.Exit(0)
	}
}

func TestStdioUpstreamClient_ConcurrentFirstCalls_SharedHandshake(t *testing.T) {
	helperCmd, err := os.Executable()
	if err != nil {
		helperCmd = os.Args[0]
	}
	client := NewStdioUpstreamClient("delayed", "delayed", helperCmd, []string{"-test.run=^TestHelperProcess$"}, map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
		"GO_HELPER_MODE":         "delayed_init",
	}, false)
	defer client.Close()

	const concurrent = 10
	var wg sync.WaitGroup
	errs := make([]error, concurrent)
	results := make([]any, concurrent)

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			res, err := client.CallTool(ctx, "echo", map[string]any{"idx": idx})
			errs[idx] = err
			results[idx] = res
		}(i)
	}

	wg.Wait()

	for i := 0; i < concurrent; i++ {
		if errs[i] != nil {
			t.Fatalf("call %d failed unexpectedly: %v", i, errs[i])
		}
		resMap, ok := results[i].(map[string]any)
		if !ok {
			t.Fatalf("call %d returned invalid result type: %T", i, results[i])
		}
		content, ok := resMap["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("call %d returned empty content: %v", i, results[i])
		}
	}

	if !client.IsRunning() {
		t.Error("expected client to be running after successful initialization")
	}
}

func TestStdioUpstreamClient_DelayedInitialization_FailedStartup_BoundedError(t *testing.T) {
	helperCmd, err := os.Executable()
	if err != nil {
		helperCmd = os.Args[0]
	}
	client := NewStdioUpstreamClient("fail-init", "fail", helperCmd, []string{"-test.run=^TestHelperProcess$"}, map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
		"GO_HELPER_MODE":         "fail_init",
	}, false)
	defer client.Close()

	const concurrent = 8
	var wg sync.WaitGroup
	errs := make([]error, concurrent)

	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.CallTool(ctx, "echo", map[string]any{"idx": idx})
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	for i := 0; i < concurrent; i++ {
		if errs[i] == nil {
			t.Fatalf("call %d expected error, got nil", i)
		}
	}

	if client.IsRunning() {
		t.Error("expected client to NOT be running after failed startup")
	}

	ps := client.ProcessState()
	if ps == nil {
		t.Fatal("expected non-nil ProcessState after child exit")
	}
	if !ps.Exited() {
		t.Errorf("expected child process to be exited, got: %v", ps)
	}
}

func TestStdioUpstreamClient_ChildReaped_OnEOF_And_RepeatedClose(t *testing.T) {
	helperCmd, err := os.Executable()
	if err != nil {
		helperCmd = os.Args[0]
	}
	client := NewStdioUpstreamClient("early-exit", "early", helperCmd, []string{"-test.run=^TestHelperProcess$"}, map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
		"GO_HELPER_MODE":         "early_exit",
	}, false)

	// First call initializes and child exits immediately
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = client.CallTool(ctx, "echo", map[string]any{})

	// Wait briefly for EOF reaper goroutine to finish
	time.Sleep(100 * time.Millisecond)

	if client.IsRunning() {
		t.Error("expected client to not be running after child EOF exit")
	}

	// Repeated Close() must be idempotent, safe, and not panic or race
	for i := 0; i < 5; i++ {
		if err := client.Close(); err != nil {
			t.Errorf("Close() call %d returned error: %v", i, err)
		}
	}

	ps := client.ProcessState()
	if ps == nil {
		t.Fatal("expected non-nil ProcessState after child reaped on EOF")
	}
	if !ps.Exited() {
		t.Errorf("expected child process to have exited, got: %v", ps)
	}
}

func TestStdioUpstreamClient_SilentChild_TimeoutAndCancellation(t *testing.T) {
	helperCmd, err := os.Executable()
	if err != nil {
		helperCmd = os.Args[0]
	}
	client := NewStdioUpstreamClient("silent-call", "silent", helperCmd, []string{"-test.run=^TestHelperProcess$"}, map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
		"GO_HELPER_MODE":         "silent_call",
	}, false)
	defer client.Close()

	// CallTool with short deadline: must time out and reap silent child
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = client.CallTool(ctx, "echo", map[string]any{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from silent child call, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
		t.Errorf("expected deadline exceeded error, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("expected call to return promptly on timeout, took %v", elapsed)
	}

	// Child must be reaped on timeout (either exited or terminated by kill signal)
	ps := client.ProcessState()
	if ps == nil {
		t.Fatal("expected non-nil ProcessState after silent child timeout reap")
	}
	if !ps.Exited() && !strings.Contains(ps.String(), "killed") {
		t.Errorf("expected silent child process to have exited or been killed after reap, got: %v", ps)
	}
}

func TestStdioUpstreamClient_MalformedOutput_IgnoredWithoutCrashing(t *testing.T) {
	helperCmd, err := os.Executable()
	if err != nil {
		helperCmd = os.Args[0]
	}
	client := NewStdioUpstreamClient("malformed", "malformed", helperCmd, []string{"-test.run=^TestHelperProcess$"}, map[string]string{
		"GO_WANT_HELPER_PROCESS": "1",
		"GO_HELPER_MODE":         "malformed_output",
	}, false)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	res, err := client.CallTool(ctx, "echo", map[string]any{})
	if err != nil {
		t.Fatalf("expected successful call despite noisy stdout, got: %v", err)
	}

	resMap, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type: %T", res)
	}
	content, ok := resMap["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("unexpected content format: %v", res)
	}
}

func TestUpstreamManager_ConcurrentCalls_Toggles_Close_Race(t *testing.T) {
	helperCmd, err := os.Executable()
	if err != nil {
		helperCmd = os.Args[0]
	}

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"echo": {
				Prefix:  "echo",
				Command: helperCmd,
				Args:    []string{"-test.run=^TestHelperProcess$"},
				Env: map[string]string{
					"GO_WANT_HELPER_PROCESS": "1",
					"GO_HELPER_MODE":         "echo_server",
				},
			},
		},
	}
	mgr := NewUpstreamManager(st, cfg)
	defer mgr.Close()

	// Register the echo tool dynamically
	mgr.RegisterServerTools("echo", []Tool{
		{
			Name:        "echo_ping",
			Description: "Ping echo",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{Type: "object"},
		},
	})

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Workers making tool calls
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for ctx.Err() == nil {
				callCtx, callCancel := context.WithTimeout(ctx, 300*time.Millisecond)
				_, _ = mgr.CallTool(callCtx, "echo_ping", map[string]any{"id": workerID}, "")
				callCancel()
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	// Worker toggling server
	wg.Add(1)
	go func() {
		defer wg.Done()
		state := true
		for ctx.Err() == nil {
			state = !state
			_, _ = mgr.ToggleServer("echo", state)
			time.Sleep(30 * time.Millisecond)
		}
	}()

	// Worker reading statuses and tools
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ctx.Err() == nil {
			_ = mgr.GetStatuses()
			_ = mgr.ListTools()
			_ = mgr.HasTool("echo_ping")
			time.Sleep(15 * time.Millisecond)
		}
	}()

	wg.Wait()

	// Close should succeed cleanly under race
	if err := mgr.Close(); err != nil {
		t.Errorf("mgr.Close() error: %v", err)
	}
}

func TestInterceptAutoIngest_ErrorsAndNonReferenceActions_CreateNoDocs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	proj, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"drizzle":    {Prefix: "drizzle", AutoIngest: true, Command: "dummy"},
			"cloudflare": {Prefix: "cf", AutoIngest: true, Command: "dummy"},
			"alphaxiv":   {Prefix: "alphaxiv", AutoIngest: true, Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(st, cfg)

	validContent := "Valid documentation text describing table schemas, database indexes, and relational query structures for SQLite."

	testCases := []struct {
		name       string
		serverName string
		toolName   string
		args       map[string]any
		result     any
	}{
		{
			name:       "nil result",
			serverName: "drizzle",
			toolName:   "drizzle_fetch_page",
			args:       map[string]any{"slug": "test-slug"},
			result:     nil,
		},
		{
			name:       "isError true",
			serverName: "drizzle",
			toolName:   "drizzle_fetch_page",
			args:       map[string]any{"slug": "test-slug"},
			result: map[string]any{
				"isError": true,
				"content": []any{map[string]any{"type": "text", "text": validContent}},
			},
		},
		{
			name:       "error object present",
			serverName: "drizzle",
			toolName:   "drizzle_fetch_page",
			args:       map[string]any{"slug": "test-slug"},
			result: map[string]any{
				"error":   map[string]any{"code": -32603, "message": "Internal error"},
				"content": []any{map[string]any{"type": "text", "text": validContent}},
			},
		},
		{
			name:       "status 401 unauthorized",
			serverName: "drizzle",
			toolName:   "drizzle_fetch_page",
			args:       map[string]any{"slug": "test-slug"},
			result: map[string]any{
				"status":  401.0,
				"content": []any{map[string]any{"type": "text", "text": validContent}},
			},
		},
		{
			name:       "content contains 401 unauthorized",
			serverName: "drizzle",
			toolName:   "drizzle_fetch_page",
			args:       map[string]any{"slug": "test-slug"},
			result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "HTTP 401 Unauthorized: Invalid API key credentials supplied for documentation fetch."}},
			},
		},
		{
			name:       "content contains 403 forbidden",
			serverName: "drizzle",
			toolName:   "drizzle_fetch_page",
			args:       map[string]any{"slug": "test-slug"},
			result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "403 Forbidden: Access denied for requested endpoint resources and documentation."}},
			},
		},
		{
			name:       "non-reference tool drizzle_list_topics",
			serverName: "drizzle",
			toolName:   "drizzle_list_topics",
			args:       map[string]any{},
			result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": validContent}},
			},
		},
		{
			name:       "non-reference tool cf_execute",
			serverName: "cloudflare",
			toolName:   "cf_execute",
			args:       map[string]any{"code": "console.log('hi')"},
			result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": validContent}},
			},
		},
		{
			name:       "non-reference tool alphaxiv_discover_papers",
			serverName: "alphaxiv",
			toolName:   "alphaxiv_discover_papers",
			args:       map[string]any{"keywords": []any{"transformer"}},
			result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": validContent}},
			},
		},
		{
			name:       "short content under 50 chars",
			serverName: "drizzle",
			toolName:   "drizzle_fetch_page",
			args:       map[string]any{"slug": "test-slug"},
			result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "too short"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_ = mgr.interceptAutoIngest(tc.serverName, tc.toolName, tc.args, tc.result, proj.ID)
			docs, err := st.DocsByProject(proj.ID, 10)
			if err != nil {
				t.Fatalf("DocsByProject: %v", err)
			}
			if len(docs) != 0 {
				t.Fatalf("case %q: expected 0 docs persisted, got %d", tc.name, len(docs))
			}
		})
	}
}

func TestInterceptAutoIngest_DistinctSearches_RetainIndependentIdentity(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	proj, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"drizzle": {Prefix: "drizzle", AutoIngest: true, Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(st, cfg)

	// Search 1: sqlite foreign keys
	res1 := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Drizzle ORM documentation on SQLite foreign key constraints, cascade rules, and schema definitions.",
			},
		},
	}
	err = mgr.interceptAutoIngest("drizzle", "drizzle_search_docs", map[string]any{"query": "sqlite foreign keys"}, res1, proj.ID)
	if err != nil {
		t.Fatalf("interceptAutoIngest search 1 failed: %v", err)
	}

	// Search 2: d1 batching
	res2 := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Drizzle ORM guide for Cloudflare D1 batching, prepared statements, and transactional round-trip minimization.",
			},
		},
	}
	err = mgr.interceptAutoIngest("drizzle", "drizzle_search_docs", map[string]any{"query": "d1 batching"}, res2, proj.ID)
	if err != nil {
		t.Fatalf("interceptAutoIngest search 2 failed: %v", err)
	}

	// Both searches must be independently retrievable and not overwrite each other
	docs, err := st.DocsByProject(proj.ID, 10)
	if err != nil {
		t.Fatalf("DocsByProject: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 distinct documents for two different searches, got %d", len(docs))
	}

	if docs[0].ID == docs[1].ID {
		t.Fatalf("expected distinct doc IDs, but both got %q", docs[0].ID)
	}

	// Verify both searches find their respective doc via FTS
	resFTS1, err := st.SearchDocsFTS("foreign key", proj.ID, 5)
	if err != nil || len(resFTS1) == 0 {
		t.Fatalf("expected search 1 in FTS, got err=%v results=%d", err, len(resFTS1))
	}
	resFTS2, err := st.SearchDocsFTS("batching", proj.ID, 5)
	if err != nil || len(resFTS2) == 0 {
		t.Fatalf("expected search 2 in FTS, got err=%v results=%d", err, len(resFTS2))
	}

	// Repeated identical search 1 must update in-place without creating a 3rd doc
	res1Updated := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Updated Drizzle ORM documentation on SQLite foreign key constraints with updated syntax examples.",
			},
		},
	}
	err = mgr.interceptAutoIngest("drizzle", "drizzle_search_docs", map[string]any{"query": "sqlite foreign keys"}, res1Updated, proj.ID)
	if err != nil {
		t.Fatalf("interceptAutoIngest repeated search 1 failed: %v", err)
	}

	docsAfter, err := st.DocsByProject(proj.ID, 10)
	if err != nil {
		t.Fatalf("DocsByProject after repeated search: %v", err)
	}
	if len(docsAfter) != 2 {
		t.Fatalf("expected doc count to remain 2 after identical retrieval update, got %d", len(docsAfter))
	}
}

func TestInterceptAutoIngest_ProvenanceAndActualURLs(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	proj, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"drizzle":    {Prefix: "drizzle", AutoIngest: true, Command: "dummy"},
			"alphaxiv":   {Prefix: "alphaxiv", AutoIngest: true, Command: "dummy"},
			"cloudflare": {Prefix: "cf", AutoIngest: true, Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(st, cfg)

	// 1. Canonical page with actual page URL
	resPage := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Drizzle schema declaration guidelines for PostgreSQL, MySQL, and SQLite dialects with full TypeScript type inference.",
			},
		},
	}
	err = mgr.interceptAutoIngest("drizzle", "drizzle_fetch_page", map[string]any{"slug": "schema-declaration"}, resPage, proj.ID)
	if err != nil {
		t.Fatalf("interceptAutoIngest fetch_page failed: %v", err)
	}

	// 2. Query search with NO actual URL supplied -> URL must be empty, not a generic overview URL
	resQuery := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Cloudflare Workers routing rules, custom domain mappings, and route patterns across Cloudflare Edge zones.",
			},
		},
	}
	err = mgr.interceptAutoIngest("cloudflare", "cf_search", map[string]any{"query": "custom domain mapping"}, resQuery, proj.ID)
	if err != nil {
		t.Fatalf("interceptAutoIngest cf_search failed: %v", err)
	}

	// 3. AlphaXiv paper with actual paper URL
	resPaper := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Attention Is All You Need introduces the Transformer, the first sequence transduction model based entirely on self-attention.",
			},
		},
	}
	err = mgr.interceptAutoIngest("alphaxiv", "alphaxiv_get_paper", map[string]any{"paper_id": "1706.03762"}, resPaper, proj.ID)
	if err != nil {
		t.Fatalf("interceptAutoIngest get_paper failed: %v", err)
	}

	docs, err := st.DocsByProject(proj.ID, 10)
	if err != nil {
		t.Fatalf("DocsByProject: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}

	var foundCanonical, foundQuery, foundPaper bool
	for _, d := range docs {
		if strings.Contains(d.Title, "schema-declaration") {
			foundCanonical = true
			if d.URL != "https://orm.drizzle.team/schema-declaration" {
				t.Errorf("expected canonical URL 'https://orm.drizzle.team/schema-declaration', got %q", d.URL)
			}
		}
		if strings.Contains(d.Title, "custom domain mapping") {
			foundQuery = true
			if d.URL != "" {
				t.Errorf("generic overview URL must not be used for search queries, expected empty URL but got %q", d.URL)
			}
		}
		if strings.Contains(d.Title, "1706.03762") {
			foundPaper = true
			if d.URL != "https://alphaxiv.org/abs/1706.03762" {
				t.Errorf("expected paper URL 'https://alphaxiv.org/abs/1706.03762', got %q", d.URL)
			}
		}
	}
	if !foundCanonical || !foundQuery || !foundPaper {
		t.Errorf("did not find all expected documents: canonical=%v, query=%v, paper=%v", foundCanonical, foundQuery, foundPaper)
	}
}

func TestInterceptAutoIngest_BoundedSize_And_FailedStore(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(repoDir, 0755)
	proj, err := st.EnsureProject(repoDir)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"drizzle": {Prefix: "drizzle", AutoIngest: true, Command: "dummy"},
		},
	}
	mgr := NewUpstreamManager(st, cfg)

	// 1. Oversized content (>512KB) must be rejected with error and not inserted
	hugeText := strings.Repeat("X", MaxAutoIngestContentSize+100)
	hugeResult := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": hugeText,
			},
		},
	}
	err = mgr.interceptAutoIngest("drizzle", "drizzle_fetch_page", map[string]any{"slug": "huge-page"}, hugeResult, proj.ID)
	if err == nil {
		t.Fatal("expected error for oversized doc result, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Errorf("expected error message to mention size limit, got: %v", err)
	}

	docs, err := st.DocsByProject(proj.ID, 10)
	if err != nil {
		t.Fatalf("DocsByProject: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs persisted for oversized payload, got %d", len(docs))
	}

	// 2. Failed store write (closed database) must return an error and not claim cached success
	st.Close()
	validResult := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Valid Drizzle documentation text about relations and queries that should fail because store is closed.",
			},
		},
	}
	err = mgr.interceptAutoIngest("drizzle", "drizzle_fetch_page", map[string]any{"slug": "fail-page"}, validResult, proj.ID)
	if err == nil {
		t.Fatal("expected error when persisting to closed store, got nil")
	}
	if !strings.Contains(err.Error(), "failed to persist upstream doc") {
		t.Errorf("expected error message to mention persistence failure, got: %v", err)
	}
}

func TestAstroUpstream_AutoIngest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open error: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject("astro-web-app")
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
	}
	mgr := NewUpstreamManager(st, cfg)

	if !mgr.HasTool("astro_search_astro_docs") {
		t.Fatal("expected mgr.HasTool(astro_search_astro_docs) == true")
	}

	resAstro := map[string]any{
		"content": []any{
			map[string]any{
				"type": "text",
				"text": "Content collections are the best way to manage and author content in any Astro project. Collections help to organize your documents, validate your frontmatter, and provide automatic TypeScript type-safety for all your content.",
			},
		},
	}

	err = mgr.interceptAutoIngest("astro", "search_astro_docs", map[string]any{
		"query": "content collections",
		"url":   "https://docs.astro.build/en/guides/content-collections/",
	}, resAstro, proj.ID)
	if err != nil {
		t.Fatalf("interceptAutoIngest astro failed: %v", err)
	}

	docs, err := st.DocsByProject(proj.ID, 10)
	if err != nil {
		t.Fatalf("DocsByProject: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc persisted, got %d", len(docs))
	}

	if docs[0].Title != "Astro Docs: content collections" {
		t.Errorf("unexpected doc title: %s", docs[0].Title)
	}
	if docs[0].URL != "https://docs.astro.build/en/guides/content-collections/" {
		t.Errorf("unexpected doc URL: %s", docs[0].URL)
	}
	if !strings.Contains(docs[0].Content, "Content collections are the best way") {
		t.Errorf("unexpected doc content: %s", docs[0].Content)
	}
}

func TestScienceGateway_ToolCatalogueAndToggle(t *testing.T) {
	mgr := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: true})

	rawTools := []string{
		"science_search_papers",
		"science_get_papers",
		"science_find_similar_papers",
		"science_citation_walk",
		"science_map_topic",
		"science_read_topic_group",
		"science_find_people",
		"science_get_profile",
	}

	if !mgr.HasTool("science_query") {
		t.Fatal("expected science_query gateway in default catalogue")
	}
	for _, name := range rawTools {
		if mgr.HasTool(name) {
			t.Errorf("raw tool %q must stay hidden before toggle", name)
		}
	}
	for _, tool := range mgr.ListTools() {
		if tool.Name == "science_query" {
			continue
		}
		for _, name := range rawTools {
			if tool.Name == name {
				t.Errorf("raw tool %q leaked into default ListTools", name)
			}
		}
	}

	if _, err := mgr.ToggleServer("science", true); err != nil {
		t.Fatalf("ToggleServer('science', true): %v", err)
	}
	listed := make(map[string]bool)
	for _, tool := range mgr.ListTools() {
		listed[tool.Name] = true
	}
	if !listed["science_query"] {
		t.Error("science_query disappeared after toggle ON")
	}
	for _, name := range rawTools {
		if !listed[name] {
			t.Errorf("expected %q in ListTools after toggle ON", name)
		}
	}

	if _, err := mgr.ToggleServer("science", false); err != nil {
		t.Fatalf("ToggleServer('science', false): %v", err)
	}
	listedOff := make(map[string]bool)
	for _, tool := range mgr.ListTools() {
		listedOff[tool.Name] = true
	}
	if !listedOff["science_query"] {
		t.Error("science_query should remain visible after toggle OFF")
	}
	for _, name := range rawTools {
		if listedOff[name] {
			t.Errorf("raw tool %q remained visible after toggle OFF", name)
		}
	}
}

func TestScienceGateway_ActionDispatch(t *testing.T) {
	mgr := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: true})
	rec := newRecordingClient("science", "science")
	mgr.SetClient("science", rec)

	cases := []struct {
		name     string
		args     map[string]any
		wantTool string
		wantArgs map[string]any
	}{
		{
			name:     "search",
			args:     map[string]any{"action": "search", "query": "graph neural networks", "limit": 5, "mode": "semantic", "year_from": 2020, "abstracts": "short"},
			wantTool: "search_papers",
			wantArgs: map[string]any{"query": "graph neural networks", "limit": 5, "mode": "semantic", "year_from": 2020, "abstracts": "short"},
		},
		{
			name:     "get_paper",
			args:     map[string]any{"action": "get_paper", "ids": []any{"W123", "10.1000/xyz"}},
			wantTool: "get_papers",
			wantArgs: map[string]any{"ids": []string{"W123", "10.1000/xyz"}},
		},
		{
			name:     "similar",
			args:     map[string]any{"action": "similar", "id": "W123", "limit": 3},
			wantTool: "find_similar_papers",
			wantArgs: map[string]any{"id": "W123", "limit": 3},
		},
		{
			name:     "citation_walk",
			args:     map[string]any{"action": "citation_walk", "id": "W123", "max_neighbours": 4},
			wantTool: "citation_walk",
			wantArgs: map[string]any{"id": "W123", "max_neighbours": 4},
		},
		{
			name:     "map_topic",
			args:     map[string]any{"action": "map_topic", "topic": "protein folding", "scope": "quick"},
			wantTool: "map_topic",
			wantArgs: map[string]any{"topic": "protein folding", "scope": "quick"},
		},
		{
			name:     "read_topic_group",
			args:     map[string]any{"action": "read_topic_group", "topic": "protein folding", "group": 0, "part": "papers"},
			wantTool: "read_topic_group",
			wantArgs: map[string]any{"topic": "protein folding", "group": 0, "part": "papers"},
		},
		{
			name:     "find_people",
			args:     map[string]any{"action": "find_people", "name": "Ada Lovelace", "kind": "author"},
			wantTool: "find_people",
			wantArgs: map[string]any{"name": "Ada Lovelace", "kind": "author"},
		},
		{
			name:     "get_profile",
			args:     map[string]any{"action": "get_profile", "id": "A123", "list": "works", "offset": 10, "together_with": []any{"A9"}},
			wantTool: "get_profile",
			wantArgs: map[string]any{"id": "A123", "list": "works", "offset": 10, "together_with": []any{"A9"}},
		},
		{
			name:     "raw_tool",
			args:     map[string]any{"action": "raw_tool", "tool_name": "science_search_papers", "arguments": map[string]any{"query": "raw"}},
			wantTool: "search_papers",
			wantArgs: map[string]any{"query": "raw"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := rec.CallCount()
			if _, err := mgr.CallTool(context.Background(), "science_query", tc.args, ""); err != nil {
				t.Fatalf("CallTool failed: %v", err)
			}
			if rec.CallCount() != before+1 {
				t.Fatalf("expected one upstream call, got %d", rec.CallCount()-before)
			}
			rec.mu.Lock()
			gotTool := rec.calls[len(rec.calls)-1]
			gotArgs := rec.callArgs[len(rec.callArgs)-1]
			rec.mu.Unlock()

			if gotTool != tc.wantTool {
				t.Errorf("expected upstream tool %q, got %q", tc.wantTool, gotTool)
			}
			for key, want := range tc.wantArgs {
				if fmt.Sprintf("%v", gotArgs[key]) != fmt.Sprintf("%v", want) {
					t.Errorf("arg %q: expected %v, got %v", key, want, gotArgs[key])
				}
			}
		})
	}
}

func TestScienceGateway_ValidationAndRateLimit(t *testing.T) {
	mgr := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: true})
	rec := newRecordingClient("science", "science")
	mgr.SetClient("science", rec)

	validationCases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing action", map[string]any{}, "missing required parameter 'action'"},
		{"unknown action", map[string]any{"action": "nope"}, "unknown science action"},
		{"search missing query", map[string]any{"action": "search"}, "requires 'query'"},
		{"get_paper missing ids", map[string]any{"action": "get_paper"}, "requires 'ids'"},
		{"similar missing id", map[string]any{"action": "similar"}, "requires 'id'"},
		{"map_topic missing topic", map[string]any{"action": "map_topic"}, "requires 'topic'"},
		{"read_topic_group missing group", map[string]any{"action": "read_topic_group", "topic": "x"}, "requires 'group'"},
		{"find_people missing name", map[string]any{"action": "find_people"}, "requires 'name'"},
		{"get_profile missing id", map[string]any{"action": "get_profile"}, "requires 'id'"},
		{"raw_tool reserved core", map[string]any{"action": "raw_tool", "tool_name": "salam"}, "reserved core tool"},
	}
	for _, tc := range validationCases {
		t.Run(tc.name, func(t *testing.T) {
			before := rec.CallCount()
			_, err := mgr.CallGateway(context.Background(), "science", tc.args, "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
			if rec.CallCount() != before {
				t.Fatalf("validation failure caused %d transport calls", rec.CallCount()-before)
			}
		})
	}

	rateLimited := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: true})
	rateLimited.SetClient("science", &failingUpstreamClient{err: errors.New("HTTP 429 too many requests")})
	_, err := rateLimited.CallGateway(context.Background(), "science", map[string]any{"action": "search", "query": "x"}, "")
	if err == nil || !strings.Contains(err.Error(), "30 requests/minute") {
		t.Fatalf("expected rate-limit guidance, got %v", err)
	}
}

func TestScienceGateway_EgressPrivacy(t *testing.T) {
	mgr := NewUpstreamManager(nil, &config.Config{MCPUpstreamsEnabled: true})
	rec := newRecordingClient("science", "science")
	mgr.SetClient("science", rec)

	_, err := mgr.CallGateway(context.Background(), "science", map[string]any{
		"action": "search",
		"query":  "papers under /home/charlie/data with sk-1234567890123456789012345",
	}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rec.mu.Lock()
	sent := rec.callArgs[0]["query"].(string)
	rec.mu.Unlock()
	if strings.Contains(sent, "/home/charlie") || strings.Contains(sent, "sk-1234567890123456789012345") {
		t.Errorf("science query leaked sensitive data: %s", sent)
	}
	if !strings.Contains(sent, "~") || !strings.Contains(sent, "[REDACTED_API_KEY]") {
		t.Errorf("science query missing redaction placeholders: %s", sent)
	}
}

func TestScienceAutoIngest_Papers(t *testing.T) {
	tmpDir := t.TempDir()
	st, err := store.Open(filepath.Join(tmpDir, "science.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	mgr := NewUpstreamManager(st, &config.Config{MCPUpstreamsEnabled: true})

	searchResult := map[string]any{
		"papers": []any{
			map[string]any{
				"id":    "W123",
				"doi":   "https://doi.org/10.1234/abcd",
				"title": "Residual Learning for Deep Networks",
				"year":  float64(2016),
				"venue": "CVPR",
				"authors": []any{
					map[string]any{"name": "Ada Lovelace"},
					map[string]any{"name": "Alan Turing"},
				},
				"abstract": "A residual learning framework eases the training of substantially deeper networks.",
			},
		},
	}
	if err := mgr.interceptAutoIngest("science", "search_papers", map[string]any{"query": "residual"}, searchResult, "science-proj"); err != nil {
		t.Fatalf("interceptAutoIngest search: %v", err)
	}

	docs, err := st.SearchDocsFTS("residual", "science-proj", 10)
	if err != nil {
		t.Fatalf("SearchDocsFTS: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected auto-ingested paper, got 0")
	}
	if docs[0].URL != "https://doi.org/10.1234/abcd" {
		t.Errorf("expected canonical DOI URL, got %q", docs[0].URL)
	}
	if !strings.Contains(docs[0].Title, "Residual Learning for Deep Networks") {
		t.Errorf("unexpected title: %q", docs[0].Title)
	}
	if !strings.Contains(docs[0].Content, "Ada Lovelace") || !strings.Contains(docs[0].Content, "residual learning framework") {
		t.Errorf("expected metadata and abstract in content, got: %s", docs[0].Content)
	}

	getResult := map[string]any{
		"structuredContent": map[string]any{
			"results": []any{
				map[string]any{
					"id":       "W456",
					"title":    "Protein Folding with Attention",
					"abstract": "Attention models predict protein folding structure.",
				},
			},
		},
	}
	if err := mgr.interceptAutoIngest("science", "get_papers", map[string]any{"ids": []string{"W456"}}, getResult, "science-proj"); err != nil {
		t.Fatalf("interceptAutoIngest get_papers: %v", err)
	}
	docsFold, err := st.SearchDocsFTS("protein", "science-proj", 10)
	if err != nil {
		t.Fatalf("SearchDocsFTS protein: %v", err)
	}
	foundOpenAlex := false
	for _, doc := range docsFold {
		if doc.URL == "https://openalex.org/W456" {
			foundOpenAlex = true
		}
	}
	if !foundOpenAlex {
		t.Fatalf("expected OpenAlex URL for W456, got docs: %+v", docsFold)
	}

	// Non-reference actions must not ingest.
	before, _ := st.DocsByProject("science-proj", 100)
	if err := mgr.interceptAutoIngest("science", "map_topic", map[string]any{"topic": "x"}, searchResult, "science-proj"); err != nil {
		t.Fatalf("interceptAutoIngest map_topic: %v", err)
	}
	after, _ := st.DocsByProject("science-proj", 100)
	if len(after) != len(before) {
		t.Errorf("map_topic must not ingest: before=%d after=%d", len(before), len(after))
	}
}

type failingUpstreamClient struct {
	err error
}

func (f *failingUpstreamClient) Name() string    { return "science" }
func (f *failingUpstreamClient) Prefix() string  { return "science" }
func (f *failingUpstreamClient) IsRunning() bool { return true }
func (f *failingUpstreamClient) Close() error    { return nil }
func (f *failingUpstreamClient) CallTool(context.Context, string, map[string]any) (any, error) {
	return nil, f.err
}
