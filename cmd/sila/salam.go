package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/agents"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/salam"
	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
	"github.com/mattn/go-isatty"
)

// HandshakeOptions configures interactive assistance onboarding.
type HandshakeOptions struct {
	In          io.Reader
	Out         io.Writer
	ForcePrompt bool
	Timeout     time.Duration
}

func promptAssistanceHandshake(ui render.UI, repoRoot, project string, opts HandshakeOptions) bool {
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	isTerm := opts.ForcePrompt
	if !isTerm {
		if f, ok := in.(*os.File); ok {
			isTerm = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
		}
	}
	if !isTerm || (!opts.ForcePrompt && (os.Getenv("CI") != "" || os.Getenv("DEBIAN_FRONTEND") == "noninteractive")) {
		return false
	}

	fmt.Fprintf(out, "\n%s Enable JEV semantic diff sentry for this repository? [y/N] (auto-skip in %ds): ",
		ui.Cyan("[Handshake]"), int(timeout.Seconds()))

	inputCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		if scanner.Scan() {
			inputCh <- strings.TrimSpace(scanner.Text())
		} else {
			inputCh <- ""
		}
	}()

	select {
	case line := <-inputCh:
		if strings.EqualFold(line, "y") || strings.EqualFold(line, "yes") {
			pCfg := &config.Config{
				AssistEnabled: true,
				AssistScopes:  []string{"diff", "gate"},
			}
			if err := config.SaveProjectConfig(repoRoot, pCfg); err != nil {
				fmt.Fprintf(out, "%s Failed to save project config: %v\n", ui.Red("✖"), err)
				return false
			}
			fmt.Fprintf(out, "%s JEV semantic diff sentry enabled in .sila/config.json (scopes: diff, gate)\n", ui.Green("✔"))
			return true
		}
		fmt.Fprintf(out, "%s Skipped (pure offline operation retained).\n", ui.Dim("•"))
		return false
	case <-time.After(timeout):
		fmt.Fprintf(out, "%s\n", ui.Dim("(auto-skipped)"))
		return false
	}
}

// runSalam is the front door for an agent starting in a repository: it prints
// the working loop and the live next steps (heal findings, ready goals,
// active claims, last handoff) so one command is enough to start rolling.
func runSalam(ui render.UI, dbPath, project string, yes, jsonOut bool, args []string) {
	st, _, err := store.OpenInspection(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	if st != nil {
		defer st.Close()
	}

	full := false
	global := false
	for _, a := range args {
		switch a {
		case "--full":
			full = true
		case "--global", "-g":
			global = true
		}
	}
	if global {
		gb, err := salam.BuildGlobalBrief(st, full)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila salam: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(gb)
			return
		}
		printGlobalSalam(ui, gb)
		return
	}

	brief, err := salam.BuildBrief(st, "", project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila salam: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		if full {
			emitJSON(brief)
		} else {
			emitJSON(compactSalamBrief(brief))
		}
		return
	}
	printSalam(ui, brief)
	cwd, _ := os.Getwd()
	fmt.Fprintf(os.Stdout, "  capture: %s\n", captureLine(captureStateFor(st, store.ProjectID(cwd))))
	if full {
		printSalamFull(ui, brief)
		if id, err := session.ResolveIdentity("", project, brief.RepoRoot); err == nil && id.SessionID == "" {
			fmt.Fprintln(os.Stdout, ui.Dim("session: unbound — run `sila session env` or `sila session start --harness=<name>`"))
		}
	}

	// Check for explicit flag in args (--yes, -y, --assist)
	autoEnable := yes
	for _, a := range args {
		if a == "--yes" || a == "-y" || a == "--assist" {
			autoEnable = true
			break
		}
	}

	if autoEnable && !brief.Assistance.ProjectEnabled {
		pCfg := &config.Config{
			AssistEnabled: true,
			AssistScopes:  []string{"diff", "gate"},
		}
		if err := config.SaveProjectConfig(brief.RepoRoot, pCfg); err != nil {
			fmt.Fprintf(os.Stderr, "sila: failed to save project config: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "\n%s JEV semantic diff sentry enabled for %s (.sila/config.json)\n", ui.Green("✔"), brief.Project)
		return
	}

	// Interactive handshake prompt only when explicitly requested (--interactive / --handshake).
	// A bare `sila salam` must stay non-blocking and read-only as documented.
	wantPrompt := false
	for _, a := range args {
		if a == "--interactive" || a == "-i" || a == "--handshake" || a == "--prompt" {
			wantPrompt = true
			break
		}
	}
	if wantPrompt && brief.Assistance.CanPrompt {
		promptAssistanceHandshake(ui, brief.RepoRoot, brief.Project, HandshakeOptions{})
	}
}

// compactSalamBrief is the bounded default JSON shape. `--full` emits the
// complete SalamBrief (loop, protocol, markdown).
func compactSalamBrief(b *salam.SalamBrief) map[string]any {
	ready := b.Ready
	if len(ready) > 3 {
		ready = ready[:3]
	}
	claimed := b.Claimed
	if len(claimed) > 3 {
		claimed = claimed[:3]
	}
	return map[string]any{
		"project":        b.Project,
		"repo_root":      b.RepoRoot,
		"connected":      b.Connected,
		"wiring":         b.Wiring,
		"heal_automatic": b.HealAutomatic,
		"heal_judgment":  b.HealJudgment,
		"ready_goals":    ready,
		"claimed_goals":  claimed,
		"last_handoff":   b.LastHandoff,
		"memory_status":  b.MemoryStatus,
		"next":           "sila heal && sila prime",
		"full_available": "sila salam --full",
	}
}

func printSalam(ui render.UI, brief *salam.SalamBrief) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s — %s\n", ui.Cyan(ui.Bold("sila · salam")), ui.Bold(brief.Project), ui.Dim(brief.RepoRoot))
	if brief.IgnoredSubdirWarning != "" {
		fmt.Fprintf(&b, "  %s %s\n", ui.Yellow("⚠"), ui.Yellow(brief.IgnoredSubdirWarning))
	}
	switch brief.Wiring.State {
	case "wired":
		fmt.Fprintln(&b, "You are in a sila-connected repository.")
	case "unwired":
		fmt.Fprintln(&b, "This repository has no sila wiring yet. Run `sila heal` to provision it, then `sila prime`.")
	default:
		fmt.Fprintln(&b, "Sila could not verify this repository's wiring. Run `sila heal --dry-run` to inspect it.")
	}
	fmt.Fprintln(&b, ui.Bold("Live state:"))
	if brief.HealAutomatic == 0 {
		fmt.Fprintf(&b, "  %s heal: current (%d judgment %s pending)\n", ui.Green("✔"), brief.HealJudgment, plural(int64(brief.HealJudgment), "step"))
	} else {
		fmt.Fprintf(&b, "  %s heal: %d automatic %s pending → run `sila heal`\n", ui.Yellow("▲"), brief.HealAutomatic, plural(int64(brief.HealAutomatic), "fix"))
	}
	if len(brief.Ready) > 0 {
		fmt.Fprintln(&b, ui.Bold("  Ready to claim:"))
		for i, g := range brief.Ready {
			if i == 3 {
				fmt.Fprintf(&b, "     %s\n", ui.Dim(fmt.Sprintf("… and %d more; run `sila goals ready`", len(brief.Ready)-i)))
				break
			}
			fmt.Fprintf(&b, "     • %s — %s\n", ui.Bold(g.Slug), g.Title)
		}
	} else if len(brief.Claimed) == 0 {
		fmt.Fprintf(&b, "  %s No ready goals. Run `sila goals` or create one with `sila goals create <slug>`.\n", ui.Dim("•"))
	}
	if brief.MemoryUnavailable {
		fmt.Fprintf(&b, "  %s Memory: unavailable (run `sila prime` to initialize)\n", ui.Dim("•"))
	} else if brief.LastHandoff != nil {
		summary := brief.LastHandoff.Summary
		if len(summary) > 90 {
			summary = summary[:90] + "…"
		}
		fmt.Fprintf(&b, "  Last handoff: @%s — %s\n", brief.LastHandoff.Agent, summary)
	}
	fmt.Fprintf(&b, "%s %s and %s. Run %s for the full loop.\n",
		ui.Dim("Next:"), ui.Cyan("sila heal"), ui.Cyan("sila prime"), ui.Cyan("sila salam --full"))
	fmt.Fprint(os.Stdout, render.WrapAt(b.String(), render.TerminalWidth()))
}

// printSalamFull emits the detailed state mapping and working contract.
func printSalamFull(ui render.UI, brief *salam.SalamBrief) {
	var b strings.Builder
	fmt.Fprintln(&b, "\n"+ui.Bold("Working loop:"))
	for i, line := range brief.Loop {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, line)
	}
	if len(brief.Claimed) > 0 {
		fmt.Fprintln(&b, "\n"+ui.Bold("In progress:"))
		for _, c := range brief.Claimed {
			note := ""
			if c.Note != "" {
				note = " · " + c.Note
			}
			fmt.Fprintf(&b, "  • %s @%s %s%s\n", ui.Bold(c.Slug), c.Agent, ui.Dim(c.Age), ui.Dim(note))
		}
	}
	if len(brief.Harnesses) > 0 {
		fmt.Fprintf(&b, "\n  %s Harnesses on disk: %s %s\n", ui.Green("✔"), ui.Bold(strings.Join(brief.Harnesses, ", ")), ui.Dim("(ready to index: sila sync)"))
	}
	switch brief.Assistance.Status {
	case "active":
		fmt.Fprintf(&b, "  %s Assistance (JEV): %s\n", ui.Green("✔"), brief.Assistance.Message)
	case "dormant":
		fmt.Fprintf(&b, "  %s Assistance (JEV): %s\n", ui.Yellow("▲"), brief.Assistance.Message)
	default:
		fmt.Fprintf(&b, "  %s Assistance (JEV): %s\n", ui.Dim("•"), brief.Assistance.Message)
	}
	fmt.Fprintln(&b, "\n  "+ui.Bold("Memory & continuity protocol:"))
	for _, line := range agents.MemoryProtocolExpanded() {
		fmt.Fprintln(&b, "    "+line)
	}
	fmt.Fprintln(&b, "\n  "+ui.Bold("Situation routing (before generic fallbacks):"))
	for _, line := range agents.TriggerRoutingExpanded() {
		fmt.Fprintln(&b, "    • "+line)
	}
	fmt.Fprint(os.Stdout, render.WrapAt(b.String(), render.TerminalWidth()))
}

func printGlobalSalam(ui render.UI, gb *salam.GlobalBrief) {
	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{
		Command: "salam",
		Scope:   fmt.Sprintf("global (%d repositories)", gb.Scanned),
	}))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintf(os.Stdout, "  %s wired: %d · %s unwired: %d · %s ready goals: %d across %d repos · %s active claims: %d across %d repos\n\n",
		ui.Green("✔"), gb.Wired,
		ui.Yellow("▲"), gb.Unwired,
		ui.Cyan("•"), gb.ReadyGoalsTotal, gb.ReposWithReady,
		ui.Cyan("•"), gb.ClaimedGoalsTotal, gb.ReposWithClaims,
	)
	rows := make([][]string, 0, len(gb.Repositories))
	for _, r := range gb.Repositories {
		state := ui.Green("wired")
		if r.Wiring.State != "wired" {
			state = ui.Yellow(r.Wiring.State)
		}
		rows = append(rows, []string{
			r.Project,
			state,
			fmt.Sprintf("%d", r.HealAutomatic),
			fmt.Sprintf("%d", r.ReadyGoals),
			fmt.Sprintf("%d", r.ClaimedGoals),
		})
	}
	fmt.Fprintln(os.Stdout, ui.Table([]string{"project", "wiring", "auto-heal", "ready", "claimed"}, rows, []string{"l", "l", "r", "r", "r"}))
}
