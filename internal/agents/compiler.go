package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// coreHubContent is the embedded safe fallback when agents/AGENTS.latest.md
// is not present on disk. It contains only universal rules; repository-specific
// policy lives in opt-in policy packs (see MaintainerPolicyPack). It is also the
// compact baseline for project rule blocks, so maintainer-only lines such as
// release-soak telemetry live in the canonical on-disk hub instead.
const coreHubContent = `# AGENTS.md — Universal Agent Operating Rules (Core Hub)

## 0. Safety Rails (NEVER Do This)
- No unprompted remote push: Local commits are autonomous; remote push requires user consent.
- No unauthorized repo creation or re-homing: NEVER run 'gh repo create' or push external specs to personal namespaces; fail closed on auth hurdles.

## 1. Working Relationship & Discipline
- Concise and direct.
- Terminal Markdown: Keep visible line and table width under 80 chars; no hard breaks inside bullets or links.
- Decision Brief & ` + "`/adoo`" + ` Receipts: Lead with 1-line verdict ("X happens because Y, confirmed by Z"), compact receipt/risk table, file links, and a 1-line next action at the end (never bury actions or questions in prose). No 1-5 scores.
- Verified Repo Runbook Primacy: Never guess commands; verify exit code 0 locally.
- Closed-Loop Goal Attribution (6 Steps) for ` + "`goals/<slug>/`" + `:
  1. Check every completed task ` + "`- [x]`" + ` in ` + "`goals/<slug>/plan.md`" + `.
  2. Write markdown evidence with a standalone ` + "`Verdict: PASS`" + ` line.
  3. Commit with ` + "`(goals/<slug>/goal.md)`" + ` or ` + "`[goal: <slug>]`" + ` under ` + "`feat:`" + `, ` + "`fix:`" + `, ` + "`refactor:`" + `, ` + "`perf:`" + `, or ` + "`chore:`" + ` (` + "`docs:`" + `/` + "`test:`" + ` ignored).
  4. Certify via ` + "`sila goals certify <slug> --evidence=goals/<slug>/evidence.md`" + ` or ` + "`sila goals audit <slug>`" + `.
  5. Run ` + "`sila goals`" + ` to reconcile ` + "`goals/README.md`" + ` and commit receipts.
  6. Verify ` + "`sila goals --json`" + ` reports ` + "`status: \"implemented\"`" + `, ` + "`verification_status: \"verified\"`" + `, ` + "`unverified_count: 0`" + `, and clean ` + "`git status`" + `.
`

// DefaultCoreHubContent is the embedded fallback hub with the shared trigger
// contract appended. agents/AGENTS.latest.md carries the same block verbatim.
var DefaultCoreHubContent = coreHubContent + "\n" + TriggerContract

// PolicyPacks are opt-in rule sets. None is applied unless explicitly requested
// via CompileOptions.PolicyPack: the embedded defaults must not impose one
// maintainer's policies on unrelated adopters.
var PolicyPacks = map[string]string{
	"maintainer": `## Sila Maintainer Policy Pack (opt-in)
- Zero GitHub Workflows / CI Sprawl: All builds, lints, and tests MUST run locally.
- No unapproved package managers: NEVER use npm or yarn. Use aube or pnpm.
- No Python/Bash automation: Helper utilities and CLI tools MUST be written in Go.
- No CGO dependencies: Pure-Go SQLite (modernc.org/sqlite) with CGO_ENABLED=0 only.
- No orphaned or sticky goals: Always append (goals/<slug>/goal.md) to commits and verify with 'sila goals'.
`,
}

// MemoryProtocolChecklist is the compact, gate-linked verb checklist injected
// into managed rules. It is the single source for AGENTS.md, salam, and docs.
var MemoryProtocolChecklist = []string{
	"Memory writes are required by the goal contract and enforced by `sila gate`.",
	"- `sila prime` (start) → briefing + goals.",
	"- `sila recall \"<symptom>\"` (stuck) → prior resolutions.",
	"- `sila memo \"<directive>\" --files=...` (decision) → grounded directive.",
	"- `sila lesson \"<symptom>\" --cause=... --fix=...` (solved) → grounded lesson.",
	"- `sila case \"<intent>\" --approach=... --invariant=...` (pattern) → case record.",
	"- `sila facts verify` (grounding) → file/ledger evidence.",
	"- `sila gate` (before commit) → deterministic quality receipt.",
	"- `sila handoff \"<summary>\" --open=...` (end) → handoff record.",
	"- `sila sync` (after changes) → cross-harness continuity.",
	"- `sila salam`, `sila guide`, `docs/reference.md` → detail.",
}

// MemoryVerbNames is the canonical nine-verb order rendered by the
// post-compaction re-anchor. Keep it in lockstep with MemoryProtocolChecklist.
var MemoryVerbNames = []string{"prime", "recall", "memo", "lesson", "case", "facts", "gate", "handoff", "sync"}

// TriggerRoutingChecklist maps the four high-frequency capability groups to
// concrete CLI and MCP commands. TriggerContract and `sila salam` both render
// this slice, so the routing cannot drift between the rulebook and orientation.
var TriggerRoutingChecklist = []string{
	"- Prior research/specs (BEFORE grep/find): `sila search \"<q>\" --omni` (`omni_search`), `sila search expand <id>` (`get_message_context`), `sila doc search \"<q>\"` (`search_docs`).",
	"- Activity/domains/review/skills: `sila standup last 24h`, `sila digest --since=24h`, `sila recap`; `sila guide` (`get_operational_guidebook`), `sila learn`, `sila persona`, `sila hire --for-goal=<slug>`, `sila audit` (`audit_goal`), `sila skill` (`skill_search`, `skill_load`).",
	"- Symbols/decisions: `sila edit-check --symbol=<sym>`, `sila safe-delete --symbol=<sym>`, `sila note --symbol=<sym>`, `sila decision` (`create_decision`), `sila vision triage`.",
}

// TriggerContract is the compact operational contract written into every
// managed rules block.
var TriggerContract = "## Sila Trigger Contract\n\n" + strings.Join(MemoryProtocolChecklist, "\n") + "\n" +
	strings.Join(TriggerRoutingChecklist, "\n") + "\n"

// TriggerRoutingExpanded returns the routing checklist without leading bullet
// markers, for human orientation surfaces such as `sila salam`.
func TriggerRoutingExpanded() []string {
	out := make([]string, 0, len(TriggerRoutingChecklist))
	for _, line := range TriggerRoutingChecklist {
		out = append(out, strings.TrimPrefix(line, "- "))
	}
	return out
}

// MemoryProtocolExpanded returns the expanded verb matrix used by salam.
func MemoryProtocolExpanded() []string {
	return []string{
		"prime: at task start — `sila prime` — briefing, ready goals, active claims.",
		"recall: when stuck or before web search — `sila recall \"<symptom>\"` — prior resolutions.",
		"memo: when a decision is made — `sila memo \"<directive>\" --files=...` — grounded directive.",
		"lesson: when a problem is solved — `sila lesson \"<symptom>\" --cause=... --fix=... --files=...` — grounded lesson.",
		"case: when choosing a pattern — `sila case \"<intent>\" --approach=... --invariant=...` — case record.",
		"facts verify: before closure — `sila facts verify` — file/ledger evidence.",
		"gate: before commit — `sila gate` — quality receipt.",
		"handoff: at session end — `sila handoff \"<summary>\" --open=...` — handoff record.",
		"sync: after changes — `sila sync` — cross-harness continuity.",
	}
}

// Owned-block markers delimit the content sila manages inside instruction
// files. Everything outside them belongs to the user and is preserved verbatim
// on every regeneration. The begin/end strings are a compatibility contract:
// they must stay byte-for-byte stable so existing repositories and older sila
// binaries keep finding the block.
const (
	OwnedBlockBegin      = "<!-- sila:begin (managed by sila; content inside is regenerated by `sila install` / `sila agents sync`) -->"
	OwnedBlockEnd        = "<!-- sila:end -->"
	OwnedBlockMetaPrefix = "<!-- sila:meta "
	OwnedBlockMetaSuffix = " -->"
)

// ManagedRevision is the schema revision of the sila-managed block. It is not
// the sila release version: it changes only when the block layout changes.
const ManagedRevision = 1

// ManagedScopeCore identifies the core hub block owned by sila inside AGENTS.md.
const ManagedScopeCore = "core"

// CompileOptions specifies parameters for assembling AGENTS.md and tool rules.
type CompileOptions struct {
	RepoRoot          string
	ProjectName       string
	Domains           []string
	IncludeCore       bool
	Preview           bool   // compute the plan without writing any file
	Replace           bool   // allow replacing existing user-owned files (e.g. CLAUDE.md)
	ChangedOnly       bool   // default changed-only behavior; false with Force means repair
	Force             bool   // repair canonical targets and modes even when content matches
	PolicyPack        string // opt-in policy pack name ("", or a PolicyPacks key)
	IncludePrinciples bool   // include Universal Principles Pack (Layered Constitution & 9-verb memory loop)
	Stack             string // target technology stack (e.g. go, ts, cloudflare, python, rust, polyglot)
}

// Compiler manages the Hub-and-Spoke rule assembly.
type Compiler struct {
	agentsDir string
}

// NewCompiler initializes a compiler for the given repository root.
func NewCompiler(repoRoot string) *Compiler {
	return &Compiler{
		agentsDir: FindAgentsDir(repoRoot),
	}
}

// FindAgentsDir searches for the agents definition directory.
func FindAgentsDir(repoRoot string) string {
	if repoRoot != "" {
		localDir := filepath.Join(repoRoot, "agents")
		if fi, err := os.Stat(localDir); err == nil && fi.IsDir() {
			return localDir
		}
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		globalDir := filepath.Join(home, ".local", "share", "sila", "agents")
		if fi, err := os.Stat(globalDir); err == nil && fi.IsDir() {
			return globalDir
		}
	}
	return ""
}

// Compile generates the sila-managed body: Core Hub, domain standards, and
// project overlay. It is the content that lives inside the owned block.
func (c *Compiler) Compile(opts CompileOptions) (string, error) {
	var sb strings.Builder

	// 1. Resolve Project Header
	proj := opts.ProjectName
	if proj == "" && opts.RepoRoot != "" {
		proj = filepath.Base(opts.RepoRoot)
	}
	if proj != "" {
		sb.WriteString(fmt.Sprintf("# AGENTS.md — %s\n\n", proj))
	} else {
		sb.WriteString("# AGENTS.md — Universal Agent Operating Rules\n\n")
	}

	// 2. Core Hub (AGENTS.latest.md) + opt-in policy pack. An unknown pack name
	// is an error: silently ignoring it would apply no policy while the caller
	// believes one was requested.
	coreContent := c.readCoreHub(opts)
	if opts.PolicyPack != "" {
		pack, ok := PolicyPacks[opts.PolicyPack]
		if !ok {
			names := make([]string, 0, len(PolicyPacks))
			for name := range PolicyPacks {
				names = append(names, name)
			}
			sort.Strings(names)
			return "", fmt.Errorf("unknown policy pack %q (available: %s)", opts.PolicyPack, strings.Join(names, ", "))
		}
		coreContent = strings.TrimRight(coreContent, "\n") + "\n\n" + pack
	}
	// Filter Go-specific directives if the target project stack is not Go
	normStack := strings.ToLower(strings.TrimSpace(opts.Stack))
	if normStack == "" && opts.RepoRoot != "" {
		normStack = DetectStack(opts.RepoRoot)
	}
	hasGo := hasGoManifests(opts.RepoRoot)
	if (normStack != "" && normStack != "go" && normStack != "golang" && normStack != "polyglot") ||
		(normStack == "polyglot" && !hasGo) || normStack == "polyglot-no-go" {
		coreContent = FilterRulesForStackWithRoot(coreContent, normStack, opts.RepoRoot)
	}
	// The composed file already carries the project H1; the hub's and the
	// overlay's own titles would duplicate it inside the managed block.
	coreContent = stripLeadingH1(coreContent)
	sb.WriteString(strings.TrimSpace(coreContent))
	sb.WriteString("\n\n---\n\n")

	// 3. Domain standards (e.g. cloudflare, security, ui, testing)
	for _, domain := range opts.Domains {
		domainContent := c.readDomain(domain)
		if domainContent != "" {
			sb.WriteString(fmt.Sprintf("## Domain Standard: %s\n\n", strings.Title(domain)))
			sb.WriteString(strings.TrimSpace(domainContent))
			sb.WriteString("\n\n---\n\n")
		}
	}

	// 3b. Stack-specific standards
	if normStack != "" && normStack != "polyglot" && normStack != "unknown" {
		stackRules := StackRules(normStack)
		if len(stackRules) > 0 {
			sb.WriteString(fmt.Sprintf("## Stack-Specific Standards (%s)\n\n", strings.Title(normStack)))
			for _, r := range stackRules {
				sb.WriteString(fmt.Sprintf("- %s\n", r))
			}
			sb.WriteString("\n---\n\n")
		}
	}

	// 4. Project-specific overlay
	projectContent := c.readProjectOverlay(opts.RepoRoot, proj)
	if projectContent != "" {
		sb.WriteString("## Project-Specific Overrides & Context\n\n")
		sb.WriteString(stripLeadingH1(strings.TrimSpace(projectContent)))
		sb.WriteString("\n")
	}

	// 5. Operational trigger contract: the moments where sila earns its keep.
	// Written once: the hub may already carry the shared block.
	if !hasTriggerContract(coreContent) {
		sb.WriteString("\n" + TriggerContract)
	}

	return strings.TrimRight(sb.String(), "\n"), nil
}

// hasTriggerContract reports whether content already includes the shared
// trigger contract, so the hub and the composed block never duplicate it.
func hasTriggerContract(content string) bool {
	return strings.Contains(content, "## Sila Trigger Contract")
}

// CompileProjectManagedBlock generates the concise project-managed block:
// Section 0 (Safety Rails), Section 1 (Working Relationship & Discipline),
// and the Sila Trigger Contract. It intentionally omits global operating rules,
// stack guidelines, and external maintainer policies that belong in
// ~/.agents/AGENTS.md, keeping project rules well under context budgets.
func (c *Compiler) CompileProjectManagedBlock(opts CompileOptions) (string, error) {
	var sb strings.Builder

	// 1. Resolve Project Header
	proj := opts.ProjectName
	if proj == "" && opts.RepoRoot != "" {
		proj = filepath.Base(opts.RepoRoot)
	}
	if proj != "" {
		sb.WriteString(fmt.Sprintf("# AGENTS.md — %s\n\n", proj))
	} else {
		sb.WriteString("# AGENTS.md — Universal Agent Operating Rules\n\n")
	}

	// 2. Focused Core Safety & Discipline
	sb.WriteString(strings.TrimSpace(stripLeadingH1(DefaultCoreHubContent)))
	sb.WriteString("\n\n---\n\n")

	// 2b. Stack-specific standards (if detected/specified)
	normStack := strings.ToLower(strings.TrimSpace(opts.Stack))
	if normStack == "" && opts.RepoRoot != "" {
		normStack = DetectStack(opts.RepoRoot)
	}
	if normStack != "" && normStack != "polyglot" && normStack != "unknown" {
		stackRules := StackRules(normStack)
		if len(stackRules) > 0 {
			sb.WriteString(fmt.Sprintf("## Stack-Specific Standards (%s)\n\n", strings.Title(normStack)))
			for _, r := range stackRules {
				sb.WriteString(fmt.Sprintf("- %s\n", r))
			}
			sb.WriteString("\n---\n\n")
		}
	}

	// 3. Operational Trigger Contract (once; the fallback hub carries it too)
	if !hasTriggerContract(DefaultCoreHubContent) {
		sb.WriteString(TriggerContract)
	}

	return strings.TrimRight(sb.String(), "\n"), nil
}

func (c *Compiler) readCoreHub(opts CompileOptions) string {
	if isSilaSourceRepo(opts.RepoRoot) || opts.PolicyPack != "" {
		if c.agentsDir != "" {
			p := filepath.Join(c.agentsDir, "AGENTS.latest.md")
			if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
				return string(data)
			}
		}
	} else if opts.RepoRoot != "" {
		localHub := filepath.Join(opts.RepoRoot, "agents", "AGENTS.latest.md")
		if data, err := os.ReadFile(localHub); err == nil && len(data) > 0 {
			return string(data)
		}
	}
	return DefaultCoreHubContent
}

// isSilaSourceRepo reports whether the directory is the sila source repository.
func isSilaSourceRepo(repoRoot string) bool {
	if repoRoot == "" {
		return false
	}
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

// FilterRulesForStack removes language-specific directives that do not apply to the target stack.
func FilterRulesForStack(content, stack string) string {
	return FilterRulesForStackWithRoot(content, stack, ".")
}

// FilterRulesForStackWithRoot removes language-specific directives that do not apply to the target stack,
// with repository context to check whether Go manifests exist in polyglot projects.
func FilterRulesForStackWithRoot(content, stack, repoRoot string) string {
	norm := strings.ToLower(strings.TrimSpace(stack))
	if norm == "" || norm == "go" || norm == "golang" {
		return content
	}
	if norm == "polyglot" && hasGoManifests(repoRoot) {
		return content
	}

	lines := strings.Split(content, "\n")
	type block struct {
		isBullet  bool
		isHeader  bool
		isSep     bool
		headerLvl int
		lines     []string
		text      string
	}

	var blocks []block
	var curBlock *block

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isBulletStart := strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || (len(trimmed) > 3 && trimmed[0] >= '0' && trimmed[0] <= '9' && trimmed[1] == '.' && trimmed[2] == ' ')
		isH := strings.HasPrefix(trimmed, "#")
		isSep := trimmed == "---" || trimmed == "***" || trimmed == "___"

		if isBulletStart {
			if curBlock != nil {
				blocks = append(blocks, *curBlock)
			}
			curBlock = &block{
				isBullet: true,
				lines:    []string{line},
				text:     trimmed,
			}
		} else if isH {
			if curBlock != nil {
				blocks = append(blocks, *curBlock)
			}
			lvl := 0
			for _, r := range trimmed {
				if r == '#' {
					lvl++
				} else {
					break
				}
			}
			curBlock = &block{
				isHeader:  true,
				headerLvl: lvl,
				lines:     []string{line},
				text:      trimmed,
			}
		} else if isSep {
			if curBlock != nil {
				blocks = append(blocks, *curBlock)
			}
			curBlock = &block{
				isSep: true,
				lines: []string{line},
				text:  trimmed,
			}
		} else if trimmed == "" {
			if curBlock != nil {
				blocks = append(blocks, *curBlock)
				curBlock = nil
			}
			blocks = append(blocks, block{lines: []string{line}})
		} else {
			// Continuation line
			if curBlock != nil && curBlock.isBullet {
				curBlock.lines = append(curBlock.lines, line)
				curBlock.text += " " + trimmed
			} else {
				if curBlock == nil {
					curBlock = &block{
						lines: []string{line},
						text:  trimmed,
					}
				} else {
					curBlock.lines = append(curBlock.lines, line)
					curBlock.text += " " + trimmed
				}
			}
		}
	}
	if curBlock != nil {
		blocks = append(blocks, *curBlock)
	}

	// Filter out Go-specific bullet blocks
	var keptBlocks []block
	for _, b := range blocks {
		if b.isBullet && isGoSpecificRule(b.text) {
			continue
		}
		keptBlocks = append(keptBlocks, b)
	}

	// Filter out empty headers (headers that have no non-empty content before next header or EOF)
	// and redundant consecutive separators.
	var finalLines []string
	for i := 0; i < len(keptBlocks); i++ {
		b := keptBlocks[i]
		if b.isHeader {
			hasContent := false
			for j := i + 1; j < len(keptBlocks); j++ {
				next := keptBlocks[j]
				if next.isHeader && next.headerLvl <= b.headerLvl {
					break
				}
				if !next.isSep && strings.TrimSpace(next.text) != "" {
					hasContent = true
					break
				}
			}
			if !hasContent {
				continue
			}
		}
		if b.isSep {
			if len(finalLines) > 0 && strings.TrimSpace(finalLines[len(finalLines)-1]) == "---" {
				continue
			}
		}
		finalLines = append(finalLines, b.lines...)
	}

	result := strings.Join(finalLines, "\n")
	for strings.Contains(result, "---\n\n---") {
		result = strings.ReplaceAll(result, "---\n\n---", "---")
	}
	for strings.Contains(result, "\n\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n\n", "\n\n\n")
	}
	return result
}

func isGoSpecificRule(line string) bool {
	low := strings.ToLower(line)
	if strings.Contains(low, "cgo_enabled") ||
		strings.Contains(low, "modernc.org/sqlite") ||
		strings.Contains(low, "pure-go sqlite") ||
		strings.Contains(low, "must be written in go") ||
		strings.Contains(low, "mandatory language for scripts") ||
		strings.Contains(low, "no cgo dependencies") ||
		strings.Contains(low, "go is the mandatory language") ||
		strings.Contains(low, "no python or bash automation scripts") ||
		strings.Contains(low, "no python/bash sprawl") ||
		strings.Contains(low, "do not write python or bash scripts") {
		return true
	}
	return false
}

// DetectStack inspects repository manifests to determine the project stack.
func DetectStack(repoRoot string) string {
	if repoRoot == "" {
		return ""
	}
	hasGo := hasGoManifests(repoRoot)
	hasRust := fileExists(filepath.Join(repoRoot, "Cargo.toml"))
	hasPython := fileExists(filepath.Join(repoRoot, "pyproject.toml")) || fileExists(filepath.Join(repoRoot, "requirements.txt")) || fileExists(filepath.Join(repoRoot, "setup.py"))
	hasCF := fileExists(filepath.Join(repoRoot, "wrangler.jsonc")) || fileExists(filepath.Join(repoRoot, "wrangler.toml"))
	hasAstro := fileExists(filepath.Join(repoRoot, "astro.config.mjs")) || fileExists(filepath.Join(repoRoot, "astro.config.ts"))
	hasNode := false
	pkgPath := filepath.Join(repoRoot, "package.json")
	if fileExists(pkgPath) {
		hasNode = true
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

	nonGoCount := 0
	if hasNode {
		nonGoCount++
	}
	if hasPython {
		nonGoCount++
	}
	if hasRust {
		nonGoCount++
	}

	switch {
	case hasGo && nonGoCount > 0:
		return "polyglot"
	case !hasGo && nonGoCount > 1:
		return "polyglot-no-go"
	case hasAstro:
		return "astro"
	case hasCF && !hasGo:
		return "cloudflare"
	case hasNode && !hasGo:
		return "node"
	case hasGo && nonGoCount == 0:
		return "go"
	case hasPython && !hasGo:
		return "python"
	case hasRust && !hasGo:
		return "rust"
	default:
		return "unknown"
	}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func hasGoManifests(dir string) bool {
	if dir == "" {
		return false
	}
	return fileExists(filepath.Join(dir, "go.mod")) || fileExists(filepath.Join(dir, "go.work"))
}

func (c *Compiler) readDomain(name string) string {
	if c.agentsDir == "" || name == "" {
		return ""
	}
	filename := fmt.Sprintf("AGENTS.%s.md", strings.ToLower(name))
	p := filepath.Join(c.agentsDir, filename)
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(data)
}

func (c *Compiler) readProjectOverlay(repoRoot, projectName string) string {
	// First check agents/AGENTS.<project>.md in agentsDir
	if c.agentsDir != "" && projectName != "" {
		candidates := []string{
			filepath.Join(c.agentsDir, fmt.Sprintf("AGENTS.%s.md", projectName)),
			filepath.Join(c.agentsDir, fmt.Sprintf("AGENTS.%s.md", strings.ToLower(projectName))),
		}
		for _, cand := range candidates {
			if data, err := os.ReadFile(cand); err == nil && len(data) > 0 {
				return string(data)
			}
		}
	}

	// The root AGENTS.md is never a source of overlay text: whatever the file
	// holds outside sila's markers is preserved verbatim by ComposeAgentsFile,
	// and re-ingesting it here would both duplicate it and drop it on the next
	// run (when the block is regenerated from a compile that no longer sees it).
	return ""
}

// ListDomains enumerates available domain overlays in agents/.
func (c *Compiler) ListDomains() []string {
	if c.agentsDir == "" {
		return nil
	}
	entries, err := os.ReadDir(c.agentsDir)
	if err != nil {
		return nil
	}
	var domains []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "AGENTS.") && strings.HasSuffix(name, ".md") {
			sub := strings.TrimSuffix(strings.TrimPrefix(name, "AGENTS."), ".md")
			// Exclude non-domain files
			if sub == "latest" || sub == "global" {
				continue
			}
			domains = append(domains, sub)
		}
	}
	return domains
}
