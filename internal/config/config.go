package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config represents persistent user settings for sila.
type Config struct {
	GoogleAPIKey           string                       `json:"google_api_key,omitempty"`
	GoogleCSECX            string                       `json:"google_cse_cx,omitempty"`
	TavilyAPIKey           string                       `json:"tavily_api_key,omitempty"`
	BraveAPIKey            string                       `json:"brave_api_key,omitempty"`
	KagiAPIKey             string                       `json:"kagi_api_key,omitempty"`
	GitHubToken            string                       `json:"github_token,omitempty"`
	SourcegraphToken       string                       `json:"sourcegraph_token,omitempty"`
	StackOverflowKey       string                       `json:"stackoverflow_key,omitempty"`
	SearchProvider         string                       `json:"search_provider,omitempty"`
	TypeSafeAPIKey         string                       `json:"typesafe_api_key,omitempty"`
	AssistProvider         string                       `json:"assist_provider,omitempty"`
	AssistEnabled          bool                         `json:"assist_enabled,omitempty"`
	AssistScopes           []string                     `json:"assist_scopes,omitempty"`
	AssistProjects         []string                     `json:"assist_projects,omitempty"`
	CloudEnabled           bool                         `json:"cloud_enabled,omitempty"`
	CloudScopes            []string                     `json:"cloud_scopes,omitempty"`
	CloudProjects          []string                     `json:"cloud_projects,omitempty"`
	CloudEndpoint          string                       `json:"cloud_endpoint,omitempty"`
	CloudToken             string                       `json:"cloud_token,omitempty"`
	DocSites               []string                     `json:"doc_sites,omitempty"`
	GatewayEnabled         bool                         `json:"gateway_enabled,omitempty"`
	GatewaySurfaces        []string                     `json:"gateway_surfaces,omitempty"`
	GatewayDiscord         DiscordConfig                `json:"gateway_discord,omitempty"`
	GatewayTelegram        TelegramConfig               `json:"gateway_telegram,omitempty"`
	GatewayGemini          GeminiBridgeConfig           `json:"gateway_gemini,omitempty"`
	GatewayMCP             MCPGatewayConfig             `json:"gateway_mcp,omitempty"`
	CloudflareAPIToken     string                       `json:"cloudflare_api_token,omitempty"`
	CloudflareAccountID    string                       `json:"cloudflare_account_id,omitempty"`
	CloudflareD1DatabaseID string                       `json:"cloudflare_d1_database_id,omitempty"`
	AlphaXivAPIToken       string                       `json:"alphaxiv_api_token,omitempty"`
	StitchAPIKey           string                       `json:"stitch_api_key,omitempty"`
	MCPUpstreams           map[string]UpstreamMCPServer `json:"mcp_upstreams,omitempty"`
	MCPUpstreamsEnabled    bool                         `json:"mcp_upstreams_enabled,omitempty"`
	RecallGate             string                       `json:"recall_gate_policy,omitempty"`
	StateBlock             StateBlockConfig             `json:"state_block,omitempty"`
	Memory                 MemoryConfig                 `json:"memory,omitempty"`
	Agent                  AgentConfig                  `json:"agent,omitempty"`
	Redaction              RedactionConfig              `json:"redaction,omitempty"`
	Trust                  TrustConfig                  `json:"trust,omitempty"`
	Recall                 RecallConfig                 `json:"recall,omitempty"`
	Retrieval              RetrievalConfig              `json:"retrieval,omitempty"`
}

// RetrievalConfig controls the precision guards.
type RetrievalConfig struct {
	IDFVeto  *bool    `json:"idf_veto,omitempty"`
	OODGuard *bool    `json:"ood_guard,omitempty"`
	OODTheta *float64 `json:"ood_theta,omitempty"`
}

// RecallConfig flips the compact default for recall surfaces.
type RecallConfig struct {
	CompactDefault bool `json:"compact_default,omitempty"`
}

// TrustConfig lists full commit SHAs trusted by the review gate.
type TrustConfig struct {
	Targets []string `json:"targets,omitempty"`
}

// RedactionConfig controls mask mode and operator rules.
type RedactionConfig struct {
	Mask     string `json:"mask,omitempty"`      // full | prefix
	RulesDir string `json:"rules_dir,omitempty"` // directory of *.yaml rules
}

// AgentConfig carries the default agent identity for MCP attribution.
type AgentConfig struct {
	Identity string `json:"identity,omitempty"`
}

// MemoryConfig controls the real-time harness memory mirror.
type MemoryConfig struct {
	Mirror string `json:"mirror,omitempty"` // on | off; default on
}

// MemoryMirrorEnabled resolves the mirror switch. The environment variable
// SILA_MEMORY_MIRROR overrides the config in both directions.
func (c *Config) MemoryMirrorEnabled() bool {
	if env := strings.ToLower(strings.TrimSpace(os.Getenv("SILA_MEMORY_MIRROR"))); env != "" {
		return env != "off"
	}
	if c != nil {
		switch strings.ToLower(strings.TrimSpace(c.Memory.Mirror)) {
		case "off":
			return false
		case "on":
			return true
		}
	}
	return true
}

// StateBlockConfig controls the pushed continuity state block.
type StateBlockConfig struct {
	WelcomeBackHours *int `json:"welcome_back_hours,omitempty"` // 0 disables the since-you-left line; default 6
}

// WelcomeBackWindow returns the configured gap window. Zero disables the line.
func (c *Config) WelcomeBackWindow() time.Duration {
	hours := 6
	if c != nil && c.StateBlock.WelcomeBackHours != nil {
		hours = *c.StateBlock.WelcomeBackHours
	}
	if hours <= 0 {
		return 0
	}
	return time.Duration(hours) * time.Hour
}

// UpstreamMCPServer defines an upstream Model Context Protocol server configuration.
type UpstreamMCPServer struct {
	Command    string            `json:"command,omitempty"`     // Executable for stdio (e.g. "npx", "go")
	Args       []string          `json:"args,omitempty"`        // CLI arguments
	Env        map[string]string `json:"env,omitempty"`         // Environment variables
	ServerURL  string            `json:"server_url,omitempty"`  // Remote HTTP/SSE URL
	Headers    map[string]string `json:"headers,omitempty"`     // Remote headers
	AuthToken  string            `json:"auth_token,omitempty"`  // Bearer or auth token
	Prefix     string            `json:"prefix,omitempty"`      // Namespace prefix (e.g. "uicraft", "drizzle", "cf", "gopls")
	Disabled   bool              `json:"disabled,omitempty"`    // Explicitly disabled
	AutoIngest bool              `json:"auto_ingest,omitempty"` // Auto-ingest docs/search results into SQLite FTS5
	OnDemand   bool              `json:"on_demand,omitempty"`   // Lazy on-demand activation (not in base tools/list until toggled or queried)
}

// DiscordConfig holds settings for Discord webhook notifications.
type DiscordConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	WebhookURL string `json:"webhook_url,omitempty"`
	Username   string `json:"username,omitempty"`
	AvatarURL  string `json:"avatar_url,omitempty"`
}

// TelegramConfig holds settings for Telegram Bot notifications.
type TelegramConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
}

// GeminiBridgeConfig holds settings for the Gemini / Cloudflare D1 task bridge.
type GeminiBridgeConfig struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	AuthToken string `json:"auth_token,omitempty"`
}

// MCPGatewayConfig holds settings for remote Model Context Protocol gateways.
type MCPGatewayConfig struct {
	Enabled   bool   `json:"enabled,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	AuthToken string `json:"auth_token,omitempty"`
}

// MaskSecret masks an API key or token, showing only the last 4 characters if long enough.
func MaskSecret(k string) string {
	if k == "" {
		return ""
	}
	if len(k) > 4 {
		return "••••••••" + k[len(k)-4:]
	}
	return "••••••••"
}

// Redacted returns a shallow copy of the config with all secret keys masked.
func (c *Config) Redacted() *Config {
	if c == nil {
		return nil
	}
	rc := *c
	if rc.GoogleAPIKey != "" {
		rc.GoogleAPIKey = MaskSecret(rc.GoogleAPIKey)
	}
	if rc.TavilyAPIKey != "" {
		rc.TavilyAPIKey = MaskSecret(rc.TavilyAPIKey)
	}
	if rc.BraveAPIKey != "" {
		rc.BraveAPIKey = MaskSecret(rc.BraveAPIKey)
	}
	if rc.KagiAPIKey != "" {
		rc.KagiAPIKey = MaskSecret(rc.KagiAPIKey)
	}
	if rc.GitHubToken != "" {
		rc.GitHubToken = MaskSecret(rc.GitHubToken)
	}
	if rc.SourcegraphToken != "" {
		rc.SourcegraphToken = MaskSecret(rc.SourcegraphToken)
	}
	if rc.StackOverflowKey != "" {
		rc.StackOverflowKey = MaskSecret(rc.StackOverflowKey)
	}
	if rc.TypeSafeAPIKey != "" {
		rc.TypeSafeAPIKey = MaskSecret(rc.TypeSafeAPIKey)
	}
	if rc.CloudToken != "" {
		rc.CloudToken = MaskSecret(rc.CloudToken)
	}
	if rc.GatewayDiscord.WebhookURL != "" {
		rc.GatewayDiscord.WebhookURL = MaskSecret(rc.GatewayDiscord.WebhookURL)
	}
	if rc.GatewayTelegram.BotToken != "" {
		rc.GatewayTelegram.BotToken = MaskSecret(rc.GatewayTelegram.BotToken)
	}
	if rc.GatewayGemini.AuthToken != "" {
		rc.GatewayGemini.AuthToken = MaskSecret(rc.GatewayGemini.AuthToken)
	}
	if rc.GatewayMCP.AuthToken != "" {
		rc.GatewayMCP.AuthToken = MaskSecret(rc.GatewayMCP.AuthToken)
	}
	if rc.CloudflareAPIToken != "" {
		rc.CloudflareAPIToken = MaskSecret(rc.CloudflareAPIToken)
	}
	if rc.AlphaXivAPIToken != "" {
		rc.AlphaXivAPIToken = MaskSecret(rc.AlphaXivAPIToken)
	}
	if rc.StitchAPIKey != "" {
		rc.StitchAPIKey = MaskSecret(rc.StitchAPIKey)
	}
	return &rc
}

// HasAssistScope reports whether the specified feature scope is consented.
// Storing a key or selecting a provider alone causes zero uploads; explicit enablement
// and scope consent are strictly required.
func (c *Config) HasAssistScope(scope string) bool {
	if c == nil || !c.AssistEnabled {
		return false
	}
	if scope == "" {
		return len(c.AssistScopes) > 0 || c.AssistEnabled
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	for _, s := range c.AssistScopes {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "all" || s == "*" || s == scope {
			return true
		}
	}
	return false
}

// AllowsAssistProject reports whether assistance is permitted for the given project.
func (c *Config) AllowsAssistProject(project string) bool {
	if c == nil || !c.AssistEnabled {
		return false
	}
	if len(c.AssistProjects) == 0 {
		return true
	}
	project = strings.ToLower(strings.TrimSpace(project))
	for _, p := range c.AssistProjects {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "*" || p == "all" || (project != "" && p == project) {
			return true
		}
	}
	return false
}

// HasCloudScope reports whether the specified cloud feature scope is consented.
// Storing an endpoint or token alone causes zero network calls; explicit enablement
// and scope consent are strictly required.
func (c *Config) HasCloudScope(scope string) bool {
	if c == nil || !c.CloudEnabled {
		return false
	}
	if scope == "" {
		return len(c.CloudScopes) > 0 || c.CloudEnabled
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	for _, s := range c.CloudScopes {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "all" || s == "*" || s == scope {
			return true
		}
	}
	return false
}

// AllowsCloudProject reports whether cloud synchronization is permitted for the given project.
func (c *Config) AllowsCloudProject(project string) bool {
	if c == nil || !c.CloudEnabled {
		return false
	}
	if len(c.CloudProjects) == 0 {
		return true
	}
	project = strings.ToLower(strings.TrimSpace(project))
	for _, p := range c.CloudProjects {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "*" || p == "all" || (project != "" && p == project) {
			return true
		}
	}
	return false
}

// HasGatewaySurface reports whether the specified gateway surface is enabled and configured.
func (c *Config) HasGatewaySurface(surface string) bool {
	if c == nil || !c.GatewayEnabled {
		return false
	}
	surface = strings.ToLower(strings.TrimSpace(surface))
	if len(c.GatewaySurfaces) > 0 {
		matched := false
		for _, s := range c.GatewaySurfaces {
			s = strings.ToLower(strings.TrimSpace(s))
			if s == "all" || s == "*" || s == surface {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	switch surface {
	case "discord":
		return c.GatewayDiscord.Enabled && c.GatewayDiscord.WebhookURL != ""
	case "telegram":
		return c.GatewayTelegram.Enabled && c.GatewayTelegram.BotToken != "" && c.GatewayTelegram.ChatID != ""
	case "gemini":
		return c.GatewayGemini.Enabled && c.GatewayGemini.Endpoint != ""
	case "mcp":
		return c.GatewayMCP.Enabled && c.GatewayMCP.Endpoint != ""
	default:
		return false
	}
}

// DefaultUpstreamMCPServers returns the default 5 ecosystem upstream MCP configurations.
func DefaultUpstreamMCPServers() map[string]UpstreamMCPServer {
	return map[string]UpstreamMCPServer{
		"ui-craft": {
			Command:    "npx",
			Args:       []string{"-y", "ui-craft-mcp@0.9.0"},
			Prefix:     "uicraft",
			AutoIngest: true,
		},
		"cloudflare": {
			ServerURL:  "https://mcp.cloudflare.com/mcp",
			Prefix:     "cf",
			AutoIngest: true,
		},
		"drizzle": {
			Command:    "npx",
			Args:       []string{"--min-release-age=0", "-y", "drizzle-docs-mcp"},
			Prefix:     "drizzle",
			AutoIngest: true,
		},
		"gopls": {
			Command:    "go",
			Args:       []string{"run", "golang.org/x/tools/gopls@latest", "mcp"},
			Prefix:     "gopls",
			AutoIngest: false,
		},
		"astro": {
			ServerURL:  "https://mcp.docs.astro.build/mcp",
			Prefix:     "astro",
			AutoIngest: true,
		},
		"science": {
			ServerURL:  "https://api.sciencemadereadable.com/mcp",
			Prefix:     "science",
			AutoIngest: true,
			OnDemand:   true,
		},
	}
}

// ResolvedUpstreamMCPServers returns the merged set of default and configured upstream MCP servers.
func (c *Config) ResolvedUpstreamMCPServers() map[string]UpstreamMCPServer {
	defaults := DefaultUpstreamMCPServers()
	res := make(map[string]UpstreamMCPServer, len(defaults)+len(c.MCPUpstreams)+2)
	for k, v := range defaults {
		res[k] = v
	}

	if c != nil {
		// Resolve AlphaXiv token from config or env
		alphaToken := c.AlphaXivAPIToken
		if alphaToken == "" {
			alphaToken = os.Getenv("ALPHAXIV_API_TOKEN")
		}
		if alphaToken != "" {
			res["alphaxiv"] = UpstreamMCPServer{
				ServerURL:  "https://api.alphaxiv.org/mcp/v1",
				AuthToken:  alphaToken,
				Headers:    map[string]string{"Authorization": "Bearer " + alphaToken},
				Prefix:     "alphaxiv",
				AutoIngest: true,
				OnDemand:   true,
			}
		}

		// Resolve Stitch API key from config or env
		stitchKey := c.StitchAPIKey
		if stitchKey == "" {
			stitchKey = os.Getenv("STITCH_API_KEY")
			if stitchKey == "" {
				stitchKey = os.Getenv("GOOGLE_API_KEY")
			}
		}
		if stitchKey != "" {
			res["stitch"] = UpstreamMCPServer{
				ServerURL:  "https://stitch.googleapis.com/mcp",
				Headers:    map[string]string{"X-Goog-Api-Key": stitchKey},
				Prefix:     "stitch",
				AutoIngest: false,
				OnDemand:   true,
			}
		}

		for k, v := range c.MCPUpstreams {
			res[k] = v
		}
	}

	return res
}

// AssistanceTierStatus reports the two-tier configuration state (global vs project) for semantic assistance.
type AssistanceTierStatus struct {
	Active           bool     `json:"active"`
	GlobalConfigured bool     `json:"global_configured"`
	ProjectEnabled   bool     `json:"project_enabled"`
	Provider         string   `json:"provider,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	Message          string   `json:"message"`
}

// CheckAssistanceTier evaluates the two-tier scope state for the given project.
func (c *Config) CheckAssistanceTier(project string) AssistanceTierStatus {
	if c == nil {
		return AssistanceTierStatus{
			Active:           false,
			GlobalConfigured: false,
			ProjectEnabled:   false,
			Message:          "Assistance unconfigured (pure local offline operation).",
		}
	}

	globalConfigured := c.TypeSafeAPIKey != "" || (c.AssistProvider != "" && c.AssistProvider != "none")
	projectAllowed := c.AllowsAssistProject(project)
	projectEnabled := c.AssistEnabled && projectAllowed

	active := globalConfigured && projectEnabled

	status := AssistanceTierStatus{
		Active:           active,
		GlobalConfigured: globalConfigured,
		ProjectEnabled:   projectEnabled,
		Provider:         c.AssistProvider,
		Scopes:           c.AssistScopes,
	}

	if active {
		status.Message = fmt.Sprintf("JEV semantic assistance is active for %s (scopes: %s).", project, strings.Join(c.AssistScopes, ", "))
	} else if globalConfigured && !c.AssistEnabled {
		status.Message = "Configured globally, but disabled for this project (.sila/config.json assist_enabled=false)."
	} else if globalConfigured && !projectAllowed {
		status.Message = fmt.Sprintf("Configured globally, but project %q is not in assist_projects allowlist.", project)
	} else {
		status.Message = "Unconfigured (pure local offline operation; zero external calls)."
	}

	return status
}

var (
	cfgMu sync.RWMutex
)

// DefaultConfigDir returns the XDG-compliant configuration directory ($XDG_CONFIG_HOME/sila).
func DefaultConfigDir() string {
	return ConfigDir()
}

// DefaultConfigPath returns the standard configuration file path ($XDG_CONFIG_HOME/sila/config.json).
func DefaultConfigPath() string {
	return ConfigPath()
}

// Load reads the configuration from disk, falling back to environment variables.
// If no explicit path is passed and the primary config ($XDG_CONFIG_HOME/sila/config.json)
// does not exist, it seamlessly checks legacy locations (~/.local/share/sila/config.json),
// migrates the contents forward to the XDG config directory, and returns the configuration.
func Load(path string) (*Config, error) {
	return LoadForDir(path, "")
}

// LoadForDir loads the configuration, applying cascading project-level configuration
// located in projectDir (or the current working directory if projectDir is empty).
func LoadForDir(path, projectDir string) (*Config, error) {
	cfgMu.RLock()
	defer cfgMu.RUnlock()

	migrating := false
	var legacySourcePath string
	wasDefault := (path == "")

	if path == "" {
		primary := filepath.Join(ConfigDir(), "config.json")
		if _, err := os.Stat(primary); err == nil {
			path = primary
		} else {
			// Check legacy path in DataDir
			legacyData := filepath.Join(DataDir(), "config.json")
			if _, err := os.Stat(legacyData); err == nil {
				path = legacyData
				migrating = true
				legacySourcePath = legacyData
			} else {
				path = primary
			}
		}
	}

	cfg := &Config{}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading config %s: %w", path, err)
		}
	} else {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config %s: %w", path, err)
		}
		// If loaded from legacy source, migrate forward to primary XDG config
		if migrating && legacySourcePath != "" {
			primaryTarget := filepath.Join(ConfigDir(), "config.json")
			_ = os.MkdirAll(ConfigDir(), 0700)
			_ = os.WriteFile(primaryTarget, data, 0600)
		}
	}

	// Cascading project configuration: .sila/config.json in repo root or current directory
	if wasDefault {
		if projPath := FindProjectConfig(projectDir); projPath != "" && projPath != path {
			if projData, err := os.ReadFile(projPath); err == nil {
				var projCfg Config
				if err := json.Unmarshal(projData, &projCfg); err == nil {
					Merge(cfg, &projCfg)
					var raw map[string]json.RawMessage
					if err := json.Unmarshal(projData, &raw); err == nil {
						if _, ok := raw["assist_enabled"]; ok {
							cfg.AssistEnabled = projCfg.AssistEnabled
						}
						if _, ok := raw["cloud_enabled"]; ok {
							cfg.CloudEnabled = projCfg.CloudEnabled
						}
					}
				}
			}
		}
	}

	// Environment variable overrides
	if envKey := os.Getenv("TAVILY_API_KEY"); envKey != "" {
		cfg.TavilyAPIKey = envKey
	}
	if envKey := os.Getenv("BRAVE_API_KEY"); envKey != "" {
		cfg.BraveAPIKey = envKey
	}
	if envKey := os.Getenv("KAGI_API_KEY"); envKey != "" {
		cfg.KagiAPIKey = envKey
	}
	if envKey := os.Getenv("GITHUB_TOKEN"); envKey != "" {
		cfg.GitHubToken = envKey
	}
	if envKey := os.Getenv("SRC_ACCESS_TOKEN"); envKey != "" {
		cfg.SourcegraphToken = envKey
	}
	if envKey := os.Getenv("SOURCEGRAPH_TOKEN"); envKey != "" {
		cfg.SourcegraphToken = envKey
	}
	if envKey := os.Getenv("STACKOVERFLOW_KEY"); envKey != "" {
		cfg.StackOverflowKey = envKey
	}
	if envKey := os.Getenv("GOOGLE_API_KEY"); envKey != "" {
		cfg.GoogleAPIKey = envKey
	}
	if envCX := os.Getenv("GOOGLE_CSE_CX"); envCX != "" {
		cfg.GoogleCSECX = envCX
	}
	if envProv := os.Getenv("SILA_SEARCH_PROVIDER"); envProv != "" {
		cfg.SearchProvider = envProv
	}
	if envKey := os.Getenv("TYPESAFE_API_KEY"); envKey != "" {
		cfg.TypeSafeAPIKey = envKey
	}
	if envAssist := os.Getenv("SILA_ASSIST_PROVIDER"); envAssist != "" {
		cfg.AssistProvider = envAssist
	}
	if envEnable := os.Getenv("SILA_ASSIST_ENABLED"); envEnable != "" {
		cfg.AssistEnabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}
	if envScopes := os.Getenv("SILA_ASSIST_SCOPES"); envScopes != "" {
		parts := strings.Split(envScopes, ",")
		cfg.AssistScopes = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.AssistScopes = append(cfg.AssistScopes, trimmed)
			}
		}
	}
	if envProjects := os.Getenv("SILA_ASSIST_PROJECTS"); envProjects != "" {
		parts := strings.Split(envProjects, ",")
		cfg.AssistProjects = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.AssistProjects = append(cfg.AssistProjects, trimmed)
			}
		}
	}
	if envEndpoint := os.Getenv("SILA_CLOUD_ENDPOINT"); envEndpoint != "" {
		cfg.CloudEndpoint = envEndpoint
	}
	if envToken := os.Getenv("SILA_CLOUD_AUTH_TOKEN"); envToken != "" {
		cfg.CloudToken = envToken
	}
	if envToken := os.Getenv("SILA_CLOUD_TOKEN"); envToken != "" {
		cfg.CloudToken = envToken
	}
	if envEnable := os.Getenv("SILA_CLOUD_ENABLED"); envEnable != "" {
		cfg.CloudEnabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}
	if envScopes := os.Getenv("SILA_CLOUD_SCOPES"); envScopes != "" {
		parts := strings.Split(envScopes, ",")
		cfg.CloudScopes = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.CloudScopes = append(cfg.CloudScopes, trimmed)
			}
		}
	}
	if envProjects := os.Getenv("SILA_CLOUD_PROJECTS"); envProjects != "" {
		parts := strings.Split(envProjects, ",")
		cfg.CloudProjects = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.CloudProjects = append(cfg.CloudProjects, trimmed)
			}
		}
	}

	if envEnable := os.Getenv("SILA_GATEWAY_ENABLED"); envEnable != "" {
		cfg.GatewayEnabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}
	if envSurfaces := os.Getenv("SILA_GATEWAY_SURFACES"); envSurfaces != "" {
		parts := strings.Split(envSurfaces, ",")
		cfg.GatewaySurfaces = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.GatewaySurfaces = append(cfg.GatewaySurfaces, trimmed)
			}
		}
	}
	if envURL := os.Getenv("SILA_GATEWAY_DISCORD_WEBHOOK"); envURL != "" {
		cfg.GatewayDiscord.WebhookURL = envURL
	}
	if envUser := os.Getenv("SILA_GATEWAY_DISCORD_USERNAME"); envUser != "" {
		cfg.GatewayDiscord.Username = envUser
	}
	if envEnable := os.Getenv("SILA_GATEWAY_DISCORD_ENABLED"); envEnable != "" {
		cfg.GatewayDiscord.Enabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}
	if envToken := os.Getenv("SILA_GATEWAY_TELEGRAM_TOKEN"); envToken != "" {
		cfg.GatewayTelegram.BotToken = envToken
	}
	if envChatID := os.Getenv("SILA_GATEWAY_TELEGRAM_CHAT_ID"); envChatID != "" {
		cfg.GatewayTelegram.ChatID = envChatID
	}
	if envEnable := os.Getenv("SILA_GATEWAY_TELEGRAM_ENABLED"); envEnable != "" {
		cfg.GatewayTelegram.Enabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}
	if envEndpoint := os.Getenv("SILA_GATEWAY_GEMINI_ENDPOINT"); envEndpoint != "" {
		cfg.GatewayGemini.Endpoint = envEndpoint
	}
	if envToken := os.Getenv("SILA_GATEWAY_GEMINI_TOKEN"); envToken != "" {
		cfg.GatewayGemini.AuthToken = envToken
	}
	if envEnable := os.Getenv("SILA_GATEWAY_GEMINI_ENABLED"); envEnable != "" {
		cfg.GatewayGemini.Enabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}
	if envEndpoint := os.Getenv("SILA_GATEWAY_MCP_ENDPOINT"); envEndpoint != "" {
		cfg.GatewayMCP.Endpoint = envEndpoint
	}
	if envToken := os.Getenv("SILA_GATEWAY_MCP_TOKEN"); envToken != "" {
		cfg.GatewayMCP.AuthToken = envToken
	}
	if envEnable := os.Getenv("SILA_GATEWAY_MCP_ENABLED"); envEnable != "" {
		cfg.GatewayMCP.Enabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}
	if envEnable := os.Getenv("SILA_MCP_UPSTREAMS_ENABLED"); envEnable != "" {
		cfg.MCPUpstreamsEnabled = strings.EqualFold(envEnable, "true") || envEnable == "1"
	}

	if cfg.Retrieval.OODTheta != nil && (*cfg.Retrieval.OODTheta < 0 || *cfg.Retrieval.OODTheta > 1) {
		return nil, fmt.Errorf("retrieval.ood_theta must be between 0 and 1, got %v", *cfg.Retrieval.OODTheta)
	}
	seenTargets := map[string]bool{}
	var deduped []string
	for _, target := range cfg.Trust.Targets {
		normalized := strings.ToLower(strings.TrimSpace(target))
		if normalized == "" {
			continue
		}
		if !isFullCommitSHA(normalized) {
			return nil, fmt.Errorf("trust.targets: %q is not a full 40-character commit SHA", target)
		}
		if seenTargets[normalized] {
			continue
		}
		seenTargets[normalized] = true
		deduped = append(deduped, normalized)
	}
	cfg.Trust.Targets = deduped
	if value := strings.ToLower(strings.TrimSpace(cfg.Redaction.Mask)); value != "" && value != "full" && value != "prefix" {
		return nil, fmt.Errorf("redaction.mask must be full or prefix, got %q", cfg.Redaction.Mask)
	}
	if value := strings.ToLower(strings.TrimSpace(cfg.Memory.Mirror)); value != "" && value != "on" && value != "off" {
		return nil, fmt.Errorf("memory.mirror must be on or off, got %q", cfg.Memory.Mirror)
	}
	if cfg.StateBlock.WelcomeBackHours != nil {
		hours := *cfg.StateBlock.WelcomeBackHours
		if hours < 0 || hours > 720 {
			return nil, fmt.Errorf("state_block.welcome_back_hours must be between 0 and 720, got %d", hours)
		}
	}
	return cfg, nil
}

// Save writes the configuration to disk.
func Save(path string, cfg *Config) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()

	if path == "" {
		path = DefaultConfigPath()
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

// SetKey updates a single configuration key and persists it.
func SetKey(path, key, value string) error {
	cfg, err := Load(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
			return fmt.Errorf("cannot update malformed configuration: %w", err)
		}
		cfg = &Config{}
	}

	switch strings.ToLower(key) {
	case "tavily_api_key", "tavily":
		cfg.TavilyAPIKey = strings.TrimSpace(value)
	case "brave_api_key", "brave":
		cfg.BraveAPIKey = strings.TrimSpace(value)
	case "kagi_api_key", "kagi":
		cfg.KagiAPIKey = strings.TrimSpace(value)
	case "github_token", "github", "gh_token":
		cfg.GitHubToken = strings.TrimSpace(value)
	case "sourcegraph_token", "sourcegraph", "src_token":
		cfg.SourcegraphToken = strings.TrimSpace(value)
	case "stackoverflow_key", "stackoverflow", "so_key":
		cfg.StackOverflowKey = strings.TrimSpace(value)
	case "google_api_key", "googleapikey", "api_key":
		cfg.GoogleAPIKey = strings.TrimSpace(value)
	case "google_cse_cx", "googlecsecx", "cse_cx", "cx":
		cfg.GoogleCSECX = strings.TrimSpace(value)
	case "search_provider", "provider":
		cfg.SearchProvider = strings.TrimSpace(value)
	case "typesafe_api_key", "typesafe", "jev_api_key":
		cfg.TypeSafeAPIKey = strings.TrimSpace(value)
	case "assist_provider", "assistance_provider":
		cfg.AssistProvider = strings.TrimSpace(value)
	case "assist_enabled", "assistance_enabled":
		val := strings.TrimSpace(value)
		cfg.AssistEnabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "assist_scopes", "assistance_scopes", "assist_scope", "assistance_scope":
		parts := strings.Split(value, ",")
		cfg.AssistScopes = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.AssistScopes = append(cfg.AssistScopes, trimmed)
			}
		}
	case "assist_projects", "assistance_projects", "assist_project", "assistance_project":
		parts := strings.Split(value, ",")
		cfg.AssistProjects = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.AssistProjects = append(cfg.AssistProjects, trimmed)
			}
		}
	case "cloud_enabled":
		val := strings.TrimSpace(value)
		cfg.CloudEnabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "cloud_endpoint", "cloud_url":
		cfg.CloudEndpoint = strings.TrimSpace(value)
	case "cloud_token", "cloud_auth_token", "edge_token", "edge_auth_token", "edge.token":
		cfg.CloudToken = strings.TrimSpace(value)
	case "cloud_scopes", "cloud_scope":
		parts := strings.Split(value, ",")
		cfg.CloudScopes = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.CloudScopes = append(cfg.CloudScopes, trimmed)
			}
		}
	case "cloud_projects", "cloud_project":
		parts := strings.Split(value, ",")
		cfg.CloudProjects = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.CloudProjects = append(cfg.CloudProjects, trimmed)
			}
		}
	case "gateway_enabled":
		val := strings.TrimSpace(value)
		cfg.GatewayEnabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "gateway_surfaces", "gateway_surface":
		parts := strings.Split(value, ",")
		cfg.GatewaySurfaces = nil
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cfg.GatewaySurfaces = append(cfg.GatewaySurfaces, trimmed)
			}
		}
	case "gateway_discord_enabled":
		val := strings.TrimSpace(value)
		cfg.GatewayDiscord.Enabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "gateway_discord_webhook", "gateway_discord_url":
		cfg.GatewayDiscord.WebhookURL = strings.TrimSpace(value)
	case "gateway_discord_username":
		cfg.GatewayDiscord.Username = strings.TrimSpace(value)
	case "gateway_telegram_enabled":
		val := strings.TrimSpace(value)
		cfg.GatewayTelegram.Enabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "gateway_telegram_token", "gateway_telegram_bot_token":
		cfg.GatewayTelegram.BotToken = strings.TrimSpace(value)
	case "gateway_telegram_chat_id", "gateway_telegram_chat":
		cfg.GatewayTelegram.ChatID = strings.TrimSpace(value)
	case "gateway_gemini_enabled":
		val := strings.TrimSpace(value)
		cfg.GatewayGemini.Enabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "gateway_gemini_endpoint", "gateway_gemini_url":
		cfg.GatewayGemini.Endpoint = strings.TrimSpace(value)
	case "gateway_gemini_token", "gateway_gemini_auth_token":
		cfg.GatewayGemini.AuthToken = strings.TrimSpace(value)
	case "gateway_mcp_enabled":
		val := strings.TrimSpace(value)
		cfg.GatewayMCP.Enabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "gateway_mcp_endpoint", "gateway_mcp_url":
		cfg.GatewayMCP.Endpoint = strings.TrimSpace(value)
	case "gateway_mcp_token", "gateway_mcp_auth_token":
		cfg.GatewayMCP.AuthToken = strings.TrimSpace(value)
	case "cloudflare_api_token", "cloudflare_token", "cf_token":
		cfg.CloudflareAPIToken = strings.TrimSpace(value)
	case "cloudflare_account_id", "cloudflare_account", "cf_account":
		cfg.CloudflareAccountID = strings.TrimSpace(value)
	case "cloudflare_d1_database_id", "cloudflare_d1_database", "cloudflare_d1_id", "cf_d1_id":
		cfg.CloudflareD1DatabaseID = strings.TrimSpace(value)
	case "alphaxiv_api_token", "alphaxiv_token", "alphaxiv":
		cfg.AlphaXivAPIToken = strings.TrimSpace(value)
	case "stitch_api_key", "stitch_key", "stitch":
		cfg.StitchAPIKey = strings.TrimSpace(value)
	case "mcp_upstreams_enabled", "upstreams_enabled", "mcp_upstream_enabled":
		val := strings.TrimSpace(value)
		cfg.MCPUpstreamsEnabled = strings.EqualFold(val, "true") || val == "1" || strings.EqualFold(val, "yes")
	case "recall_gate_policy", "recall_gate", "recall-policy":
		policy, err := ParseRecallGatePolicy(value)
		if err != nil {
			return err
		}
		cfg.RecallGate = string(policy)
	default:
		return errors.New("unknown config key: " + key + " (supported: brave_api_key, tavily_api_key, kagi_api_key, github_token, sourcegraph_token, stackoverflow_key, google_api_key, google_cse_cx, search_provider, typesafe_api_key, assist_provider, assist_enabled, assist_scopes, assist_projects, cloud_enabled, cloud_endpoint, cloud_token, cloud_scopes, cloud_projects, gateway_enabled, gateway_surfaces, gateway_discord_enabled, gateway_discord_webhook, gateway_telegram_enabled, gateway_telegram_token, gateway_telegram_chat_id, gateway_gemini_enabled, gateway_gemini_endpoint, gateway_gemini_token, gateway_mcp_enabled, gateway_mcp_endpoint, gateway_mcp_token, cloudflare_api_token, cloudflare_account_id, cloudflare_d1_database_id, alphaxiv_api_token, stitch_api_key, mcp_upstreams_enabled, recall_gate_policy)")
	}

	return Save(path, cfg)
}

// AddDocSite adds a documentation URL/domain to the registered doc sites.
func AddDocSite(path, site string) error {
	site = strings.TrimSpace(site)
	if site == "" {
		return errors.New("site URL cannot be empty")
	}

	cfg, err := Load(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
			return fmt.Errorf("cannot update malformed configuration: %w", err)
		}
		cfg = &Config{}
	}

	for _, s := range cfg.DocSites {
		if strings.EqualFold(s, site) {
			return nil // already exists
		}
	}

	cfg.DocSites = append(cfg.DocSites, site)
	return Save(path, cfg)
}

// RemoveDocSite removes a documentation URL/domain from the registered doc sites.
func RemoveDocSite(path, site string) error {
	site = strings.TrimSpace(site)
	cfg, err := Load(path)
	if err != nil {
		return nil
	}

	var updated []string
	for _, s := range cfg.DocSites {
		if !strings.EqualFold(s, site) {
			updated = append(updated, s)
		}
	}

	cfg.DocSites = updated
	return Save(path, cfg)
}

func isFullCommitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

// RetrievalSettings resolves the precision guard controls with env overrides.
func (c *Config) RetrievalSettings() (idfVeto, oodGuard bool, theta float64) {
	idfVeto, oodGuard, theta = true, true, 0.60
	if c != nil {
		if c.Retrieval.IDFVeto != nil {
			idfVeto = *c.Retrieval.IDFVeto
		}
		if c.Retrieval.OODGuard != nil {
			oodGuard = *c.Retrieval.OODGuard
		}
		if c.Retrieval.OODTheta != nil {
			theta = *c.Retrieval.OODTheta
		}
	}
	idfVeto = envSwitch("SILA_IDF_VETO", idfVeto)
	oodGuard = envSwitch("SILA_OOD_GUARD", oodGuard)
	return idfVeto, oodGuard, theta
}

func envSwitch(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "off", "0", "false":
		return false
	case "on", "1", "true":
		return true
	default:
		return fallback
	}
}
