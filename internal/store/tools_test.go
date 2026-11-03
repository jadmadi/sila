package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractToolInvocations(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "empty content",
			content: "",
			want:    nil,
		},
		{
			name:    "ToolName key-value",
			content: `call_mcp_tool: {"ServerName": "sila", "ToolName": "search_knowledge"}`,
			want:    []string{"search_knowledge"},
		},
		{
			name:    "XML invoke tag",
			content: `<invoke name="run_command"><param>ls</param></invoke>`,
			want:    []string{"run_command"},
		},
		{
			name:    "JSON function call",
			content: `{"type": "function", "name": "view_file", "parameters": {"path": "main.go"}}`,
			want:    []string{"view_file"},
		},
		{
			name:    "MCP eager tool prefix",
			content: `Executing mcp_sila_claim_goal for goal XYZ`,
			want:    []string{"mcp_sila_claim_goal", "claim_goal"},
		},
		{
			name:    "Double underscore MCP prefix",
			content: `invoking mcp__github__create_issue`,
			want:    []string{"mcp__github__create_issue", "create_issue"},
		},
		{
			name:    "Multiple tools in one turn",
			content: `<tool_call name="read_file"></tool_call> and then ToolName: "save_memo"`,
			want:    []string{"save_memo", "read_file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractToolInvocations(tt.content)
			for _, w := range tt.want {
				found := false
				for _, g := range got {
					if g == w {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ExtractToolInvocations() missing expected tool %q; got %v", w, got)
				}
			}
		})
	}
}

func TestGetToolInvocationStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_tools.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	t1 := now.Add(-10 * 24 * time.Hour).Format(time.RFC3339)
	t2 := now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)
	tOld := now.Add(-40 * 24 * time.Hour).Format(time.RFC3339)

	// Insert test project
	mustExec(t, st.DB, `INSERT INTO projects (path, id, created_at) VALUES ('/w/proj1', 'proj1', ?)`, t1)
	mustExec(t, st.DB, `INSERT INTO projects (path, id, created_at) VALUES ('/w/proj2', 'proj2', ?)`, t1)

	// Insert test messages
	// Proj1 - Devin used run_command 10 days ago
	mustExec(t, st.DB, `INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES ('proj1', 'h1', ?, 'assistant', '<invoke name="run_command">ls</invoke>', '["devin"]', ?)`, t1, t1)

	// Proj1 - Claude Code used search_knowledge 2 days ago
	mustExec(t, st.DB, `INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES ('proj1', 'h2', ?, 'assistant', '{"name": "search_knowledge", "arguments": {}}', '["claude-code"]', ?)`, t2, t2)

	// Proj1 - AGY also used search_knowledge 2 days ago
	mustExec(t, st.DB, `INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES ('proj1', 'h3', ?, 'assistant', 'ToolName: "search_knowledge"', '["agy"]', ?)`, t2, t2)

	// Proj1 - Ancient call to old_tool 40 days ago
	mustExec(t, st.DB, `INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES ('proj1', 'h4', ?, 'assistant', '<invoke name="old_tool"></invoke>', '["codex"]', ?)`, tOld, tOld)

	// Proj2 - different project used other_tool
	mustExec(t, st.DB, `INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES ('proj2', 'h5', ?, 'assistant', '<invoke name="other_tool"></invoke>', '["opencode"]', ?)`, t2, t2)

	ctx := context.Background()

	// 1. Query all stats without project or time filter
	allStats, err := st.GetToolInvocationStats(ctx, "", 0)
	if err != nil {
		t.Fatalf("GetToolInvocationStats all: %v", err)
	}

	foundSearch := false
	for _, s := range allStats {
		if s.ToolName == "search_knowledge" {
			foundSearch = true
			if s.CallCount != 2 {
				t.Errorf("search_knowledge call count = %d, want 2", s.CallCount)
			}
			if s.Harnesses["claude-code"] != 1 || s.Harnesses["agy"] != 1 {
				t.Errorf("search_knowledge harnesses = %v, want claude-code:1, agy:1", s.Harnesses)
			}
		}
	}
	if !foundSearch {
		t.Errorf("search_knowledge not found in all stats: %v", allStats)
	}

	// 2. Query with project filter "proj1"
	projStats, err := st.GetToolInvocationStats(ctx, "proj1", 0)
	if err != nil {
		t.Fatalf("GetToolInvocationStats proj1: %v", err)
	}
	for _, s := range projStats {
		if s.ToolName == "other_tool" {
			t.Errorf("other_tool leaked into proj1 stats: %v", s)
		}
	}

	// 3. Query with since filter (last 14 days) - should exclude old_tool
	recentStats, err := st.GetToolInvocationStats(ctx, "proj1", 14*24*time.Hour)
	if err != nil {
		t.Fatalf("GetToolInvocationStats recent: %v", err)
	}
	for _, s := range recentStats {
		if s.ToolName == "old_tool" {
			t.Errorf("old_tool (40 days old) should have been excluded by 14-day window: %v", s)
		}
	}

	// 4. Test context cancellation
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = st.GetToolInvocationStats(canceledCtx, "", 0)
	if err == nil {
		t.Errorf("expected error on canceled context, got nil")
	}
}

func mustExec(t testing.TB, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("mustExec failed for %q: %v", q, err)
	}
}

func TestGetToolInvocationStatsParity(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "parity_test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	mustExec(t, st.DB, `INSERT INTO projects (path, id, created_at) VALUES ('/w/proj1', 'proj1', ?)`, now.Format(time.RFC3339))

	// Insert diverse tool call representations
	samples := []struct {
		content string
		harness string
		age     time.Duration
	}{
		{`<invoke name="tool_a">args</invoke>`, "devin", 1 * time.Hour},
		{`{"name": "tool_b", "arguments": {}}`, "claude-code", 2 * time.Hour},
		{`ToolName: "tool_c"`, "agy", 3 * time.Hour},
		{`mcp_github_create_issue`, "opencode", 4 * time.Hour},
		{`mcp__server__tool_d`, "mimocode", 5 * time.Hour},
		{`{"type": "function", "function": {"name": "tool_e"}}`, "codex", 6 * time.Hour},
		{`regular prose with no tools mentioned`, "git", 7 * time.Hour},
		{`call_mcp_tool: {"ToolName": "tool_a"}`, "agy", 8 * time.Hour},
	}

	for i, s := range samples {
		ts := now.Add(-s.age).Format(time.RFC3339)
		mustExec(t, st.DB, `INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
			VALUES ('proj1', ?, ?, 'assistant', ?, ?, ?)`,
			fmt.Sprintf("hash_%d", i), ts, s.content, fmt.Sprintf(`[%q]`, s.harness), ts)
	}

	ctx := context.Background()

	// Run both FTS and Legacy
	ftsStats, err := st.getToolInvocationStatsFTS(ctx, "proj1", 24*time.Hour)
	if err != nil {
		t.Fatalf("getToolInvocationStatsFTS: %v", err)
	}

	legacyStats, err := st.getToolInvocationStatsLegacy(ctx, "proj1", 24*time.Hour)
	if err != nil {
		t.Fatalf("getToolInvocationStatsLegacy: %v", err)
	}

	if len(ftsStats) != len(legacyStats) {
		t.Fatalf("stat count mismatch: FTS=%d, Legacy=%d", len(ftsStats), len(legacyStats))
	}

	ftsMap := make(map[string]ToolInvocationStat)
	for _, s := range ftsStats {
		ftsMap[s.ToolName] = s
	}

	for _, leg := range legacyStats {
		fts, ok := ftsMap[leg.ToolName]
		if !ok {
			t.Errorf("tool %q present in legacy but missing in FTS", leg.ToolName)
			continue
		}
		if fts.CallCount != leg.CallCount {
			t.Errorf("tool %q CallCount mismatch: FTS=%d, Legacy=%d", leg.ToolName, fts.CallCount, leg.CallCount)
		}
		for h, c := range leg.Harnesses {
			if fts.Harnesses[h] != c {
				t.Errorf("tool %q harness %q count mismatch: FTS=%d, Legacy=%d", leg.ToolName, h, fts.Harnesses[h], c)
			}
		}
	}
}

func BenchmarkGetToolInvocationStats(b *testing.B) {
	dir := b.TempDir()
	dbPath := filepath.Join(dir, "bench_tools.db")

	st, err := Open(dbPath)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	mustExec(b, st.DB, `INSERT INTO projects (path, id, created_at) VALUES ('/w/proj1', 'proj1', ?)`, now.Format(time.RFC3339))

	// Seed 500 messages
	for i := 0; i < 500; i++ {
		ts := now.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339)
		content := fmt.Sprintf("random conversation step %d with no tools", i)
		if i%3 == 0 {
			content = fmt.Sprintf(`<invoke name="tool_%d">args</invoke>`, i%10)
		} else if i%5 == 0 {
			content = fmt.Sprintf(`{"name": "tool_%d", "arguments": {}}`, i%10)
		}
		mustExec(b, st.DB, `INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
			VALUES ('proj1', ?, ?, 'assistant', ?, '["devin"]', ?)`,
			fmt.Sprintf("h_%d", i), ts, content, ts)
	}

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := st.GetToolInvocationStats(ctx, "proj1", 7*24*time.Hour)
		if err != nil {
			b.Fatalf("GetToolInvocationStats: %v", err)
		}
	}
}
