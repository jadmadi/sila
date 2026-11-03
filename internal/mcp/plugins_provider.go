package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/plugins"
)

// PluginResult contains the parsed result of a plugin execution.
type PluginResult struct {
	Text           string         `json:"text"`
	StructuredData map[string]any `json:"structured_data,omitempty"`
	IsError        bool           `json:"is_error,omitempty"`
}

type pluginRoute struct {
	PluginName  string
	BinaryPath  string
	CommandName string
	Flags       map[string]plugins.FlagDoc
}

// PluginToolProvider manages dynamic MCP tool schemas derived from active plugins.
type PluginToolProvider struct {
	projectDir string
	binDir     string
	mu         sync.RWMutex
	routes     map[string]pluginRoute
	tools      []Tool
}

// NewPluginToolProvider constructs a provider scanning plugins for projectDir.
func NewPluginToolProvider(projectDir, binDir string) *PluginToolProvider {
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	}
	if binDir == "" {
		binDir = plugins.ResolveBinDir()
	}
	p := &PluginToolProvider{
		projectDir: projectDir,
		binDir:     binDir,
		routes:     make(map[string]pluginRoute),
	}
	_ = p.Refresh(context.Background())
	return p
}

// Refresh re-scans active plugins in projectDir and synthesizes MCP tools.
func (p *PluginToolProvider) Refresh(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.routes = make(map[string]pluginRoute)
	p.tools = nil

	activeNames, err := plugins.ListActivePlugins(p.projectDir)
	if err != nil || len(activeNames) == 0 {
		return nil
	}

	for _, name := range activeNames {
		binPath := p.findBinary(name)
		if binPath == "" {
			continue
		}

		meta, err := p.extractMetadata(ctx, binPath)
		if err != nil || meta == nil {
			meta = &plugins.PluginMetadata{
				Name:  name,
				Short: fmt.Sprintf("Execute %s plugin", name),
			}
		}

		p.registerMetadataTools(name, binPath, meta)
	}

	sort.Slice(p.tools, func(i, j int) bool {
		return p.tools[i].Name < p.tools[j].Name
	})

	return nil
}

func (p *PluginToolProvider) findBinary(name string) string {
	candidate := filepath.Join(p.binDir, name)
	if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
		return candidate
	}
	if links, err := plugins.LoadLinks(""); err == nil {
		if entry, ok := links[name]; ok {
			if fi, err := os.Stat(entry.Target); err == nil && !fi.IsDir() {
				return entry.Target
			}
		}
	}
	if lp, err := exec.LookPath(name); err == nil {
		return lp
	}
	return ""
}

func (p *PluginToolProvider) extractMetadata(ctx context.Context, binPath string) (*plugins.PluginMetadata, error) {
	inspectCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(inspectCtx, binPath, "help", "--json")
	out, err := cmd.Output()
	if err != nil {
		cmd2 := exec.CommandContext(inspectCtx, binPath, "--help", "--json")
		out, err = cmd2.Output()
		if err != nil {
			return nil, err
		}
	}

	var meta plugins.PluginMetadata
	if err := json.Unmarshal(out, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (p *PluginToolProvider) registerMetadataTools(pluginName, binPath string, meta *plugins.PluginMetadata) {
	cleanPlugin := sanitizeIdentifier(pluginName)
	category := ToolCategoryCollab
	if meta.Category != "" {
		category = meta.Category
	}

	if len(meta.Commands) == 0 {
		toolName := fmt.Sprintf("plugin_%s", cleanPlugin)
		desc := meta.Short
		if desc == "" {
			desc = fmt.Sprintf("Execute %s plugin", pluginName)
		}
		tool := Tool{
			Name:        toolName,
			Description: desc,
			Category:    category,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"args": {
						Type:        "string",
						Description: "Command-line arguments passed to the plugin",
					},
				},
			},
		}
		p.tools = append(p.tools, tool)
		p.routes[toolName] = pluginRoute{
			PluginName: pluginName,
			BinaryPath: binPath,
			Flags:      make(map[string]plugins.FlagDoc),
		}
		return
	}

	for _, cmd := range meta.Commands {
		cmdName := strings.TrimSpace(cmd.Name)
		var toolName string
		if cmdName == "" || cmdName == meta.Name {
			toolName = fmt.Sprintf("plugin_%s", cleanPlugin)
		} else {
			cleanCmd := sanitizeIdentifier(cmdName)
			toolName = fmt.Sprintf("plugin_%s_%s", cleanPlugin, cleanCmd)
		}

		desc := cmd.Short
		if desc == "" {
			desc = cmd.Long
		}
		if desc == "" {
			desc = fmt.Sprintf("Execute %s %s plugin subcommand", pluginName, cmdName)
		}

		properties := make(map[string]PropertySchema)
		var required []string
		flagMap := make(map[string]plugins.FlagDoc)

		for _, flag := range cmd.Flags {
			flagKey := sanitizeIdentifier(flag.Name)
			propType := "string"
			switch strings.ToLower(flag.Type) {
			case "bool", "boolean":
				propType = "boolean"
			case "int", "integer", "int64", "uint":
				propType = "integer"
			case "float", "float64", "number":
				propType = "number"
			}
			properties[flagKey] = PropertySchema{
				Type:        propType,
				Description: flag.Usage,
			}
			if flag.Required {
				required = append(required, flagKey)
			}
			flagMap[flagKey] = flag
			if flag.Name != flagKey {
				flagMap[flag.Name] = flag
			}
		}

		properties["args"] = PropertySchema{
			Type:        "string",
			Description: "Optional positional arguments passed to the command",
		}

		tool := Tool{
			Name:        toolName,
			Description: desc,
			Category:    category,
			InputSchema: InputSchema{
				Type:       "object",
				Properties: properties,
				Required:   required,
			},
		}

		p.tools = append(p.tools, tool)
		p.routes[toolName] = pluginRoute{
			PluginName:  pluginName,
			BinaryPath:  binPath,
			CommandName: cmdName,
			Flags:       flagMap,
		}
	}
}

// Tools returns the registered plugin MCP tools.
func (p *PluginToolProvider) Tools() []Tool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]Tool, len(p.tools))
	copy(out, p.tools)
	return out
}

// HasTool checks whether toolName is handled by this provider.
func (p *PluginToolProvider) HasTool(toolName string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.routes[toolName]
	return ok
}

// Call executes the plugin tool with arguments.
func (p *PluginToolProvider) Call(ctx context.Context, toolName string, rawArgs json.RawMessage) (*PluginResult, error) {
	p.mu.RLock()
	route, ok := p.routes[toolName]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plugin tool %q not found", toolName)
	}

	var argsMap map[string]any
	if len(rawArgs) > 0 {
		if err := json.Unmarshal(rawArgs, &argsMap); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	var execArgs []string
	if route.CommandName != "" {
		execArgs = append(execArgs, route.CommandName)
	}

	var posArgs []string
	for k, v := range argsMap {
		if k == "args" {
			switch val := v.(type) {
			case string:
				if strings.TrimSpace(val) != "" {
					posArgs = append(posArgs, strings.Fields(val)...)
				}
			case []any:
				for _, item := range val {
					posArgs = append(posArgs, fmt.Sprint(item))
				}
			}
			continue
		}

		flagDoc, hasFlag := route.Flags[k]
		flagName := k
		if hasFlag && flagDoc.Name != "" {
			flagName = flagDoc.Name
		}

		switch val := v.(type) {
		case bool:
			if val {
				execArgs = append(execArgs, fmt.Sprintf("--%s", flagName))
			} else {
				execArgs = append(execArgs, fmt.Sprintf("--%s=false", flagName))
			}
		default:
			execArgs = append(execArgs, fmt.Sprintf("--%s=%v", flagName, val))
		}
	}

	execArgs = append(execArgs, posArgs...)

	cmd := exec.CommandContext(ctx, route.BinaryPath, execArgs...)
	cmd.Dir = p.projectDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	res := &PluginResult{}
	outStr := strings.TrimSpace(stdout.String())
	errStr := strings.TrimSpace(stderr.String())

	if runErr != nil {
		res.IsError = true
	}

	if outStr != "" {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(outStr), &decoded); err == nil {
			res.StructuredData = decoded
			res.Text = outStr
		} else {
			var decodedArr []any
			if err := json.Unmarshal([]byte(outStr), &decodedArr); err == nil {
				res.StructuredData = map[string]any{"items": decodedArr}
				res.Text = outStr
			} else {
				res.StructuredData = map[string]any{"output": outStr}
				res.Text = outStr
			}
		}
	}

	if errStr != "" {
		if res.Text != "" {
			res.Text += "\n\nstderr:\n" + errStr
		} else {
			res.Text = errStr
		}
	}

	if runErr != nil && res.Text == "" {
		res.Text = fmt.Sprintf("execution failed: %v", runErr)
	}

	return res, nil
}

func sanitizeIdentifier(s string) string {
	s = strings.TrimSpace(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	res := strings.Trim(sb.String(), "_")
	if res == "" {
		return "cmd"
	}
	return res
}
