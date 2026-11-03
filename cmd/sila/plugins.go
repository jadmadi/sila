package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/plugins"
	"github.com/jadmadi/sila/internal/render"
)

func runPlugins(ui render.UI, jsonOut, toonOut bool, args []string) {
	mgr := plugins.NewManager("", "")

	if len(args) == 0 {
		runPluginsList(ui, mgr, jsonOut, toonOut)
		return
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "list", "ls":
		runPluginsList(ui, mgr, jsonOut, toonOut)
	case "init", "new", "create":
		runPluginsInit(ui, mgr, rest)
	case "install", "add":
		runPluginsInstall(ui, mgr, jsonOut, rest)
	case "uninstall", "remove", "rm":
		runPluginsUninstall(ui, mgr, jsonOut, rest)
	case "link":
		runPluginsLink(ui, mgr, jsonOut, rest)
	case "unlink":
		runPluginsUnlink(ui, mgr, jsonOut, rest)
	case "activate", "enable":
		runPluginsActivate(ui, jsonOut, rest)
	case "deactivate", "disable":
		runPluginsDeactivate(ui, jsonOut, rest)
	case "lint", "audit", "verify":
		runPluginsLint(ui, mgr, jsonOut, toonOut, rest)
	case "help", "--help", "-h":
		printPluginsUsage(ui)
	default:
		// If first argument matches an installed plugin name, show its info
		items, err := mgr.List(context.Background())
		if err == nil {
			for _, item := range items {
				if item.Name == sub {
					if jsonOut {
						emitJSON(item)
					} else {
						printPluginDetail(ui, item)
					}
					return
				}
			}
		}
		fmt.Fprintf(os.Stderr, "sila plugins: unknown command %q\n\n", sub)
		printPluginsUsage(ui)
		os.Exit(2)
	}
}

func printPluginsUsage(ui render.UI) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila plugins"))+" — manage standalone Go plugins and tools")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Usage:"))
	fmt.Fprintln(os.Stdout, "  sila plugins <command> [arguments...]")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Commands:"))
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("list"), "List all installed, linked, and available plugins")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("init <name>"), "Scaffold a new plugin in sila-plugins/cmd/<name>")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("install [name]"), "Compile and install plugin(s) to ~/.local/bin")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("uninstall <name>"), "Remove an installed plugin binary")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("link <path>"), "Symlink a local executable into ~/.local/bin")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("unlink <name>"), "Remove a plugin symlink from ~/.local/bin")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("activate <name>"), "Enable a plugin for MCP exposure in current project")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("deactivate <name>"), "Disable a plugin from MCP exposure in current project")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", ui.Bold("lint <name|path>"), "Audit a plugin binary against Sila Plugin Standards")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Options:"))
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", "--json", "Emit machine-readable JSON output")
	fmt.Fprintf(os.Stdout, "  %-22s %s\n", "--toon", "Emit Token-Oriented Object Notation (TOON)")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Examples:"))
	fmt.Fprintln(os.Stdout, "  sila plugins list")
	fmt.Fprintln(os.Stdout, "  sila plugins init cfclean")
	fmt.Fprintln(os.Stdout, "  sila plugins install cfclean")
	fmt.Fprintln(os.Stdout, "  sila plugins link ./bin/my-tool")
	fmt.Fprintln(os.Stdout, "  sila plugins lint cfclean")
}

func runPluginsList(ui render.UI, mgr *plugins.Manager, jsonOut, toonOut bool) {
	ctx := context.Background()
	items, err := mgr.List(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins: %v\n", err)
		os.Exit(1)
	}

	cwd, _ := os.Getwd()
	for i := range items {
		items[i].Active = plugins.IsPluginActive(cwd, items[i].Name)
	}

	if jsonOut {
		emitJSON(items)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "plugins[%d]:\n", len(items))
		for _, item := range items {
			fmt.Fprintf(os.Stdout, "  - name=%s status=%s active=%t version=%s short=%q\n",
				item.Name, item.Status, item.Active, item.Version, item.Short)
		}
		return
	}

	if len(items) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("No plugins found in ~/.local/bin or sila-plugins."))
		return
	}

	fmt.Fprintln(os.Stdout, ui.Bold("Sila Plugins:"))
	fmt.Fprintln(os.Stdout)

	for _, item := range items {
		statusBadge := ""
		switch item.Status {
		case plugins.StatusInstalled:
			statusBadge = ui.Green("installed")
		case plugins.StatusLinked:
			statusBadge = ui.Cyan("linked -> " + filepath.Base(item.TargetLink))
		case plugins.StatusAvailable:
			statusBadge = ui.Yellow("available")
		default:
			statusBadge = ui.Dim(string(item.Status))
		}

		activeBadge := ""
		if item.Active {
			activeBadge = ui.Green("[mcp active]")
		}

		ver := ""
		if item.Version != "" {
			ver = ui.Dim("v" + item.Version)
		}

		caps := ""
		if len(item.Capabilities) > 0 {
			caps = ui.Dim("[" + strings.Join(item.Capabilities, ", ") + "]")
		}

		header := fmt.Sprintf("  • %-16s %-12s %-12s %-8s %s", ui.Bold(item.Name), statusBadge, activeBadge, ver, caps)
		fmt.Fprintln(os.Stdout, strings.TrimRight(header, " "))

		if item.Short != "" {
			fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(item.Short))
		}
	}
	fmt.Fprintln(os.Stdout)
}

func printPluginDetail(ui render.UI, item plugins.PluginItem) {
	fmt.Fprintf(os.Stdout, "%s — %s\n\n", ui.Bold(item.Name), item.Short)
	fmt.Fprintf(os.Stdout, "Status:       %s\n", item.Status)
	if item.Version != "" {
		fmt.Fprintf(os.Stdout, "Version:      %s\n", item.Version)
	}
	if item.BinaryPath != "" {
		fmt.Fprintf(os.Stdout, "Binary:       %s\n", item.BinaryPath)
	}
	if item.TargetLink != "" {
		fmt.Fprintf(os.Stdout, "Target:       %s\n", item.TargetLink)
	}
	if item.SourcePath != "" {
		fmt.Fprintf(os.Stdout, "Source:       %s\n", item.SourcePath)
	}
	if len(item.Capabilities) > 0 {
		fmt.Fprintf(os.Stdout, "Capabilities: %s\n", strings.Join(item.Capabilities, ", "))
	}
	if len(item.Commands) > 0 {
		fmt.Fprintln(os.Stdout, "\nCommands:")
		for _, cmd := range item.Commands {
			fmt.Fprintf(os.Stdout, "  • %-14s %s\n", ui.Bold(cmd.Name), cmd.Short)
			if cmd.Example != "" {
				lines := strings.Split(cmd.Example, "\n")
				for _, l := range lines {
					fmt.Fprintf(os.Stdout, "      %s\n", ui.Dim(l))
				}
			}
		}
	}
}

func runPluginsInit(ui render.UI, mgr *plugins.Manager, args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	author := fs.String("author", "Sila Contributor", "plugin author name or GitHub handle")
	license := fs.String("license", "MIT", "plugin license")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "sila plugins init: missing plugin name")
		fmt.Fprintln(os.Stderr, "Usage: sila plugins init <name> [--author=...] [--license=...]")
		os.Exit(2)
	}

	name := rest[0]
	dir, err := mgr.Init(name, *author, *license)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins init: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stdout, ui.Green("✔ Plugin scaffolded successfully:"))
	fmt.Fprintf(os.Stdout, "  Location: %s\n\n", dir)
	fmt.Fprintln(os.Stdout, ui.Bold("Next steps:"))
	fmt.Fprintf(os.Stdout, "  1. Test:    go test -race %s/...\n", dir)
	fmt.Fprintf(os.Stdout, "  2. Build:   sila plugins install %s\n", name)
	fmt.Fprintf(os.Stdout, "  3. Audit:   sila plugins lint %s\n", name)
}

func runPluginsInstall(ui render.UI, mgr *plugins.Manager, jsonOut bool, args []string) {
	name := ""
	if len(args) > 0 {
		name = args[0]
	}

	ctx := context.Background()
	installed, err := mgr.Install(ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins install: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"installed": installed,
			"bin_dir":   mgr.BinDir,
		})
		return
	}

	for _, p := range installed {
		fmt.Fprintf(os.Stdout, "%s Installed %s into %s\n",
			ui.Green("✔"), ui.Bold(p), filepath.Join(mgr.BinDir, p))
	}
}

func runPluginsUninstall(ui render.UI, mgr *plugins.Manager, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila plugins uninstall: missing plugin name")
		os.Exit(2)
	}

	name := args[0]
	if err := mgr.Uninstall(name); err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins uninstall: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"uninstalled": name,
			"bin_dir":     mgr.BinDir,
		})
		return
	}

	fmt.Fprintf(os.Stdout, "%s Uninstalled %s from %s\n",
		ui.Green("✔"), ui.Bold(name), mgr.BinDir)
}

func runPluginsLink(ui render.UI, mgr *plugins.Manager, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila plugins link: missing path to executable")
		os.Exit(2)
	}

	path := args[0]
	name, err := mgr.Link(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins link: %v\n", err)
		os.Exit(1)
	}

	dest := filepath.Join(mgr.BinDir, name)
	if jsonOut {
		emitJSON(map[string]any{
			"linked":  name,
			"target":  path,
			"symlink": dest,
		})
		return
	}

	fmt.Fprintf(os.Stdout, "%s Linked %s -> %s\n",
		ui.Green("✔"), ui.Bold(dest), path)
}

func runPluginsUnlink(ui render.UI, mgr *plugins.Manager, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila plugins unlink: missing plugin name")
		os.Exit(2)
	}

	name := args[0]
	if err := mgr.Unlink(name); err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins unlink: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"unlinked": name,
			"bin_dir":  mgr.BinDir,
		})
		return
	}

	fmt.Fprintf(os.Stdout, "%s Unlinked %s from %s\n",
		ui.Green("✔"), ui.Bold(name), mgr.BinDir)
}

func runPluginsLint(ui render.UI, mgr *plugins.Manager, jsonOut, toonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila plugins lint: missing plugin name or binary path")
		os.Exit(2)
	}

	target := args[0]
	ctx := context.Background()
	report, err := mgr.Lint(ctx, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins lint: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "plugin_lint[name=%s clean=%v latency=%dms commands=%d]:\n",
			report.Name, report.Clean, report.LatencyMs, report.CommandCount)
		for _, e := range report.Errors {
			fmt.Fprintf(os.Stdout, "  error: %s\n", e)
		}
		for _, w := range report.Warnings {
			fmt.Fprintf(os.Stdout, "  warning: %s\n", w)
		}
		return
	}

	fmt.Fprintf(os.Stdout, "Auditing plugin %s (%s)...\n\n", ui.Bold(report.Name), report.BinaryPath)
	fmt.Fprintf(os.Stdout, "  Startup Latency:    %dms\n", report.LatencyMs)
	fmt.Fprintf(os.Stdout, "  Schema Version:     %s\n", report.SchemaVersion)
	fmt.Fprintf(os.Stdout, "  Commands Defined:   %d\n", report.CommandCount)
	fmt.Fprintf(os.Stdout, "  Dry-run Supported:  %v\n", report.HasDryRun)
	fmt.Fprintf(os.Stdout, "  Help Exit Code:     %d\n", report.ExitCodeHelp)
	fmt.Fprintf(os.Stdout, "  Syntax Exit Code:   %d\n", report.ExitCodeSyntax)
	fmt.Fprintln(os.Stdout)

	if len(report.Errors) > 0 {
		fmt.Fprintln(os.Stdout, ui.Red("Errors:"))
		for _, e := range report.Errors {
			fmt.Fprintf(os.Stdout, "  ✘ %s\n", e)
		}
		fmt.Fprintln(os.Stdout)
	}

	if len(report.Warnings) > 0 {
		fmt.Fprintln(os.Stdout, ui.Yellow("Warnings:"))
		for _, w := range report.Warnings {
			fmt.Fprintf(os.Stdout, "  ⚠ %s\n", w)
		}
		fmt.Fprintln(os.Stdout)
	}

	if report.Clean {
		fmt.Fprintln(os.Stdout, ui.Green("✔ Plugin conforms cleanly to Sila Plugin Standard Specification v1.0.0"))
	} else {
		fmt.Fprintln(os.Stdout, ui.Red("✘ Plugin failed verification checks."))
		os.Exit(1)
	}
}

func runPluginsActivate(ui render.UI, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila plugins activate: missing plugin name")
		fmt.Fprintln(os.Stderr, "Usage: sila plugins activate <name>")
		os.Exit(2)
	}
	name := args[0]
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if err := plugins.ActivatePlugin(cwd, name); err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins activate: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{
			"plugin":  name,
			"active":  true,
			"project": cwd,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "✔ Plugin %s activated for project (%s)\n", ui.Bold(name), cwd)
	fmt.Fprintf(os.Stdout, "  Exposed dynamically over Sila MCP as plugin_%s_*\n", name)
}

func runPluginsDeactivate(ui render.UI, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila plugins deactivate: missing plugin name")
		fmt.Fprintln(os.Stderr, "Usage: sila plugins deactivate <name>")
		os.Exit(2)
	}
	name := args[0]
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	if err := plugins.DeactivatePlugin(cwd, name); err != nil {
		fmt.Fprintf(os.Stderr, "sila plugins deactivate: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{
			"plugin":  name,
			"active":  false,
			"project": cwd,
		})
		return
	}
	fmt.Fprintf(os.Stdout, "✔ Plugin %s deactivated for project (%s)\n", ui.Bold(name), cwd)
}
