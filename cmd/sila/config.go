package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/render"
)

// configCredential reports presence and masking without ever mixing a raw and
// masked value in one payload.
type configCredential struct {
	Set    bool   `json:"set"`
	Masked string `json:"masked,omitempty"`
	Value  string `json:"value,omitempty"`
}

func credentialReport(value string, reveal bool) configCredential {
	c := configCredential{Set: value != ""}
	if value == "" {
		return c
	}
	if reveal {
		c.Value = value
	} else {
		c.Masked = config.MaskSecret(value)
	}
	return c
}

func activeProviderReport(cfg *config.Config) map[string]any {
	name, reason, source := "duckduckgo", "zero-config default", "default"
	switch {
	case cfg.SearchProvider != "":
		name, reason, source = cfg.SearchProvider, "explicitly configured", "explicit"
	case cfg.TavilyAPIKey != "":
		name, reason, source = "tavily", "API key present", "key"
	case cfg.BraveAPIKey != "":
		name, reason, source = "brave", "API key present", "key"
	case cfg.KagiAPIKey != "":
		name, reason, source = "kagi", "API key present", "key"
	case cfg.GoogleAPIKey != "" && cfg.GoogleCSECX != "":
		name, reason, source = "google", "API key and CSE CX present", "key"
	}
	return map[string]any{"name": name, "reason": reason, "source": source}
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// buildConfigReport is the versioned machine contract. It covers every field
// the human view prints and keeps the legacy top-level keys additively.
func buildConfigReport(cfg *config.Config, reveal bool) map[string]any {
	credentials := map[string]configCredential{
		"brave":         credentialReport(cfg.BraveAPIKey, reveal),
		"tavily":        credentialReport(cfg.TavilyAPIKey, reveal),
		"kagi":          credentialReport(cfg.KagiAPIKey, reveal),
		"github":        credentialReport(cfg.GitHubToken, reveal),
		"sourcegraph":   credentialReport(cfg.SourcegraphToken, reveal),
		"stackoverflow": credentialReport(cfg.StackOverflowKey, reveal),
		"google_api":    credentialReport(cfg.GoogleAPIKey, reveal),
		"google_cse_cx": credentialReport(cfg.GoogleCSECX, reveal),
		"typesafe":      credentialReport(cfg.TypeSafeAPIKey, reveal),
		"cloudflare":    credentialReport(cfg.CloudflareAPIToken, reveal),
	}
	report := map[string]any{
		"schema_version":  1,
		"config_file":     config.DefaultConfigPath(),
		"config_dir":      config.ConfigDir(),
		"project_config":  config.FindProjectConfig(""),
		"active_provider": activeProviderReport(cfg),
		"credentials":     credentials,
		"assist": map[string]any{
			"enabled":  cfg.AssistEnabled,
			"provider": cfg.AssistProvider,
			"scopes":   nonNilStrings(cfg.AssistScopes),
			"projects": nonNilStrings(cfg.AssistProjects),
		},
		"cloudflare": map[string]any{
			"account_id":     cfg.CloudflareAccountID,
			"d1_database_id": cfg.CloudflareD1DatabaseID,
			"token":          credentialReport(cfg.CloudflareAPIToken, reveal),
		},
		"doc_sites": map[string]any{
			"count": len(cfg.DocSites),
			"items": nonNilStrings(cfg.DocSites),
		},
	}

	// Preserve legacy top-level keys for one release.
	legacy := cfg
	if !reveal {
		legacy = cfg.Redacted()
	}
	if b, err := json.Marshal(legacy); err == nil {
		var fields map[string]any
		if json.Unmarshal(b, &fields) == nil {
			for k, v := range fields {
				if _, exists := report[k]; !exists {
					report[k] = v
				}
			}
		}
	}
	return report
}

func runConfig(ui render.UI, jsonOut bool, reveal bool, args []string) {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}

	if len(args) == 0 {
		if jsonOut {
			emitJSON(buildConfigReport(cfg, reveal))
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · configuration")))
		maskKey := func(k string) string {
			if k == "" {
				return "(not set)"
			}
			if reveal {
				return k
			}
			return config.MaskSecret(k)
		}
		prov := "DuckDuckGo (zero-config default)"
		if cfg.SearchProvider != "" {
			prov = cfg.SearchProvider + " (explicit)"
		} else if cfg.TavilyAPIKey != "" {
			prov = "Tavily (active)"
		} else if cfg.BraveAPIKey != "" {
			prov = "Brave Search (active)"
		} else if cfg.KagiAPIKey != "" {
			prov = "Kagi Search (active)"
		} else if cfg.GoogleAPIKey != "" && cfg.GoogleCSECX != "" {
			prov = "Google Custom Search (active)"
		}
		rows := [][2]string{
			{"Config File", config.DefaultConfigPath()},
			{"Active Provider", prov},
			{"Brave API Key", maskKey(cfg.BraveAPIKey)},
			{"Tavily API Key", maskKey(cfg.TavilyAPIKey)},
			{"Kagi API Key", maskKey(cfg.KagiAPIKey)},
			{"GitHub Token", maskKey(cfg.GitHubToken)},
			{"Sourcegraph Token", maskKey(cfg.SourcegraphToken)},
			{"StackOverflow Key", maskKey(cfg.StackOverflowKey)},
			{"Google API Key", maskKey(cfg.GoogleAPIKey)},
			{"Google CSE CX", firstNonEmpty(cfg.GoogleCSECX, "(not set)")},
			{"TypeSafe API Key", maskKey(cfg.TypeSafeAPIKey)},
			{"Assist Enabled", fmt.Sprintf("%t", cfg.AssistEnabled)},
			{"Assist Provider", firstNonEmpty(cfg.AssistProvider, "(none)")},
			{"Assist Scopes", firstNonEmpty(strings.Join(cfg.AssistScopes, ", "), "(none - explicit scope required)")},
			{"Assist Projects", firstNonEmpty(strings.Join(cfg.AssistProjects, ", "), "(all)")},
		}
		if cfg.CloudflareAccountID != "" {
			rows = append(rows, [2]string{"Cloudflare Account", cfg.CloudflareAccountID})
		}
		if cfg.CloudflareD1DatabaseID != "" {
			rows = append(rows, [2]string{"Cloudflare D1 DB", cfg.CloudflareD1DatabaseID})
		}
		if cfg.CloudflareAPIToken != "" {
			rows = append(rows, [2]string{"Cloudflare Token", maskKey(cfg.CloudflareAPIToken)})
		}
		rows = append(rows, [2]string{"Doc Sites", fmt.Sprintf("%d registered", len(cfg.DocSites))})

		width := 0
		for _, row := range rows {
			if len(row[0])+1 > width {
				width = len(row[0]) + 1
			}
		}
		for _, row := range rows {
			fmt.Fprintf(os.Stdout, "  %-*s %s\n", width, row[0]+":", row[1])
		}
		for _, s := range cfg.DocSites {
			fmt.Fprintf(os.Stdout, "    • %s\n", s)
		}
		return
	}

	sub := args[0]
	switch sub {
	case "path", "paths":
		projCfgPath := config.FindProjectConfig("")
		if jsonOut {
			emitJSON(map[string]string{
				"config_file":    config.DefaultConfigPath(),
				"project_config": projCfgPath,
				"config_dir":     config.ConfigDir(),
				"data_dir":       config.DataDir(),
				"state_dir":      config.StateDir(),
				"cache_dir":      config.CacheDir(),
				"sila_db":        config.DBPath(),
			})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · paths")))
		fmt.Fprintf(os.Stdout, "  Config File:    %s\n", config.DefaultConfigPath())
		if projCfgPath != "" {
			fmt.Fprintf(os.Stdout, "  Project Config: %s\n", projCfgPath)
		} else {
			fmt.Fprintf(os.Stdout, "  Project Config: (none - initialize with sila config init --project)\n")
		}
		fmt.Fprintf(os.Stdout, "  Config Dir:     %s\n", config.ConfigDir())
		fmt.Fprintf(os.Stdout, "  Data Dir:       %s\n", config.DataDir())
		fmt.Fprintf(os.Stdout, "  State Dir:      %s\n", config.StateDir())
		fmt.Fprintf(os.Stdout, "  Cache Dir:      %s\n", config.CacheDir())
		fmt.Fprintf(os.Stdout, "  Database:       %s\n", config.DBPath())
		return

	case "schema":
		emitJSON(config.JSONSchema())
		return

	case "explain":
		if len(args) > 1 {
			targetKey := args[1]
			doc, ok := config.ExplainKey(targetKey)
			if !ok {
				fmt.Fprintf(os.Stderr, "unknown config key: %s (run 'sila config explain' to list all supported keys)\n", targetKey)
				os.Exit(1)
			}
			curVal := getConfigValueFormatted(cfg, doc.Key, reveal)
			if jsonOut {
				res := map[string]any{
					"key":          doc.Key,
					"type":         doc.Type,
					"default":      doc.Default,
					"current":      curVal,
					"description":  doc.Description,
					"security":     doc.Security,
					"is_secret":    doc.IsSecret,
					"valid_values": doc.ValidValues,
				}
				if doc.Example != "" {
					res["example"] = doc.Example
				}
				emitJSON(res)
				return
			}
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · config · "+doc.Key)))
			fmt.Fprintf(os.Stdout, "  Type:         %s\n", doc.Type)
			fmt.Fprintf(os.Stdout, "  Default:      %s\n", doc.Default)
			fmt.Fprintf(os.Stdout, "  Current:      %s\n", curVal)
			fmt.Fprintf(os.Stdout, "  Description:  %s\n", doc.Description)
			fmt.Fprintf(os.Stdout, "  Security:     %s\n", doc.Security)
			if len(doc.ValidValues) > 0 {
				fmt.Fprintf(os.Stdout, "  Valid Values: %s\n", strings.Join(doc.ValidValues, ", "))
			}
			if doc.Example != "" {
				fmt.Fprintf(os.Stdout, "  Example:      %s\n", doc.Example)
			}
			return
		}

		all := config.AllKeys()
		if jsonOut {
			type explainItem struct {
				Key         string   `json:"key"`
				Type        string   `json:"type"`
				Default     string   `json:"default"`
				Current     string   `json:"current"`
				Description string   `json:"description"`
				Security    string   `json:"security"`
				IsSecret    bool     `json:"is_secret"`
				ValidValues []string `json:"valid_values,omitempty"`
			}
			var list []explainItem
			for _, k := range all {
				list = append(list, explainItem{
					Key:         k.Key,
					Type:        k.Type,
					Default:     k.Default,
					Current:     getConfigValueFormatted(cfg, k.Key, reveal),
					Description: k.Description,
					Security:    k.Security,
					IsSecret:    k.IsSecret,
					ValidValues: k.ValidValues,
				})
			}
			emitJSON(list)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · configuration keys reference")))
		fmt.Fprintf(os.Stdout, "  Global:  %s\n", config.DefaultConfigPath())
		if p := config.FindProjectConfig(""); p != "" {
			fmt.Fprintf(os.Stdout, "  Project: %s\n", p)
		}
		fmt.Fprintln(os.Stdout)
		for _, k := range all {
			curVal := getConfigValueFormatted(cfg, k.Key, reveal)
			fmt.Fprintf(os.Stdout, "  • %s (%s)\n", ui.Bold(k.Key), k.Type)
			fmt.Fprintf(os.Stdout, "    Current:     %s\n", curVal)
			fmt.Fprintf(os.Stdout, "    Default:     %s\n", k.Default)
			fmt.Fprintf(os.Stdout, "    Description: %s\n", k.Description)
			fmt.Fprintf(os.Stdout, "    Security:    %s\n", k.Security)
			fmt.Fprintln(os.Stdout)
		}
		return

	case "init":
		isProject := false
		for _, a := range args[1:] {
			if a == "--project" || a == "-p" {
				isProject = true
			}
		}
		initPath, created, err := config.InitConfig("", isProject)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: failed to initialize configuration: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{
				"path":       initPath,
				"created":    created,
				"is_project": isProject,
			})
			return
		}
		scopeDesc := "global"
		if isProject {
			scopeDesc = "project"
		}
		if created {
			fmt.Fprintf(os.Stdout, "%s Initialized %s configuration at %s\n", ui.Green("✔"), scopeDesc, ui.Bold(initPath))
		} else {
			fmt.Fprintf(os.Stdout, "%s Configuration already exists at %s (preserved)\n", ui.Cyan("•"), ui.Bold(initPath))
		}
		return

	case "set":
		targetPath := ""
		rawArgs := args[1:]
		if len(rawArgs) > 0 && (rawArgs[0] == "--project" || rawArgs[0] == "-p") {
			targetPath = filepath.Join(config.ProjectConfigDir(""), "config.json")
			rawArgs = rawArgs[1:]
		}
		if len(rawArgs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila config set [--project] <key> <value>")
			os.Exit(2)
		}
		key := rawArgs[0]
		val := rawArgs[1]
		if err := config.SetKey(targetPath, key, val); err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		dest := "global"
		if targetPath != "" {
			dest = targetPath
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Updated configuration ("+dest+"): "+ui.Bold(key))

	case "get":
		if len(args) < 2 {
			if reveal {
				emitJSON(cfg)
			} else {
				emitJSON(cfg.Redacted())
			}
			return
		}
		key := strings.ToLower(args[1])
		switch key {
		case "tavily_api_key", "tavily":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.TavilyAPIKey)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.TavilyAPIKey))
			}
		case "brave_api_key", "brave":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.BraveAPIKey)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.BraveAPIKey))
			}
		case "kagi_api_key", "kagi":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.KagiAPIKey)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.KagiAPIKey))
			}
		case "github_token", "github", "gh_token":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.GitHubToken)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.GitHubToken))
			}
		case "sourcegraph_token", "sourcegraph", "src_token":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.SourcegraphToken)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.SourcegraphToken))
			}
		case "stackoverflow_key", "stackoverflow", "so_key":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.StackOverflowKey)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.StackOverflowKey))
			}
		case "google_api_key", "api_key":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.GoogleAPIKey)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.GoogleAPIKey))
			}
		case "google_cse_cx", "cx":
			fmt.Fprintln(os.Stdout, cfg.GoogleCSECX)
		case "search_provider", "provider":
			fmt.Fprintln(os.Stdout, cfg.SearchProvider)
		case "typesafe_api_key", "typesafe", "jev_api_key":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.TypeSafeAPIKey)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.TypeSafeAPIKey))
			}
		case "assist_provider", "assistance_provider":
			fmt.Fprintln(os.Stdout, cfg.AssistProvider)
		case "assist_enabled", "assistance_enabled":
			fmt.Fprintln(os.Stdout, cfg.AssistEnabled)
		case "assist_scopes", "assistance_scopes", "assist_scope", "assistance_scope":
			fmt.Fprintln(os.Stdout, strings.Join(cfg.AssistScopes, ","))
		case "assist_projects", "assistance_projects", "assist_project", "assistance_project":
			fmt.Fprintln(os.Stdout, strings.Join(cfg.AssistProjects, ","))
		case "cloud_enabled":
			fmt.Fprintln(os.Stdout, cfg.CloudEnabled)
		case "cloud_endpoint", "cloud_url":
			fmt.Fprintln(os.Stdout, cfg.CloudEndpoint)
		case "cloud_token", "cloud_auth_token":
			if reveal {
				fmt.Fprintln(os.Stdout, cfg.CloudToken)
			} else {
				fmt.Fprintln(os.Stdout, config.MaskSecret(cfg.CloudToken))
			}
		case "cloud_scopes", "cloud_scope":
			fmt.Fprintln(os.Stdout, strings.Join(cfg.CloudScopes, ","))
		case "cloud_projects", "cloud_project":
			fmt.Fprintln(os.Stdout, strings.Join(cfg.CloudProjects, ","))
		case "doc_sites":
			for _, s := range cfg.DocSites {
				fmt.Fprintln(os.Stdout, s)
			}
		default:
			fmt.Fprintf(os.Stderr, "unknown config key: %s\n", key)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "Usage: sila config [path | explain [<key>] | schema | init [--global|--project] | set <key> <value> | get <key>]")
		os.Exit(2)
	}
}

func getConfigValueFormatted(cfg *config.Config, key string, reveal bool) string {
	if cfg == nil {
		return "(not set)"
	}
	maskKey := func(k string) string {
		if k == "" {
			return "(not set)"
		}
		if reveal {
			return k
		}
		return config.MaskSecret(k)
	}

	switch key {
	case "search_provider":
		if cfg.SearchProvider != "" {
			return cfg.SearchProvider
		}
		return "duckduckgo (default)"
	case "brave_api_key":
		return maskKey(cfg.BraveAPIKey)
	case "tavily_api_key":
		return maskKey(cfg.TavilyAPIKey)
	case "kagi_api_key":
		return maskKey(cfg.KagiAPIKey)
	case "google_api_key":
		return maskKey(cfg.GoogleAPIKey)
	case "google_cse_cx":
		if cfg.GoogleCSECX != "" {
			return cfg.GoogleCSECX
		}
		return "(not set)"
	case "github_token":
		return maskKey(cfg.GitHubToken)
	case "sourcegraph_token":
		return maskKey(cfg.SourcegraphToken)
	case "stackoverflow_key":
		return maskKey(cfg.StackOverflowKey)
	case "typesafe_api_key":
		return maskKey(cfg.TypeSafeAPIKey)
	case "assist_provider":
		if cfg.AssistProvider != "" {
			return cfg.AssistProvider
		}
		return "typesafe (default)"
	case "assist_enabled":
		return fmt.Sprintf("%t", cfg.AssistEnabled)
	case "assist_scopes":
		if len(cfg.AssistScopes) == 0 {
			return "[]"
		}
		return "[" + strings.Join(cfg.AssistScopes, ", ") + "]"
	case "assist_projects":
		if len(cfg.AssistProjects) == 0 {
			return "[] (all)"
		}
		return "[" + strings.Join(cfg.AssistProjects, ", ") + "]"
	case "cloud_enabled":
		return fmt.Sprintf("%t", cfg.CloudEnabled)
	case "cloud_endpoint":
		if cfg.CloudEndpoint != "" {
			return cfg.CloudEndpoint
		}
		return "(not set)"
	case "cloud_token":
		return maskKey(cfg.CloudToken)
	case "cloud_scopes":
		if len(cfg.CloudScopes) == 0 {
			return "[]"
		}
		return "[" + strings.Join(cfg.CloudScopes, ", ") + "]"
	case "cloud_projects":
		if len(cfg.CloudProjects) == 0 {
			return "[] (all)"
		}
		return "[" + strings.Join(cfg.CloudProjects, ", ") + "]"
	case "doc_sites":
		if len(cfg.DocSites) == 0 {
			return "[]"
		}
		return "[" + strings.Join(cfg.DocSites, ", ") + "]"
	case "cloudflare_api_token":
		return maskKey(cfg.CloudflareAPIToken)
	case "cloudflare_account_id":
		if cfg.CloudflareAccountID != "" {
			return cfg.CloudflareAccountID
		}
		return "(not set)"
	case "cloudflare_d1_database_id":
		if cfg.CloudflareD1DatabaseID != "" {
			return cfg.CloudflareD1DatabaseID
		}
		return "(not set)"
	default:
		return "(unknown)"
	}
}

// runExport dumps messages + facts per project as JSON (stdout or --out), or compiles rules.
