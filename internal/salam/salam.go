package salam

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/heal"
	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/store"
)

// SalamLoop is the working contract every agent follows in a sila repository.
var SalamLoop = []string{
	"Heal and prime: run `sila heal` (or MCP `reconcile_drift`), commit refreshed managed files, then `sila prime` (or MCP `prime_context`).",
	"Pick work: `sila goals ready -p .` (or MCP `list_goals(pending_only=true)`), claim with `sila goals claim <slug> --agent=<name>` (or MCP `claim_goal`), bind with `sila goals focus <slug> --session=<id> --agent=<name>`.",
	"When stuck: `sila recall \"<error or symptom>\"` (or MCP `recall`) before web search or trial and error. After a fix: `sila lesson \"<symptom>\" --cause=\"...\" --fix=\"...\"` (or MCP `save_lesson`).",
	"When you decide something: `sila memo \"<directive>\"` (or MCP `save_memo`) immediately.",
	"When done: commit `feat: ... (goals/<slug>/goal.md)`, run `sila goals` (or MCP `audit_goals`) to confirm it is implemented, then `sila handoff \"<summary>\" --open=\"...\"` (or MCP `save_handoff`) and `sila sync`.",
	"Report back: the goal and commit, the lessons recorded, and anything sila did not know but should.",
	"Sila friction or ideas: `sila feedback \"<what happened>\" --type=issue|tip|idea` (or MCP `record_feedback`).",
}

// SalamGoal describes one roadmap goal ready to be picked by an agent.
type SalamGoal struct {
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Mode        string `json:"mode"`
	Tier        string `json:"tier,omitempty"`
	LaunchCmd   string `json:"launch_cmd"`
	MCPToolCall string `json:"mcp_tool_call"`
}

// SalamClaim describes a currently active goal claimed by an agent.
type SalamClaim struct {
	Slug  string `json:"slug"`
	Agent string `json:"agent"`
	Age   string `json:"age"`
	Note  string `json:"note,omitempty"`
}

// SalamAssistance describes the assistance / JEV tier state for salam.
type SalamAssistance struct {
	Active           bool     `json:"active"`
	GlobalConfigured bool     `json:"global_configured"`
	ProjectEnabled   bool     `json:"project_enabled"`
	Provider         string   `json:"provider,omitempty"`
	Scopes           []string `json:"scopes,omitempty"`
	Status           string   `json:"status"` // "active", "dormant", "unconfigured"
	Message          string   `json:"message"`
	CanPrompt        bool     `json:"can_prompt"`
}

// SalamWiring reports whether the repository has sila's managed wiring.
// State is "wired", "unwired", or "unknown" (the heal plan could not be
// computed, so the brief must not claim a connection).
type SalamWiring struct {
	State        string   `json:"state"`
	NextCommand  string   `json:"next_command,omitempty"`
	MissingPaths []string `json:"missing_paths,omitempty"`
}

// SalamBrief holds the complete first-contact briefing for an agent starting work.
type SalamBrief struct {
	RepoRoot              string                       `json:"repo_root"`
	Project               string                       `json:"project"`
	Connected             bool                         `json:"connected"`
	Wiring                SalamWiring                  `json:"wiring"`
	Branch                string                       `json:"branch,omitempty"`
	Harnesses             []string                     `json:"harnesses,omitempty"`
	Assistance            SalamAssistance              `json:"assistance"`
	HealAutomatic         int                          `json:"heal_automatic"`
	HealJudgment          int                          `json:"heal_judgment"`
	HealSteps             []string                     `json:"heal_steps,omitempty"`
	Ready                 []SalamGoal                  `json:"ready_goals"`
	Claimed               []SalamClaim                 `json:"claimed_goals"`
	LastHandoff           *store.Handoff               `json:"last_handoff,omitempty"`
	MemoryUnavailable     bool                         `json:"memory_unavailable,omitempty"`
	MemoryStatus          string                       `json:"memory_status,omitempty"`
	Loop                  []string                     `json:"loop"`
	SubshellGuide         string                       `json:"subshell_guide"`
	MemoryContractVersion string                       `json:"memory_contract_version"`
	HookStatus            map[string]hookconfig.Status `json:"hook_status,omitempty"`
	HookHint              string                       `json:"hook_hint,omitempty"`
	IgnoredSubdirWarning  string                       `json:"ignored_subdir_warning,omitempty"`
	Markdown              string                       `json:"markdown"`
}

// BuildBrief constructs an orientation brief for the given repository and project.
func BuildBrief(st *store.Store, repoRoot, projectSel string) (*SalamBrief, error) {
	var projectID string

	if projectSel == "" {
		base := repoRoot
		if base == "" || base == "." {
			if cwd, err := os.Getwd(); err == nil {
				base = cwd
			}
		}
		identity, err := store.ResolveWorkspaceIdentity(base)
		if err != nil {
			return nil, err
		}
		repoRoot = identity.ActiveDir
	}

	if projectSel != "" {
		projectSel = strings.TrimSpace(projectSel)
		if st != nil {
			p, err := st.ResolveScope(projectSel, repoRoot)
			if err != nil {
				return nil, err
			}
			projectID = p.ID
			if repoRoot == "" || repoRoot == "." {
				if fi, err := os.Stat(p.Path); err == nil && fi.IsDir() {
					repoRoot = p.Path
				}
			}
		} else {
			if fi, err := os.Stat(projectSel); err == nil && fi.IsDir() {
				repoRoot = filepath.Clean(projectSel)
				projectID = store.ProjectID(repoRoot)
			} else {
				projectID = projectSel
			}
		}
	}

	if repoRoot == "" || repoRoot == "." {
		repoRoot = heal.RepoRootPath("")
		if repoRoot == "" {
			if cwd, err := os.Getwd(); err == nil {
				repoRoot = filepath.Clean(cwd)
			}
		}
	} else {
		repoRoot = filepath.Clean(repoRoot)
	}

	branch := detectGitBranch(repoRoot)

	if projectID == "" {
		if st != nil {
			p, err := st.ResolveScope("", repoRoot)
			if err == nil && p.ID != "" {
				projectID = p.ID
			}
		}
	}
	if projectID == "" {
		if repoRoot != "" {
			canon := store.CanonicalProjectPath(repoRoot)
			projectID = store.ProjectID(canon)
		} else {
			projectID = "default"
		}
	}

	subshellGuide := "If the `sila` CLI is not found in your subshell $PATH (e.g. exit code 127), invoke it directly as `~/.local/bin/sila` or use the corresponding Sila MCP tools."

	brief := &SalamBrief{
		RepoRoot:              repoRoot,
		MemoryContractVersion: hookconfig.MemoryContractVersion,
		HookStatus:            hookconfig.StatusAll(repoRoot),
		Project:               projectID,
		Branch:                branch,
		Loop:                  SalamLoop,
		SubshellGuide:         subshellGuide,
	}

	// 1. Local harnesses on disk
	var harnesses []string
	srcs := loaders.AllSources()
	for _, name := range loaders.SortedNames(srcs) {
		if name == "git" {
			continue
		}
		if s := srcs[name]; s != nil {
			if _, ok := s.ResolvePath(); ok {
				harnesses = append(harnesses, name)
			}
		}
	}
	brief.Harnesses = harnesses

	// 2. Semantic assistance (JEV) tier status
	cfg, _ := config.LoadForDir("", repoRoot)
	tier := cfg.CheckAssistanceTier(brief.Project)
	assistStatus := "unconfigured"
	if tier.Active {
		assistStatus = "active"
	} else if tier.GlobalConfigured {
		assistStatus = "dormant"
	}
	brief.Assistance = SalamAssistance{
		Active:           tier.Active,
		GlobalConfigured: tier.GlobalConfigured,
		ProjectEnabled:   tier.ProjectEnabled,
		Provider:         tier.Provider,
		Scopes:           tier.Scopes,
		Status:           assistStatus,
		Message:          tier.Message,
		CanPrompt:        !tier.Active,
	}

	// 3. Healing plan. Wiring is derived from the same plan, so the greeting
	// cannot claim a connection that the heal report denies.
	brief.Wiring = SalamWiring{State: "unknown", NextCommand: "sila heal --dry-run"}
	var preScannedGoals *goals.AuditResult
	if repoRoot != "" {
		plan, err := heal.BuildPlan(st, repoRoot)
		if err == nil {
			preScannedGoals = plan.GoalsScan
			brief.HealAutomatic = plan.Automatic
			brief.HealJudgment = plan.Judgment
			brief.Wiring = wiringFromPlan(plan)
			brief.Connected = brief.Wiring.State == "wired"
			for _, s := range plan.Steps {
				if s.Automatic {
					brief.HealSteps = append(brief.HealSteps, s.Title)
				}
				if len(brief.HealSteps) == 3 {
					break
				}
			}
		}
	}

	// 4. Goal roadmap state
	if repoRoot != "" {
		audit := preScannedGoals
		if audit == nil {
			audit, _ = goals.ScanGoals(repoRoot)
		}
		if audit != nil {
			for _, g := range audit.Goals {
				switch {
				case g.Status == "claimed":
					claim := SalamClaim{Slug: g.Slug, Agent: "agent", Age: "recently"}
					if g.Claim != nil {
						if g.Claim.ClaimedBy != "" {
							claim.Agent = g.Claim.ClaimedBy
						}
						if !g.Claim.ClaimedAt.IsZero() {
							claim.Age = time.Since(g.Claim.ClaimedAt).Round(time.Minute).String() + " ago"
						}
						claim.Note = g.Claim.Note
					}
					brief.Claimed = append(brief.Claimed, claim)
				case g.Status == "pending" && !g.Blocked:
					mode := "independent"
					if !g.Independent {
						mode = "dependent"
					}
					brief.Ready = append(brief.Ready, SalamGoal{
						Slug:        g.Slug,
						Title:       g.Title,
						Mode:        mode,
						Tier:        g.Tier,
						LaunchCmd:   fmt.Sprintf("/goal goals/%s/goal.md", g.Slug),
						MCPToolCall: fmt.Sprintf(`claim_goal(slug=%q)`, g.Slug),
					})
				}
			}
		}
	}

	// 5. Last session handoff & memory status
	if st != nil && brief.Project != "" {
		if h, _ := st.LatestHandoff(brief.Project); h != nil {
			brief.LastHandoff = h
		}
	} else if st == nil {
		brief.MemoryUnavailable = true
		brief.MemoryStatus = "unavailable"
	}

	for _, harness := range []string{"claude", "command-code"} {
		status := brief.HookStatus[harness]
		if status.Supported && !status.Installed {
			brief.HookHint = fmt.Sprintf("sila hook install --harness=%s", harness)
			break
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		brief.IgnoredSubdirWarning = DetectIgnoredSubdirWarning(repoRoot, cwd)
	}
	brief.Markdown = RenderMarkdown(brief)
	return brief, nil
}

// wiringFromPlan maps a heal plan to a truthful wiring state. Only an explicit
// provision step makes a repository unwired; other automatic steps (claim
// pruning, alias merges) are maintenance, not wiring.
func wiringFromPlan(plan *heal.Plan) SalamWiring {
	w := SalamWiring{State: "wired"}
	if plan == nil {
		return SalamWiring{State: "unknown", NextCommand: "sila heal --dry-run"}
	}
	for _, step := range plan.Steps {
		if step.ID == "provision" {
			w.State = "unwired"
			w.NextCommand = "sila heal"
			w.MissingPaths = append([]string(nil), step.Files...)
			return w
		}
	}
	return w
}

// DetectIgnoredSubdirWarning returns a warning when cwd is an uninitialized
// subdirectory that is gitignored by the resolved parent repository root.
func DetectIgnoredSubdirWarning(repoRoot, cwd string) string {
	if cwd == "" {
		return ""
	}
	cleanCwd := filepath.Clean(cwd)
	if _, err := os.Stat(filepath.Join(cleanCwd, ".git")); err == nil {
		return ""
	}
	cleanRoot := filepath.Clean(repoRoot)
	if repoRoot == "" || cleanRoot == cleanCwd {
		if id, err := store.ResolveWorkspaceIdentity(cleanCwd); err == nil && id.ActiveDir != "" {
			cleanRoot = filepath.Clean(id.ActiveDir)
		}
	}
	if cleanRoot == "" || cleanRoot == cleanCwd || !strings.HasPrefix(cleanCwd, cleanRoot+string(filepath.Separator)) {
		return ""
	}
	cmd := exec.Command("git", "-C", cleanRoot, "check-ignore", "-q", cleanCwd)
	if err := cmd.Run(); err == nil {
		return fmt.Sprintf("Current directory (%s) is gitignored by parent repository %s and has no .git. Run `sila heal --init-here` to initialize it here, or `git init` then `sila heal` if this is a standalone sub-project.", cleanCwd, cleanRoot)
	}
	return ""
}

// RenderMarkdown formats a SalamBrief into crisp, readable markdown.
func RenderMarkdown(b *SalamBrief) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# sila · salam — %s\n\n", b.Project))
	sb.WriteString(fmt.Sprintf("**Workspace**: `%s`", b.RepoRoot))
	if b.Branch != "" {
		sb.WriteString(fmt.Sprintf(" · **Branch**: `%s`", b.Branch))
	}
	sb.WriteString("\n\n")
	switch b.Wiring.State {
	case "wired":
		sb.WriteString("**Wiring**: connected\n\n")
	case "unwired":
		sb.WriteString("**Wiring**: not provisioned — run `sila heal`\n\n")
	default:
		sb.WriteString("**Wiring**: unknown — run `sila heal --dry-run`\n\n")
	}
	if b.IgnoredSubdirWarning != "" {
		sb.WriteString(fmt.Sprintf("> [!WARNING]\n> %s\n\n", b.IgnoredSubdirWarning))
	}

	sb.WriteString("## The 7-Step Working Contract (Dual CLI & MCP Runbook)\n\n")
	for i, step := range b.Loop {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	sb.WriteString("\n")

	sb.WriteString("## Memory Contract\n\n")
	sb.WriteString(fmt.Sprintf("**%s** — prime, recall, memo, lesson/case, facts verify, gate, handoff.\n\n", b.MemoryContractVersion))
	for _, harness := range []string{"claude", "command-code"} {
		if status := b.HookStatus[harness]; status.Installed {
			sb.WriteString(fmt.Sprintf("✔ **Lifecycle hooks**: %s installed (`%s`).\n", harness, status.Path))
		}
	}
	if b.HookHint != "" {
		sb.WriteString(fmt.Sprintf("▲ **Lifecycle hooks**: none installed for this harness. Run `%s`.\n", b.HookHint))
	}
	sb.WriteString("\n")

	sb.WriteString("## Live Repository State\n\n")

	// Heal
	if b.HealAutomatic == 0 {
		sb.WriteString("✔ **Health**: Configuration is clean and current.\n")
	} else {
		sb.WriteString(fmt.Sprintf("▲ **Health**: %d automatic fix(es) pending → run `sila heal` or MCP `reconcile_drift`.\n", b.HealAutomatic))
		for _, step := range b.HealSteps {
			sb.WriteString(fmt.Sprintf("  - %s\n", step))
		}
	}

	// Harnesses on disk
	if len(b.Harnesses) > 0 {
		sb.WriteString(fmt.Sprintf("✔ **Harnesses on disk**: `%s` (ready to index)\n", strings.Join(b.Harnesses, "`, `")))
	}

	// Semantic Assistance
	switch b.Assistance.Status {
	case "active":
		sb.WriteString(fmt.Sprintf("✔ **Semantic Assistance (JEV)**: Active — %s\n", b.Assistance.Message))
	case "dormant":
		sb.WriteString(fmt.Sprintf("▲ **Semantic Assistance (JEV)**: Dormant — %s\n", b.Assistance.Message))
	default:
		sb.WriteString(fmt.Sprintf("• **Semantic Assistance (JEV)**: Unconfigured — %s\n", b.Assistance.Message))
	}
	sb.WriteString("\n")

	// In progress
	if len(b.Claimed) > 0 {
		sb.WriteString("### In Progress (Claimed)\n\n")
		for _, c := range b.Claimed {
			note := ""
			if c.Note != "" {
				note = fmt.Sprintf(" — _%s_", c.Note)
			}
			sb.WriteString(fmt.Sprintf("- **`%s`** (@%s, %s)%s\n", c.Slug, c.Agent, c.Age, note))
		}
		sb.WriteString("\n")
	}

	// Ready to pick
	if len(b.Ready) > 0 {
		sb.WriteString("### Ready to Claim (Next Actions)\n\n")
		for i, g := range b.Ready {
			if i >= 5 {
				sb.WriteString(fmt.Sprintf("  _...and %d more ready goals (run `sila goals ready` or MCP `list_goals(pending_only=true)`)_\n", len(b.Ready)-i))
				break
			}
			sb.WriteString(fmt.Sprintf("- **`%s`**: %s _(%s)_\n  - CLI: `%s`\n  - MCP: `%s`\n",
				g.Slug, g.Title, g.Mode, g.LaunchCmd, g.MCPToolCall))
		}
		sb.WriteString("\n")
	} else if len(b.Claimed) == 0 {
		sb.WriteString("No ready goals currently queued. Use `sila goals create <slug>` or MCP `create_goal` to scaffold your next work package.\n\n")
	}

	// Last handoff
	if b.MemoryUnavailable {
		sb.WriteString("### Prior Session Handoff\n\n")
		sb.WriteString("> _Memory unavailable: no knowledge store found (run `sila sync` to initialize)._\n\n")
	} else if b.LastHandoff != nil {
		sb.WriteString(fmt.Sprintf("### Prior Session Handoff (@%s · %s)\n\n", b.LastHandoff.Agent, b.LastHandoff.CreatedAt))
		sb.WriteString(fmt.Sprintf("> %s\n\n", b.LastHandoff.Summary))
		if len(b.LastHandoff.OpenItems) > 0 {
			sb.WriteString("**Open Items**:\n")
			for _, item := range b.LastHandoff.OpenItems {
				sb.WriteString(fmt.Sprintf("- [ ] %s\n", item))
			}
			sb.WriteString("\n")
		}
	}

	// PATH Guidance
	sb.WriteString("## Subshell & Environment Guidance\n\n")
	sb.WriteString(fmt.Sprintf("> [!TIP]\n> %s\n", b.SubshellGuide))

	return strings.TrimSpace(sb.String())
}

func detectGitBranch(dir string) string {
	if dir == "" {
		dir = "."
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		b := strings.TrimSpace(string(out))
		if b != "HEAD" {
			return b
		}
	}
	return ""
}

// GlobalRepoSummary holds the compact orientation state for one registered repository.
type GlobalRepoSummary struct {
	Project       string      `json:"project"`
	RepoRoot      string      `json:"repo_root"`
	Connected     bool        `json:"connected"`
	Wiring        SalamWiring `json:"wiring"`
	HealAutomatic int         `json:"heal_automatic"`
	HealJudgment  int         `json:"heal_judgment"`
	ReadyGoals    int         `json:"ready_goals"`
	ClaimedGoals  int         `json:"claimed_goals"`
	HasHandoff    bool        `json:"has_handoff"`
	Brief         *SalamBrief `json:"brief,omitempty"`
}

// GlobalBrief aggregates salam orientation across all registered repositories.
type GlobalBrief struct {
	Global            bool                `json:"global"`
	Scanned           int                 `json:"scanned"`
	Wired             int                 `json:"wired"`
	Unwired           int                 `json:"unwired"`
	NeedsAutoHeal     int                 `json:"needs_auto_heal"`
	ReadyGoalsTotal   int                 `json:"ready_goals_total"`
	ClaimedGoalsTotal int                 `json:"claimed_goals_total"`
	ReposWithReady    int                 `json:"repos_with_ready"`
	ReposWithClaims   int                 `json:"repos_with_claims"`
	ReposWithHandoff  int                 `json:"repos_with_handoff"`
	MissingPathCounts map[string]int      `json:"missing_path_counts,omitempty"`
	Repositories      []GlobalRepoSummary `json:"repositories"`
}

// BuildGlobalBrief scans all registered on-disk repositories (skipping ephemeral
// /tmp directories and deduplicating by canonical real path) and returns an
// aggregated fleet orientation brief.
func BuildGlobalBrief(st *store.Store, full bool) (*GlobalBrief, error) {
	if st == nil {
		return nil, fmt.Errorf("knowledge store unavailable")
	}
	projects, err := st.Projects()
	if err != nil {
		return nil, err
	}
	gb := &GlobalBrief{
		Global:            true,
		MissingPathCounts: make(map[string]int),
	}
	seenPaths := make(map[string]bool)
	for _, p := range projects {
		if p.Path == "" || p.ID == "no-project" || store.IsTempDirPath(p.Path) || !store.IsRepoOrWorkspaceDir(p.Path) {
			continue
		}
		canon := store.CanonicalProjectPath(p.Path)
		if store.IsTempDirPath(canon) || !store.IsRepoOrWorkspaceDir(canon) || seenPaths[canon] {
			continue
		}
		seenPaths[canon] = true
		b, err := BuildBrief(st, p.Path, p.ID)
		if err != nil || b == nil {
			continue
		}
		gb.Scanned++
		if b.Wiring.State == "wired" {
			gb.Wired++
		} else {
			gb.Unwired++
		}
		if b.HealAutomatic > 0 {
			gb.NeedsAutoHeal++
		}
		gb.ReadyGoalsTotal += len(b.Ready)
		gb.ClaimedGoalsTotal += len(b.Claimed)
		if len(b.Ready) > 0 {
			gb.ReposWithReady++
		}
		if len(b.Claimed) > 0 {
			gb.ReposWithClaims++
		}
		if b.LastHandoff != nil {
			gb.ReposWithHandoff++
		}
		for _, mp := range b.Wiring.MissingPaths {
			gb.MissingPathCounts[mp]++
		}
		sum := GlobalRepoSummary{
			Project:       b.Project,
			RepoRoot:      b.RepoRoot,
			Connected:     b.Connected,
			Wiring:        b.Wiring,
			HealAutomatic: b.HealAutomatic,
			HealJudgment:  b.HealJudgment,
			ReadyGoals:    len(b.Ready),
			ClaimedGoals:  len(b.Claimed),
			HasHandoff:    b.LastHandoff != nil,
		}
		if full {
			sum.Brief = b
		}
		gb.Repositories = append(gb.Repositories, sum)
	}
	sort.SliceStable(gb.Repositories, func(i, j int) bool {
		return gb.Repositories[i].Project < gb.Repositories[j].Project
	})
	return gb, nil
}
