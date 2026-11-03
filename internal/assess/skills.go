package assess

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// RegisteredTool represents an MCP tool or skill registered in the environment.
type RegisteredTool struct {
	ServerName      string `json:"server_name"`
	ToolName        string `json:"tool_name"`
	SourceConfig    string `json:"source_config"`
	SchemaBytes     int    `json:"schema_bytes"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

// DormantToolReport describes a registered tool with zero observed invocations.
type DormantToolReport struct {
	ServerName      string `json:"server_name"`
	ToolName        string `json:"tool_name"`
	SourceConfig    string `json:"source_config"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Recommendation  string `json:"recommendation"`
}

// ActiveToolReport describes an actively invoked tool.
type ActiveToolReport struct {
	ServerName  string           `json:"server_name"`
	ToolName    string           `json:"tool_name"`
	CallCount   int64            `json:"call_count"`
	LastInvoked time.Time        `json:"last_invoked"`
	Harnesses   map[string]int64 `json:"harnesses"`
}

// SkillBloatAudit contains the full analytical evaluation of MCP tools and skills.
type SkillBloatAudit struct {
	AuditPeriod      time.Duration       `json:"audit_period"`
	AuditPeriodDays  int                 `json:"audit_period_days"`
	TotalRegistered  int                 `json:"total_registered"`
	ActiveCount      int                 `json:"active_count"`
	DormantCount     int                 `json:"dormant_count"`
	WastedTokensTurn int                 `json:"wasted_tokens_per_turn"`
	ProjectID        string              `json:"project_id,omitempty"`
	RegisteredTools  []RegisteredTool    `json:"registered_tools"`
	DormantTools     []DormantToolReport `json:"dormant_tools"`
	ActiveTools      []ActiveToolReport  `json:"active_tools"`
	Recommendations  []string            `json:"recommendations"`
}

// AuditOptions configures the skill bloat auditor.
type AuditOptions struct {
	ProjectID     string
	ProjectDir    string
	AuditPeriod   time.Duration
	MCPConfigDirs []string         // optional explicit MCP schema roots for testing
	BuiltinTools  []RegisteredTool // optional explicit built-in tools (avoids circular dependency with internal/mcp)
}

// EstimateTokens calculates an approximate token count from schema character/byte length.
func EstimateTokens(schemaBytes int) int {
	if schemaBytes <= 0 {
		return 35 // base minimum for tool declaration
	}
	tok := int(math.Ceil(float64(schemaBytes) / 3.7))
	if tok < 35 {
		tok = 35
	}
	return tok
}

// DiscoverRegisteredTools discovers all MCP tools registered in project configs or user environment.
func DiscoverRegisteredTools(opts AuditOptions) ([]RegisteredTool, error) {
	var registered []RegisteredTool
	seen := make(map[string]bool)

	addTool := func(server, name, source string, rawBytes int) {
		key := strings.ToLower(server + "/" + name)
		if seen[key] {
			return
		}
		seen[key] = true
		tokens := EstimateTokens(rawBytes)
		registered = append(registered, RegisteredTool{
			ServerName:      server,
			ToolName:        name,
			SourceConfig:    source,
			SchemaBytes:     rawBytes,
			EstimatedTokens: tokens,
		})
	}

	// 1. Explicit MCP Config Dirs (if supplied via options)
	for _, dir := range opts.MCPConfigDirs {
		if dir == "" {
			continue
		}
		scanMCPDir(dir, addTool)
	}

	// 2. Discover from standard system locations if no explicit dirs were passed
	if len(opts.MCPConfigDirs) == 0 {
		if home, err := os.UserHomeDir(); err == nil {
			for _, sub := range []string{
				filepath.Join(home, ".gemini", "antigravity-cli", "mcp"),
				filepath.Join(home, ".gemini", "mcp"),
			} {
				if fi, err := os.Stat(sub); err == nil && fi.IsDir() {
					scanMCPDir(sub, addTool)
				}
			}

			// Also inspect ~/.claude.json for mcpServers
			claudeJSON := filepath.Join(home, ".claude.json")
			scanConfigJSON(claudeJSON, addTool)
		}

		// Check project root .mcp.json
		if opts.ProjectDir != "" {
			for _, mcpPath := range []string{
				filepath.Join(opts.ProjectDir, ".mcp.json"),
				filepath.Join(opts.ProjectDir, "mcp.json"),
			} {
				scanConfigJSON(mcpPath, addTool)
			}
		}
	}

	// 3. Explicit Built-in Tools (if provided via options)
	for _, bt := range opts.BuiltinTools {
		addTool(bt.ServerName, bt.ToolName, bt.SourceConfig, bt.SchemaBytes)
	}

	sort.Slice(registered, func(i, j int) bool {
		if registered[i].ServerName != registered[j].ServerName {
			return registered[i].ServerName < registered[j].ServerName
		}
		return registered[i].ToolName < registered[j].ToolName
	})

	return registered, nil
}

func scanMCPDir(root string, addTool func(server, name, source string, rawBytes int)) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		serverName := entry.Name()
		serverDir := filepath.Join(root, serverName)

		toolFiles, err := os.ReadDir(serverDir)
		if err != nil {
			continue
		}

		for _, tf := range toolFiles {
			if tf.IsDir() || !strings.HasSuffix(tf.Name(), ".json") {
				continue
			}
			toolName := strings.TrimSuffix(tf.Name(), ".json")
			toolPath := filepath.Join(serverDir, tf.Name())
			info, err := tf.Info()
			fileSize := 200 // default fallback
			if err == nil && info.Size() > 0 {
				fileSize = int(info.Size())
			}
			addTool(serverName, toolName, toolPath, fileSize)
		}
	}
}

type genericMCPConfig struct {
	MCPServers map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"mcpServers"`
}

func scanConfigJSON(path string, addTool func(server, name, source string, rawBytes int)) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var cfg genericMCPConfig
	if err := json.Unmarshal(data, &cfg); err == nil && len(cfg.MCPServers) > 0 {
		for serverName := range cfg.MCPServers {
			if serverName == "sila" {
				continue // handled by built-in definitions
			}
			// Registered server without enumerated individual tool schemas
			addTool(serverName, "*", path, 450)
		}
	}
}

// AuditSkillBloat cross-references registered MCP tools and skills against observed
// invocations in the store over the audit period, calculating dormant schemas and context waste.
func AuditSkillBloat(ctx context.Context, st *store.Store, opts AuditOptions) (*SkillBloatAudit, error) {
	if opts.AuditPeriod <= 0 {
		opts.AuditPeriod = 30 * 24 * time.Hour
	}
	days := int(opts.AuditPeriod.Hours() / 24)

	registered, err := DiscoverRegisteredTools(opts)
	if err != nil {
		return nil, fmt.Errorf("discover registered tools: %w", err)
	}

	var stats []store.ToolInvocationStat
	if st != nil {
		stats, err = st.GetToolInvocationStats(ctx, opts.ProjectID, opts.AuditPeriod)
		if err != nil {
			return nil, fmt.Errorf("retrieve tool invocation stats: %w", err)
		}
	}

	statsMap := make(map[string]store.ToolInvocationStat)
	for _, s := range stats {
		statsMap[strings.ToLower(s.ToolName)] = s
	}

	var active []ActiveToolReport
	var dormant []DormantToolReport
	wastedTokensTurn := 0

	// Track server-level dormancy
	serverDormantCount := make(map[string]int)
	serverTotalCount := make(map[string]int)

	for _, rt := range registered {
		serverTotalCount[rt.ServerName]++
		toolLower := strings.ToLower(rt.ToolName)

		// Check if invoked under tool name, or mcp prefix
		var matchedStat *store.ToolInvocationStat
		candidates := []string{
			toolLower,
			fmt.Sprintf("mcp_%s_%s", strings.ToLower(rt.ServerName), toolLower),
			fmt.Sprintf("mcp__%s__%s", strings.ToLower(rt.ServerName), toolLower),
		}
		for _, cand := range candidates {
			if st, ok := statsMap[cand]; ok && st.CallCount > 0 {
				matchedStat = &st
				break
			}
		}

		// Also if tool is wildcard "*" for a whole server
		if rt.ToolName == "*" {
			serverPrefix := fmt.Sprintf("mcp_%s_", strings.ToLower(rt.ServerName))
			for name, st := range statsMap {
				if strings.HasPrefix(name, serverPrefix) && st.CallCount > 0 {
					matchedStat = &st
					break
				}
			}
		}

		if matchedStat != nil {
			active = append(active, ActiveToolReport{
				ServerName:  rt.ServerName,
				ToolName:    rt.ToolName,
				CallCount:   matchedStat.CallCount,
				LastInvoked: matchedStat.LastInvoked,
				Harnesses:   matchedStat.Harnesses,
			})
		} else {
			serverDormantCount[rt.ServerName]++
			wastedTokensTurn += rt.EstimatedTokens

			rec := fmt.Sprintf("Tool %q (server %q) was registered in %s but never called in the last %d days. Consider disabling it to save ~%d tokens/turn.",
				rt.ToolName, rt.ServerName, rt.SourceConfig, days, rt.EstimatedTokens)

			dormant = append(dormant, DormantToolReport{
				ServerName:      rt.ServerName,
				ToolName:        rt.ToolName,
				SourceConfig:    rt.SourceConfig,
				EstimatedTokens: rt.EstimatedTokens,
				Recommendation:  rec,
			})
		}
	}

	// Sort reports cleanly
	sort.Slice(active, func(i, j int) bool {
		if active[i].CallCount != active[j].CallCount {
			return active[i].CallCount > active[j].CallCount
		}
		return active[i].ToolName < active[j].ToolName
	})

	sort.Slice(dormant, func(i, j int) bool {
		if dormant[i].EstimatedTokens != dormant[j].EstimatedTokens {
			return dormant[i].EstimatedTokens > dormant[j].EstimatedTokens
		}
		return dormant[i].ToolName < dormant[j].ToolName
	})

	// Build high-level actionable recommendations
	var recs []string
	for srv, dormCount := range serverDormantCount {
		total := serverTotalCount[srv]
		if dormCount == total && total > 0 {
			recs = append(recs, fmt.Sprintf("Server %q has 100%% dormant tools (%d/%d unused in last %d days). Consider unregistering this server to reclaim prompt context.", srv, dormCount, total, days))
		} else if dormCount > 0 {
			recs = append(recs, fmt.Sprintf("Server %q has %d/%d dormant tools (%d%% unused). Consider lazy-loading or pruning unused tools.", srv, dormCount, total, (dormCount*100)/total))
		}
	}

	if len(dormant) == 0 {
		recs = append(recs, "All registered tools and MCP servers are actively utilized. Zero dormant context bloat detected.")
	} else {
		sort.Strings(recs)
	}

	return &SkillBloatAudit{
		AuditPeriod:      opts.AuditPeriod,
		AuditPeriodDays:  days,
		TotalRegistered:  len(registered),
		ActiveCount:      len(active),
		DormantCount:     len(dormant),
		WastedTokensTurn: wastedTokensTurn,
		ProjectID:        opts.ProjectID,
		RegisteredTools:  registered,
		DormantTools:     dormant,
		ActiveTools:      active,
		Recommendations:  recs,
	}, nil
}
