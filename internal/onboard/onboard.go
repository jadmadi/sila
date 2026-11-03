package onboard

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

// Surface is one legacy or current task/knowledge surface found in a repository.
type Surface struct {
	Kind     string `json:"kind"`  // brain | tasks | goals | scaffold | plannotator | instructions | runbook
	Path     string `json:"path"`  // repository-relative
	State    string `json:"state"` // managed | unmanaged | scaffold
	Detail   string `json:"detail,omitempty"`
	Proposal string `json:"proposal,omitempty"`
}

// Report is the read-only inventory of a repository's sila-relevant surfaces.
type Report struct {
	RepoRoot  string         `json:"repo_root"`
	Surfaces  []Surface      `json:"surfaces"`
	Counts    map[string]int `json:"counts"`
	Unmanaged int            `json:"unmanaged"`
}

// CountLegacyUnmanaged returns the count of unmanaged legacy task surfaces (brain stores and task lists).
func (r *Report) CountLegacyUnmanaged() int {
	if r == nil {
		return 0
	}
	count := 0
	for _, s := range r.Surfaces {
		if (s.Kind == "brain" || s.Kind == "tasks") && s.State == "unmanaged" {
			count++
		}
	}
	return count
}

// Probe inventories the task systems, instruction files, and references a
// repository carries. It writes nothing.
func Probe(repoRoot string) (*Report, error) {
	return probeWithGoals(repoRoot, nil)
}

func probeWithGoals(repoRoot string, preScanned *goals.AuditResult) (*Report, error) {
	rep := &Report{RepoRoot: repoRoot, Counts: map[string]int{}}
	add := func(kind, path, state, detail, proposal string) {
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = path
		}
		rep.Surfaces = append(rep.Surfaces, Surface{Kind: kind, Path: rel, State: state, Detail: detail, Proposal: proposal})
		rep.Counts[state]++
		if state == "unmanaged" {
			rep.Unmanaged++
		}
	}

	deferrals, _ := LoadDeferrals(repoRoot)

	// Legacy brain task stores.
	for _, name := range []string{".brain", "brain.json"} {
		full := filepath.Join(repoRoot, name)
		if _, err := os.Stat(full); err == nil {
			if deferred, reason := IsSurfaceDeferred(name, deferrals); deferred {
				add("brain", full, "deferred", fmt.Sprintf("legacy brain task store (deferred: %s)", reason), "review deferrals via sila onboard")
			} else {
				add("brain", full, "unmanaged", "legacy brain task store", "map its open items into a sila goal, then freeze the file")
			}
		}
	}

	// Markdown task lists.
	for _, name := range []string{"TODO.md", "TASKS.md", "TODO.txt", "TASKS.txt", "TODO", "TASKS"} {
		full := filepath.Join(repoRoot, name)
		fi, err := os.Stat(full)
		if err != nil || fi.IsDir() {
			continue
		}
		if deferred, reason := IsSurfaceDeferred(name, deferrals); deferred {
			add("tasks", full, "deferred", fmt.Sprintf("legacy task list (deferred: %s)", reason), "review deferrals via sila onboard")
		} else {
			open := countOpenCheckboxes(full)
			add("tasks", full, "unmanaged", fmt.Sprintf("%d open checkboxes", open),
				"sila onboard --convert-tasks --from="+name+" --review")
		}
	}

	// Goal packages and scaffolding.
	goalsDir := filepath.Join(repoRoot, "goals")
	if fi, err := os.Stat(goalsDir); err == nil && fi.IsDir() {
		res := preScanned
		var scanErr error
		if res == nil {
			res, scanErr = goals.ScanGoals(repoRoot)
		}
		if scanErr == nil && res != nil {
			drafts := 0
			plannotator := 0
			for _, g := range res.Goals {
				if g.Status == "draft" {
					drafts++
				}
			}
			add("goals", goalsDir, "managed", fmt.Sprintf("%d goals (%d draft)", res.Total, drafts),
				"keep working through `sila goals`; drafts need their missing files")
			_ = plannotator
		}
		entries, _ := os.ReadDir(goalsDir)
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || (!strings.HasPrefix(name, "_") && !strings.HasPrefix(name, ".")) {
				continue
			}
			add("scaffold", filepath.Join(goalsDir, name), "scaffold", "non-goal scaffolding directory",
				"leave in place or move it outside goals/")
		}
	} else {
		add("goals", goalsDir, "unmanaged", "no goals directory", "sila goals create <slug>")
	}

	// Agent instruction files.
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".cursorrules", ".windsurfrules", ".github/copilot-instructions.md"} {
		full := filepath.Join(repoRoot, name)
		if _, err := os.Stat(full); err != nil {
			continue
		}
		state := "unmanaged"
		if data, err := os.ReadFile(full); err == nil && strings.Contains(string(data), "sila:begin") {
			state = "managed"
		}
		add("instructions", full, state, "agent instruction file", "`sila install` refreshes the sila-managed block")
	}

	// Verified runbook commands.
	if rb := dream.DetectRunbook(repoRoot); rb != nil && len(rb.Entries) > 0 {
		add("runbook", filepath.Join(repoRoot, "AGENTS.md"), "managed", fmt.Sprintf("%d verified commands", len(rb.Entries)),
			"keep the runbook current after build or test changes")
	} else {
		add("runbook", filepath.Join(repoRoot, "AGENTS.md"), "unmanaged", "no verified build/test commands detected",
			"record them in the project rules so `sila prime` can surface them")
	}

	sort.SliceStable(rep.Surfaces, func(i, j int) bool {
		if rep.Surfaces[i].Kind != rep.Surfaces[j].Kind {
			return rep.Surfaces[i].Kind < rep.Surfaces[j].Kind
		}
		return rep.Surfaces[i].Path < rep.Surfaces[j].Path
	})
	return rep, nil
}

func countOpenCheckboxes(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "- [ ]") || strings.HasPrefix(line, "* [ ]") {
			n++
		}
	}
	return n
}

// IsSilaSourceRepo reports whether the directory is the sila source repository
// itself. sila installs its Go discipline hook only there; other repositories
// keep their own hooks.
func IsSilaSourceRepo(repoRoot string) bool {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(after) == "github.com/jadmadi/sila"
		}
	}
	return false
}

// StackInfo contains the detected repository technology stack and manifest details.
type StackInfo struct {
	Primary   string   `json:"primary"`             // e.g. "go", "node", "astro", "cloudflare", "python", "rust", "polyglot"
	Framework string   `json:"framework,omitempty"` // e.g. "astro", "next", "hono", "vitest"
	Manifests []string `json:"manifests,omitempty"` // e.g. ["package.json", "go.mod"]
}

// DetectProjectStack inspects repository manifests and source layout to determine the technology stack.
func DetectProjectStack(repoRoot string) StackInfo {
	info := StackInfo{Primary: "polyglot"}
	if repoRoot == "" {
		return info
	}

	var manifests []string
	hasGo := false
	hasNode := false
	hasPython := false
	hasRust := false
	hasCF := false
	hasAstro := false

	if fileExists(filepath.Join(repoRoot, "go.mod")) || fileExists(filepath.Join(repoRoot, "go.work")) {
		hasGo = true
		manifests = append(manifests, "go.mod")
	}
	if fileExists(filepath.Join(repoRoot, "Cargo.toml")) {
		hasRust = true
		manifests = append(manifests, "Cargo.toml")
	}
	if fileExists(filepath.Join(repoRoot, "pyproject.toml")) || fileExists(filepath.Join(repoRoot, "requirements.txt")) || fileExists(filepath.Join(repoRoot, "setup.py")) {
		hasPython = true
		manifests = append(manifests, "pyproject.toml")
	}
	if fileExists(filepath.Join(repoRoot, "wrangler.jsonc")) || fileExists(filepath.Join(repoRoot, "wrangler.toml")) {
		hasCF = true
		manifests = append(manifests, "wrangler.jsonc")
	}
	pkgPath := filepath.Join(repoRoot, "package.json")
	if fileExists(pkgPath) {
		hasNode = true
		manifests = append(manifests, "package.json")
		if data, err := os.ReadFile(pkgPath); err == nil {
			content := string(data)
			if strings.Contains(content, "\"astro\"") {
				hasAstro = true
			}
			if strings.Contains(content, "\"wrangler\"") || strings.Contains(content, "@cloudflare/workers-types") {
				hasCF = true
			}
		}
	}
	if fileExists(filepath.Join(repoRoot, "astro.config.mjs")) || fileExists(filepath.Join(repoRoot, "astro.config.ts")) {
		hasAstro = true
		manifests = append(manifests, "astro.config.mjs")
	}

	info.Manifests = manifests

	switch {
	case hasAstro:
		info.Primary = "astro"
		info.Framework = "astro"
	case hasCF && !hasGo:
		info.Primary = "cloudflare"
		info.Framework = "cloudflare"
	case hasNode && !hasGo && !hasPython && !hasRust:
		info.Primary = "node"
	case hasGo && !hasNode && !hasPython && !hasRust:
		info.Primary = "go"
	case hasPython && !hasGo && !hasNode && !hasRust:
		info.Primary = "python"
	case hasRust && !hasGo && !hasNode && !hasPython:
		info.Primary = "rust"
	case hasGo:
		info.Primary = "polyglot"
	case !hasGo && len(manifests) > 0:
		info.Primary = "polyglot-no-go"
	default:
		info.Primary = "unknown"
	}

	return info
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// GenerateUniversalBlock generates a clean, stack-contained Sila-managed block.
func GenerateUniversalBlock(stack StackInfo) string {
	compiler := agents.NewCompiler("")
	opts := agents.CompileOptions{
		Stack: stack.Primary,
	}
	compiled, err := compiler.Compile(opts)
	if err != nil {
		return ""
	}
	return agents.ComposeAgentsFile("", compiled)
}

// CheckItem is one onboarding verification item.
type CheckItem struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"` // pass | warn | fail
	Detail   string   `json:"detail,omitempty"`
	Fix      string   `json:"fix,omitempty"`
	Critical bool     `json:"critical,omitempty"`
	Files    []string `json:"files,omitempty"`
}

// CheckReport is the verification matrix for one repository.
type CheckReport struct {
	RepoRoot  string             `json:"repo_root"`
	Project   string             `json:"project,omitempty"`
	Items     []CheckItem        `json:"items"`
	Pass      int                `json:"pass"`
	Warn      int                `json:"warn"`
	Fail      int                `json:"fail"`
	Strict    bool               `json:"strict"`
	Ready     bool               `json:"ready"`
	GoalsScan *goals.AuditResult `json:"-"`
}

// Check verifies that a repository is wired into sila and prints the exact fix
// for every gap. It reads state; it never writes.
func Check(st *store.Store, repoRoot, project string, strict bool) *CheckReport {
	return CheckWithOptions(st, repoRoot, project, strict, false)
}

// CheckWithOptions verifies repository onboarding and optionally skips the
// duplicate-alias scan when the caller has already computed alias pairs.
func CheckWithOptions(st *store.Store, repoRoot, project string, strict, skipAliasCheck bool) *CheckReport {
	rep := &CheckReport{RepoRoot: repoRoot, Project: project, Strict: strict}
	item := func(name, status, detail, fix string, critical bool) {
		rep.Items = append(rep.Items, CheckItem{Name: name, Status: status, Detail: detail, Fix: fix, Critical: critical})
		switch status {
		case "pass":
			rep.Pass++
		case "warn":
			rep.Warn++
		case "fail":
			rep.Fail++
		}
	}
	itemWithFiles := func(name, status, detail, fix string, critical bool, files []string) {
		rep.Items = append(rep.Items, CheckItem{Name: name, Status: status, Detail: detail, Fix: fix, Critical: critical, Files: files})
		switch status {
		case "pass":
			rep.Pass++
		case "warn":
			rep.Warn++
		case "fail":
			rep.Fail++
		}
	}

	// 1. Registered project (critical).
	if st == nil {
		item("Registered project", "warn", "knowledge store unavailable (read-only orientation)", "sila sync", false)
	} else {
		proj, found, err := st.ProjectForDir(repoRoot)
		switch {
		case err != nil:
			item("Registered project", "fail", err.Error(), "sila sync", true)
		case !found:
			item("Registered project", "fail", "not in the knowledge store", "sila heal", true)
		default:
			if project == "" {
				project = proj.ID
				rep.Project = project
			}
			item("Registered project", "pass", proj.ID, "", true)
		}
	}

	// 2. Single canonical registration (critical when duplicated).
	if st != nil && !skipAliasCheck {
		if pairs, err := st.AliasDuplicatePairs(); err == nil {
			dup := ""
			for _, p := range pairs {
				if sameDirReal(p.KeepPath, repoRoot) || sameDirReal(p.DropPath, repoRoot) {
					dup = p.KeepID + " ↔ " + p.DropID
					break
				}
			}
			if dup != "" {
				item("Canonical project id", "fail", "duplicate registrations: "+dup, "sila doctor --repair", true)
			} else {
				item("Canonical project id", "pass", "one registration for this directory", "", true)
			}
		}
	} else {
		item("Canonical project id", "pass", "one registration for this directory", "", true)
	}

	// 3. Managed rules currency.
	stack := DetectProjectStack(repoRoot)
	opts := agents.CompileOptions{RepoRoot: repoRoot, ProjectName: project, Stack: stack.Primary, Preview: true}
	if plan, err := agents.DeployTargets(repoRoot, opts, []string{"all"}); err == nil {
		needs := 0
		var names []string
		for _, p := range plan.Plan {
			if p.Action == "skip" {
				continue
			}
			needs++
			names = append(names, p.Path)
		}
		if needs > 0 {
			itemWithFiles("Agent rules", "warn", fmt.Sprintf("%d rule files would be updated: %s", needs, strings.Join(names, ", ")), "sila install", false, names)
		} else {
			item("Agent rules", "pass", "sila-managed rules are current", "", false)
		}
	}

	// 3b. Sila skill currency.
	skillPath := filepath.Join(repoRoot, ".agents", "skills", "sila", "SKILL.md")
	data, err := os.ReadFile(skillPath)
	skillScope := "canonical Sila SKILL.md is current"
	if err != nil {
		if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
			if globalData, gErr := os.ReadFile(filepath.Join(home, ".agents", "skills", "sila", "SKILL.md")); gErr == nil {
				data = globalData
				err = nil
				skillScope = "global ~/.agents/skills/sila/SKILL.md is current"
			}
		}
	}
	if err != nil {
		itemWithFiles("Sila skill", "warn", "not provisioned", "sila install", false, []string{".agents/skills/sila/SKILL.md"})
	} else {
		var missing []string
		for _, marker := range []string{"sila standup", "sila case", "sila persona", "sila edit-check"} {
			if !strings.Contains(string(data), marker) {
				missing = append(missing, marker)
			}
		}
		if len(missing) > 0 {
			itemWithFiles("Sila skill", "warn", "stale; missing markers: "+strings.Join(missing, ", "), "sila install", false, []string{".agents/skills/sila/SKILL.md"})
		} else {
			item("Sila skill", "pass", skillScope, "", false)
		}
	}

	// 4. Git hook: sila installs its discipline hook only in its own source
	// repository. Other repos keep their own hooks, and a non-git directory has
	// nothing to wire.
	hook := filepath.Join(repoRoot, ".git", "hooks", "pre-commit")
	_, hookErr := os.Stat(hook)
	_, gitErr := os.Stat(filepath.Join(repoRoot, ".git"))
	switch {
	case hookErr == nil:
		item("Git hook", "pass", "pre-commit hook installed", "", false)
	case gitErr != nil:
		item("Git hook", "pass", "not a git repository; no hook needed", "", false)
	case IsSilaSourceRepo(repoRoot):
		itemWithFiles("Git hook", "warn", "no pre-commit hook", "sila install", false, []string{".git/hooks/pre-commit"})
	default:
		item("Git hook", "pass", "project-managed hooks (sila installs none here)", "", false)
	}

	// 5. MCP registration (best effort across known configs).
	if mcpConfigured(repoRoot) {
		item("MCP server", "pass", "sila MCP registered in a known client config", "", false)
	} else {
		hasCursor := false
		if fi, err := os.Stat(filepath.Join(repoRoot, ".cursor")); err == nil && fi.IsDir() {
			hasCursor = true
		}
		if hasCursor {
			itemWithFiles("MCP server", "warn", "no sila MCP entry found in known client configs", "sila install --no-hooks", false, []string{".cursor/mcp.json"})
		} else {
			itemWithFiles("MCP server", "warn", "no sila MCP entry found in known client configs", "sila install --no-hooks", true, nil)
		}
	}

	// 6. Sync freshness.
	var projectFacts []store.Fact
	if project != "" {
		if st == nil {
			item("Sync freshness", "warn", "knowledge store unavailable (read-only orientation)", "sila sync", false)
		} else {
			projectFacts, _ = st.FactsByProject(project)
			if len(projectFacts) == 0 {
				item("Sync freshness", "warn", "no facts recorded for this project yet", "sila sync", false)
			} else {
				item("Sync freshness", "pass", fmt.Sprintf("%d facts recorded", len(projectFacts)), "", false)
			}
		}
	}

	// 7. Goals present or explicitly none.
	goalsDir := filepath.Join(repoRoot, "goals")
	if fi, err := os.Stat(goalsDir); err == nil && fi.IsDir() {
		res, scanErr := goals.ScanGoals(repoRoot)
		if scanErr == nil {
			rep.GoalsScan = res
			item("Goals", "pass", fmt.Sprintf("%d goals tracked", res.Total), "", false)
		}
	} else if _, err := os.Stat(filepath.Join(repoRoot, ".sila-no-goals")); err == nil {
		item("Goals", "pass", "explicitly none (.sila-no-goals)", "", false)
	} else {
		item("Goals", "warn", "no goals directory", "sila goals create <slug> (or add .sila-no-goals)", false)
	}

	// 8. Handoff recency.
	if project != "" {
		if st == nil {
			item("Session handoff", "warn", "knowledge store unavailable (read-only orientation)", "sila handoff \"<summary>\" --open=\"<task>\"", false)
			item("Recorded directives", "warn", "knowledge store unavailable (read-only orientation)", "sila memo \"<directive>\"", false)
			item("Solution lessons", "warn", "knowledge store unavailable (read-only orientation)", "sila lesson \"<symptom>\" --cause=... --fix=...", false)
		} else {
			h, _ := st.LatestHandoff(project)
			if h == nil {
				item("Session handoff", "warn", "no handoff recorded", "sila handoff \"<summary>\" --open=\"<task>\"", false)
			} else if age, ok := ageOf(h.CreatedAt); ok && age > 14*24*time.Hour {
				item("Session handoff", "warn", fmt.Sprintf("latest handoff is %s old", formatAge(age)), "sila handoff \"<summary>\"", false)
			} else {
				item("Session handoff", "pass", "recent handoff on record", "", false)
			}

			// 9. Memo recency.
			memos := 0
			for _, f := range projectFacts {
				if f.IsExplicitMemo() || f.IsInvariant() {
					memos++
				}
			}
			if memos == 0 {
				item("Recorded directives", "warn", "no memos or invariants recorded", "sila memo \"<directive>\"", false)
			} else {
				item("Recorded directives", "pass", fmt.Sprintf("%d memos or invariants", memos), "", false)
			}

			// 10. Lessons (advisory).
			lessons, _ := st.LessonFacts(project, 1)
			if len(lessons) == 0 {
				item("Solution lessons", "warn", "no lessons recorded yet", "sila lesson \"<symptom>\" --cause=... --fix=...", false)
			} else {
				item("Solution lessons", "pass", "lessons recorded", "", false)
			}
		}
	}

	// 11. Legacy inventory.
	if probe, err := probeWithGoals(repoRoot, rep.GoalsScan); err == nil {
		unmanagedLegacy := 0
		var storeDefs []Deferral
		if st != nil && project != "" {
			storeDefs = LoadStoreDeferrals(st, project)
		}
		for _, s := range probe.Surfaces {
			if s.Kind == "brain" || s.Kind == "tasks" {
				if s.State == "unmanaged" {
					if len(storeDefs) > 0 {
						if def, _ := IsSurfaceDeferred(s.Path, storeDefs); def {
							continue
						}
					}
					unmanagedLegacy++
				}
			}
		}
		if unmanagedLegacy > 0 {
			item("Legacy inventory", "warn", fmt.Sprintf("%d unmanaged surfaces", unmanagedLegacy), "sila onboard (review the inventory)", false)
		} else {
			item("Legacy inventory", "pass", "no unmanaged task surfaces found", "", false)
		}
	} else {
		item("Legacy inventory", "pass", "no unmanaged task surfaces found", "", false)
	}

	// 12. Assistance consent state.
	if cfg, err := config.Load(""); err == nil && cfg != nil {
		if cfg.AssistEnabled {
			scopes := strings.Join(cfg.AssistScopes, ",")
			if scopes == "" {
				scopes = "all"
			}
			item("Assistance consent", "pass", fmt.Sprintf("enabled for scopes: %s (network calls still require a provider and a request)", scopes), "", false)
		} else {
			item("Assistance consent", "pass", "disabled; zero network calls", "", false)
		}
	}

	rep.Ready = rep.Fail == 0
	return rep
}

// sameDirReal reports whether a registered path and a local path are the same real directory.
func sameDirReal(registered, local string) bool {
	ra, errA := filepath.EvalSymlinks(registered)
	rb, errB := filepath.EvalSymlinks(local)
	if errA != nil || errB != nil {
		return false
	}
	return filepath.Clean(ra) == filepath.Clean(rb)
}

// mcpConfigured looks for a sila stdio entry in the client configs sila knows,
// including the project's own .cursor config.
func mcpConfigured(repoRoot string) bool {
	candidates := []string{
		filepath.Join(repoRoot, ".cursor", "mcp.json"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".gemini", "config", "mcp_config.json"),
			filepath.Join(home, ".config", "opencode", "opencode.json"),
			filepath.Join(home, ".config", "opencode", "opencode.jsonc"),
		)
	}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), `"sila"`) || strings.Contains(string(data), "sila mcp") {
			return true
		}
	}
	return false
}

func ageOf(ts string) (time.Duration, bool) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return 0, false
	}
	return time.Since(t), true
}

func formatAge(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 48*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
