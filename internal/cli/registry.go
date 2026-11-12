package cli

import (
	"strings"
)

// SubcommandSpec describes a known subcommand path under a top-level command.
type SubcommandSpec struct {
	Path     []string `json:"path"`
	Synopsis string   `json:"synopsis,omitempty"`
}

// CommandSpec is the canonical metadata entry for one Sila CLI command.
type CommandSpec struct {
	Name              string           `json:"name"`
	Aliases           []string         `json:"aliases,omitempty"`
	HelpAliases       []string         `json:"help_aliases,omitempty"`
	DeprecatedAliases []string         `json:"deprecated_aliases,omitempty"`
	Category          string           `json:"category"`
	VisibleTier       string           `json:"visible_tier"` // core|standard|advanced|hidden
	Synopsis          string           `json:"synopsis"`
	Usage             string           `json:"usage"`
	Subcommands       []SubcommandSpec `json:"subcommands,omitempty"`
	AcceptsPositional bool             `json:"accepts_positional,omitempty"`
	Mutating          bool             `json:"mutating"`
	Tier              string           `json:"tier"` // query|mutation|destructive
	DocsAnchor        string           `json:"docs_anchor,omitempty"`
	Deprecated        string           `json:"deprecated,omitempty"`
	Replacement       string           `json:"replacement,omitempty"`
}

// FlagSpec describes a CLI flag and its arity, type, and scope.
type FlagSpec struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	HelpSyntax  string   `json:"help_syntax,omitempty"`
	CoreOption  bool     `json:"core_option,omitempty"`
	Arity       int      `json:"arity"` // 0 = bool, 1 = value
	Type        string   `json:"type"`  // bool|string|int
	Global      bool     `json:"global"`
	Scope       []string `json:"scope,omitempty"`
	Description string   `json:"description,omitempty"`
	SafetyClass string   `json:"safety_class"` // none|control|destructive
	Dest        string   `json:"dest,omitempty"`
}

// Resolved holds the static resolution result for an argv vector.
type Resolved struct {
	Command           CommandSpec `json:"command"`
	AliasUsed         string      `json:"alias_used,omitempty"`
	Subcommand        []string    `json:"subcommand,omitempty"`
	UnknownSubcommand string      `json:"unknown_subcommand,omitempty"`
	Positionals       int         `json:"positionals"`
	ExtraPositionals  int         `json:"extra_positionals,omitempty"`
	Flags             []string    `json:"flags,omitempty"`
	UnknownFlags      []string    `json:"unknown_flags,omitempty"`
	KnownPath         bool        `json:"known_path"`
}

// Registry provides immutable, concurrency-safe lookup of commands, aliases,
// subcommand paths, and flags.
type Registry struct {
	commands []CommandSpec
	byName   map[string]CommandSpec
	flags    []FlagSpec
	byFlag   map[string]FlagSpec
}

var defaultRegistry = buildDefaultRegistry()

// Default returns the singleton immutable CLI registry.
func Default() *Registry { return defaultRegistry }

// All returns a copy of all canonical CommandSpec entries in deterministic order.
func All() []CommandSpec { return defaultRegistry.All() }

// Lookup looks up a command by canonical name or alias.
func Lookup(name string) (CommandSpec, bool) { return defaultRegistry.Lookup(name) }

// Resolve statically resolves argv without executing any command, opening any DB,
// or touching the filesystem/network.
func Resolve(argv []string) (Resolved, bool) { return defaultRegistry.Resolve(argv) }

// CanonicalAccepts reports whether argv begins with a known canonical command or
// alias and resolves to a valid canonical path.
func CanonicalAccepts(argv []string) bool { return defaultRegistry.CanonicalAccepts(argv) }

// Flags returns a copy of all canonical FlagSpec entries in deterministic order.
func Flags() []FlagSpec { return defaultRegistry.Flags() }

// LookupFlag looks up a flag by canonical name or alias (with or without leading dashes).
func LookupFlag(name string) (FlagSpec, bool) { return defaultRegistry.LookupFlag(name) }

// All returns a copy of all canonical CommandSpec entries in deterministic order.
func (r *Registry) All() []CommandSpec {
	out := make([]CommandSpec, len(r.commands))
	copy(out, r.commands)
	return out
}

// Lookup resolves a command or alias name to its canonical CommandSpec.
func (r *Registry) Lookup(name string) (CommandSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return CommandSpec{}, false
	}
	spec, ok := r.byName[key]
	return spec, ok
}

// Flags returns a copy of all FlagSpec entries in deterministic order.
func (r *Registry) Flags() []FlagSpec {
	out := make([]FlagSpec, len(r.flags))
	copy(out, r.flags)
	return out
}

// LookupFlag looks up a flag by name or alias (normalized without leading `-` or `--`).
func (r *Registry) LookupFlag(name string) (FlagSpec, bool) {
	key := normalizeFlagKey(name)
	if key == "" {
		return FlagSpec{}, false
	}
	spec, ok := r.byFlag[key]
	return spec, ok
}

func normalizeFlagKey(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.IndexByte(s, '='); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimLeft(s, "-")
}

// CanonicalAccepts reports whether argv resolves to a valid canonical command path.
func (r *Registry) CanonicalAccepts(argv []string) bool {
	res, ok := r.Resolve(argv)
	return ok && res.KnownPath
}

// Resolve statically classifies an argv slice into command, alias, subcommand path,
// positional count, and flags. `--` terminates flag scanning; `--` before a
// command returns (_, false).
func (r *Registry) Resolve(argv []string) (Resolved, bool) {
	if len(argv) == 0 {
		return Resolved{}, false
	}

	var flags []string
	var unknownFlags []string
	var pos []string
	cmdToken := ""
	flagsTerminated := false

	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if !flagsTerminated && tok == "--" {
			if cmdToken == "" {
				return Resolved{}, false
			}
			flagsTerminated = true
			continue
		}

		if !flagsTerminated && strings.HasPrefix(tok, "-") && tok != "-" {
			hasEq := strings.Contains(tok, "=")
			fSpec, known := r.LookupFlag(tok)
			flags = append(flags, tok)
			if !known {
				unknownFlags = append(unknownFlags, tok)
				continue
			}
			if fSpec.Arity == 1 && !hasEq && i+1 < len(argv) {
				i++
				flags = append(flags, argv[i])
			}
			continue
		}

		if cmdToken == "" {
			cmdToken = tok
			continue
		}
		pos = append(pos, tok)
	}

	if cmdToken == "" {
		return Resolved{}, false
	}
	spec, ok := r.Lookup(cmdToken)
	if !ok {
		return Resolved{}, false
	}

	res := Resolved{
		Command:      spec,
		Flags:        flags,
		UnknownFlags: unknownFlags,
		KnownPath:    true,
	}
	if !strings.EqualFold(cmdToken, spec.Name) {
		res.AliasUsed = cmdToken
	}

	if len(pos) == 0 {
		return res, true
	}

	if len(spec.Subcommands) > 0 {
		first := strings.ToLower(pos[0])
		for _, sub := range spec.Subcommands {
			if len(sub.Path) >= 2 && strings.EqualFold(sub.Path[1], first) {
				res.Subcommand = append([]string(nil), sub.Path...)
				res.Positionals = len(pos) - 1
				return res, true
			}
		}
		if spec.AcceptsPositional {
			res.Positionals = len(pos)
			res.KnownPath = true
		} else {
			res.UnknownSubcommand = pos[0]
			res.Positionals = len(pos)
			res.ExtraPositionals = len(pos)
			res.KnownPath = false
		}
		return res, true
	}

	res.Positionals = len(pos)
	if !spec.AcceptsPositional {
		res.ExtraPositionals = len(pos)
		res.KnownPath = false
	}
	return res, true
}

func subSpecs(cmd string, names ...string) []SubcommandSpec {
	out := make([]SubcommandSpec, 0, len(names))
	for _, n := range names {
		out = append(out, SubcommandSpec{Path: []string{cmd, n}})
	}
	return out
}

func buildDefaultRegistry() *Registry {
	const (
		catLoop     = "Agent Continuity & Working Loop"
		catMemory   = "Knowledge, Recall & Memory"
		catRoadmap  = "Roadmap, Governance & Multi-Agent"
		catPersonas = "Personas, Audits & Skill Authoring"
		catGates    = "Quality Gates, Verification & Health"
		catSetup    = "Repository Setup, Rules & Onboarding"
		catStore    = "Sync, Ingestion & Store Operations"
		catSurfaces = "Surfaces, Services & Integrations"
	)

	commands := []CommandSpec{
		// Agent Continuity & Working Loop
		{Name: "salam", Aliases: []string{"hi", "hello", "merhaba", "salaam"}, HelpAliases: []string{"hi", "hello", "merhaba"}, Category: catLoop, VisibleTier: "core", Usage: "[--assist] [--json]", Synopsis: "Agent front door: 7-step working loop, ready goals & active claims", AcceptsPositional: true, Tier: "query"},
		{Name: "workspace", Category: catLoop, VisibleTier: "standard", Usage: "status [--json]", Synopsis: "Resolve the active project identity from a workspace root, child repo, or subdirectory", Subcommands: subSpecs("workspace", "status"), Tier: "query"},
		{Name: "prime", Category: catLoop, VisibleTier: "core", Usage: "[--top=N] [--budget=<tokens>]", Synopsis: "Session-start context briefing with declarative token compression", AcceptsPositional: true, Tier: "query"},
		{Name: "preflight", Category: catLoop, VisibleTier: "standard", Usage: "status|set off|warn|enforce [--user]", Synopsis: "Inspect or set the Command Code preflight policy and marker validation", Subcommands: subSpecs("preflight", "status", "set"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "handoff", Category: catLoop, VisibleTier: "core", Usage: "[<summary>] [--open=...]", Synopsis: "View or record structured session handoffs & open items", Subcommands: subSpecs("handoff", "list", "show"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "standup", Aliases: []string{"recap"}, DeprecatedAliases: []string{"recap"}, Category: catLoop, VisibleTier: "standard", Usage: "[last <window>] [-g]", Synopsis: "Human morning standup: decisions, commits shipped, open items", Subcommands: subSpecs("standup", "last"), AcceptsPositional: true, Tier: "query"},
		{Name: "hook", Category: catLoop, VisibleTier: "standard", Usage: "[session-start|session-end]", Synopsis: "Execute agent lifecycle hooks & git pre-commit quality gates", Subcommands: subSpecs("hook", "session-start", "session-end", "stop", "pre-tool-use", "post-tool-use", "pre-compact", "post-compaction", "pre-commit", "pre-push", "install", "uninstall", "status", "drain", "serve"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "session", Category: catLoop, VisibleTier: "standard", Usage: "start|status|env|end|list|prune", Synopsis: "Resolve, export, bind, and track local agent session identity", Subcommands: subSpecs("session", "start", "status", "env", "end", "list", "prune"), Mutating: true, Tier: "mutation"},
		{Name: "memory", Category: catLoop, VisibleTier: "standard", Usage: "add|validate|show|list|digest|archive|restore|history|report|prune", Synopsis: "Typed sila/memory/v1 envelopes, reversible archive/restore lifecycle, validation, digests, and adoption reports", Subcommands: subSpecs("memory", "add", "validate", "show", "list", "digest", "archive", "restore", "history", "report", "prune"), Mutating: true, Tier: "mutation"},
		{Name: "intro", Category: catLoop, VisibleTier: "standard", Usage: "[--agent=<name>]", Synopsis: "Agent onboarding guide & AGENTS.md template block", AcceptsPositional: true, Tier: "query"},

		// Knowledge, Recall & Memory
		{Name: "search", Category: catMemory, VisibleTier: "core", Usage: "<query> [--omni] | expand <id>", Synopsis: "Search knowledge: omni-search, messages, claims, or docs", Subcommands: subSpecs("search", "expand"), AcceptsPositional: true, Tier: "query"},
		{Name: "recall", Category: catMemory, VisibleTier: "core", Usage: "\"<symptom|error>\" [--top=N]", Synopsis: "Fast cross-project solution lookup before web search", AcceptsPositional: true, Tier: "query"},
		{Name: "doc", Category: catMemory, VisibleTier: "standard", Usage: "<url|file|search|pkg|sites>", Synopsis: "Ingest, crawl, search, serve, or query external docs & packages", Subcommands: subSpecs("doc", "list", "search", "get", "sites", "web-search", "code-search", "pkg", "so", "serve", "export"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "memo", Category: catMemory, VisibleTier: "core", Usage: "add|list|show [--text=] [--since=] [--limit=N] [--claim=] [--json]", Synopsis: "Record, list, or show architectural directives; 'memo list' never writes", Subcommands: subSpecs("memo", "add", "list", "ls", "show", "get"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "lesson", Category: catMemory, VisibleTier: "core", Usage: "add|list|show|distill [--since=] [--json]", Synopsis: "Record, browse, show, or distill bug-cause-fix solution lessons", Subcommands: subSpecs("lesson", "add", "list", "show", "distill"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "case", Aliases: []string{"cases"}, HelpAliases: []string{"cases"}, Category: catMemory, VisibleTier: "standard", Usage: "add|list|show [--since=] [--json]", Synopsis: "Record, browse, or show architectural procedural cases", Subcommands: subSpecs("case", "add", "list", "show"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "facts", Category: catMemory, VisibleTier: "standard", Usage: "[verify|show <id>|list]", Synopsis: "Cryptographic SHA-256 hash-chained fact ledger audit", Subcommands: subSpecs("facts", "verify", "show", "list", "hygiene", "rebaseline"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "guide", Category: catMemory, VisibleTier: "standard", Usage: "[domain] [--format=...] [--out=]", Synopsis: "Compile and export living Developer Operational Guidebook (10 domains)", AcceptsPositional: true, Tier: "query"},
		{Name: "learn", Category: catMemory, VisibleTier: "standard", Usage: "[list|add|export] [domain]", Synopsis: "Record, list, and export distilled operational learnings & guardrails", Subcommands: subSpecs("learn", "list", "add", "export"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},

		// Roadmap, Governance & Multi-Agent
		{Name: "goals", Aliases: []string{"goal"}, Category: catRoadmap, VisibleTier: "core", Usage: "[claim|release|ready|review]", Synopsis: "Roadmap matrix, claims, commit matching & Plannotator review", Subcommands: subSpecs("goals", "claim", "release", "ready", "review", "create", "update", "depend", "tasks", "certify", "audit", "validate", "readiness", "reopen", "supersede", "prune", "stats", "focus", "active", "report", "status", "commit", "attribute", "contract"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "graph", Category: catMemory, VisibleTier: "standard", Usage: "build | report | query | slice | path | affected | stats | doctor", Synopsis: "Build, audit, query, slice, blast-radius, and inspect the code + memory graph", Subcommands: subSpecs("graph", "build", "report", "query", "slice", "path", "affected", "stats", "doctor"), AcceptsPositional: true, Tier: "query"},
		{Name: "vision", Category: catRoadmap, VisibleTier: "standard", Usage: "[compile|triage|add-nongoal]", Synopsis: "Strategic North Star, VISION.md compiler & proposal triage", Subcommands: subSpecs("vision", "compile", "triage", "add-nongoal", "add-milestone", "list"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "approve", Aliases: []string{"approval"}, HelpAliases: []string{"approval"}, Category: catRoadmap, VisibleTier: "standard", Usage: "[<id>] [--reject] [--pending]", Synopsis: "Human-in-the-loop approval steering for autonomous AI agents", Subcommands: subSpecs("approve", "list", "request", "show"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "adr", Category: catRoadmap, VisibleTier: "standard", Usage: "[new|list|compile|doctor]", Synopsis: "Author, compile, ground, and doctor Architecture Decision Records", Subcommands: subSpecs("adr", "new", "list", "compile", "doctor", "from-memo"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "decision", Aliases: []string{"decisions"}, Category: catRoadmap, VisibleTier: "standard", Usage: "[create|resolve|list|view|check]", Synopsis: "Epistemic addressed decision log (D#), options & collision check", Subcommands: subSpecs("decision", "create", "resolve", "list", "view", "check"), Mutating: true, Tier: "mutation"},
		{Name: "opinions", Category: catRoadmap, VisibleTier: "standard", Usage: "[compile|add|list] [--toon]", Synopsis: "Living technical taste ledger & OPINIONS.md compilation", Subcommands: subSpecs("opinions", "compile", "add", "list"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "feedback", Category: catRoadmap, VisibleTier: "standard", Usage: "[<msg>] [--type=...] [--issue]", Synopsis: "Closed-loop feedback ticket ledger for agent suggestions & issues", Subcommands: subSpecs("feedback", "list", "resolve", "drain", "submit"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "crew", Category: catRoadmap, VisibleTier: "advanced", Usage: "[spawn|list|status|kill]", Synopsis: "First Mate crew orchestrator: tmux sessions with zero-token supervision", Subcommands: subSpecs("crew", "spawn", "list", "status", "kill", "plan-lanes"), Mutating: true, Tier: "destructive"},
		{Name: "worktree", Category: catRoadmap, VisibleTier: "advanced", Usage: "[create|list|clean|run]", Synopsis: "Disposable Git worktree sandboxes with context seeding & gates", Subcommands: subSpecs("worktree", "create", "list", "clean", "run"), Mutating: true, Tier: "destructive"},
		{Name: "man", Category: catRoadmap, VisibleTier: "standard", Usage: "[topic] [--search=<term>] [--json]", Synopsis: "Read the embedded manual pages", AcceptsPositional: true, Tier: "help"},
		{Name: "daemon", Category: catRoadmap, VisibleTier: "standard", Usage: "start|stop|status [--foreground] [--socket=<path>]", Synopsis: "Run the local Unix-socket daemon with one store and a proxied MCP path", AcceptsPositional: true, Tier: "local"},
		{Name: "commits", Category: catRoadmap, VisibleTier: "standard", Usage: "[--since=<dur>] [--json] | show <sha>", Synopsis: "Commit-to-session receipts from the local store", AcceptsPositional: true, Tier: "query"},
		{Name: "digest", Category: catRoadmap, VisibleTier: "standard", Usage: "[--activity] [--since=24h]", Synopsis: "Multi-agent activity digest & session ledger for AI coding agents", AcceptsPositional: true, Tier: "query"},
		{Name: "benchmark", Category: catRoadmap, VisibleTier: "advanced", Usage: "[--min-sessions=N] [--toon]", Synopsis: "Cross-agent empirical scorecard: grounding rate & goal attribution", AcceptsPositional: true, Tier: "query"},
		{Name: "pause", Category: catRoadmap, VisibleTier: "standard", Usage: "[--global] [--reason=<text>]", Synopsis: "Pause hook and MCP capture; reads stay available", Mutating: true, Tier: "mutation"},
		{Name: "resume", Category: catRoadmap, VisibleTier: "standard", Usage: "[--global]", Synopsis: "Resume capture and clear a forget exclusion", Mutating: true, Tier: "mutation"},
		{Name: "forget", Category: catRoadmap, VisibleTier: "advanced", Usage: "[--include-transcripts] [--yes]", Synopsis: "Archive captured knowledge and exclude it from reads", Mutating: true, Tier: "destructive"},

		// Personas, Audits & Skill Authoring
		{Name: "persona", Aliases: []string{"personas"}, Category: catPersonas, VisibleTier: "advanced", Usage: "list [--group=ID] | get <id|num>", Synopsis: "Modular persona registry & edge vault across 31 specialist personas", Subcommands: subSpecs("persona", "list", "get", "show"), AcceptsPositional: true, Tier: "query"},
		{Name: "hire", Category: catPersonas, VisibleTier: "advanced", Usage: "group <id> | <1,4,6,3,8> [url]", Synopsis: "Autonomous multi-persona hiring, group audits, and goal package synthesis", Subcommands: subSpecs("hire", "group", "assess"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "reports", Aliases: []string{"report"}, Category: catPersonas, VisibleTier: "standard", Usage: "[list] | show <id>", Synopsis: "Browse, inspect, and trace persona evaluation reports in .sila/reports/", Subcommands: subSpecs("reports", "list", "show"), AcceptsPositional: true, Tier: "query"},
		{Name: "audit", Category: catPersonas, VisibleTier: "advanced", Usage: "web <url> | squad | gap", Synopsis: "Autonomous multi-agent groupthink audit & Sila goals synthesis", Subcommands: subSpecs("audit", "web", "squad", "gap"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "skill", Aliases: []string{"skills", "skill-create", "skills-create"}, HelpAliases: []string{"skills", "skill-create", "skills-create"}, Category: catPersonas, VisibleTier: "standard", Usage: "preview | apply | remove | assess | list | show | create", Synopsis: "Project skill governance, filtering, assessment & bespoke skill authoring", Subcommands: subSpecs("skill", "preview", "apply", "remove", "assess", "list", "show", "create"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "plugins", Aliases: []string{"plugin"}, HelpAliases: []string{"plugin"}, Category: catPersonas, VisibleTier: "standard", Usage: "list | init | install | uninstall | link | unlink | lint", Synopsis: "Manage standalone Go plugins and tools", Subcommands: subSpecs("plugins", "list", "init", "install", "uninstall", "link", "unlink", "lint"), Mutating: true, Tier: "mutation"},

		// Quality Gates, Verification & Health
		{Name: "gate", Category: catGates, VisibleTier: "standard", Usage: "[pipeline|install|coverage]", Synopsis: "Local quality gate pipeline: pre-commit/pre-push & sentries", Subcommands: subSpecs("gate", "pipeline", "install", "coverage", "report-card"), AcceptsPositional: true, Mutating: true, Tier: "query"},
		{Name: "doctor", Category: catGates, VisibleTier: "core", Usage: "[--repair] [--edge] [--skills]", Synopsis: "End-to-end SQLite integrity, index sync, skill bloat & test audit", AcceptsPositional: true, Mutating: true, Tier: "query"},
		{Name: "verify", Aliases: []string{"reconcile"}, Category: catGates, VisibleTier: "standard", Usage: "[<project>] [--dry-run] [--json]", Synopsis: "Re-verify referenced files on disk and detect stale claims", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "soak", Category: catGates, VisibleTier: "standard", Usage: "[--json|--toon]", Synopsis: "Release soak progress and the exact actions still needed", Tier: "query"},
		{Name: "reportcard", Aliases: []string{"report-card"}, HelpAliases: []string{"report-card"}, Category: catGates, VisibleTier: "standard", Usage: "[--json] [--toon]", Synopsis: "Local Go Report Card analyzer (gofmt, govet, gocyclo, misspell)", Tier: "query"},
		{Name: "coverage", Category: catGates, VisibleTier: "standard", Usage: "[--strict] [--profile=path]", Synopsis: "Evaluate test statement coverage against package quality thresholds", Tier: "query"},
		{Name: "eval", Category: catGates, VisibleTier: "advanced", Usage: "[run|ablate|init|synthesize]", Synopsis: "Universal epistemic evaluation, multi-harness runner & ablation", Subcommands: subSpecs("eval", "run", "ablate", "init", "synthesize", "adoption"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},

		// Repository Setup, Rules & Onboarding
		{Name: "install", Category: catSetup, VisibleTier: "standard", Usage: "[--tools=...] [--no-hooks]", Synopsis: "Provision agent rules, git quality gates, and tool configs", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "agents", Category: catSetup, VisibleTier: "standard", Usage: "[check|sync|compile|review] [--all]", Synopsis: "Hub-and-spoke agent SOP rule compiler, drift auditor & multi-project sync", Subcommands: subSpecs("agents", "check", "sync", "compile", "review", "list"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "onboard", Category: catSetup, VisibleTier: "standard", Usage: "[check|--convert-tasks]", Synopsis: "Inventory legacy task systems and verify sila participation", Subcommands: subSpecs("onboard", "check", "defer"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "heal", Category: catSetup, VisibleTier: "standard", Usage: "[--dry-run] [--json]", Synopsis: "Bring repository wiring, duplicate registrations & claims current", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "export", Category: catSetup, VisibleTier: "standard", Usage: "[rules] [--target=...]", Synopsis: "Dump messages/claims as JSON or compile agent rules", Subcommands: subSpecs("export", "rules", "opinions", "vision"), AcceptsPositional: true, Tier: "query"},

		// Sync, Ingestion & Store Operations
		{Name: "import", Category: catStore, VisibleTier: "standard", Usage: "[--from=claude|codex|grok|all] [--project=] [--dry-run|--yes] [--json|--toon]", Synopsis: "Import durable memory files from Claude Code, Codex, and Grok; dry-run by default", Mutating: true, Tier: "mutation"},
		{Name: "sync", Category: catStore, VisibleTier: "standard", Usage: "[--project=] [--since=] [--full]", Synopsis: "Ingest from all tools + distill claims with live progress preview", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "live", Aliases: []string{"watch"}, Category: catStore, VisibleTier: "advanced", Usage: "[--interval=15s] [--compact]", Synopsis: "Continuous live 9-box mission control cockpit & background sync", AcceptsPositional: true, Tier: "query"},
		{Name: "dream", Aliases: []string{"compact"}, Category: catStore, VisibleTier: "standard", Usage: "[<project>] [--llm=...]", Synopsis: "Distill claims and atomic facts from messages (query, reconcile)", Subcommands: subSpecs("dream", "query", "reconcile", "compact"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "status", Category: catStore, VisibleTier: "standard", Usage: "[--scan]", Synopsis: "Sync health dashboard, tool metrics, and stale claim detection", Tier: "query"},
		{Name: "ingest", Category: catStore, VisibleTier: "standard", Usage: "[<tool>|all] [--since=...]", Synopsis: "Incremental ingestion of normalized messages from AI CLI tools", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "assess", Category: catStore, VisibleTier: "standard", Usage: "[<tool>|all]", Synopsis: "Dry-run preview of what ingest would pull in from local tools", AcceptsPositional: true, Tier: "query"},
		{Name: "projects", Category: catStore, VisibleTier: "standard", Usage: "[merge <keep> <drop> | rename <project> <new-id>]", Synopsis: "Catalog indexed repositories and claim counts; merge path aliases or correct an id", Subcommands: subSpecs("projects", "merge", "rename"), Mutating: true, Tier: "destructive"},
		{Name: "overlaps", Category: catStore, VisibleTier: "advanced", Usage: "[--project=...]", Synopsis: "List cross-tool overlaps and co-occurrence", Tier: "query"},
		{Name: "store", Category: catStore, VisibleTier: "standard", Usage: "[status|smoke|verify|migrate]", Synopsis: "Inspect store metrics, verify invariants, and run Smoke A/B sandbox", Subcommands: subSpecs("store", "status", "smoke", "verify", "migrate"), Mutating: true, Tier: "mutation"},
		{Name: "storage", Category: catStore, VisibleTier: "advanced", Usage: "[--json]", Synopsis: "Show disk footprint: sila's store plus each source tool's data", Tier: "query"},
		{Name: "clean", Category: catStore, VisibleTier: "advanced", Usage: "[--global|--repo] [-y]", Synopsis: "Universal multi-toolchain disk hygiene and cache pruning", AcceptsPositional: true, Mutating: true, Tier: "destructive"},
		{Name: "vacuum", Category: catStore, VisibleTier: "advanced", Usage: "[--stats] [--force]", Synopsis: "Defragment SQLite store, reclaim free pages, and optimize planner", Mutating: true, Tier: "destructive"},
		{Name: "backup", Category: catStore, VisibleTier: "standard", Usage: "[--out=<path>]", Synopsis: "Write a consistent snapshot of the knowledge store", AcceptsPositional: true, Mutating: true, Tier: "mutation"},

		// Surfaces, Services & Integrations
		{Name: "ui", Aliases: []string{"web"}, Category: catSurfaces, VisibleTier: "standard", Usage: "[--port=7452] [--no-open]", Synopsis: "Interactive local web cockpit for decisions & knowledge navigation", AcceptsPositional: true, Tier: "query"},
		{Name: "demo", Category: catSurfaces, VisibleTier: "standard", Usage: "[--step=<list>] [--keep] [--web]", Synopsis: "7-stage continuity walkthrough; --web opens the Web Cockpit", Tier: "query"},
		{Name: "mcp", Category: catSurfaces, VisibleTier: "core", Usage: "[--profile=continuity|full] [--print-schema] [--trace-args]", Synopsis: "Start Model Context Protocol (MCP) JSON-RPC stdio server, print its schema, or trace argument metadata", Tier: "query"},
		{Name: "cloud", Category: catSurfaces, VisibleTier: "advanced", Usage: "[status|pull|push]", Synopsis: "D1 edge bridge: probe connectivity, drain cloud mailbox", Subcommands: subSpecs("cloud", "status", "pull", "push"), Mutating: true, Tier: "mutation"},
		{Name: "gateway", Category: catSurfaces, VisibleTier: "advanced", Usage: "[status|test|send]", Synopsis: "Pluggable external relay: Discord, Telegram, Gemini, and MCP", Subcommands: subSpecs("gateway", "status", "test", "send"), Mutating: true, Tier: "mutation"},
		{Name: "sources", Category: catSurfaces, VisibleTier: "standard", Usage: "[--verbose] [--json]", Synopsis: "Discover installed AI tools on this host: log paths & setup guidance", AcceptsPositional: true, Tier: "query"},
		{Name: "connect", Category: catSurfaces, VisibleTier: "standard", Usage: "[list|add|verify|demo|remove] [<harness>]", Synopsis: "Connect, verify, and remove sila MCP in a second harness", Subcommands: subSpecs("connect", "list", "add", "verify", "demo", "remove"), Mutating: true, Tier: "mutation"},
		{Name: "cards", Category: catSurfaces, VisibleTier: "standard", Usage: "[list|show] [<card-id|session-id>] [--limit=N]", Synopsis: "Inspect typed session cards written at session end", Subcommands: subSpecs("cards", "list", "show"), Tier: "query"},
		{Name: "config", Category: catSurfaces, VisibleTier: "standard", Usage: "[set|get|path|explain]", Synopsis: "Manage settings, API keys, search engine credentials & doc sites", Subcommands: subSpecs("config", "set", "get", "path", "explain", "list"), Mutating: true, Tier: "mutation"},
		{Name: "changelog", Category: catSurfaces, VisibleTier: "standard", Usage: "[<ver>] [--timeline] [--full] [--scan]", Synopsis: "Display Sila release history or synthesize repo release notes", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "upgrade", Aliases: []string{"update"}, HelpAliases: []string{"update"}, Category: catSurfaces, VisibleTier: "standard", Usage: "[--force] [--check]", Synopsis: "Self-upgrade sila binary to latest release", Mutating: true, Tier: "destructive"},
		{Name: "release", Category: catSurfaces, VisibleTier: "standard", Usage: "[status|preflight|check|snapshot|metadata]", Synopsis: "Release status, readiness scorecard, compatibility checks, snapshots & metadata", Subcommands: subSpecs("release", "status", "preflight", "check", "snapshot", "metadata"), Mutating: true, Tier: "mutation"},
		{Name: "credits", Category: catSurfaces, VisibleTier: "standard", Usage: "[<query>] [--toon|--json]", Synopsis: "Display project credits, research inspirations, and AXI attribution", AcceptsPositional: true, Tier: "query"},
		{Name: "completions", Category: catSurfaces, VisibleTier: "standard", Usage: "[bash|zsh|fish]", Synopsis: "Print a shell completion script", Subcommands: subSpecs("completions", "bash", "zsh", "fish"), Tier: "query"},

		// Additional dispatched commands (hidden from primary help groups to preserve help layout contract)
		{Name: "note", Aliases: []string{"notes"}, Category: catMemory, VisibleTier: "hidden", Usage: "[add|list|sync] [--symbol=<sym>]", Synopsis: "Symbol-pinned architectural notes and invariants", Subcommands: subSpecs("note", "add", "list", "sync", "show"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "edit-check", Aliases: []string{"editcheck"}, Category: catGates, VisibleTier: "hidden", Usage: "[--symbol=<sym>]", Synopsis: "Inspect symbol blast radius and contract callers before editing", AcceptsPositional: true, Tier: "query"},
		{Name: "safe-delete", Aliases: []string{"safedelete"}, Category: catGates, VisibleTier: "hidden", Usage: "[--symbol=<sym>]", Synopsis: "Verify symbol has zero live callers before deletion", AcceptsPositional: true, Tier: "query"},
		{Name: "test", Category: catGates, VisibleTier: "hidden", Usage: "[--run]", Synopsis: "Select targeted tests for modified symbols and packages", AcceptsPositional: true, Tier: "query"},
		{Name: "stats", Category: catStore, VisibleTier: "hidden", Usage: "[project] [--plain] [--json]", Synopsis: "One-shot snapshot of the 9-box agent mission control cockpit", AcceptsPositional: true, Tier: "query"},
		{Name: "adoption", Category: catLoop, VisibleTier: "hidden", Usage: "[report] [--json]", Synopsis: "7-day tool-call adoption funnel and advisory verdict", Subcommands: subSpecs("adoption", "report", "eval"), AcceptsPositional: true, Tier: "query"},
		{Name: "subagent", Category: catRoadmap, VisibleTier: "hidden", Usage: "[charter|status|verify]", Synopsis: "Signed subagent continuity charters and path-lane enforcement", Subcommands: subSpecs("subagent", "charter", "status", "verify", "list"), AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "unslop", Category: catGates, VisibleTier: "hidden", Usage: "[--check] [--fix] [--diff]", Synopsis: "Detect and strip conversational AI slop and filler", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "silence", Category: catGates, VisibleTier: "hidden", Usage: "[status|list]", Synopsis: "Diagnostic silence sentry and cluster tracker", Subcommands: subSpecs("silence", "status", "list", "clear"), AcceptsPositional: true, Tier: "query"},

		// Reserved CCLI commands (R2.5: registered in internal/cli before goal 4 adds behavior)
		{Name: "do", Category: catLoop, VisibleTier: "hidden", Usage: "<phrase...>", Synopsis: "Execute an explicit conversational CLI phrase", AcceptsPositional: true, Mutating: true, Tier: "mutation"},
		{Name: "ccli", Category: catLoop, VisibleTier: "hidden", Usage: "explain|rewrite|list", Synopsis: "Inspect, explain, rewrite, or list conversational CLI phrases", Subcommands: subSpecs("ccli", "explain", "rewrite", "list"), Tier: "query"},
	}

	flags := buildCanonicalFlagSpecs()

	byName := make(map[string]CommandSpec, len(commands)*2)
	for _, c := range commands {
		byName[strings.ToLower(c.Name)] = c
		for _, a := range c.Aliases {
			byName[strings.ToLower(a)] = c
		}
	}

	byFlag := make(map[string]FlagSpec, len(flags)*2)
	for _, f := range flags {
		byFlag[f.Name] = f
		for _, a := range f.Aliases {
			byFlag[a] = f
		}
	}

	return &Registry{
		commands: commands,
		byName:   byName,
		flags:    flags,
		byFlag:   byFlag,
	}
}

func buildCanonicalFlagSpecs() []FlagSpec {
	return []FlagSpec{
		// Global & Machine/Control Flags (including reserved CCLI controls per R2.7)
		{Name: "project", Aliases: []string{"p"}, HelpSyntax: "-p, --project=<name>", CoreOption: true, Arity: 1, Type: "string", Global: true, Description: "Scope command to a specific project directory or ID", SafetyClass: "none", Dest: "project"},
		{Name: "global", Aliases: []string{"g"}, HelpSyntax: "-g, --global", Arity: 0, Type: "bool", Global: true, Description: "Run operation globally across all indexed projects", SafetyClass: "none", Dest: "global"},
		{Name: "json", HelpSyntax: "    --json", CoreOption: true, Arity: 0, Type: "bool", Global: true, Description: "Emit machine-readable JSON output (for agents & scripting)", SafetyClass: "control", Dest: "json"},
		{Name: "json-envelope", Arity: 0, Type: "bool", Global: true, Description: "Emit the versioned sila/json/v1 envelope on stdout (implies --json)", SafetyClass: "control", Dest: "json-envelope"},
		{Name: "toon", HelpSyntax: "    --toon", Arity: 0, Type: "bool", Global: true, Description: "Emit compact Token-Oriented Object Notation (~40% token savings)", SafetyClass: "control", Dest: "toon"},
		{Name: "quiet", Arity: 0, Type: "bool", Global: true, Description: "Suppress human hints and success decoration; errors and exit codes are preserved", SafetyClass: "control", Dest: "quiet"},
		{Name: "porcelain", Arity: 0, Type: "bool", Global: true, Description: "Emit stable line-oriented output", SafetyClass: "control", Dest: "porcelain"},
		{Name: "verbose", Aliases: []string{"v"}, HelpSyntax: "-v, --verbose", CoreOption: true, Arity: 0, Type: "bool", Global: true, Scope: []string{"*"}, Description: "Display detailed output, inspection cards, and file checks", SafetyClass: "none", Dest: "verbose"},
		{Name: "dry-run", Aliases: []string{"preview", "n"}, HelpSyntax: "-n, --dry-run", CoreOption: true, Arity: 0, Type: "bool", Global: true, Description: "Preview changes without writing files to disk", SafetyClass: "control", Dest: "dry-run"},
		{Name: "since", HelpSyntax: "    --since=<time>", Arity: 1, Type: "string", Global: true, Description: "Filter by duration (e.g. 24h, 7d) or RFC3339 timestamp", SafetyClass: "none", Dest: "since"},
		{Name: "db", HelpSyntax: "    --db=<path>", Arity: 1, Type: "string", Global: true, Description: "Knowledge store database path (default ~/.local/share/sila/sila.db)", SafetyClass: "none", Dest: "db"},
		{Name: "no-color", HelpSyntax: "    --no-color", CoreOption: true, Arity: 0, Type: "bool", Global: true, Description: "Disable colored terminal output", SafetyClass: "control", Dest: "no-color"},
		{Name: "help", Aliases: []string{"h"}, HelpSyntax: "-h, --help", CoreOption: true, Arity: 0, Type: "bool", Global: true, Description: "Show help for sila or any subcommand", SafetyClass: "control", Dest: "help"},
		{Name: "version", Aliases: []string{"V"}, HelpSyntax: "-V, --version", CoreOption: true, Arity: 0, Type: "bool", Global: true, Description: "Print version information and exit", SafetyClass: "control", Dest: "version"},
		{Name: "yes", Aliases: []string{"y"}, Arity: 0, Type: "bool", Global: true, Description: "Confirm action without interactive confirmation prompt", SafetyClass: "destructive", Dest: "yes"},
		{Name: "force", Aliases: []string{"f"}, Arity: 0, Type: "bool", Global: true, Description: "Force operation or bypass non-critical checks", SafetyClass: "destructive", Dest: "force"},
		{Name: "no-ccli", Arity: 0, Type: "bool", Global: true, Description: "Disable conversational CLI phrase rewriting for this invocation", SafetyClass: "control", Dest: "no-ccli"},
		{Name: "ccli-strict-exit", Arity: 0, Type: "bool", Global: true, Description: "Enforce strict CCLI exit codes", SafetyClass: "control", Dest: "ccli-strict-exit"},
		{Name: "no-input", Arity: 0, Type: "bool", Global: true, Description: "Fail closed instead of prompting on interactive confirmation", SafetyClass: "control", Dest: "no-input"},
		{Name: "confirm", Arity: 1, Type: "string", Global: true, Description: "Explicit target confirmation token for destructive operations", SafetyClass: "destructive", Dest: "confirm"},

		// Scoped & Command-Specific Flags in newRootFlags
		{Name: "sync", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Allow machine-readable reads to reconcile goals/README.md", SafetyClass: "none"},
		{Name: "relative-paths", Arity: 0, Type: "bool", Scope: []string{"*"}, Description: "Emit repository-relative paths in JSON", SafetyClass: "none"},
		{Name: "advisory", Arity: 0, Type: "bool", Scope: []string{"onboard check"}, Description: "Report check failures without a non-zero exit", SafetyClass: "control"},
		{Name: "human", Arity: 0, Type: "bool", Scope: []string{"digest"}, Description: "Render human executive briefing", SafetyClass: "none"},
		{Name: "include-transcripts", Arity: 0, Type: "bool", Scope: []string{"forget"}, Description: "Also exclude stored messages from search and recall", SafetyClass: "none"},
		{Name: "as-of", Arity: 1, Type: "string", Scope: []string{"search", "recall"}, Description: "Point-in-time view using YYYY-MM-DD or RFC3339", SafetyClass: "none"},
		{Name: "post-commit", Arity: 0, Type: "bool", Scope: []string{"gate"}, Description: "Install the managed post-commit hook that records commit receipts", SafetyClass: "none"},
		{Name: "trust-target", Arity: 1, Type: "string", Scope: []string{"gate"}, Description: "Trust one full 40-character commit SHA for this gate run", SafetyClass: "none"},
		{Name: "show-config", Arity: 0, Type: "bool", Scope: []string{"gate"}, Description: "Print the trust identity, targets, and sensitive set, then exit", SafetyClass: "none"},
		{Name: "activity", Arity: 0, Type: "bool", Scope: []string{"digest", "standup"}, Description: "Show recent session activity across tools", SafetyClass: "none"},
		{Name: "tool", Aliases: []string{"t"}, Arity: 1, Type: "string", Scope: []string{"dream", "search", "mcp", "digest", "standup"}, Description: "Filter by contributing tool", SafetyClass: "none"},
		{Name: "role", Arity: 1, Type: "string", Scope: []string{"search", "crew"}, Description: "Filter messages by role or crew role", SafetyClass: "none"},
		{Name: "refs-exist", Aliases: []string{"verified-only"}, Arity: 0, Type: "bool", Scope: []string{"dream", "search"}, Description: "Only show claims whose referenced files exist on disk", SafetyClass: "none"},
		{Name: "facts-only", Arity: 0, Type: "bool", Scope: []string{"dream", "search"}, Description: "Only show promoted verified facts", SafetyClass: "none"},
		{Name: "stale", Aliases: []string{"unverified"}, Arity: 0, Type: "bool", Scope: []string{"dream", "search", "goals release"}, Description: "Only show stale claims or release stale goal claims", SafetyClass: "none"},
		{Name: "llm", Arity: 1, Type: "string", Scope: []string{"dream"}, Description: "Distill pending claims with an LLM", SafetyClass: "none"},
		{Name: "retry-failed", Arity: 0, Type: "bool", Scope: []string{"dream"}, Description: "Retry failed LLM distillation attempts", SafetyClass: "none"},
		{Name: "top", Arity: 1, Type: "int", Scope: []string{"dream", "search", "recall", "prime", "doc", "hook", "feedback", "digest", "standup", "changelog", "graph"}, Description: "Limit query/search results", SafetyClass: "none"},
		{Name: "include-ambiguous", Arity: 0, Type: "bool", Scope: []string{"graph"}, Description: "Allow AMBIGUOUS edges in graph path queries", SafetyClass: "none"},
		{Name: "code-only", Arity: 0, Type: "bool", Scope: []string{"graph"}, Description: "Restrict graph path, affected, and build to code nodes", SafetyClass: "none"},
		{Name: "if-stale", Arity: 0, Type: "bool", Scope: []string{"graph"}, Description: "Skip graph build when the graph is already current", SafetyClass: "none"},
		{Name: "max-hops", Arity: 1, Type: "int", Scope: []string{"graph"}, Description: "Maximum hops for graph path search", SafetyClass: "none"},
		{Name: "max-depth", Arity: 1, Type: "int", Scope: []string{"graph"}, Description: "Maximum depth for graph query and blast radius", SafetyClass: "none"},
		{Name: "scan", Aliases: []string{"s"}, Arity: 0, Type: "bool", Scope: []string{"status", "changelog"}, Description: "Live-scan tool sources or commits", SafetyClass: "none"},
		{Name: "min-sessions", Arity: 1, Type: "int", Scope: []string{"benchmark"}, Description: "Minimum project sessions to include in benchmark", SafetyClass: "none"},
		{Name: "tag-range", Aliases: []string{"range"}, Arity: 1, Type: "string", Scope: []string{"changelog"}, Description: "Git tag range for changelog", SafetyClass: "none"},
		{Name: "tag", Arity: 1, Type: "string", Scope: []string{"changelog", "doc", "eval"}, Description: "Tag filter or changelog tag range", SafetyClass: "none"},
		{Name: "write", Aliases: []string{"w"}, Arity: 0, Type: "bool", Scope: []string{"changelog"}, Description: "Write changes to CHANGELOG.md", SafetyClass: "none"},
		{Name: "unreleased", Aliases: []string{"u"}, Arity: 0, Type: "bool", Scope: []string{"changelog"}, Description: "Scan unreleased commits for changelog", SafetyClass: "none"},
		{Name: "agent", Arity: 1, Type: "string", Global: true, Description: "Author or target agent identifier", SafetyClass: "none"},
		{Name: "type", Aliases: []string{"category"}, Arity: 1, Type: "string", Scope: []string{"feedback", "opinions"}, Description: "Feedback or opinion category", SafetyClass: "none"},
		{Name: "id", Arity: 1, Type: "string", Scope: []string{"feedback"}, Description: "Explicit feedback ticket id", SafetyClass: "none"},
		{Name: "branch", Arity: 1, Type: "string", Scope: []string{"handoff", "gate", "recall"}, Description: "Active git branch name", SafetyClass: "none"},
		{Name: "open", Arity: 1, Type: "string", Scope: []string{"handoff", "feedback"}, Description: "Open items list (handoff) or open filter (feedback)", SafetyClass: "none"},
		{Name: "closed", Arity: 0, Type: "bool", Scope: []string{"feedback"}, Description: "Only list closed feedback", SafetyClass: "none"},
		{Name: "decisions", Arity: 1, Type: "string", Scope: []string{"handoff"}, Description: "Comma-separated list of key decisions", SafetyClass: "none"},
		{Name: "files", Arity: 1, Type: "string", Scope: []string{"handoff", "memo", "lesson", "case", "adr", "goals", "graph"}, Description: "Comma-separated list of referenced repo files", SafetyClass: "none"},
		{Name: "file", Arity: 1, Type: "string", Scope: []string{"memory"}, Description: "JSON memory envelope file (- reads stdin)", SafetyClass: "none"},
		{Name: "title", Arity: 1, Type: "string", Scope: []string{"doc", "learn", "goals"}, Description: "Document, learning, or goal title", SafetyClass: "none"},
		{Name: "deciders", Arity: 1, Type: "string", Scope: []string{"adr"}, Description: "Decision participants / agent name", SafetyClass: "none"},
		{Name: "decision", Arity: 1, Type: "string", Scope: []string{"memo", "decision"}, Description: "Addressed decision identifier (e.g. D1)", SafetyClass: "none"},
		{Name: "lang", Arity: 1, Type: "string", Scope: []string{"doc"}, Description: "Programming language filter", SafetyClass: "none"},
		{Name: "repo", Aliases: []string{"r"}, Arity: 1, Type: "string", Scope: []string{"clean", "doc"}, Description: "Scope cleanup to active repo (clean bool) or repo filter (doc string)", SafetyClass: "none"},
		{Name: "ext", Arity: 1, Type: "string", Scope: []string{"doc"}, Description: "File extension filter", SafetyClass: "none"},
		{Name: "eco", Arity: 1, Type: "string", Scope: []string{"doc"}, Description: "Package registry ecosystem", SafetyClass: "none"},
		{Name: "crawl", Arity: 0, Type: "bool", Scope: []string{"doc"}, Description: "Recursively crawl and ingest documentation site", SafetyClass: "none"},
		{Name: "depth", Arity: 1, Type: "int", Scope: []string{"doc"}, Description: "Max crawl depth", SafetyClass: "none"},
		{Name: "max-pages", Arity: 1, Type: "int", Scope: []string{"doc"}, Description: "Max pages to crawl", SafetyClass: "none"},
		{Name: "limit", Arity: 1, Type: "int", Scope: []string{"cloud", "memo", "memory"}, Description: "Maximum items to pull or list", SafetyClass: "none"},
		{Name: "auto-ingest", Arity: 0, Type: "bool", Scope: []string{"doc"}, Description: "Automatically save search results", SafetyClass: "none"},
		{Name: "auto-sync", Arity: 0, Type: "bool", Scope: []string{"hook"}, Description: "Trigger sync on session end", SafetyClass: "none"},
		{Name: "watch", Aliases: []string{"live"}, Arity: 0, Type: "bool", Scope: []string{"sync", "goals"}, Description: "Run continuous live synchronization loop", SafetyClass: "none"},
		{Name: "interval", Arity: 1, Type: "string", Scope: []string{"sync", "live", "watch", "goals"}, Description: "Polling interval for live sync", SafetyClass: "none"},
		{Name: "compact", Arity: 0, Type: "bool", Scope: []string{"sync", "live", "watch", "changelog", "skill"}, Description: "Compact ticker output", SafetyClass: "none"},
		{Name: "line", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Print goal stats as a single line", SafetyClass: "none"},
		{Name: "plain", Arity: 0, Type: "bool", Scope: []string{"stats", "goals"}, Description: "Disable colors and ANSI styling", SafetyClass: "none"},
		{Name: "ascii", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Replace emoji with stable ASCII tags and bars", SafetyClass: "none"},
		{Name: "changed-only", Arity: 0, Type: "bool", Scope: []string{"agents"}, Description: "Write only changed targets", SafetyClass: "none"},
		{Name: "adoption", Arity: 0, Type: "bool", Scope: []string{"doctor"}, Description: "Show 7-day tool-call adoption funnel", SafetyClass: "none"},
		{Name: "pending", Arity: 0, Type: "bool", Scope: []string{"goals", "approve"}, Description: "Only list pending goals or approvals", SafetyClass: "none"},
		{Name: "claimed", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Only list claimed/in-progress goals", SafetyClass: "none"},
		{Name: "claims", Arity: 0, Type: "bool", Scope: []string{"goals prune"}, Description: "Prune claim records on implemented or superseded goals", SafetyClass: "none"},
		{Name: "older-than", Arity: 1, Type: "string", Scope: []string{"goals release", "memory prune", "session prune"}, Description: "Minimum age threshold", SafetyClass: "none"},
		{Name: "convert-tasks", Arity: 0, Type: "bool", Scope: []string{"onboard"}, Description: "Convert a markdown task list into a backlog goal", SafetyClass: "none"},
		{Name: "from", Arity: 1, Type: "string", Scope: []string{"onboard", "import"}, Description: "Source markdown file for --convert-tasks (onboard) or memory origin for import", SafetyClass: "none"},
		{Name: "again", Arity: 0, Type: "bool", Scope: []string{"import"}, Description: "Resend changed files under a fresh import key", SafetyClass: "none"},
		{Name: "force-similar", Arity: 0, Type: "bool", Scope: []string{"import"}, Description: "Write near-duplicate imported memories instead of skipping them", SafetyClass: "none"},
		{Name: "include-instructions", Arity: 0, Type: "bool", Scope: []string{"import"}, Description: "Refused instruction import (IMPORT_INSTRUCTIONS_REFUSED)", SafetyClass: "control"},
		{Name: "max-files", Arity: 1, Type: "int", Scope: []string{"import"}, Description: "Maximum files to scan in one import run, 1-200", SafetyClass: "control"},
		{Name: "strict", Arity: 0, Type: "bool", Scope: []string{"onboard", "gate", "coverage", "release", "goals"}, Description: "Strict failure mode", SafetyClass: "control"},
		{Name: "require-tests", Arity: 0, Type: "bool", Scope: []string{"gate"}, Description: "Treat a skipped test run as a blocking gate failure", SafetyClass: "control"},
		{Name: "width", Arity: 1, Type: "int", Scope: []string{"salam", "prime"}, Description: "Override rendered width in columns", SafetyClass: "none"},
		{Name: "strict-memory", Arity: 0, Type: "bool", Scope: []string{"goals validate"}, Description: "Promote legacy Memory Protocol warnings to errors", SafetyClass: "control"},
		{Name: "no-memory", Arity: 0, Type: "bool", Scope: []string{"goals certify"}, Description: "Waive required memory evidence", SafetyClass: "control"},
		{Name: "text", Arity: 1, Type: "string", Scope: []string{"memo"}, Description: "Literal memo text", SafetyClass: "none"},
		{Name: "claim-text", Arity: 1, Type: "string", Scope: []string{"memo list"}, Description: "Deprecated alias for --claim=<text>", SafetyClass: "none"},
		{Name: "policy", Arity: 1, Type: "string", Scope: []string{"hook"}, Description: "Stop-gate policy: off, warn, or enforce", SafetyClass: "control"},
		{Name: "enforce", Arity: 0, Type: "bool", Scope: []string{"hook"}, Description: "Enforce the stop memory gate", SafetyClass: "control"},
		{Name: "issue", Aliases: []string{"github", "gh"}, Arity: 0, Type: "bool", Scope: []string{"feedback"}, Description: "Submit feedback upstream to GitHub Issues", SafetyClass: "none"},
		{Name: "share", Aliases: []string{"d1"}, Arity: 0, Type: "bool", Scope: []string{"feedback"}, Description: "Share feedback via Cloudflare D1", SafetyClass: "none"},
		{Name: "token", Arity: 1, Type: "string", Scope: []string{"feedback drain"}, Description: "Authentication token for edge feedback drain", SafetyClass: "none"},
		{Name: "remote", Arity: 1, Type: "string", Scope: []string{"feedback", "gate install"}, Description: "Remote edge URL (feedback) or git remote proxy (gate)", SafetyClass: "none"},
		{Name: "ready", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Only list unblocked goals ready to pick", SafetyClass: "none"},
		{Name: "blocked", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Only list goals blocked by prerequisites", SafetyClass: "none"},
		{Name: "timeline", Aliases: []string{"oneline"}, Arity: 0, Type: "bool", Scope: []string{"changelog"}, Description: "Display compact release matrix timeline", SafetyClass: "none"},
		{Name: "all", Aliases: []string{"a"}, Arity: 0, Type: "bool", Scope: []string{"clean", "agents", "changelog", "goals", "guide"}, Description: "Scope across all items/targets/releases/goals", SafetyClass: "none"},
		{Name: "implemented", Aliases: []string{"done"}, Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Only list implemented goals", SafetyClass: "none"},
		{Name: "independent", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Mark or filter goal as independent", SafetyClass: "none"},
		{Name: "dependent", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Mark goal as dependent on prerequisites", SafetyClass: "none"},
		{Name: "desc", Aliases: []string{"description"}, Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Goal description or summary", SafetyClass: "none"},
		{Name: "depends-on", Aliases: []string{"deps", "set"}, Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Comma-separated goal slugs this goal depends on", SafetyClass: "none"},
		{Name: "add-dep", Aliases: []string{"add"}, Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Comma-separated goal slugs to add as dependencies", SafetyClass: "none"},
		{Name: "remove-dep", Aliases: []string{"remove"}, Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Comma-separated goal slugs to remove from dependencies", SafetyClass: "none"},
		{Name: "seq", Aliases: []string{"sequence"}, Arity: 1, Type: "int", Scope: []string{"goals"}, Description: "Recommended sequence index for execution", SafetyClass: "none"},
		{Name: "touch", Aliases: []string{"files-to-touch"}, Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Comma-separated target file or directory paths to touch", SafetyClass: "none"},
		{Name: "shape", Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Task shape: ship or scout", SafetyClass: "none"},
		{Name: "note", Arity: 1, Type: "string", Scope: []string{"goals", "feedback", "memory"}, Description: "Progress report note, status update, or archive note", SafetyClass: "none"},
		{Name: "goal", Arity: 1, Type: "string", Scope: []string{"goals", "feedback", "hire", "session", "decision"}, Description: "Goal slug or ID", SafetyClass: "none"},
		{Name: "goals", Arity: 1, Type: "string", Scope: []string{"crew", "hire"}, Description: "Candidate goal slug (crew) or audit squad coverage (hire)", SafetyClass: "none"},
		{Name: "session", Arity: 1, Type: "string", Scope: []string{"goals", "hook", "session"}, Description: "Agent session ID", SafetyClass: "none"},
		{Name: "status", Arity: 1, Type: "string", Scope: []string{"goals", "feedback", "decision"}, Description: "Structured status filter or value", SafetyClass: "none"},
		{Name: "tier", Arity: 1, Type: "string", Scope: []string{"goals", "ccli list"}, Description: "Goal tier or CCLI safety tier filter", SafetyClass: "none"},
		{Name: "allow", Arity: 1, Type: "string", Scope: []string{"goals claim"}, Description: "Comma-separated authorized mutation paths", SafetyClass: "none"},
		{Name: "deny", Arity: 1, Type: "string", Scope: []string{"goals claim"}, Description: "Comma-separated boundary paths to forbid", SafetyClass: "none"},
		{Name: "duration", Arity: 1, Type: "string", Scope: []string{"goals claim"}, Description: "Auto-release duration", SafetyClass: "none"},
		{Name: "invariants", Aliases: []string{"invariant"}, Arity: 1, Type: "string", Scope: []string{"goals create", "case"}, Description: "Architectural invariants and constraints", SafetyClass: "none"},
		{Name: "tasks", Aliases: []string{"task"}, Arity: 1, Type: "string", Scope: []string{"goals create", "crew", "recall"}, Description: "Execution tasks, candidate goal slug, or recall task description", SafetyClass: "none"},
		{Name: "work-package", Arity: 1, Type: "string", Scope: []string{"goals create"}, Description: "Path to work package JSON contract", SafetyClass: "none"},
		{Name: "spec-review", Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Path to spec review JSON contract", SafetyClass: "none"},
		{Name: "review", Arity: 0, Type: "bool", Scope: []string{"goals readiness", "export", "agents", "onboard"}, Description: "Preview proposed updates or import spec review", SafetyClass: "none"},
		{Name: "selection", Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Receipt selection artifact path", SafetyClass: "none"},
		{Name: "auto-run", Arity: 0, Type: "bool", Scope: []string{"goals certify"}, Description: "Automatically run task verification commands", SafetyClass: "none"},
		{Name: "cause", Arity: 1, Type: "string", Scope: []string{"lesson", "learn"}, Description: "Root cause of a solved problem", SafetyClass: "none"},
		{Name: "fix", Arity: 1, Type: "string", Scope: []string{"lesson", "learn", "unslop"}, Description: "What fixed the problem (lesson) or auto-fix (unslop)", SafetyClass: "none"},
		{Name: "diff", Arity: 0, Type: "bool", Scope: []string{"unslop"}, Description: "Display unified diff of proposed changes", SafetyClass: "none"},
		{Name: "approach", Arity: 1, Type: "string", Scope: []string{"case"}, Description: "Structural or design pattern approach", SafetyClass: "none"},
		{Name: "tags", Arity: 1, Type: "string", Scope: []string{"lesson", "case", "learn"}, Description: "Comma-separated tags", SafetyClass: "none"},
		{Name: "domain", Arity: 1, Type: "string", Scope: []string{"guide", "learn", "ccli list"}, Description: "Operational domain category", SafetyClass: "none"},
		{Name: "symptom", Arity: 1, Type: "string", Scope: []string{"learn"}, Description: "Symptom or failure mode", SafetyClass: "none"},
		{Name: "rule", Arity: 1, Type: "string", Scope: []string{"learn"}, Description: "Durable guardrail invariant rule", SafetyClass: "none"},
		{Name: "stack", Aliases: []string{"tech-stack"}, Arity: 1, Type: "string", Scope: []string{"learn", "agents"}, Description: "Technology stack", SafetyClass: "none"},
		{Name: "principles", Arity: 0, Type: "bool", Scope: []string{"agents"}, Description: "Include Universal Principles Pack", SafetyClass: "none"},
		{Name: "state-block", Arity: 0, Type: "bool", Scope: []string{"agents"}, Description: "Write the continuity state block into AGENTS.md", SafetyClass: "none"},
		{Name: "save", Arity: 0, Type: "bool", Scope: []string{"lesson distill"}, Description: "Save distilled lessons into store", SafetyClass: "none"},
		{Name: "reason", Aliases: []string{"reopen-reason"}, Arity: 1, Type: "string", Scope: []string{"goals", "onboard", "approve", "facts", "memory"}, Description: "Reason or remediation explanation, or archive reason", SafetyClass: "none"},
		{Name: "claim", Arity: 0, Type: "bool", Scope: []string{"goals reopen"}, Description: "Immediately claim goal upon reopening", SafetyClass: "none"},
		{Name: "stage", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Automatically git add goals/README.md on sync", SafetyClass: "none"},
		{Name: "commit", Aliases: []string{"m", "message", "c"}, Arity: 1, Type: "string", Scope: []string{"goals"}, Description: "Git commit message or hash", SafetyClass: "none"},
		{Name: "no-build", Arity: 0, Type: "bool", Scope: []string{"goals commit"}, Description: "Skip rebuild step during two-stage goal commit", SafetyClass: "none"},
		{Name: "check-tasks", Arity: 0, Type: "bool", Scope: []string{"goals commit"}, Description: "Auto-check unchecked non-attribution plan tasks", SafetyClass: "none"},
		{Name: "no-sync", Arity: 0, Type: "bool", Scope: []string{"goals"}, Description: "Audit goals without modifying goals/README.md", SafetyClass: "none"},
		{Name: "manifest", Arity: 1, Type: "string", Scope: []string{"goals validate"}, Description: "Path to expected-state manifest file", SafetyClass: "none"},
		{Name: "stats", Aliases: []string{"analyze"}, Arity: 0, Type: "bool", Scope: []string{"vacuum", "goals"}, Description: "Display database metrics or tabular goals statistics", SafetyClass: "none"},
		{Name: "by", Arity: 1, Type: "string", Scope: []string{"goals supersede", "approve"}, Description: "Superseding goal slug, author, or human approver", SafetyClass: "none"},
		{Name: "reject", Arity: 0, Type: "bool", Scope: []string{"approve"}, Description: "Reject approval request instead of approving", SafetyClass: "none"},
		{Name: "evidence", Arity: 1, Type: "string", Scope: []string{"goals certify"}, Description: "Evidence report file to certify a goal", SafetyClass: "none"},
		{Name: "full", Arity: 0, Type: "bool", Scope: []string{"sync", "ingest", "goals", "gate", "changelog"}, Description: "Full history sync or full gate", SafetyClass: "none"},
		{Name: "check", Arity: 0, Type: "bool", Scope: []string{"upgrade", "update", "goals", "unslop"}, Description: "Check without installing/mutating", SafetyClass: "none"},
		{Name: "source", Arity: 0, Type: "bool", Scope: []string{"upgrade", "update"}, Description: "Force rebuild from local source repository", SafetyClass: "none"},
		{Name: "repair", Arity: 0, Type: "bool", Scope: []string{"doctor"}, Description: "Automatically repair FTS5 indexes and optimize storage", SafetyClass: "none"},
		{Name: "skills", Aliases: []string{"skill"}, Arity: 1, Type: "string", Scope: []string{"doctor", "skill"}, Description: "Audit skills (doctor bool) or skill slugs (skill string)", SafetyClass: "none"},
		{Name: "mcp", Arity: 0, Type: "bool", Scope: []string{"doctor"}, Description: "Show running MCP server freshness", SafetyClass: "none"},
		{Name: "memory", Arity: 0, Type: "bool", Scope: []string{"doctor"}, Description: "Show local memory adoption report", SafetyClass: "none"},
		{Name: "exclude", Arity: 1, Type: "string", Scope: []string{"skill"}, Description: "Comma-separated skill exclusion patterns", SafetyClass: "none"},
		{Name: "covenant", Arity: 0, Type: "bool", Scope: []string{"doctor"}, Description: "Verify Digital Data Sovereignty Covenant invariants", SafetyClass: "none"},
		{Name: "edge", Arity: 0, Type: "bool", Scope: []string{"doctor"}, Description: "Audit Cloudflare Workers test suites", SafetyClass: "none"},
		{Name: "debt", Arity: 0, Type: "bool", Scope: []string{"doctor"}, Description: "Audit repository for tracked debt comments", SafetyClass: "none"},
		{Name: "scar-tissue", Aliases: []string{"scar"}, Arity: 0, Type: "bool", Scope: []string{"gate"}, Description: "Audit git diff against historical scar tissue", SafetyClass: "none"},
		{Name: "doc-coverage", Aliases: []string{"doc-lint"}, Arity: 0, Type: "bool", Scope: []string{"gate"}, Description: "Audit exported Go declarations for doc comments", SafetyClass: "none"},
		{Name: "report-card", Aliases: []string{"reportcard"}, Arity: 0, Type: "bool", Scope: []string{"gate", "reportcard"}, Description: "Run local Go Report Card analyzer", SafetyClass: "none"},
		{Name: "coverage", Arity: 0, Type: "bool", Scope: []string{"gate", "coverage"}, Description: "Evaluate test statement coverage", SafetyClass: "none"},
		{Name: "profile", Arity: 1, Type: "string", Scope: []string{"coverage", "mcp", "gate"}, Description: "Coverage profile path or MCP server profile", SafetyClass: "none"},
		{Name: "print-schema", Arity: 0, Type: "bool", Scope: []string{"mcp"}, Description: "Print canonical MCP tool schema and exit", SafetyClass: "none"},
		{Name: "trace-args", Arity: 0, Type: "bool", Scope: []string{"mcp"}, Description: "Append metadata-only MCP argument traces", SafetyClass: "none"},
		{Name: "pre-commit", Aliases: []string{"quick", "fast"}, Arity: 0, Type: "bool", Scope: []string{"gate"}, Description: "Run fast pre-commit checks", SafetyClass: "none"},
		{Name: "skip-arch", Arity: 0, Type: "bool", Scope: []string{"gate"}, Description: "Skip architectural sentry checks", SafetyClass: "none"},
		{Name: "budget", Arity: 1, Type: "string", Scope: []string{"prime", "hook", "recall", "graph"}, Description: "Target context or graph slice budget", SafetyClass: "none"},
		{Name: "max-tokens", Arity: 1, Type: "string", Scope: []string{"prime"}, Description: "Target context token budget", SafetyClass: "none"},
		{Name: "pressure", Arity: 1, Type: "string", Scope: []string{"prime"}, Description: "Context pressure mode", SafetyClass: "none"},
		{Name: "include-markdown", Aliases: []string{"markdown"}, Arity: 0, Type: "bool", Scope: []string{"prime"}, Description: "Include full rendered markdown string in JSON", SafetyClass: "none"},
		{Name: "include-archived", Arity: 0, Type: "bool", Scope: []string{"recall", "prime", "search", "memory"}, Description: "Include archived and superseded memories", SafetyClass: "none"},
		{Name: "acl-scoped", Arity: 0, Type: "bool", Scope: []string{"search", "recall"}, Description: "Restrict cross-project results to the requester project plus global records", SafetyClass: "none"},
		{Name: "visibility", Arity: 1, Type: "string", Scope: []string{"memo", "lesson", "case"}, Description: "Memory visibility scope: project (default) or global", SafetyClass: "none"},
		{Name: "authority", Arity: 1, Type: "string", Scope: []string{"memory"}, Description: "Archive or restore authority: user_request, agent_verified, sensitive", SafetyClass: "none"},
		{Name: "duplicate-of", Arity: 1, Type: "string", Scope: []string{"memory"}, Description: "Duplicate target memory id for reason=duplicate", SafetyClass: "none"},
		{Name: "hard-delete", Arity: 0, Type: "bool", Scope: []string{"memory"}, Description: "Refused: memory archive never deletes content", SafetyClass: "destructive"},
		{Name: "reveal-sensitive", Arity: 0, Type: "bool", Scope: []string{"memory"}, Description: "Reveal sensitive archive content on a TTY", SafetyClass: "destructive"},
		{Name: "verify", Aliases: []string{"verify-chain"}, Arity: 0, Type: "bool", Scope: []string{"memory"}, Description: "Verify the lifecycle hash chain", SafetyClass: "none"},
		{Name: "cache-pinned", Arity: 0, Type: "bool", Scope: []string{"prime"}, Description: "Format priming output with cache-pinned segregation", SafetyClass: "none"},
		{Name: "all-goals", Arity: 0, Type: "bool", Scope: []string{"prime"}, Description: "Include all completed roadmap goals", SafetyClass: "none"},
		{Name: "no-map", Aliases: []string{"disable-map"}, Arity: 0, Type: "bool", Scope: []string{"prime"}, Description: "Disable automatic codebase symbol digest", SafetyClass: "none"},
		{Name: "map-chars", Arity: 1, Type: "int", Scope: []string{"prime"}, Description: "Maximum character budget for codebase symbol digest", SafetyClass: "none"},
		{Name: "event", Arity: 1, Type: "string", Scope: []string{"hook"}, Description: "Lifecycle event kind", SafetyClass: "none"},
		{Name: "no-auto-handoff", Arity: 0, Type: "bool", Scope: []string{"hook"}, Description: "Disable automatic session-end handoff", SafetyClass: "none"},
		{Name: "payload", Arity: 1, Type: "string", Scope: []string{"hook", "edit-check"}, Description: "JSON payload for hook event", SafetyClass: "none"},
		{Name: "drain", Arity: 0, Type: "bool", Scope: []string{"hook"}, Description: "Immediately drain spool queue into store", SafetyClass: "none"},
		{Name: "cwd", Arity: 1, Type: "string", Scope: []string{"hook"}, Description: "Working directory for lifecycle event", SafetyClass: "none"},
		{Name: "hook", Arity: 0, Type: "bool", Scope: []string{"gate install"}, Description: "Install git pre-push hook", SafetyClass: "none"},
		{Name: "forward", Arity: 1, Type: "string", Scope: []string{"gate pipeline"}, Description: "Forward push target remote upon green gate", SafetyClass: "none"},
		{Name: "no-hooks", Arity: 0, Type: "bool", Scope: []string{"install"}, Description: "Skip git hooks installation", SafetyClass: "none"},
		{Name: "no-mcp", Arity: 0, Type: "bool", Scope: []string{"install"}, Description: "Skip MCP registration", SafetyClass: "none"},
		{Name: "shim", Arity: 0, Type: "bool", Scope: []string{"install"}, Description: "Provision standalone workspace binary runner in .bin/sila", SafetyClass: "none"},
		{Name: "run", Arity: 0, Type: "bool", Scope: []string{"test"}, Description: "Execute targeted test command immediately", SafetyClass: "none"},
		{Name: "cloud", Aliases: []string{"with-cloud"}, Arity: 0, Type: "bool", Scope: []string{"sync"}, Description: "Pull from edge before sync and push after sync", SafetyClass: "none"},
		{Name: "tools", Arity: 1, Type: "string", Scope: []string{"install"}, Description: "Comma-separated tool targets", SafetyClass: "none"},
		{Name: "replace", Arity: 0, Type: "bool", Scope: []string{"install", "agents", "hook"}, Description: "Authorize replacing existing user-owned files", SafetyClass: "destructive"},
		{Name: "policy-pack", Arity: 1, Type: "string", Scope: []string{"install", "agents"}, Description: "Opt-in policy pack to embed in compiled rules", SafetyClass: "none"},
		{Name: "omni", Arity: 0, Type: "bool", Scope: []string{"search"}, Description: "Multi-source hybrid search across facts, transcripts, git, docs", SafetyClass: "none"},
		{Name: "semantic", Aliases: []string{"rerank"}, Arity: 0, Type: "bool", Scope: []string{"search", "dream", "verify", "reconcile", "recall"}, Description: "Enable semantic assistance", SafetyClass: "none"},
		{Name: "window", Arity: 1, Type: "int", Scope: []string{"search expand"}, Description: "Number of preceding and following turns to retrieve", SafetyClass: "none"},
		{Name: "occurrence", Arity: 1, Type: "int", Scope: []string{"search expand"}, Description: "Select among multiple recorded source occurrences", SafetyClass: "none"},
		{Name: "two-pass", Arity: 0, Type: "bool", Scope: []string{"search"}, Description: "Enable two-pass asymmetric retrieval", SafetyClass: "none"},
		{Name: "no-two-pass", Arity: 0, Type: "bool", Scope: []string{"search"}, Description: "Disable two-pass search", SafetyClass: "none"},
		{Name: "target", Arity: 1, Type: "string", Scope: []string{"export", "agents", "guide", "eval"}, Description: "Target rule format or evaluation target", SafetyClass: "none"},
		{Name: "scope", Arity: 1, Type: "string", Scope: []string{"export", "agents"}, Description: "Instruction review scope: project or global", SafetyClass: "none"},
		{Name: "port", Arity: 1, Type: "int", Scope: []string{"ui", "web", "demo", "doc"}, Description: "HTTP server port", SafetyClass: "none"},
		{Name: "host", Arity: 1, Type: "string", Scope: []string{"ui", "web", "demo", "doc"}, Description: "HTTP server host binding", SafetyClass: "none"},
		{Name: "expose", Arity: 0, Type: "bool", Scope: []string{"ui", "web", "demo"}, Description: "Authorize non-loopback --host binding", SafetyClass: "destructive"},
		{Name: "no-open", Arity: 0, Type: "bool", Scope: []string{"ui", "web", "demo", "doc", "feedback"}, Description: "Do not automatically launch system browser", SafetyClass: "none"},
		{Name: "demo", Arity: 0, Type: "bool", Scope: []string{"ui", "web"}, Description: "Launch ephemeral demo playground", SafetyClass: "none"},
		{Name: "web", Arity: 0, Type: "bool", Scope: []string{"demo"}, Description: "Launch the HTTP Web Cockpit instead of the terminal walkthrough", SafetyClass: "none"},
		{Name: "keep", Arity: 0, Type: "bool", Scope: []string{"demo"}, Description: "Keep the demo sandbox on disk for live exploration", SafetyClass: "none"},
		{Name: "step", Arity: 1, Type: "string", Scope: []string{"demo"}, Description: "Comma-separated walkthrough stages or all", SafetyClass: "none"},
		{Name: "out", Arity: 1, Type: "string", Scope: []string{"export", "intro", "backup", "release", "opinions", "vision", "adr", "agents", "skill", "guide"}, Description: "Destination output file", SafetyClass: "none"},
		{Name: "reveal", Arity: 0, Type: "bool", Scope: []string{"config"}, Description: "Reveal secrets unmasked in output", SafetyClass: "destructive"},
		{Name: "format", Arity: 1, Type: "string", Scope: []string{"doc", "guide"}, Description: "Documentation export format", SafetyClass: "none"},
		{Name: "heavy", Aliases: []string{"deep"}, Arity: 0, Type: "bool", Scope: []string{"clean"}, Description: "Include heavy or network-dependent caches", SafetyClass: "destructive"},
		{Name: "harness", Arity: 1, Type: "string", Scope: []string{"skill", "eval", "hook", "session"}, Description: "Target harness identifier", SafetyClass: "none"},
		{Name: "ablation", Arity: 0, Type: "bool", Scope: []string{"eval"}, Description: "Run ablation study comparing with vs without target", SafetyClass: "none"},
		{Name: "eval-dir", Arity: 1, Type: "string", Scope: []string{"eval"}, Description: "Directory containing test cases", SafetyClass: "none"},
		{Name: "from-lesson", Arity: 1, Type: "string", Scope: []string{"eval"}, Description: "Fact ID of recorded lesson to synthesize", SafetyClass: "none"},
		{Name: "from-feedback", Arity: 1, Type: "string", Scope: []string{"eval"}, Description: "Feedback record ID to synthesize", SafetyClass: "none"},
		{Name: "all-lessons", Arity: 0, Type: "bool", Scope: []string{"eval"}, Description: "Synthesize tests for all recorded project lessons", SafetyClass: "none"},
		{Name: "case", Arity: 1, Type: "string", Scope: []string{"eval"}, Description: "Filter to specific case name", SafetyClass: "none"},
		{Name: "bare", Arity: 0, Type: "bool", Scope: []string{"eval init"}, Description: "Scaffold only prompt.md without starter graders", SafetyClass: "none"},
		{Name: "report", Arity: 0, Type: "bool", Scope: []string{"eval"}, Description: "Generate HTML dashboard", SafetyClass: "none"},
		{Name: "allow-dirty", Arity: 0, Type: "bool", Scope: []string{"release preflight"}, Description: "Allow running checks on uncommitted worktree", SafetyClass: "none"},
		{Name: "offline", Arity: 0, Type: "bool", Scope: []string{"release preflight"}, Description: "Force offline mode skipping remote probes", SafetyClass: "none"},
		{Name: "prompt-only", Arity: 0, Type: "bool", Scope: []string{"audit", "hire", "skill"}, Description: "Generate turnkey multi-agent prompt markdown without run", SafetyClass: "none"},
		{Name: "register-goals", Arity: 0, Type: "bool", Scope: []string{"audit", "hire"}, Description: "Persist synthesized consensus proposals into goals/", SafetyClass: "none"},
		{Name: "squad", Arity: 1, Type: "string", Scope: []string{"audit", "hire"}, Description: "Preset squad template or persona IDs", SafetyClass: "none"},
		{Name: "personas", Arity: 1, Type: "string", Scope: []string{"audit", "hire", "goals"}, Description: "Comma-separated persona IDs", SafetyClass: "none"},
		{Name: "pages", Arity: 1, Type: "string", Scope: []string{"audit", "hire"}, Description: "Comma-separated sub-pages to scope", SafetyClass: "none"},
		{Name: "app", Arity: 1, Type: "string", Scope: []string{"audit", "hire"}, Description: "Application name under test", SafetyClass: "none"},
		{Name: "recommend", Arity: 1, Type: "string", Scope: []string{"audit", "hire", "decision create"}, Description: "URL to recommend an audit squad for or recommended decision option letter", SafetyClass: "none"},
		{Name: "preset", Arity: 1, Type: "string", Scope: []string{"audit", "hire"}, Description: "Preset squad name to view", SafetyClass: "none"},
		{Name: "register", Arity: 0, Type: "bool", Scope: []string{"audit", "hire"}, Description: "Register detected gap personas", SafetyClass: "none"},
		{Name: "context", Arity: 1, Type: "string", Scope: []string{"audit", "hire", "decision"}, Description: "Business or domain context", SafetyClass: "none"},
		{Name: "group", Arity: 1, Type: "string", Scope: []string{"persona"}, Description: "Filter personas by group ID or slug", SafetyClass: "none"},
		{Name: "interactive", Arity: 0, Type: "bool", Scope: []string{"skill"}, Description: "Conduct interactive terminal interview", SafetyClass: "none"},
		{Name: "upstreams", Arity: 0, Type: "bool", Scope: []string{"mcp"}, Description: "Enable upstream MCP servers", SafetyClass: "none"},
		{Name: "no-upstreams", Arity: 0, Type: "bool", Scope: []string{"mcp"}, Description: "Disable upstream MCP servers", SafetyClass: "none"},
		{Name: "symbol", Aliases: []string{"sym"}, Arity: 1, Type: "string", Scope: []string{"note", "recall", "edit-check", "safe-delete"}, Description: "Target symbol name or canonical ID", SafetyClass: "none"},
		{Name: "paths", Arity: 1, Type: "string", Scope: []string{"recall"}, Description: "Comma-separated repository-relative paths in play", SafetyClass: "none"},
		{Name: "symbols", Arity: 1, Type: "string", Scope: []string{"recall"}, Description: "Comma-separated symbol names in play", SafetyClass: "none"},
		{Name: "head", Arity: 1, Type: "string", Scope: []string{"recall"}, Description: "Git HEAD commit for context scoring", SafetyClass: "none"},
		{Name: "context-file", Arity: 1, Type: "string", Scope: []string{"recall"}, Description: "JSON recall context file", SafetyClass: "none"},
		{Name: "kind", Aliases: []string{"k"}, Arity: 1, Type: "string", Scope: []string{"note", "memory"}, Description: "Note kind: memo, lesson, invariant", SafetyClass: "none"},
		{Name: "export", Aliases: []string{"e"}, Arity: 0, Type: "bool", Scope: []string{"note"}, Description: "Export symbol notes to .sila/notes.json", SafetyClass: "none"},
		{Name: "import", Aliases: []string{"i"}, Arity: 0, Type: "bool", Scope: []string{"note"}, Description: "Import symbol notes from .sila/notes.json", SafetyClass: "none"},
		{Name: "brief", Arity: 1, Type: "string", Scope: []string{"crew"}, Description: "Path to task brief file", SafetyClass: "none"},
		{Name: "surface", Arity: 1, Type: "string", Scope: []string{"crew"}, Description: "Comma-separated exclusive write surface patterns", SafetyClass: "none"},
	}
}
