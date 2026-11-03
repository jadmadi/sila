package main

import (
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/mcp"
)

func introMarkdown(agent, dbPath string) string {
	if agent == "" {
		agent = "your AI CLI"
	}
	var b strings.Builder
	b.WriteString("# sila — agent onboarding\n\n")
	b.WriteString("Created by Jad Madi ([@jadmadi on X.com](https://x.com/jadmadi)) — part of the devtools family.\n\n")
	b.WriteString("sila is the cross-harness continuity engine for AI coding agents: it consolidates the " +
		"AI-CLI work of many tools (MiMoCode, Devin, OpenCode, Codex, command-code, codewhale, " +
		"Agy, ZCode, Claude Code, Muse) into one local store, so any agent can query what was done, " +
		"across tools, without re-reading every tool's logs.\n\n")
	fmt.Fprintf(&b, "* **Store:** `%s` (you can point elsewhere with `--db=`).\n", dbPath)
	b.WriteString("* **Safety:** reads every tool source read-only; never modifies another tool's database.\n")
	b.WriteString("* **Machine interface:** every command supports `--json` — prefer it when consuming programmatically.\n")
	fmt.Fprintf(&b, "* **MCP Server:** run `sila mcp` for native JSON-RPC 2.0 tool integration with AI agents (%d tools).\n\n", mcp.PublicToolCount)

	b.WriteString("## The work cycle (make it habitual)\n\n")
	fmt.Fprintf(&b, "As %s, fold these into every session:\n\n", agent)
	b.WriteString("1. **Start each session:** `sila status --json` — are sources in sync? anything pending? stale claims?\n")
	b.WriteString("2. **Audit disk truth:** `sila dream reconcile` — verify referenced repo files on disk.\n")
	b.WriteString("3. **Triage & check goals:** `sila goals ready` — check unblocked goals ready for execution in `goals/`.\n")
	b.WriteString("4. **After substantive work (or at end of session):** `sila sync` — import new messages and distill claims.\n")
	b.WriteString("5. **Retrieve prior knowledge:** `sila dream query <term> [--project=] [--tool=] [--refs-exist] [--facts-only] [--top=N]`.\n")
	b.WriteString("6. **Search raw transcripts:** `sila search <term> [--project=] [--tool=] [--role=] [--since=]`.\n")
	b.WriteString("7. **Cross-tool overlap:** `sila overlaps` — which repos had similar work in multiple tools.\n")
	b.WriteString("8. **Repo inventory:** `sila projects` — messages / claims / refs_exist per project.\n")
	b.WriteString("9. **Verify discipline before commit:** `sila gate` — test race suite, format, changelog, binary currency, and docs.\n\n")

	b.WriteString("## Closed-Loop Goal Execution (Never Leave Orphaned Goals)\n\n")
	b.WriteString("When managing or executing goal packages under `goals/<slug>/`:\n\n")
	b.WriteString("1. **Create Goals:** Scaffold a standardized goal package: `sila goals create <slug> --title=\"...\" [--depends-on=\"...\"] [--independent] [--seq=N]` (or MCP `create_goal`).\n")
	b.WriteString("2. **Discovery:** Run `sila goals ready` (or `sila goals independent` / `sila goals global`) to find unblocked, parallelizable tasks.\n")
	b.WriteString("3. **Claim & Lock:** Lock the goal: `sila goals claim <slug> --agent=" + agent + " --note=\"starting implementation\"` to prevent multi-agent collisions.\n")
	b.WriteString("4. **Visual Review:** Run `sila goals review <slug>` (or `sila goals <project> review <slug>`) to trigger Plannotator for visual plan inspection.\n")
	b.WriteString("5. **Execution & Report:** Launch `/goal goals/<slug>/goal.md` and report progress with `sila goals report <slug> \"<status>\"`.\n")
	b.WriteString("6. **Attribution Commit:** Commit with explicit reference: `git commit -m \"feat(scope): description (goals/<slug>/goal.md)\"`.\n")
	b.WriteString("7. **Auto-Reconciliation:** Run `sila goals` (or `sila goals <project>`) to detect the commit, mark 🟢 Implemented, and sync `goals/README.md`.\n")
	b.WriteString("8. **Handoff:** Run `sila handoff` before exiting to guarantee zero context amnesia.\n\n")

	b.WriteString("## Consuming output\n\n")
	b.WriteString("- **`status --json`** → `in_sync`, `pending`, `pending_tools`, `stale_facts`, totals. Decide whether to `sync`.\n")
	b.WriteString("- **`goals --json`** → `{total, implemented_count, claimed_count, pending_count, ready_count, blocked_count, independent_count, goals}`.\n")
	b.WriteString("- **`dream query --json`** → ranked claims: `{claim, corroboration, file_refs_exist, is_fact, repo_files, tools, project_id}`. \n")
	b.WriteString("  `file_refs_exist=true` means the referenced files exist on disk — trust those first.\n")
	b.WriteString("- **`search --json`** → raw transcripts: `{project_id, role, content, source_tools, ts}`.\n")
	b.WriteString("- **`projects --json`** / **`overlaps --json`** / **`export --json`** → inventory and full dumps.\n\n")

	b.WriteString("## AGENTS.md block — append this so sila stays in the loop\n\n")
	b.WriteString("```markdown\n")
	b.WriteString("## sila (cross-tool knowledge store & goal lifecycle)\n")
	b.WriteString("sila collects the AI-CLI work of MiMo/Devin/OpenCode/Codex into a local SQLite store. ")
	b.WriteString("Read it read-only; it never touches other tools' databases.\n")
	b.WriteString("- Query it as part of the work cycle. Start sessions with:")
	b.WriteString(" `sila status --json` and `sila goals ready` (or `sila goals global`); after work run `sila sync`; retrieve prior knowledge with:")
	b.WriteString(" `sila dream query <term> --refs-exist --json` or `sila search <term> --json`.\n")
	b.WriteString("- Create new goals via `sila goals create <slug> --title=\"...\" [--depends-on=\"...\"]`.\n")
	b.WriteString("- Claim and lock goals before starting: `sila goals claim <slug> --agent=<name>`.\n")
	b.WriteString("- Audit any project's roadmap with `sila goals <project>` or `sila goals <project> ready`.\n")
	b.WriteString("- When completing a goal, reference its path in your commit: `git commit -m \"feat: description (goals/<slug>/goal.md)\"` (docs/test commits do not certify), then run `sila goals`.\n")
	b.WriteString("- Run `sila gate` before committing to verify test race conditions, format, changelog, and docs.\n")
	b.WriteString("- Prefer `--json` for programmatic consumption; prefer `file_refs_exist=true` claims (files exist on disk).\n")
	b.WriteString("- Scope by repo with `--project=<name>`; discover cross-tool overlap with `sila overlaps`; ")
	b.WriteString("inventory repos with `sila projects`.\n")
	b.WriteString("- Wire as an MCP server with `sila mcp` in your agent's MCP config.\n")
	b.WriteString("```\n\n")

	b.WriteString("## Getting started\n\n")
	b.WriteString("```sh\n")
	b.WriteString("sila prime               # get session-start context briefing\n")
	b.WriteString("sila goals create <slug> # scaffold a new goal package with goal.md, facts.md, plan.md\n")
	b.WriteString("sila goals ready         # list unblocked goals ready to execute right now\n")
	b.WriteString("sila goals independent   # list disjoint/parallelizable goals\n")
	b.WriteString("sila goals claimed       # list claimed/in-progress goals\n")
	b.WriteString("sila goals claim <slug>  # claim and lock a goal package\n")
	b.WriteString("sila goals global        # list pending goals across ALL projects\n")
	b.WriteString("sila goals <project>     # audit roadmap for any registered project\n")
	b.WriteString("sila goals review <slug> # review plan visually in Plannotator\n")
	b.WriteString("sila status              # am I in sync / is there an opportunity?\n")
	b.WriteString("sila sync                # import + distill, one pass\n")
	b.WriteString("sila dream query auth --refs-exist # find verified prior work on 'auth'\n")
	b.WriteString("sila search deploy       # search raw message transcripts for 'deploy'\n")
	b.WriteString("sila recall \"xlsx export hangs\"   # prior fixes for a symptom, across projects\n")
	b.WriteString("sila lesson \"symptom\" --cause=... --fix=... # record a solved problem\n")
	b.WriteString("sila onboard             # wire a legacy project into sila and scaffold its goal\n")
	b.WriteString("sila heal                # bring this repository current: rules, hooks, MCP, stale claims\n")
	b.WriteString("sila salam               # the agent front door: loop + live next steps (hi, hello, merhaba)\n")
	b.WriteString("sila agents review       # preview proposed rule updates from session friction\n")
	b.WriteString("sila gate                # verify engineering discipline (tests, format, binary, readme)\n")
	b.WriteString("sila feedback \"tip/issue\" --type=tip # submit experience feedback & tips\n")
	b.WriteString("sila handoff \"summary\"   # leave handoff for next agent session\n")
	b.WriteString("```\n")
	return b.String()
}

// introGoalsMarkdown builds the agent guide specifically for goal execution and roadmap hygiene.
func introGoalsMarkdown(agent string) string {
	if agent == "" {
		agent = "your AI CLI"
	}
	var b strings.Builder
	b.WriteString("# sila — Agent Guide: Multi-Agent Goal Lifecycle & Locking Protocol\n\n")
	b.WriteString("Created by Jad Madi ([@jadmadi on X.com](https://x.com/jadmadi)) — part of the devtools family.\n\n")
	b.WriteString("> **Golden Rule for Agents**: Never leave orphaned, stalled, sticky, or duplicate goals. Every goal package follows a deterministic, closed cycle from creation and claim lock to verified git commit attribution.\n\n")

	b.WriteString("## The 8-Step Goal Runbook\n\n")

	b.WriteString("### 1. 🏗️ Goal Creation (`sila goals create <slug>` / MCP `create_goal`)\n")
	b.WriteString("- Agents can autonomously scaffold new goal packages conforming to repository standards:\n")
	b.WriteString("  ```bash\n")
	b.WriteString("  sila goals create <slug> --title=\"...\" [--desc=\"...\"] [--depends-on=\"slug-a,slug-b\"] [--independent] [--seq=N]\n")
	b.WriteString("  ```\n")
	b.WriteString("- Automatically generates `goals/<slug>/goal.md`, `facts.md`, and `plan.md` and updates `goals/README.md`.\n\n")

	b.WriteString("### 2. 🔍 Discovery & Dependency Triage (`sila goals ready` / `sila goals independent`)\n")
	b.WriteString("- Check unblocked goals ready to pick right now: `sila goals ready`.\n")
	b.WriteString("- Check parallelizable/disjoint tasks for swarm workers: `sila goals independent`.\n")
	b.WriteString("- Inspect blocked items: `sila goals blocked` (surfaces unmet prerequisites).\n")
	b.WriteString("- Target any specific project: `sila goals <project> ready`.\n")
	b.WriteString("- Check `sila goals claimed` to inspect what other agents are currently executing.\n\n")

	b.WriteString("### 3. 🔒 Claim & Lock Goal (`sila goals claim <slug>` / MCP `claim_goal`)\n")
	b.WriteString("- Before starting work or evaluation, lock the goal to prevent other agents from taking it:\n")
	b.WriteString("  ```bash\n")
	b.WriteString("  sila goals claim <slug> --agent=" + agent + " --note=\"evaluating implementation plan\"\n")
	b.WriteString("  ```\n")
	b.WriteString("- This creates `goals/<slug>/claim.json` and updates `goals/README.md` to 🔵 **In Progress**.\n")
	b.WriteString("- *If you need to release or unclaim a goal*: `sila goals release <slug>` (MCP `release_goal`).\n\n")

	b.WriteString("### 4. 🎨 Visual Plan Review (`sila goals review <slug>`)\n")
	b.WriteString("- Run `sila goals review <slug>` (or `sila goals <project> review <slug>`) to trigger the local Plannotator CLI for visual interactive plan inspection.\n")
	b.WriteString("- Review `facts.md` (verified codebase constraints and interface contracts) and `plan.md` (ordered execution steps).\n\n")

	b.WriteString("### 5. 🚀 Execution & Live Progress Reporting (`/goal` & `sila goals report`)\n")
	b.WriteString("- Launch the goal in your agent session: `/goal goals/<slug>/goal.md`.\n")
	b.WriteString("- Implement tasks incrementally and run test suites.\n")
	b.WriteString("- Report intermediate milestones during execution:\n")
	b.WriteString("  ```bash\n")
	b.WriteString("  sila goals report <slug> \"Completed step 2 of 4, running e2e tests\"\n")
	b.WriteString("  ```\n\n")

	b.WriteString("### 6. 📝 Semantic Attribution Commit (`git commit`)\n")
	b.WriteString("- Once verified, commit all modified files locally with explicit goal attribution:\n")
	b.WriteString("  ```bash\n")
	b.WriteString("  git commit -m \"feat(scope): implement description (goals/<slug>/goal.md)\"\n")
	b.WriteString("  ```\n")
	b.WriteString("  *(Commit prefix MUST be `feat:`, `fix:`, `refactor:`, or `perf:` — NEVER `docs:` or `chore(goals):`)*.\n\n")

	b.WriteString("### 7. 🔄 Auto-Reconciliation & Lock Clearance (`sila goals`)\n")
	b.WriteString("- Run `sila goals` (or `sila goals <project>`). Sila will automatically:\n")
	b.WriteString("  - Scan recent git history to detect your verifying commit hash.\n")
	b.WriteString("  - Transition the goal from 🔵 In Progress to 🟢 Implemented.\n")
	b.WriteString("  - Unblock any dependent downstream goals in the roadmap.\n")
	b.WriteString("  - Re-generate and synchronize `goals/README.md`.\n")
	b.WriteString("- Run `sila sync` to index newly verified codebase facts into `sila.db`.\n\n")

	b.WriteString("### 8. 🤝 Clean Baton Passing & Feedback (`sila handoff` / `sila feedback`)\n")
	b.WriteString("- If ending the session, record a structured handoff with `sila handoff \"Completed <slug>\"`.\n")
	b.WriteString("- Submit any friction or tips: `sila feedback \"note\" --type=tip` or MCP `record_feedback`.\n")
	b.WriteString("- **Discipline Rule**: Do not pause, abandon, or conclude a session with uncommitted or sticky goals.\n\n")

	b.WriteString("## Quick Agent Reference\n\n")
	b.WriteString("```bash\n")
	b.WriteString("sila goals create <slug>       # Scaffold a new goal package\n")
	b.WriteString("sila goals ready               # List unblocked goals ready to execute right now\n")
	b.WriteString("sila goals independent         # List disjoint / parallelizable goals\n")
	b.WriteString("sila goals blocked             # List goals waiting on unmet prerequisites\n")
	b.WriteString("sila goals global              # List pending goals across ALL projects\n")
	b.WriteString("sila goals claimed             # List claimed in-progress goals across projects\n")
	b.WriteString("sila goals claim <slug>        # Claim & lock goal: --agent=<name> --note=\"...\"\n")
	b.WriteString("sila goals report <slug> msg   # Update intermediate live progress note\n")
	b.WriteString("sila goals release <slug>      # Release claimed lock back to pending queue\n")
	b.WriteString("sila goals release --stale     # Release dead or inactive claims (preview with --dry-run)\n")
	b.WriteString("sila goals prune --claims      # Clear locks left on implemented goals\n")
	b.WriteString("sila goals active --session=<id> --json # The goal this session is bound to\n")
	b.WriteString("sila goals <project> ready     # List ready goals in a specific project\n")
	b.WriteString("sila goals <project>           # Audit and sync roadmap for a specific project\n")
	b.WriteString("sila goals review <slug>       # Launch visual review in Plannotator\n")
	b.WriteString("sila feedback \"msg\" --type=tip  # Share agent tips, suggestions or friction\n")
	b.WriteString("sila recall \"error or symptom\"  # Find prior cross-project fixes before wandering\n")
	b.WriteString("sila lesson \"symptom\" --cause=<> --fix=<> # Record a solved problem for recall\n")
	b.WriteString("sila onboard check --strict     # Verify this project is fully wired into sila\n")
	b.WriteString("sila heal                       # Bring this repository current with sila in one command\n")
	b.WriteString("sila salam                      # Start here: the working loop plus live next steps\n")
	b.WriteString("sila prime                     # View active handoff and top pending goal\n")
	b.WriteString("sila sync                      # Sync and distill newly verified knowledge\n")
	b.WriteString("```\n")
	return b.String()
}

// runCompletions prints a shell completion script for the given shell.
