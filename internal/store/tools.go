package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ToolInvocationStat holds aggregated metrics for a single tool.
type ToolInvocationStat struct {
	ToolName    string           `json:"tool_name"`
	CallCount   int64            `json:"call_count"`
	LastInvoked time.Time        `json:"last_invoked"`
	Harnesses   map[string]int64 `json:"harnesses"`
}

// Regex patterns for detecting tool invocations across diverse agent message formats.
var (
	reToolNameKV  = regexp.MustCompile(`(?i)["']?(?:ToolName|tool_name)["']?\s*[:=]\s*["']?([a-zA-Z0-9_\-\.]+)["']?`)
	reInvokeTag   = regexp.MustCompile(`(?i)<(?:invoke|tool_call|action)[^>]*?\b(?:name|tool)=["']([a-zA-Z0-9_\-\.]+)["']`)
	reJSONFunc    = regexp.MustCompile(`"(?:name|tool_name|tool)":\s*"([a-zA-Z0-9_\-\.]+)"\s*,\s*"(?:arguments|input|parameters)":`)
	reMcpDouble   = regexp.MustCompile(`\bmcp__([a-zA-Z0-9\-]+)__([a-zA-Z0-9_\-]+)\b`)
	reMcpSingle   = regexp.MustCompile(`\bmcp_([a-zA-Z0-9\-]+)_([a-zA-Z0-9_\-]+)\b`)
	reToolCallObj = regexp.MustCompile(`(?i)"(?:type)":\s*"function",\s*"function":\s*\{\s*"name":\s*"([a-zA-Z0-9_\-\.]+)"`)
)

// ExtractToolInvocations extracts tool names called within a session message.
func ExtractToolInvocations(content string) []string {
	if len(content) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var tools []string

	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > 64 {
			return
		}
		lower := strings.ToLower(name)
		switch lower {
		case "true", "false", "null", "undefined", "string", "number", "boolean", "object", "array", "user", "assistant", "system", "text":
			return
		}
		if !seen[lower] {
			seen[lower] = true
			tools = append(tools, lower)
		}
	}

	contentLower := strings.ToLower(content)

	// 1. Check ToolName key-values (e.g. ToolName: "save_memo" or "ToolName": "search_knowledge")
	if strings.Contains(contentLower, "toolname") || strings.Contains(contentLower, "tool_name") {
		for _, m := range reToolNameKV.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				add(m[1])
			}
		}
	}

	// 2. Check XML-like invoke tags (e.g. <invoke name="run_command">, <tool_call name="...">, <action name="...">)
	if strings.Contains(content, "<invoke") || strings.Contains(content, "<tool_call") || strings.Contains(content, "<action") {
		for _, m := range reInvokeTag.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				add(m[1])
			}
		}
	}

	// 3. Check JSON tool call structures
	if strings.Contains(content, `"arguments":`) || strings.Contains(content, `"parameters":`) || strings.Contains(content, `"input":`) {
		for _, m := range reJSONFunc.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				add(m[1])
			}
		}
	}

	// 4. Check OpenAI function call object
	if strings.Contains(content, `"function"`) {
		for _, m := range reToolCallObj.FindAllStringSubmatch(content, -1) {
			if len(m) > 1 {
				add(m[1])
			}
		}
	}

	// 5. Check MCP prefix double or single underscore (e.g. mcp__server__tool or mcp_server_tool)
	if strings.Contains(contentLower, "mcp_") || strings.Contains(contentLower, "mcp__") {
		for _, m := range reMcpDouble.FindAllStringSubmatch(content, -1) {
			if len(m) > 2 {
				add(m[0])
				add(m[2])
			}
		}
		for _, m := range reMcpSingle.FindAllStringSubmatch(content, -1) {
			if len(m) > 2 {
				add(m[0])
				add(m[2])
			}
		}
	}

	return tools
}

// parseSourceTools extracts harness names from raw JSON string with fast-path for common single harnesses.
func parseSourceTools(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return []string{"unknown"}
	}
	if strings.HasPrefix(raw, `["`) && strings.HasSuffix(raw, `"]`) && !strings.Contains(raw, `,`) {
		val := raw[2 : len(raw)-2]
		if val != "" && !strings.Contains(val, `"`) {
			return []string{val}
		}
	}
	var harnesses []string
	if err := json.Unmarshal([]byte(raw), &harnesses); err != nil || len(harnesses) == 0 {
		return []string{"unknown"}
	}
	return harnesses
}

// GetToolInvocationStats queries messages across sila.db to compute call counts,
// last invoked timestamps, and harness attribution for tools.
// It leverages the messages_fts full-text index with time-scoped filters to avoid
// table scans on large message sets, falling back to sequential scans if FTS is unavailable.
// If projectID is non-empty, results are scoped to that project.
// If since > 0, results are restricted to messages within the given duration.
func (s *Store) GetToolInvocationStats(ctx context.Context, projectID string, since time.Duration) ([]ToolInvocationStat, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("knowledge store is not open")
	}

	// 1. Try fast path via messages_fts full-text index
	stats, err := s.getToolInvocationStatsFTS(ctx, projectID, since)
	if err == nil {
		return stats, nil
	}

	// If context was canceled or timed out, do not fall back
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// 2. Try healing corrupted FTS index if possible
	if healErr := s.RebuildFTSIndex("messages_fts"); healErr == nil {
		if healedStats, retryErr := s.getToolInvocationStatsFTS(ctx, projectID, since); retryErr == nil {
			return healedStats, nil
		}
	}

	// 3. Fall back to legacy scan
	return s.getToolInvocationStatsLegacy(ctx, projectID, since)
}

func (s *Store) getToolInvocationStatsFTS(ctx context.Context, projectID string, since time.Duration) ([]ToolInvocationStat, error) {
	query := `SELECT m.ts, m.source_tools, m.content
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH ?`
	args := []any{"tool* OR invoke* OR mcp* OR action* OR arguments*"}

	if projectID != "" {
		query += ` AND m.project_id = ?`
		args = append(args, projectID)
	}

	if since > 0 {
		cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
		query += ` AND m.ts >= ?`
		args = append(args, cutoff)
	}

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return processToolInvocationRows(ctx, rows)
}

func (s *Store) getToolInvocationStatsLegacy(ctx context.Context, projectID string, since time.Duration) ([]ToolInvocationStat, error) {
	query := `SELECT ts, source_tools, content FROM messages WHERE (content LIKE '%tool%' OR content LIKE '%invoke%' OR content LIKE '%mcp%' OR content LIKE '%"name":%' OR content LIKE '%<action%')`
	var args []any

	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}

	if since > 0 {
		cutoff := time.Now().UTC().Add(-since).Format(time.RFC3339)
		query += ` AND ts >= ?`
		args = append(args, cutoff)
	}

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query tool invocations: %w", err)
	}
	defer rows.Close()

	return processToolInvocationRows(ctx, rows)
}

func processToolInvocationRows(ctx context.Context, rows *sql.Rows) ([]ToolInvocationStat, error) {
	statMap := make(map[string]*ToolInvocationStat)

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		var tsStr, sourceToolsRaw, content string
		if err := rows.Scan(&tsStr, &sourceToolsRaw, &content); err != nil {
			return nil, fmt.Errorf("scan message row: %w", err)
		}

		ts, _ := time.Parse(time.RFC3339, tsStr)
		if ts.IsZero() {
			ts, _ = time.Parse("2006-01-02 15:04:05", tsStr)
		}

		harnesses := parseSourceTools(sourceToolsRaw)

		invoked := ExtractToolInvocations(content)
		for _, tool := range invoked {
			st, exists := statMap[tool]
			if !exists {
				st = &ToolInvocationStat{
					ToolName:  tool,
					Harnesses: make(map[string]int64),
				}
				statMap[tool] = st
			}
			st.CallCount++
			if ts.After(st.LastInvoked) {
				st.LastInvoked = ts
			}
			for _, h := range harnesses {
				st.Harnesses[h]++
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	var result []ToolInvocationStat
	for _, st := range statMap {
		result = append(result, *st)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].CallCount != result[j].CallCount {
			return result[i].CallCount > result[j].CallCount
		}
		return result[i].ToolName < result[j].ToolName
	})

	return result, nil
}
