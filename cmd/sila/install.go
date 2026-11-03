package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/harness/registry"
	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/onboard"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// InstallResult details all provisions executed during `sila install`.
type InstallResult struct {
	RepoRoot      string                `json:"repo_root"`
	Project       string                `json:"project"`
	RulesUpdated  []string              `json:"rules_updated"`
	Hooks         []string              `json:"hooks_installed"`
	MCPConfigs    []string              `json:"mcp_configs"`
	SkillsUpdated []string              `json:"skills_updated"`
	WorkspaceShim string                `json:"workspace_shim,omitempty"`
	Notes         []string              `json:"notes,omitempty"`
	Errors        []string              `json:"errors,omitempty"`
	PreviewPlan   []agents.PlannedWrite `json:"preview_plan,omitempty"`
}

func runInstall(ui render.UI, project, toolsFlag, policyPack string, noHooks, noMCP, withShim, dryRun, replace, jsonOut bool) {
	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}

	res := &InstallResult{
		RepoRoot:      cwd,
		Project:       project,
		RulesUpdated:  []string{},
		Hooks:         []string{},
		MCPConfigs:    []string{},
		SkillsUpdated: []string{},
		Errors:        []string{},
	}

	tools := []string{"all"}
	if toolsFlag != "" && toolsFlag != "all" {
		tools = strings.Split(toolsFlag, ",")
	}

	home, _ := os.UserHomeDir()
	ccDir := filepath.Join(home, ".commandcode")
	ccDetected := false
	if fi, err := os.Stat(ccDir); err == nil && fi.IsDir() {
		ccDetected = true
	}
	ccExplicit := false
	cursorExplicit := false
	var requestedHarnesses []string
	deployTools := make([]string, 0, len(tools)+2)
	for _, t := range tools {
		trimmed := strings.ToLower(strings.TrimSpace(t))
		switch trimmed {
		case "command-code", "commandcode", "command_code":
			ccExplicit = true
		case "cursor":
			cursorExplicit = true
			requestedHarnesses = append(requestedHarnesses, "cursor")
			deployTools = append(deployTools, "cursor")
		case "all", "":
			deployTools = append(deployTools, t)
		default:
			if sp, ok := registry.Lookup(trimmed); ok {
				requestedHarnesses = append(requestedHarnesses, sp.ID)
				switch sp.ID {
				case "claude", "cursor", "devin":
					if sp.ID == "cursor" {
						cursorExplicit = true
					}
					deployTools = append(deployTools, sp.ID)
				default:
					deployTools = append(deployTools, "agents")
				}
			} else {
				deployTools = append(deployTools, t)
			}
		}
	}
	if ccExplicit && !ccDetected {
		msg := fmt.Sprintf("command-code target requires the Command Code config directory at %s; install Command Code first", ccDir)
		res.Errors = append(res.Errors, msg)
		if jsonOut {
			emitJSON(res)
		} else {
			fmt.Fprintln(os.Stderr, "sila install: "+msg)
		}
		os.Exit(1)
	}
	ccRequested := ccExplicit || (ccDetected && (toolsFlag == "" || toolsFlag == "all"))
	ccProjectHooks := shouldProvisionProjectCommandCodeHooks(cwd, ccDetected, ccExplicit, toolsFlag)
	cursorMCP := !noMCP && shouldProvisionCursorMCP(cwd, cursorExplicit, toolsFlag)
	if ccRequested {
		deployTools = append(deployTools, "agents")
	}
	if cursorMCP && !cursorExplicit {
		deployTools = append(deployTools, "cursor")
	}

	// Rules deployment: preview and execution derive from the same plan.
	opts := agents.CompileOptions{
		RepoRoot:    cwd,
		ProjectName: project,
		Replace:     replace,
		PolicyPack:  policyPack,
	}
	if dryRun {
		opts.Preview = true
		previewRes, err := agents.DeployTargets(cwd, opts, deployTools)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("preview targets: %v", err))
		} else {
			res.PreviewPlan = previewRes.Plan
		}
		// The preview plan must enumerate everything execution can write, not
		// only the rule projections: hooks and MCP registrations use the same
		// conditions execution will check.
		if !noHooks && onboard.IsSilaSourceRepo(cwd) {
			// Only promise a hook the execution path will install: sila's Go
			// discipline hook belongs to the sila source repository only.
			if _, err := os.Stat(filepath.Join(cwd, ".git")); err == nil {
				action := "install pre-commit hook (pre-push via 'sila gate install --hook')"
				if _, err := os.Stat(filepath.Join(cwd, ".git", "hooks", "pre-commit")); err == nil {
					action = "update pre-commit hook (pre-push via 'sila gate install --hook')"
				}
				res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: ".git/hooks/pre-commit", Detail: action})
			}
		}
		if !noMCP {
			if cursorMCP {
				res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: ".cursor/mcp.json", Detail: "register sila stdio MCP server (merge-preserving)"})
			}
			if home, err := os.UserHomeDir(); err == nil {
				if _, err := os.Stat(filepath.Join(home, ".gemini", "config")); err == nil {
					res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: "~/.gemini/config/mcp_config.json", Detail: "register sila stdio MCP server (merge-preserving; this config dir exists)"})
				}
				geminiDir := filepath.Join(home, ".gemini")
				if _, err := os.Stat(geminiDir); err == nil {
					if _, err := os.Lstat(filepath.Join(geminiDir, "AGENTS.md")); os.IsNotExist(err) {
						res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: "~/.gemini/AGENTS.md", Detail: "provision global Antigravity rule directive pointing to ~/.agents/AGENTS.md"})
					}
				}
				zedDir := filepath.Join(home, ".config", "zed")
				if _, err := os.Stat(zedDir); err == nil {
					res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: "~/.config/zed/settings.json", Detail: "register sila stdio MCP server (merge-preserving; this config dir exists)"})
					if _, err := os.Lstat(filepath.Join(zedDir, "AGENTS.md")); os.IsNotExist(err) {
						res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: "~/.config/zed/AGENTS.md", Detail: "provision global Zed rule directive pointing to ~/.agents/AGENTS.md"})
					}
				}
			}
		}
		if ccRequested {
			if !noHooks && ccProjectHooks {
				hr, err := hookconfig.Install("command-code", cwd, true, replace)
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("command-code hooks preview: %v", err))
				} else {
					for _, entry := range hr.Plan {
						path := strings.TrimPrefix(entry, "write ")
						if rel, err := filepath.Rel(cwd, path); err == nil {
							path = rel
						}
						detail := "merge Sila lifecycle hooks (merge-preserving)"
						res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: path, Detail: detail})
					}
				}
			}
			if !noMCP {
				res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: "~/.commandcode/mcp.json", Detail: "register sila stdio MCP server (merge-preserving; Command Code detected)"})
			}
		}
		if !noHooks {
			for _, h := range requestedHarnesses {
				var hr hookconfig.Result
				var err error
				if h == "zcode" && home != "" {
					hr, err = hookconfig.InstallUser(h, home, true, replace)
				} else {
					hr, err = hookconfig.Install(h, cwd, true, replace)
				}
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s hooks preview: %v", h, err))
				} else if hr.Refused {
					res.Notes = append(res.Notes, fmt.Sprintf("%s: %s", h, hr.Detail))
				} else {
					appendHookPreview(res, hr, cwd, home)
				}
			}
		}
		if !noHooks && (toolsFlag == "" || strings.EqualFold(strings.TrimSpace(toolsFlag), "all")) {
			for _, h := range detectedUserHookHarnesses(home) {
				hr, err := hookconfig.InstallUser(h, home, true, replace)
				if err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s user hooks preview: %v", h, err))
					continue
				}
				if hr.Refused {
					res.Notes = append(res.Notes, fmt.Sprintf("%s: %s", h, hr.Detail))
					continue
				}
				appendHookPreview(res, hr, cwd, home)
			}
		}
		if withShim {
			res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: ".bin/sila", Detail: "provision standalone workspace runner for sandboxed AI agents"})
			if needs, _ := goals.NeedsGitIgnoreWorkspaceShim(cwd); needs {
				res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: ".gitignore", Detail: "add .bin/ to .gitignore for workspace runner"})
			}
		}
		res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: ".agents/skills/sila/SKILL.md", Detail: "provision the canonical Sila skill; also writes ~/.agents/skills/sila/SKILL.md and harness discovery symlinks"})
		if jsonOut {
			emitJSON(map[string]any{"dry_run": true, "repo_root": cwd, "project": project, "tools": tools, "plan": res.PreviewPlan, "notes": res.Notes, "errors": res.Errors})
		} else {
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · install (dry-run)"))+fmt.Sprintf(" (%s)", cwd))
			for _, p := range res.PreviewPlan {
				detail := p.Detail
				if detail != "" {
					detail = " — " + detail
				}
				fmt.Fprintf(os.Stdout, "  • %s%s (%d → %d bytes)\n", p.Path, detail, p.BeforeBytes, p.AfterBytes)
			}
			for _, e := range res.Errors {
				fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Red("✖"), e)
			}
		}
		return
	}

	syncRes, err := agents.DeployTargets(cwd, opts, deployTools)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("deploy targets: %v", err))
	} else {
		res.RulesUpdated = append(res.RulesUpdated, syncRes.FilesWritten...)
		res.RulesUpdated = append(res.RulesUpdated, syncRes.Symlinks...)
		res.Errors = append(res.Errors, syncRes.Errors...)
	}
	if skills, err := onboard.WriteSilaSkill(cwd); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("sila skill: %v", err))
	} else {
		res.SkillsUpdated = append(res.SkillsUpdated, skills...)
	}

	// 2. Install Git Hooks (pre-commit engineering discipline check). The hook
	// runs sila's Go discipline, so it belongs to the sila source repository
	// only, and sila never overwrites a hook it did not write.
	if !noHooks && onboard.IsSilaSourceRepo(cwd) {
		mgr, err := git.NewManager(cwd)
		switch {
		case err != nil:
			res.Errors = append(res.Errors, fmt.Sprintf("git hooks: %v", err))
		default:
			h, err := mgr.InstallPreCommitHook()
			switch {
			case err == nil:
				res.Hooks = append(res.Hooks, h)
			case errors.Is(err, git.ErrForeignHook):
				res.Notes = append(res.Notes, "existing pre-commit hook left in place; sila does not overwrite hooks it did not write")
			default:
				res.Errors = append(res.Errors, fmt.Sprintf("pre-commit: %v", err))
			}
		}
	} else if !noHooks {
		res.Notes = append(res.Notes, "project git hooks left untouched (sila's hook runs 'sila gate', which checks the sila source repository)")
	}

	// 3. Register MCP Configuration (Cursor, Antigravity)
	if !noMCP {
		// Cursor
		if cursorMCP {
			cursorMCPPath := filepath.Join(cwd, ".cursor", "mcp.json")
			if err := registerMCPServer(cursorMCPPath); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("cursor mcp: %v", err))
			} else {
				res.MCPConfigs = append(res.MCPConfigs, ".cursor/mcp.json")
			}
		}

		// Antigravity (AGY)
		if home, err := os.UserHomeDir(); err == nil {
			geminiDir := filepath.Join(home, ".gemini")
			agyConfigDir := filepath.Join(geminiDir, "config")
			if _, err := os.Stat(agyConfigDir); err == nil {
				agyMCP := filepath.Join(agyConfigDir, "mcp_config.json")
				if err := registerMCPServer(agyMCP); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("antigravity mcp: %v", err))
				} else {
					res.MCPConfigs = append(res.MCPConfigs, "~/.gemini/config/mcp_config.json")
				}
			}
			if _, err := os.Stat(geminiDir); err == nil {
				targetRule := filepath.Join(geminiDir, "AGENTS.md")
				if _, err := os.Lstat(targetRule); os.IsNotExist(err) {
					content := "# Global AI Agent Operating Rules\n\nAlways read and strictly adhere to the universal agent operating rules and user preferences located at:\n- `~/.agents/AGENTS.md`\n\n@[Universal Rules](~/.agents/AGENTS.md)\n"
					if err := os.WriteFile(targetRule, []byte(content), 0644); err == nil {
						res.RulesUpdated = append(res.RulesUpdated, "~/.gemini/AGENTS.md")
						_ = os.Symlink("AGENTS.md", filepath.Join(geminiDir, "GEMINI.md"))
					}
				}
			}
			zedDir := filepath.Join(home, ".config", "zed")
			if _, err := os.Stat(zedDir); err == nil {
				zedSettings := filepath.Join(zedDir, "settings.json")
				if err := onboard.RegisterZedMCPServer(zedSettings); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("zed mcp: %v", err))
				} else {
					res.MCPConfigs = append(res.MCPConfigs, "~/.config/zed/settings.json")
				}
				targetRule := filepath.Join(zedDir, "AGENTS.md")
				if _, err := os.Lstat(targetRule); os.IsNotExist(err) {
					content := "# Global AI Agent Operating Rules\n\nAlways read and strictly adhere to the universal agent operating rules and user preferences located at:\n- `~/.agents/AGENTS.md`\n"
					if err := os.WriteFile(targetRule, []byte(content), 0644); err == nil {
						res.RulesUpdated = append(res.RulesUpdated, "~/.config/zed/AGENTS.md")
					}
				}
			}
		}
	}

	// 3b. Command Code target: merge lifecycle hooks and the user MCP entry.
	if ccRequested {
		if !noHooks && ccProjectHooks {
			hr, err := hookconfig.Install("command-code", cwd, false, replace)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("command-code hooks: %v", err))
			} else if hr.Installed {
				res.Hooks = append(res.Hooks, hr.Path)
			} else if hr.Skipped {
				res.Notes = append(res.Notes, "command-code hooks already current")
			}
		}
		if !noMCP && home != "" {
			if err := onboard.RegisterCommandCodeMCPServer(filepath.Join(home, ".commandcode", "mcp.json")); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("command-code mcp: %v", err))
			} else {
				res.MCPConfigs = append(res.MCPConfigs, "~/.commandcode/mcp.json")
			}
		}
	}
	// 3c. User-scope lifecycle hooks for detected host harnesses. Default
	// installs must never create unrequested project-local hook directories in
	// clean repositories; detected host configs are provisioned under $HOME.
	if !noHooks && (toolsFlag == "" || strings.EqualFold(strings.TrimSpace(toolsFlag), "all")) {
		for _, h := range detectedUserHookHarnesses(home) {
			hr, err := hookconfig.InstallUser(h, home, false, replace)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s user hooks: %v", h, err))
				continue
			}
			if hr.Refused {
				res.Notes = append(res.Notes, fmt.Sprintf("%s: %s", h, hr.Detail))
				continue
			}
			if hr.Installed || hr.Configured {
				res.Hooks = append(res.Hooks, hr.Files...)
				if hr.Skipped {
					res.Notes = append(res.Notes, fmt.Sprintf("%s user hooks up to date (%s)", h, hr.Path))
				} else {
					res.Notes = append(res.Notes, fmt.Sprintf("%s user hooks installed (%s)", h, hr.Path))
				}
			}
		}
	}
	if !noHooks {
		for _, h := range requestedHarnesses {
			if h == "zcode" {
				if home != "" {
					hr, err := hookconfig.InstallUser(h, home, false, replace)
					if err != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("%s hooks: %v", h, err))
					} else if hr.Refused {
						res.Notes = append(res.Notes, fmt.Sprintf("%s: %s", h, hr.Detail))
					} else if hr.Installed || hr.Configured {
						res.Hooks = append(res.Hooks, hr.Path)
					} else if hr.Skipped {
						res.Notes = append(res.Notes, fmt.Sprintf("%s hooks already current", h))
					}
					continue
				}
			}
			hr, err := hookconfig.Install(h, cwd, false, replace)
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s hooks: %v", h, err))
			} else if hr.Refused {
				res.Notes = append(res.Notes, fmt.Sprintf("%s: %s", h, hr.Detail))
			} else if hr.Installed || hr.Configured {
				res.Hooks = append(res.Hooks, hr.Path)
				if hr.TrustRequired && !hr.Trusted {
					res.Notes = append(res.Notes, fmt.Sprintf("%s: %s", h, hr.Detail))
				}
			} else if hr.Skipped {
				res.Notes = append(res.Notes, fmt.Sprintf("%s hooks already current", h))
			}
		}
	}

	// 4. Provision workspace shim runner if requested
	if withShim {
		shimPath, err := provisionWorkspaceShim(cwd)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("workspace shim: %v", err))
		} else {
			res.WorkspaceShim = shimPath
		}
		if added, err := goals.EnsureGitIgnoreWorkspaceShim(cwd); err == nil && added {
			res.Notes = append(res.Notes, "added .bin/ to .gitignore for workspace runner")
		}
	}

	// 5. Ephemeral goal claim git hygiene
	if added, err := goals.EnsureGitIgnoreClaim(cwd); err == nil && added {
		res.Notes = append(res.Notes, "added goals/*/claim.json to .gitignore")
	}

	if jsonOut {
		emitJSON(res)
		if len(res.Errors) > 0 {
			os.Exit(1)
		}
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · install"))+fmt.Sprintf(" — 3-Tier Multi-Tool SOP Provisioning (%s)", project))

	fmt.Fprintf(os.Stdout, "\n  %s %s\n", ui.Bold("1. Cognitive Anchoring:"), ui.Dim("Turn 0 instructions"))
	for _, r := range res.RulesUpdated {
		fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Green("✔"), r)
	}

	if len(res.Hooks) > 0 {
		fmt.Fprintf(os.Stdout, "\n  %s %s\n", ui.Bold("2. Mechanical Enforcement:"), ui.Dim("Git execution gates"))
		for _, h := range res.Hooks {
			fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Green("✔"), h)
		}
	}

	if len(res.MCPConfigs) > 0 {
		fmt.Fprintf(os.Stdout, "\n  %s %s\n", ui.Bold("3. Runtime Tooling:"), ui.Dim("Stdio MCP servers"))
		for _, m := range res.MCPConfigs {
			fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Green("✔"), m)
		}
	}

	if res.WorkspaceShim != "" {
		fmt.Fprintf(os.Stdout, "\n  %s %s\n", ui.Bold("4. Sandbox Workspace Runner:"), ui.Dim("Container isolation bypass"))
		fmt.Fprintf(os.Stdout, "     %s %s\n", ui.Green("✔"), res.WorkspaceShim)
	}

	if len(res.Notes) > 0 {
		fmt.Fprintln(os.Stdout)
		for _, n := range res.Notes {
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Dim("•"), ui.Dim(n))
		}
	}

	if len(res.Errors) > 0 {
		fmt.Fprintf(os.Stdout, "\n  %s %s\n", ui.Red("✖"), ui.Bold("Errors during provisioning:"))
		for _, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "     %s %s\n", ui.Red("✖"), e)
		}
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "\n  %s All AI agent tools provisioned with Sila SOPs and mechanical quality gates.\n\n", ui.Green(ui.Bold("✔ COMPLETE:")))
}

// provisionWorkspaceShim copies the running sila binary into <cwd>/.bin/sila
// with mode 0755, providing a hermetic, standalone runner inside containerized
// agent sandboxes where ~/.local/bin is omitted from filesystem mounts.
func provisionWorkspaceShim(cwd string) (string, error) {
	binDir := filepath.Join(cwd, ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", fmt.Errorf("create .bin dir: %w", err)
	}

	targetPath := filepath.Join(binDir, "sila")

	// Determine source binary location.
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("determine executable path: %w", err)
	}
	// If execPath is a symlink, resolve it to copy the real executable.
	if realPath, err := filepath.EvalSymlinks(execPath); err == nil && realPath != "" {
		execPath = realPath
	}

	srcFile, err := os.Open(execPath)
	if err != nil {
		return "", fmt.Errorf("open source executable %s: %w", execPath, err)
	}
	defer srcFile.Close()

	srcStat, err := srcFile.Stat()
	if err != nil {
		return "", fmt.Errorf("stat source executable: %w", err)
	}

	// Avoid overwriting if source and target are the same file
	if tgtStat, err := os.Stat(targetPath); err == nil {
		if os.SameFile(srcStat, tgtStat) {
			_ = os.Chmod(targetPath, 0o755)
			return ".bin/sila", nil
		}
	}

	// Write to temporary file first, then atomically rename
	tmpPath := filepath.Join(binDir, fmt.Sprintf(".sila-shim-%d.tmp", os.Getpid()))
	dstFile, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return "", fmt.Errorf("create temp shim %s: %w", tmpPath, err)
	}

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("copy binary to %s: %w", tmpPath, err)
	}
	if err := dstFile.Sync(); err != nil {
		dstFile.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sync temp shim: %w", err)
	}
	if err := dstFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("close temp shim: %w", err)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("chmod temp shim: %w", err)
	}

	// Rename temp file to target
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(targetPath)
		if err2 := os.Rename(tmpPath, targetPath); err2 != nil {
			_ = os.Remove(tmpPath)
			return "", fmt.Errorf("replace %s: %w", targetPath, err)
		}
	}

	return ".bin/sila", nil
}

func registerMCPServer(path string) error {
	return onboard.RegisterMCPServer(path)
}

func shouldProvisionCursorMCP(cwd string, cursorExplicit bool, toolsFlag string) bool {
	if cursorExplicit {
		return true
	}
	if toolsFlag == "" || toolsFlag == "all" {
		return true
	}
	return agents.HasCursorIntegration(cwd)
}

func shouldProvisionProjectCommandCodeHooks(cwd string, ccDetected, ccExplicit bool, toolsFlag string) bool {
	if !ccDetected {
		return false
	}
	if ccExplicit {
		return true
	}
	if toolsFlag != "" && toolsFlag != "all" {
		return false
	}
	fi, err := os.Stat(filepath.Join(cwd, ".commandcode"))
	return err == nil && fi.IsDir()
}

// detectedUserHookHarnesses returns harness IDs whose host user config
// directory already exists. Trust-required harnesses (codex, codewhale, muse)
// are intentionally excluded so default installs never write untrusted hooks.
func detectedUserHookHarnesses(homeDir string) []string {
	if strings.TrimSpace(homeDir) == "" {
		return nil
	}
	candidates := []struct {
		harness string
		hostDir string
	}{
		{"claude", ".claude"},
		{"cursor", ".cursor"},
		{"antigravity", ".gemini/config"},
		{"command-code", ".commandcode"},
		{"zcode", ".zcode/cli"},
		{"opencode", ".config/opencode/plugins"},
	}
	var out []string
	for _, c := range candidates {
		fi, err := os.Stat(filepath.Join(homeDir, filepath.FromSlash(c.hostDir)))
		if err == nil && fi.IsDir() {
			out = append(out, c.harness)
		}
	}
	return out
}

// appendHookPreview records the planned writes for one hook install, showing
// user-scope paths relative to $HOME and project paths relative to cwd.
func appendHookPreview(res *InstallResult, hr hookconfig.Result, cwd, home string) {
	for _, entry := range hr.Plan {
		path := strings.TrimPrefix(entry, "write ")
		switch {
		case home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)):
			path = "~/" + filepath.ToSlash(strings.TrimPrefix(path, home+string(os.PathSeparator)))
		default:
			if rel, err := filepath.Rel(cwd, path); err == nil {
				path = rel
			}
		}
		res.PreviewPlan = append(res.PreviewPlan, agents.PlannedWrite{Action: "write", Path: path, Detail: "merge Sila lifecycle hooks (merge-preserving)"})
	}
}
