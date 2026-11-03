package onboard

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/git"
	"github.com/jadmadi/sila/internal/goals"
)

//go:embed sila_skill.md
var SilaSkillMarkdown string

// GoalPackageTemplate returns the three files for the project-local onboarding goal.
func GoalPackageTemplate(project string, isGitRepo ...bool) map[string]string {
	isGit := true
	if len(isGitRepo) > 0 {
		isGit = isGitRepo[0]
	}

	doneCondition := `## Done Condition
1. Every unmanaged surface from ` + "`sila onboard`" + ` is mapped into a sila goal, deferred with a recorded reason, or frozen in place.
2. The verified runbook commands are recorded and current.
3. Directives and solved-problem lessons from the legacy system are recorded with ` + "`sila memo`" + ` and ` + "`sila lesson`" + `.
4. ` + "`sila onboard check --strict`" + ` exits clean.
`
	if isGit {
		doneCondition += `5. Goal closed via commit containing ` + "`(goals/sila-onboarding/goal.md)`" + `.` + "\n"
	}

	goal := fmt.Sprintf(`# Goal: Complete Sila Onboarding

## Goal Description
Wire %s fully into sila and verify it: map the legacy task surfaces found by `+"`sila onboard`"+`, reconcile duplicates, record the verified runbook and directives, then prove the result with `+"`sila onboard check --strict`"+`. The goal closes when the check is green.

## Dependencies & Execution Order
- **Mode**: Independent (disjoint, parallelizable)
- **Depends On**: none
- **Sequence**: -
- **Shape**: ship
- **Memory Protocol**: required

## Memory Plan
- Record verified runbook commands and architectural directives with `+"`sila memo`"+`.
- Record solved problems and root causes with `+"`sila lesson`"+`.

%s`, project, doneCondition)

	verifyStep := "  - Commit and run `sila goals`.\n"
	if !isGit {
		verifyStep = "  - Run `sila goals` or `sila goals audit sila-onboarding`.\n"
	}

	plan := `# Plan: Complete Sila Onboarding

## Execution Steps

- [ ] **Phase 1 — Review the inventory**
  - Run ` + "`sila onboard --json`" + ` and list every unmanaged surface: brain stores, markdown task lists, drafts, instructions, and the runbook.
  - For each surface, decide: map into sila goals, defer with a recorded reason, or freeze.

- [ ] **Phase 2 — Map legacy work**
  - Convert open markdown tasks with ` + "`sila onboard --convert-tasks --from=<file> --review --yes`" + ` when the list is real work; otherwise record the decision with ` + "`sila memo`" + `.
  - Reconcile duplicate or stale goals with ` + "`sila goals`" + ` and close what is already done.

- [ ] **Phase 3 — Record what the project knows**
  - Record the verified runbook commands and any architectural directives with ` + "`sila memo`" + `.
  - Record solved problems with ` + "`sila lesson`" + ` so the next session does not rediscover them.

- [ ] **Phase 4 — Verify**
  - Run ` + "`sila onboard check --strict`" + `. Resolve every critical failure and review the warnings.
` + verifyStep

	facts := fmt.Sprintf(`# Fact Sheet: Complete Sila Onboarding

## 1. Primary Purpose & Context
This package was scaffolded by `+"`sila onboard`"+` for %s. Its job is to finish connecting the repository to sila: map legacy task systems, record the knowledge they carried, and verify the wiring so every future session works through sila first.

## 2. Verification
- `+"`sila onboard check --strict`"+` is the acceptance test.
- Critical gaps: project not registered, duplicate registrations.
- Advisory gaps: missing handoff, memo, lesson, git hook, MCP registration, or an unmapped legacy surface.

## 3. Alternatives Considered
- Bulk-import the legacy tasks automatically. Rejected: old lists mix done, stale, and scratch items; a reviewed conversion avoids recreating the duplication this onboarding exists to end.
`, project)

	return map[string]string{"goal.md": goal, "plan.md": plan, "facts.md": facts}
}

// HasTrackedGoalsOrMarker reports whether the repository already tracks at
// least one goal package or opts out with .sila-no-goals.
func HasTrackedGoalsOrMarker(repoRoot string) bool {
	if _, err := os.Stat(filepath.Join(repoRoot, ".sila-no-goals")); err == nil {
		return true
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, "goals"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot, "goals", e.Name(), "goal.md")); err == nil {
			return true
		}
	}
	return false
}

// ScaffoldGoalPackage writes goals/sila-onboarding when absent and the
// repository has no existing tracked goals. It returns the directory written,
// or "" when the package already exists or another goal package is tracked.
func ScaffoldGoalPackage(repoRoot string) (string, error) {
	if HasTrackedGoalsOrMarker(repoRoot) {
		return "", nil
	}
	dir := filepath.Join(repoRoot, "goals", "sila-onboarding")
	if _, err := os.Stat(filepath.Join(dir, "goal.md")); err == nil {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	_, gitErr := os.Stat(filepath.Join(repoRoot, ".git"))
	isGit := gitErr == nil
	for name, content := range GoalPackageTemplate(filepath.Base(repoRoot), isGit) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// ConvertResult is the reviewed conversion of a markdown task list.
type ConvertResult struct {
	Source  string   `json:"source"`
	Slug    string   `json:"slug"`
	Tasks   []string `json:"tasks"`
	Written bool     `json:"written"`
}

// ProposeTaskConversion parses open checkboxes from a markdown file without
// writing anything.
func ProposeTaskConversion(repoRoot, source string) (*ConvertResult, error) {
	full := source
	if !filepath.IsAbs(full) {
		full = filepath.Join(repoRoot, source)
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tasks []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		for _, prefix := range []string{"- [ ]", "* [ ]"} {
			if after, ok := strings.CutPrefix(line, prefix); ok {
				text := strings.TrimSpace(after)
				if text != "" {
					tasks = append(tasks, text)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no open checkboxes found in %s", source)
	}
	return &ConvertResult{Source: source, Slug: "legacy-backlog", Tasks: tasks}, nil
}

// WriteConvertedGoal writes goals/legacy-backlog from a reviewed proposal. The
// source file is never modified, and its path is recorded in facts.md.
func WriteConvertedGoal(repoRoot string, res *ConvertResult) (string, error) {
	if res == nil || len(res.Tasks) == 0 {
		return "", fmt.Errorf("nothing to convert")
	}
	dir := filepath.Join(repoRoot, "goals", res.Slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	var goal strings.Builder
	goal.WriteString("# Goal: Legacy Backlog\n\n")
	goal.WriteString("## Goal Description\n")
	goal.WriteString(fmt.Sprintf("Carry the open work from `%s` into sila so it stops living in a parallel task list. Source file: `%s`.\n\n", res.Source, res.Source))
	goal.WriteString("## Dependencies & Execution Order\n")
	goal.WriteString("- **Mode**: Independent (disjoint, parallelizable)\n")
	goal.WriteString("- **Depends On**: none\n")
	goal.WriteString("- **Sequence**: -\n")
	goal.WriteString("- **Shape**: ship\n")
	goal.WriteString("- **Memory Protocol**: required\n\n")
	goal.WriteString("## Memory Plan\n")
	goal.WriteString("- Record architectural directives with `sila memo` and solved problems with `sila lesson`.\n\n")
	goal.WriteString("## Done Condition\n")
	goal.WriteString("1. Every task below is done, moved to a dedicated goal, or closed with a recorded reason.\n")
	goal.WriteString("2. The source markdown list is frozen or removed by the owner.\n")
	goal.WriteString("3. Goal closed via commit containing `(goals/legacy-backlog/goal.md)`.\n")

	var plan strings.Builder
	plan.WriteString("# Plan: Legacy Backlog\n\n## Execution Steps\n\n")
	for _, task := range res.Tasks {
		plan.WriteString(fmt.Sprintf("- [ ] %s\n", task))
	}

	var facts strings.Builder
	facts.WriteString("# Fact Sheet: Legacy Backlog\n\n")
	facts.WriteString("## 1. Primary Purpose & Context\n")
	facts.WriteString(fmt.Sprintf("Converted from `%s` by `sila onboard --convert-tasks`. The list predates sila; this package is the reviewed copy.\n\n", res.Source))
	facts.WriteString("## 2. Alternatives Considered\n")
	facts.WriteString("- Leave the list in place and rely on agents to read both. Rejected: that is the duplication this conversion ends.\n")

	files := map[string]string{"goal.md": goal.String(), "plan.md": plan.String(), "facts.md": facts.String()}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	res.Written = true
	return dir, nil
}

// OpenCodePluginContent is a compact, dependency-free OpenCode V2 plugin that
// injects the `sila prime` briefing on the first prompt of each session. The
// richer hand-written sila-prime plugin can replace it at any time.
const OpenCodePluginContent = `// OpenCode V2 sila onboarding plugin (managed by "sila onboard").
// Runs "sila prime" on the first prompt of a session and injects the briefing.
// Dependency-free: exports a plain { id, setup } object and uses Bun globals.
const VERSION = "0.1.0"
const OPEN = "<sila_memory>\n"
const CLOSE = "\n</sila_memory>"
const injected = new Set<string>()

const plugin = {
  id: "sila-onboard",
  async setup(ctx: any) {
    await ctx.session.hook("prompt", async (event: any) => {
      if (!event?.prompt || typeof event.prompt.text !== "string") return
      if (typeof event.sessionID !== "string" || event.sessionID.length === 0) return
      const key = "sila-onboard/injected/" + event.sessionID
      if (injected.has(key)) return
      injected.add(key)
      try {
        if (await ctx.storage.get(key)) return
      } catch {
        return
      }
      try {
        await ctx.storage.set(key, true)
      } catch {}
      let briefing = ""
      try {
        const cwd = typeof ctx?.location?.directory === "string" ? ctx.location.directory : process.cwd()
        const proc = Bun.spawn([process.env.SILA_BIN || "sila", "prime", "--no-color"], { cwd, stdin: "ignore", stdout: "pipe", stderr: "ignore" })
        const timer = setTimeout(() => { try { proc.kill(9) } catch {} }, 5000)
        const out = await new Response(proc.stdout).text()
        const code = await proc.exited
        clearTimeout(timer)
        if (code === 0 && out.trim()) briefing = OPEN + out.trim().slice(0, 5500) + CLOSE
      } catch {}
      if (briefing) event.prompt.text = briefing + "\n\n" + event.prompt.text
    })
  },
}

export default plugin
`

// WriteOpenCodePlugin writes the plugin into the OpenCode plugins directory.
// It returns the path written, or "" when the directory does not exist.
func WriteOpenCodePlugin() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}
	dir := filepath.Join(home, ".config", "opencode", "plugins")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", nil
	}
	path := filepath.Join(dir, "sila-onboard.ts")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == OpenCodePluginContent {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(OpenCodePluginContent), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// RegisterMCPServer merges the sila stdio MCP server registration into the target JSON config.
func RegisterMCPServer(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	config := map[string]any{
		"mcpServers": map[string]any{
			"sila": map[string]any{
				"command": "sila",
				"args":    []string{"mcp"},
			},
		},
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("existing MCP config at %s is unreadable (%v); fix its permissions, then re-run install", path, readErr)
	}
	if readErr == nil {
		var existing map[string]any
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("existing MCP config at %s is not valid JSON (%v); fix or remove the file, then re-run install", path, err)
		}
		servers, ok := existing["mcpServers"].(map[string]any)
		if !ok {
			if existing["mcpServers"] != nil {
				return fmt.Errorf("existing MCP config at %s has a non-object mcpServers member; fix the file, then re-run install", path)
			}
			servers = make(map[string]any)
		}
		servers["sila"] = config["mcpServers"].(map[string]any)["sila"]
		existing["mcpServers"] = servers
		config = existing
	}

	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

// RegisterZedMCPServer merges the sila stdio MCP server registration into Zed's settings JSON config under context_servers.
func RegisterZedMCPServer(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("existing Zed settings at %s is unreadable (%v); fix its permissions, then re-run install", path, readErr)
	}

	var existing map[string]any
	if readErr == nil {
		cleaned := stripJSONComments(string(data))
		if err := json.Unmarshal([]byte(cleaned), &existing); err != nil {
			return fmt.Errorf("existing Zed settings at %s is not valid JSON (%v); fix or remove the file, then re-run install", path, err)
		}
	} else {
		existing = make(map[string]any)
	}

	servers, ok := existing["context_servers"].(map[string]any)
	if !ok {
		if existing["context_servers"] != nil {
			return fmt.Errorf("existing Zed settings at %s has a non-object context_servers member; fix the file, then re-run install", path)
		}
		servers = make(map[string]any)
	}

	// Idempotency: if already configured, do not re-serialize or modify
	for k := range servers {
		if strings.EqualFold(k, "sila") {
			return nil
		}
	}

	servers["sila"] = map[string]any{
		"command": "sila",
		"args":    []string{"mcp"},
	}
	existing["context_servers"] = servers

	b, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

func stripJSONComments(s string) string {
	var b strings.Builder
	inString := false
	inBlockComment := false
	inLineComment := false
	var quoteChar rune

	chars := []rune(s)
	n := len(chars)

	for i := 0; i < n; i++ {
		r := chars[i]
		next := rune(0)
		if i+1 < n {
			next = chars[i+1]
		}

		if inLineComment {
			if r == '\n' {
				inLineComment = false
				b.WriteRune(r)
			}
			continue
		}

		if inBlockComment {
			if r == '*' && next == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if inString {
			b.WriteRune(r)
			if r == '\\' && i+1 < n {
				i++
				b.WriteRune(chars[i])
			} else if r == quoteChar {
				inString = false
			}
			continue
		}

		if r == '"' || r == '\'' {
			inString = true
			quoteChar = r
			b.WriteRune(r)
			continue
		}

		if r == '/' && next == '/' {
			inLineComment = true
			i++
			continue
		}

		if r == '/' && next == '*' {
			inBlockComment = true
			i++
			continue
		}

		b.WriteRune(r)
	}

	return stripTrailingJSONCommas(b.String())
}

// stripTrailingJSONCommas removes commas that directly precede a closing brace
// or bracket (ignoring whitespace), which JSONC permits but encoding/json
// rejects. String contents are preserved verbatim.
func stripTrailingJSONCommas(s string) string {
	var b strings.Builder
	inString := false
	var quoteChar rune
	chars := []rune(s)
	n := len(chars)

	for i := 0; i < n; i++ {
		r := chars[i]
		if inString {
			b.WriteRune(r)
			if r == '\\' && i+1 < n {
				i++
				b.WriteRune(chars[i])
			} else if r == quoteChar {
				inString = false
			}
			continue
		}
		if r == '"' || r == '\'' {
			inString = true
			quoteChar = r
			b.WriteRune(r)
			continue
		}
		if r == ',' {
			j := i + 1
			for j < n && (chars[j] == ' ' || chars[j] == '\t' || chars[j] == '\n' || chars[j] == '\r') {
				j++
			}
			if j < n && (chars[j] == '}' || chars[j] == ']') {
				continue
			}
		}
		b.WriteRune(r)
	}

	return b.String()
}

// ProvisionResult is the outcome of wiring one repository into sila.
type ProvisionResult struct {
	RulesUpdated   []string `json:"rules_updated,omitempty"`
	Hooks          []string `json:"hooks_installed,omitempty"`
	MCPConfigs     []string `json:"mcp_configs,omitempty"`
	SkillsUpdated  []string `json:"skills_updated,omitempty"`
	OpenCodePlugin string   `json:"opencode_plugin,omitempty"`
	GoalPackage    string   `json:"goal_package,omitempty"`
	Notes          []string `json:"notes,omitempty"`
	Errors         []string `json:"errors,omitempty"`
}

// WriteSilaSkill provisions the canonical Sila skill into the repository and
// the user's global skill directory. It creates harness discovery symlinks only
// when the harness skill parent directory already exists, and never replaces a
// foreign non-Sila skill.
func WriteSilaSkill(repoRoot string) ([]string, error) {
	var written []string
	repoPath := filepath.Join(repoRoot, ".agents", "skills", "sila", "SKILL.md")
	if err := writeSkillFile(repoPath, SilaSkillMarkdown); err != nil {
		return written, err
	}
	written = append(written, repoPath)

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return written, nil
	}
	globalDir := filepath.Join(home, ".agents", "skills", "sila")
	globalPath := filepath.Join(globalDir, "SKILL.md")
	if err := writeSkillFile(globalPath, SilaSkillMarkdown); err != nil {
		return written, err
	}
	written = append(written, globalPath)

	for _, parent := range []string{
		filepath.Join(home, ".gemini", "config", "skills"),
		filepath.Join(home, ".claude", "skills"),
	} {
		if fi, err := os.Stat(parent); err != nil || !fi.IsDir() {
			continue
		}
		link := filepath.Join(parent, "sila")
		if fi, err := os.Lstat(link); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Readlink(link); err == nil && sameSkillTarget(target, globalDir) {
					written = append(written, link)
				}
			}
			continue // foreign directory or link: leave it intact
		}
		if err := os.Symlink(globalDir, link); err == nil {
			written = append(written, link)
		}
	}
	return written, nil
}

func writeSkillFile(path, content string) error {
	if existing, err := os.ReadFile(path); err == nil && string(existing) == content {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func sameSkillTarget(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return absA == absB
}

// Provision connects a repository to Sila: rules deployment with stack containment,
// git hooks, MCP client configuration, OpenCode plugin, and completion goal scaffolding.
func Provision(cwd, project string, stackOverride ...string) ProvisionResult {
	res := ProvisionResult{}
	stk := DetectProjectStack(cwd)
	stackName := stk.Primary
	if len(stackOverride) > 0 && stackOverride[0] != "" {
		stackName = stackOverride[0]
	}

	opts := agents.CompileOptions{RepoRoot: cwd, ProjectName: project, Stack: stackName}
	if syncRes, err := agents.DeployTargets(cwd, opts, []string{"all"}); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("rules: %v", err))
	} else {
		res.RulesUpdated = append(res.RulesUpdated, syncRes.FilesWritten...)
		res.Errors = append(res.Errors, syncRes.Errors...)
	}

	if skills, err := WriteSilaSkill(cwd); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("sila skill: %v", err))
	} else {
		res.SkillsUpdated = append(res.SkillsUpdated, skills...)
	}

	// Git hook: installed only in Sila source repo
	if IsSilaSourceRepo(cwd) {
		if mgr, err := git.NewManager(cwd); err == nil {
			hook, err := mgr.InstallPreCommitHook()
			switch {
			case err == nil:
				res.Hooks = append(res.Hooks, hook)
			case errors.Is(err, git.ErrForeignHook):
				res.Notes = append(res.Notes, "left the existing pre-commit hook in place")
			default:
				res.Errors = append(res.Errors, fmt.Sprintf("git hook: %v", err))
			}
		}
	} else if _, err := os.Stat(filepath.Join(cwd, ".git", "hooks", "pre-commit")); err == nil {
		res.Notes = append(res.Notes, "project-managed pre-commit hook left untouched")
	} else if _, err := os.Stat(filepath.Join(cwd, ".git")); err != nil {
		res.Notes = append(res.Notes, "non-git workspace root detected: git commit done condition omitted from onboarding goal; run 'git init' if version control is desired")
	}

	cursorDir := filepath.Join(cwd, ".cursor")
	if fi, err := os.Stat(cursorDir); err == nil && fi.IsDir() {
		if err := RegisterMCPServer(filepath.Join(cursorDir, "mcp.json")); err == nil {
			res.MCPConfigs = append(res.MCPConfigs, ".cursor/mcp.json")
		}
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		agyDir := filepath.Join(home, ".gemini", "config")
		if _, statErr := os.Stat(agyDir); statErr == nil {
			if err := RegisterMCPServer(filepath.Join(agyDir, "mcp_config.json")); err == nil {
				res.MCPConfigs = append(res.MCPConfigs, "~/.gemini/config/mcp_config.json")
			}
		}
		zedDir := filepath.Join(home, ".config", "zed")
		if _, statErr := os.Stat(zedDir); statErr == nil {
			if err := RegisterZedMCPServer(filepath.Join(zedDir, "settings.json")); err == nil {
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
	if pluginPath, err := WriteOpenCodePlugin(); err == nil && pluginPath != "" {
		res.OpenCodePlugin = pluginPath
	}
	if pkg, err := ScaffoldGoalPackage(cwd); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("goal package: %v", err))
	} else if pkg != "" {
		res.GoalPackage = pkg
		_, _ = goals.EnsureGitIgnoreClaim(cwd)
	}
	return res
}

// RegisterCommandCodeMCPServer merges the Sila stdio server into Command
// Code's mcp.json. Existing extra keys on the sila entry (transport, enabled,
// custom args) and every foreign server are preserved; a malformed file is an
// error rather than something to overwrite.
func RegisterCommandCodeMCPServer(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	config := map[string]any{}
	data, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		if err := json.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("existing Command Code MCP config at %s is not valid JSON (%v); fix or remove the file, then re-run install", path, err)
		}
	case !os.IsNotExist(readErr):
		return fmt.Errorf("existing Command Code MCP config at %s is unreadable (%v); fix its permissions, then re-run install", path, readErr)
	}
	servers, ok := config["mcpServers"].(map[string]any)
	if !ok {
		if config["mcpServers"] != nil {
			return fmt.Errorf("existing Command Code MCP config at %s has a non-object mcpServers member; fix the file, then re-run install", path)
		}
		servers = map[string]any{}
	}
	entry, _ := servers["sila"].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	command := "sila"
	if exe, err := os.Executable(); err == nil && filepath.Base(exe) == "sila" {
		command = exe
	}
	entry["transport"] = "stdio"
	entry["enabled"] = true
	entry["command"] = command
	entry["args"] = []string{"mcp"}
	servers["sila"] = entry
	config["mcpServers"] = servers
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
