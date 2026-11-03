package plugins

// FlagDoc describes one flag in the plugin's machine-readable JSON schema.
type FlagDoc struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Default  string `json:"default"`
	Usage    string `json:"usage"`
	Required bool   `json:"required,omitempty"`
}

// CommandInfo describes one subcommand provided by a plugin.
type CommandInfo struct {
	Name    string    `json:"name"`
	Short   string    `json:"short"`
	Long    string    `json:"long,omitempty"`
	Example string    `json:"example,omitempty"`
	Flags   []FlagDoc `json:"flags,omitempty"`
}

// PluginMetadata describes the self-documenting JSON contract emitted by <tool> help --json.
type PluginMetadata struct {
	SchemaVersion string        `json:"schema_version,omitempty"`
	Name          string        `json:"name"`
	Version       string        `json:"version,omitempty"`
	Short         string        `json:"short"`
	Long          string        `json:"long,omitempty"`
	Author        string        `json:"author,omitempty"`
	License       string        `json:"license,omitempty"`
	Repository    string        `json:"repository,omitempty"`
	Category      string        `json:"category,omitempty"`
	Capabilities  []string      `json:"capabilities,omitempty"`
	Commands      []CommandInfo `json:"commands"`
}

// Status represents the installation state of a plugin.
type Status string

const (
	StatusInstalled Status = "installed"
	StatusLinked    Status = "linked"
	StatusAvailable Status = "available"
)

// PluginItem summarizes a discovered plugin for CLI display and JSON serialization.
type PluginItem struct {
	Name         string        `json:"name"`
	Version      string        `json:"version,omitempty"`
	Status       Status        `json:"status"`
	Active       bool          `json:"active,omitempty"`
	Short        string        `json:"short"`
	BinaryPath   string        `json:"binary_path,omitempty"`
	TargetLink   string        `json:"target_link,omitempty"`
	SourcePath   string        `json:"source_path,omitempty"`
	Capabilities []string      `json:"capabilities,omitempty"`
	Commands     []CommandInfo `json:"commands,omitempty"`
}

// LintReport records validation results when auditing a plugin binary.
type LintReport struct {
	Name           string   `json:"name"`
	BinaryPath     string   `json:"binary_path"`
	Clean          bool     `json:"clean"`
	SchemaVersion  string   `json:"schema_version,omitempty"`
	LatencyMs      int64    `json:"latency_ms"`
	Errors         []string `json:"errors,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
	CommandCount   int      `json:"command_count"`
	HasDryRun      bool     `json:"has_dry_run"`
	ExitCodeHelp   int      `json:"exit_code_help"`
	ExitCodeSyntax int      `json:"exit_code_syntax"`
}
