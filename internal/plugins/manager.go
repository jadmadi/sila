package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var validNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Manager orchestrates Sila plugin discovery, compilation, installation, and scaffolding.
type Manager struct {
	BinDir  string
	SrcDir  string
	DataDir string
}

// NewManager creates a new Manager instance with resolved or custom directories.
func NewManager(binDir, srcDir string) *Manager {
	if binDir == "" {
		binDir = ResolveBinDir()
	}
	if srcDir == "" {
		srcDir = ResolveSrcDir()
	}
	return &Manager{
		BinDir:  binDir,
		SrcDir:  srcDir,
		DataDir: "",
	}
}

// List discovers all installed, linked, and available plugins.
func (m *Manager) List(ctx context.Context) ([]PluginItem, error) {
	installedMap, err := ScanInstalled(m.BinDir)
	if err != nil {
		return nil, fmt.Errorf("failed to scan bin directory: %w", err)
	}

	sourceNames, _ := ScanSourcePlugins(m.SrcDir)
	catalogDescs := ReadCatalogDescriptions(m.SrcDir)
	linkedRegistry, _ := LoadLinks(m.DataDir)

	itemMap := make(map[string]*PluginItem)

	// 1. Process source plugins first as Available
	for _, name := range sourceNames {
		srcPath := ""
		if m.SrcDir != "" {
			srcPath = filepath.Join(m.SrcDir, "cmd", name)
		}
		desc := catalogDescs[name]
		if desc == "" {
			desc = "Source plugin in sila-plugins"
		}
		itemMap[name] = &PluginItem{
			Name:       name,
			Status:     StatusAvailable,
			Short:      desc,
			SourcePath: srcPath,
		}
	}

	// 2. Overlay or add installed / linked binaries that are relevant Sila plugins
	for name, bin := range installedMap {
		_, isKnownSource := itemMap[name]
		isSilaPrefixed := strings.HasPrefix(name, "sila-")
		linkEntry, isTrackedLink := linkedRegistry[name]
		isRelevant := isKnownSource || isSilaPrefixed || isTrackedLink

		if !isRelevant {
			continue
		}

		status := StatusInstalled
		if bin.IsSymlink {
			status = StatusLinked
		}

		item, exists := itemMap[name]
		if !exists {
			item = &PluginItem{
				Name: name,
			}
			itemMap[name] = item
		}

		item.Status = status
		item.BinaryPath = bin.Path
		if isTrackedLink && linkEntry.Target != "" {
			item.TargetLink = linkEntry.Target
		} else {
			item.TargetLink = bin.TargetLink
		}

		// Query metadata live from the binary
		if meta, err := QueryPluginMetadata(ctx, bin.Path); err == nil && meta != nil {
			if meta.Version != "" {
				item.Version = meta.Version
			}
			if meta.Short != "" {
				item.Short = meta.Short
			}
			if len(meta.Capabilities) > 0 {
				item.Capabilities = meta.Capabilities
			}
			if len(meta.Commands) > 0 {
				item.Commands = meta.Commands
			}
		} else if item.Short == "" {
			if desc := catalogDescs[name]; desc != "" {
				item.Short = desc
			} else {
				item.Short = "Installed binary in " + m.BinDir
			}
		}
	}

	res := make([]PluginItem, 0, len(itemMap))
	for _, item := range itemMap {
		res = append(res, *item)
	}

	sort.Slice(res, func(i, j int) bool {
		return res[i].Name < res[j].Name
	})

	return res, nil
}

// Build compiles a single plugin into the specified output path.
func (m *Manager) Build(ctx context.Context, name string, outPath string) error {
	if m.SrcDir == "" {
		return errors.New("sila-plugins source directory not found; cannot compile from source")
	}

	cmdDir := filepath.Join(m.SrcDir, "cmd", name)
	if fi, err := os.Stat(cmdDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("plugin source directory %s does not exist", cmdDir)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-o", outPath, ".")
	cmd.Dir = cmdDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compilation failed for %s: %w\n%s", name, err, string(out))
	}

	return nil
}

// Install compiles and installs one or all plugins into BinDir.
func (m *Manager) Install(ctx context.Context, name string) ([]string, error) {
	if err := os.MkdirAll(m.BinDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create bin directory %s: %w", m.BinDir, err)
	}

	var targets []string
	if name == "" || name == "all" {
		sources, err := ScanSourcePlugins(m.SrcDir)
		if err != nil {
			return nil, fmt.Errorf("failed to scan source plugins: %w", err)
		}
		if len(sources) == 0 {
			return nil, errors.New("no source plugins found in sila-plugins/cmd")
		}
		targets = sources
	} else {
		targets = []string{name}
	}

	var installed []string
	for _, target := range targets {
		dest := filepath.Join(m.BinDir, target)
		if err := m.Build(ctx, target, dest); err != nil {
			return installed, err
		}
		installed = append(installed, target)
	}

	return installed, nil
}

// Uninstall deletes a plugin binary or link from BinDir.
func (m *Manager) Uninstall(name string) error {
	target := filepath.Join(m.BinDir, name)
	fi, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plugin %q is not installed in %s", name, m.BinDir)
		}
		return err
	}

	if fi.IsDir() {
		return fmt.Errorf("%s is a directory, not a plugin binary", target)
	}

	if err := os.Remove(target); err != nil {
		return fmt.Errorf("failed to remove plugin %s: %w", name, err)
	}

	return nil
}

// Link creates a symbolic link in BinDir pointing to targetPath.
func (m *Manager) Link(targetPath string) (string, error) {
	abs, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("invalid path %s: %w", targetPath, err)
	}

	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("target file does not exist: %s", abs)
	}

	if fi.IsDir() {
		return "", fmt.Errorf("target %s is a directory; link must point to an executable file", abs)
	}

	if fi.Mode()&0111 == 0 {
		return "", fmt.Errorf("target %s is not executable", abs)
	}

	name := filepath.Base(abs)
	dest := filepath.Join(m.BinDir, name)

	if err := os.MkdirAll(m.BinDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create bin directory: %w", err)
	}

	// Remove existing binary or symlink if present
	if _, err := os.Lstat(dest); err == nil {
		_ = os.Remove(dest)
	}

	if err := os.Symlink(abs, dest); err != nil {
		return "", fmt.Errorf("failed to create symlink at %s: %w", dest, err)
	}

	_ = RecordLink(m.DataDir, name, abs)
	return name, nil
}

// Unlink removes a symlink from BinDir.
func (m *Manager) Unlink(name string) error {
	dest := filepath.Join(m.BinDir, name)
	fi, err := os.Lstat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("plugin %q is not found in %s", name, m.BinDir)
		}
		return err
	}

	if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("plugin %q is a regular file, not a symlink; use 'sila plugins uninstall %s' instead", name, name)
	}

	if err := os.Remove(dest); err != nil {
		return fmt.Errorf("failed to remove symlink %s: %w", dest, err)
	}

	_ = RemoveLink(m.DataDir, name)
	return nil
}

// Init creates a new plugin package scaffold under sila-plugins/cmd/<name>/.
func (m *Manager) Init(name, author, license string) (string, error) {
	if !validNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid plugin name %q: must be lowercase alphanumeric with hyphens (e.g. my-tool)", name)
	}

	var baseDir string
	if m.SrcDir != "" {
		baseDir = filepath.Join(m.SrcDir, "cmd", name)
	} else {
		baseDir = filepath.Join("cmd", name)
	}

	if _, err := os.Stat(baseDir); err == nil {
		return "", fmt.Errorf("directory %s already exists", baseDir)
	}

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create plugin directory %s: %w", baseDir, err)
	}

	if author == "" {
		author = "Sila Contributor"
	}
	if license == "" {
		license = "MIT"
	}

	cleanIdent := strings.ReplaceAll(name, "-", "")

	// 1. Generate main.go
	mainContent := fmt.Sprintf(`package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type FlagDoc struct {
	Name     string `+"`json:\"name\"`"+`
	Type     string `+"`json:\"type\"`"+`
	Default  string `+"`json:\"default\"`"+`
	Usage    string `+"`json:\"usage\"`"+`
	Required bool   `+"`json:\"required,omitempty\"`"+`
}

type CommandInfo struct {
	Name    string    `+"`json:\"name\"`"+`
	Short   string    `+"`json:\"short\"`"+`
	Long    string    `+"`json:\"long\"`"+`
	Example string    `+"`json:\"example\"`"+`
	Flags   []FlagDoc `+"`json:\"flags,omitempty\"`"+`
}

type PluginMetadata struct {
	SchemaVersion string        `+"`json:\"schema_version\"`"+`
	Name          string        `+"`json:\"name\"`"+`
	Version       string        `+"`json:\"version\"`"+`
	Short         string        `+"`json:\"short\"`"+`
	Long          string        `+"`json:\"long\"`"+`
	Author        string        `+"`json:\"author,omitempty\"`"+`
	License       string        `+"`json:\"license,omitempty\"`"+`
	Repository    string        `+"`json:\"repository,omitempty\"`"+`
	Category      string        `+"`json:\"category,omitempty\"`"+`
	Capabilities  []string      `+"`json:\"capabilities,omitempty\"`"+`
	Commands      []CommandInfo `+"`json:\"commands\"`"+`
}

func emitJSONHelp() {
	meta := PluginMetadata{
		SchemaVersion: "1.0.0",
		Name:          "%s",
		Version:       "0.1.0",
		Short:         "%s developer tool",
		Long:          "%s is a standalone Go tool built for the Sila ecosystem.",
		Author:        "%s",
		License:       "%s",
		Category:      "tools",
		Capabilities:  []string{"cli", "mcp"},
		Commands: []CommandInfo{
			{
				Name:    "run",
				Short:   "execute primary command",
				Long:    "Executes the primary operation of %s.",
				Example: "%s run --dry-run",
				Flags: []FlagDoc{
					{
						Name:    "dry-run",
						Type:    "bool",
						Default: "false",
						Usage:   "preview changes without writing to disk",
					},
				},
			},
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(meta)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %%s <command> [flags]\n\n", "%s")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  run          Execute primary operation\n")
	fmt.Fprintf(os.Stderr, "  help         Show usage help (or help --json)\n")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "help", "--help", "-h":
		if len(os.Args) > 2 && os.Args[2] == "--json" {
			emitJSONHelp()
			return
		}
		printUsage()
		return
	case "run":
		fs := flag.NewFlagSet("run", flag.ContinueOnError)
		dryRun := fs.Bool("dry-run", false, "preview changes without writing")
		if err := fs.Parse(os.Args[2:]); err != nil {
			os.Exit(2)
		}
		if err := run%s(*dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "error: %%v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %%s\n", cmd)
		printUsage()
		os.Exit(2)
	}
}
`, name, name, name, author, license, name, name, name, strings.Title(cleanIdent))

	if err := os.WriteFile(filepath.Join(baseDir, "main.go"), []byte(mainContent), 0644); err != nil {
		return "", err
	}

	// 2. Generate logic file
	logicContent := fmt.Sprintf(`package main

import (
	"fmt"
	"os"
)

// run%s performs the core operation.
func run%s(dryRun bool) error {
	if dryRun {
		fmt.Fprintln(os.Stderr, "[dry-run] %s operation previewed cleanly")
		return nil
	}
	fmt.Println("%s executed successfully")
	return nil
}
`, strings.Title(cleanIdent), strings.Title(cleanIdent), name, name)

	if err := os.WriteFile(filepath.Join(baseDir, cleanIdent+".go"), []byte(logicContent), 0644); err != nil {
		return "", err
	}

	// 3. Generate unit tests
	testContent := fmt.Sprintf(`package main

import (
	"testing"
)

func TestRun%sDryRun(t *testing.T) {
	if err := run%s(true); err != nil {
		t.Fatalf("expected nil error in dry-run, got: %%v", err)
	}
}

func TestRun%sLive(t *testing.T) {
	if err := run%s(false); err != nil {
		t.Fatalf("expected nil error in live run, got: %%v", err)
	}
}
`, strings.Title(cleanIdent), strings.Title(cleanIdent), strings.Title(cleanIdent), strings.Title(cleanIdent))

	if err := os.WriteFile(filepath.Join(baseDir, cleanIdent+"_test.go"), []byte(testContent), 0644); err != nil {
		return "", err
	}

	// 4. Generate README.md
	readmeContent := fmt.Sprintf(`# %s

%s developer tool for the Sila ecosystem.

## Usage

`+"```bash"+`
# Run with preview:
%s run --dry-run

# Run primary operation:
%s run

# Machine-readable schema:
%s help --json
`+"```"+`
`, name, name, name, name, name)

	if err := os.WriteFile(filepath.Join(baseDir, "README.md"), []byte(readmeContent), 0644); err != nil {
		return "", err
	}

	// 5. If tools-list.md exists, append entry
	if m.SrcDir != "" {
		toolsListPath := filepath.Join(m.SrcDir, "tools-list.md")
		if content, err := os.ReadFile(toolsListPath); err == nil {
			entry := fmt.Sprintf("- `%s`: %s developer tool. usage: `%s run [flags]`.\n", name, name, name)
			if !strings.Contains(string(content), "`"+name+"`") {
				_ = os.WriteFile(toolsListPath, append(content, []byte(entry)...), 0644)
			}
		}
	}

	return baseDir, nil
}

// Lint audits a plugin binary against the Sila Plugin Standard.
func (m *Manager) Lint(ctx context.Context, target string) (*LintReport, error) {
	binPath := target
	if !filepath.IsAbs(binPath) && !strings.Contains(binPath, string(filepath.Separator)) {
		binPath = filepath.Join(m.BinDir, target)
	}

	fi, err := os.Stat(binPath)
	if err != nil {
		return nil, fmt.Errorf("plugin binary %s not found: %w", binPath, err)
	}

	report := &LintReport{
		Name:       filepath.Base(binPath),
		BinaryPath: binPath,
		Clean:      true,
	}

	if fi.Mode()&0111 == 0 {
		report.Clean = false
		report.Errors = append(report.Errors, "binary file is not marked executable")
	}

	// 1. Verify --help
	helpCmd := exec.CommandContext(ctx, binPath, "--help")
	_ = helpCmd.Run()
	report.ExitCodeHelp = helpCmd.ProcessState.ExitCode()
	if report.ExitCodeHelp != 0 && report.ExitCodeHelp != 2 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("--help exited with code %d instead of 0", report.ExitCodeHelp))
	}

	// 2. Verify invalid flag exit code 2
	syntaxCmd := exec.CommandContext(ctx, binPath, "--nonexistent-flag-test-probe")
	_ = syntaxCmd.Run()
	report.ExitCodeSyntax = syntaxCmd.ProcessState.ExitCode()
	if report.ExitCodeSyntax != 2 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("invalid flag exited with code %d instead of standard code 2", report.ExitCodeSyntax))
	}

	// 3. Verify help --json and latency
	start := time.Now()
	jsonCmd := exec.CommandContext(ctx, binPath, "help", "--json")
	out, err := jsonCmd.Output()
	report.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		report.Clean = false
		report.Errors = append(report.Errors, fmt.Sprintf("help --json command failed: %v", err))
		return report, nil
	}

	if report.LatencyMs > 25 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("startup latency (%dms) exceeds 25ms target", report.LatencyMs))
	}

	var meta PluginMetadata
	if err := json.Unmarshal(out, &meta); err != nil {
		report.Clean = false
		report.Errors = append(report.Errors, fmt.Sprintf("help --json output is not valid JSON: %v", err))
		return report, nil
	}

	report.SchemaVersion = meta.SchemaVersion
	if meta.SchemaVersion == "" {
		report.Warnings = append(report.Warnings, "missing schema_version field in JSON help")
	}
	if meta.Name == "" {
		report.Clean = false
		report.Errors = append(report.Errors, "missing required field 'name' in JSON help")
	}
	if meta.Short == "" {
		report.Warnings = append(report.Warnings, "missing required field 'short' description in JSON help")
	}

	report.CommandCount = len(meta.Commands)
	for _, cmd := range meta.Commands {
		for _, f := range cmd.Flags {
			if f.Name == "dry-run" {
				report.HasDryRun = true
			}
		}
	}

	if len(report.Errors) > 0 {
		report.Clean = false
	}

	return report, nil
}
