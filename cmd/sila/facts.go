package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runFacts dispatches the facts ledger subcommands. reasonFlag carries the
// root --reason value: Go's flag package consumes it before this function runs
// (the flag is also used by `goals reopen`), so without plumbing it through,
// `sila facts rebaseline --reason=...` — the syntax the ledger auditor itself
// prints — would silently record the default reason.
func runFacts(ui render.UI, dbPath, project string, jsonOut, toonOut, verbose bool, reasonFlag string, args []string) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	action := "verify"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}

	switch action {
	case "status", "counts":
		counts, err := st.FactCounts(project, time.Now().UTC(), store.DefaultFactTTL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila facts status: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(counts)
			return
		}
		fmt.Fprintf(os.Stdout, "verified: %d\nstale: %d\nunverified: %d\ndisputed: %d\nbroken_chain: %d\n",
			counts.Verified, counts.Stale, counts.Unverified, counts.Disputed, counts.BrokenChain)
		return
	case "verify", "audit", "check":
		repair := false
		rebaseline := false
		var cleanArgs []string
		for _, a := range args {
			if a == "--repair" {
				repair = true
			} else if a == "--rebaseline" {
				rebaseline = true
			} else {
				cleanArgs = append(cleanArgs, a)
			}
		}
		if rebaseline {
			runFactsRebaseline(ui, st, jsonOut, reasonFlag, cleanArgs)
		} else if repair {
			runFactsRepair(ui, st, jsonOut, cleanArgs)
		} else {
			runFactsVerify(ui, st, project, jsonOut, verbose, cleanArgs)
		}
	case "repair", "heal":
		rebaseline := false
		var cleanArgs []string
		for _, a := range args {
			if a == "--rebaseline" {
				rebaseline = true
			} else {
				cleanArgs = append(cleanArgs, a)
			}
		}
		if rebaseline {
			runFactsRebaseline(ui, st, jsonOut, reasonFlag, cleanArgs)
		} else {
			runFactsRepair(ui, st, jsonOut, cleanArgs)
		}
	case "rebaseline":
		runFactsRebaseline(ui, st, jsonOut, reasonFlag, args)
	case "hygiene", "stale":
		runFactsHygiene(ui, st, project, jsonOut, args)
	case "show":
		runFactsShow(ui, st, jsonOut, args)
	case "list":
		runFactsList(ui, st, project, jsonOut, toonOut, verbose)
	default:
		if action == "--repair" {
			runFactsRepair(ui, st, jsonOut, args)
			return
		}
		if action == "--rebaseline" {
			runFactsRebaseline(ui, st, jsonOut, reasonFlag, args)
			return
		}
		// If action looks like a project name, default to verify for that project
		if action != "" && !strings.HasPrefix(action, "-") {
			runFactsVerify(ui, st, action, jsonOut, verbose, nil)
			return
		}
		runFactsVerify(ui, st, project, jsonOut, verbose, args)
	}
}

func shortHash(h string) string {
	if len(h) > 16 {
		return h[:16] + "…"
	}
	return h
}

func runFactsVerify(ui render.UI, st *store.Store, project string, jsonOut, verbose bool, args []string) {
	projSel := project
	explain := false
	limit := 0
	var cleanArgs []string
	for _, a := range args {
		switch {
		case a == "--explain":
			explain = true
		case strings.HasPrefix(a, "--limit="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--limit=")); err == nil && n > 0 {
				limit = n
			}
		case strings.HasPrefix(a, "-"):
			// ignore unrelated flags
		default:
			cleanArgs = append(cleanArgs, a)
		}
	}
	args = cleanArgs
	if projSel == "" && len(args) > 0 {
		projSel = args[0]
	}

	var res *store.ChainAuditResult
	var err error
	if jsonOut || explain {
		res, err = st.VerifyChainExplained(projSel)
	} else {
		res, err = st.VerifyChain(projSel)
	}
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "status": "fail"})
		} else {
			fmt.Fprintf(os.Stderr, "sila facts verify: %v\n", err)
		}
		os.Exit(1)
	}

	if res.Grounding == nil {
		if cwd, wdErr := os.Getwd(); wdErr == nil {
			res.Grounding = st.AuditFileGrounding(projSel, cwd)
		}
	}

	outcome := "ok"
	if !res.Intact {
		outcome = "error"
	}
	recordMemoryEvent(st, projSel, "", "facts", outcome, "", "cli", 0)
	if jsonOut {
		emitJSON(res)
		if !res.Intact {
			os.Exit(1)
		}
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · facts verify"))+" — "+ui.Dim("Hash-Chained Fact Certification Ledger"))
	if explain && res.Summary != nil {
		fmt.Fprintf(os.Stdout, "  Summary: roots=%d cascades=%d broken=%d digest=%d missing=%d genesis=%d duplicate=%d order_gap=%d\n",
			res.Summary.Roots, res.Summary.Cascades, res.Summary.BrokenPrevLink, res.Summary.DigestMismatch,
			res.Summary.MissingHash, res.Summary.GenesisMismatch, res.Summary.DuplicateHash, res.Summary.OrderGap)
		if res.FirstDivergence != nil {
			fmt.Fprintf(os.Stdout, "  First divergence: block %d fact %s (%s)\n",
				res.FirstDivergence.Block, res.FirstDivergence.FactID, res.FirstDivergence.Type)
		}
		shown := 0
		for _, v := range res.ViolationDetails {
			if limit > 0 && shown >= limit {
				fmt.Fprintf(os.Stdout, "  … %d more classified violation(s); use --limit to show more\n", len(res.ViolationDetails)-shown)
				break
			}
			fmt.Fprintf(os.Stdout, "    %s block %d %s fact %s cascade=%v\n",
				ui.Yellow("▲"), v.Block, v.Type, v.FactID, v.Cascade)
			shown++
		}
	}
	if res.Intact {
		if res.TotalBlocks == 0 {
			fmt.Fprintln(os.Stdout, "  "+ui.Green("✔")+" Ledger is empty (genesis initialized: "+ui.Dim(res.GenesisHash[:16]+"...")+")")
		} else {
			fmt.Fprintf(os.Stdout, "  %s %s (%d blocks verified)\n",
				ui.Green("✔"),
				ui.Bold(fmt.Sprintf("%d / %d blocks verified (100%% integrity)", res.VerifiedBlocks, res.TotalBlocks)),
				res.TotalBlocks,
			)
			fmt.Fprintf(os.Stdout, "  Genesis:    %s\n", ui.Dim(res.GenesisHash))
			fmt.Fprintf(os.Stdout, "  Ledger Tip: %s\n", ui.Bold(res.TipHash))
			fmt.Fprintf(os.Stdout, "  Audit Time: %dms\n", res.DurationMs)
			fmt.Fprintln(os.Stdout, "  Status:     "+ui.Green("Tamper-evident ledger intact. No redactions or silent corruption detected."))
		}
	} else {
		fmt.Fprintf(os.Stdout, "  %s %s\n",
			ui.Red("✖"),
			ui.Red(ui.Bold(fmt.Sprintf("LEDGER TAMPERING OR CORRUPTION DETECTED: %d violation(s) found across %d block(s)!", len(res.Violations), res.TotalBlocks))),
		)
		shownViolations := 0
		maxViolations := 20
		if limit > 0 {
			maxViolations = limit
		}
		for _, v := range res.Violations {
			if !verbose && shownViolations >= maxViolations {
				fmt.Fprintf(os.Stdout, "    … %d more violation(s); use --verbose or --limit=%d to show all\n", len(res.Violations)-shownViolations, len(res.Violations))
				break
			}
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Yellow("▲"), v)
			shownViolations++
		}
	}

	if res.Grounding != nil && res.Grounding.FactsWithFiles > 0 {
		if len(res.Grounding.MissingFiles) == 0 {
			fmt.Fprintf(os.Stdout, "  Grounding:  %s %d/%d file-referenced fact(s) verified on disk (%s)\n",
				ui.Green("✔"), res.Grounding.GroundedFacts, res.Grounding.FactsWithFiles, res.Grounding.ProjectID)
		} else {
			missing := res.Grounding.MissingFiles
			shown := missing
			more := ""
			if !verbose && len(shown) > 5 {
				shown = shown[:5]
				more = fmt.Sprintf(", … %d more (use --verbose to list all)", len(missing)-5)
			}
			fmt.Fprintf(os.Stdout, "  Grounding:  %s %d/%d file-referenced fact(s) grounded; %d missing file(s) on disk: %s%s (run `sila dream --reconcile`)\n",
				ui.Yellow("▲"), res.Grounding.GroundedFacts, res.Grounding.FactsWithFiles,
				len(missing), strings.Join(shown, ", "), more)
		}
	}

	formatFactsVerifyIncidents(os.Stdout, ui, res, verbose, limit)

	if !res.Intact {
		os.Exit(1)
	}
}

func formatFactsVerifyIncidents(w io.Writer, ui render.UI, res *store.ChainAuditResult, verbose bool, limit int) {
	if res == nil || len(res.Incidents) == 0 {
		return
	}
	totalInc := res.IncidentCount
	if totalInc < len(res.Incidents) {
		totalInc = len(res.Incidents)
	}
	if res.Intact && !verbose && limit == 0 {
		fmt.Fprintf(w, "\n  %s %d historical record(s) archived (active ledger intact; pass --verbose or --limit=N to inspect)\n",
			ui.Bold("Durable Integrity Incident Evidence:"), totalInc)
		return
	}
	fmt.Fprintln(w, "\n  "+ui.Bold("Durable Integrity Incident Evidence:")+" ("+fmt.Sprintf("%d record(s)", totalInc)+")")
	incLimit := 10
	if limit > 0 {
		incLimit = limit
	}
	toShow := res.Incidents
	if !verbose && len(toShow) > incLimit {
		toShow = toShow[len(toShow)-incLimit:]
	}
	for _, inc := range toShow {
		fmt.Fprintf(w, "    %s [%s] fact %s: stored %s, computed %s (%s)\n",
			ui.Yellow("▲"), inc.IncidentType, inc.FactID,
			shortHash(inc.StoredHash), shortHash(inc.ComputedHash), inc.CreatedAt)
		if inc.Details != "" {
			fmt.Fprintf(w, "      detail: %s\n", ui.Dim(inc.Details))
		}
	}
	if totalInc > len(toShow) {
		fmt.Fprintf(w, "    %s\n", ui.Dim(fmt.Sprintf("… %d earlier historical incident(s) omitted; use --verbose or --limit=N to inspect", totalInc-len(toShow))))
	}
}

func runFactsRepair(ui render.UI, st *store.Store, jsonOut bool, args []string) {
	dryRun, yes, allowNoBackup := parseRecoveryFlags(args)
	before, err := st.VerifyChain("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila facts repair: %v\n", err)
		os.Exit(1)
	}
	if dryRun {
		if jsonOut {
			emitJSON(map[string]any{"dry_run": true, "proposed_legacy_blocks": before.LegacyBlocks})
		} else {
			fmt.Fprintf(os.Stdout, "sila facts repair: dry-run; %d legacy block(s) would be initialized\n", before.LegacyBlocks)
		}
		return
	}
	if !yes {
		fmt.Fprintf(os.Stderr, "sila facts repair: refusing to mutate without --yes (or --dry-run); %d proposed change(s)\n", before.LegacyBlocks)
		os.Exit(2)
	}
	if !allowNoBackup {
		if _, err := factsBackup(st); err != nil {
			fmt.Fprintf(os.Stderr, "sila facts repair: backup failed: %v\n", err)
			os.Exit(1)
		}
	}
	repaired, err := st.RepairChain()
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "success": false})
		} else {
			fmt.Fprintf(os.Stderr, "sila facts repair: %v\n", err)
		}
		os.Exit(1)
	}

	audit, err := st.VerifyChain("")
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "success": false})
		} else {
			fmt.Fprintf(os.Stderr, "sila facts repair (post-audit): %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"legacy_initialized": repaired,
			"intact":             audit.Intact,
			"total_blocks":       audit.TotalBlocks,
			"tip_hash":           audit.TipHash,
			"violations":         audit.Violations,
			"incidents":          audit.Incidents,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · facts repair"))+" — "+ui.Dim("Fact Ledger Routine Healer"))
	if repaired == 0 && audit.Intact {
		fmt.Fprintf(os.Stdout, "  %s Ledger is already cryptographically intact (%d blocks verified).\n", ui.Green("✔"), audit.TotalBlocks)
	} else if audit.Intact {
		fmt.Fprintf(os.Stdout, "  %s %s across %d blocks\n",
			ui.Green("✔"),
			ui.Bold(fmt.Sprintf("Successfully initialized %d legacy unhashed fact blocks", repaired)),
			audit.TotalBlocks,
		)
		fmt.Fprintf(os.Stdout, "  New Ledger Tip: %s\n", ui.Bold(audit.TipHash))
		fmt.Fprintln(os.Stdout, "  Status:         "+ui.Green("Cryptographic SHA-256 chain established."))
	} else {
		if repaired > 0 {
			fmt.Fprintf(os.Stdout, "  %s Initialized %d legacy unhashed blocks.\n", ui.Green("✔"), repaired)
		}
		fmt.Fprintf(os.Stdout, "  %s %s\n",
			ui.Red("✖"),
			ui.Red(ui.Bold(fmt.Sprintf("INTEGRITY VIOLATIONS PRESERVED: %d violation(s) remain across %d block(s)!", len(audit.Violations), audit.TotalBlocks))),
		)
		for _, v := range audit.Violations {
			fmt.Fprintf(os.Stdout, "    %s %s\n", ui.Yellow("▲"), v)
		}
		fmt.Fprintln(os.Stdout, "  "+ui.Yellow("Routine repair cannot erase tampering on previously hashed facts."))
		fmt.Fprintln(os.Stdout, "  "+ui.Dim("Original mismatch evidence has been preserved in fact_incidents."))
		fmt.Fprintln(os.Stdout, "  "+ui.Dim("To explicitly rebaseline after authorized modifications, run:"))
		fmt.Fprintln(os.Stdout, "    "+ui.Bold("sila facts rebaseline --reason=\"<explanation>\""))
		os.Exit(1)
	}
}

func runFactsRebaseline(ui render.UI, st *store.Store, jsonOut bool, reasonFlag string, args []string) {
	reason := strings.TrimSpace(reasonFlag)
	for _, a := range args {
		if strings.HasPrefix(a, "--reason=") {
			reason = strings.TrimSpace(strings.TrimPrefix(a, "--reason="))
		}
	}
	dryRun, yes, allowNoBackup := parseRecoveryFlags(args)
	if reason == "" {
		fmt.Fprintln(os.Stderr, "sila facts rebaseline: --reason is required; no mutation performed")
		os.Exit(2)
	}
	before, err := st.VerifyChain("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila facts rebaseline: %v\n", err)
		os.Exit(1)
	}
	if dryRun {
		if jsonOut {
			emitJSON(map[string]any{"dry_run": true, "proposed_blocks": before.TotalBlocks, "reason": reason})
		} else {
			fmt.Fprintf(os.Stdout, "sila facts rebaseline: dry-run; %d block(s) would be rehashed; reason=%q\n", before.TotalBlocks, reason)
		}
		return
	}
	if !yes {
		fmt.Fprintf(os.Stderr, "sila facts rebaseline: refusing to mutate without --yes (or --dry-run); %d proposed block(s)\n", before.TotalBlocks)
		os.Exit(2)
	}
	if !allowNoBackup {
		if _, err := factsBackup(st); err != nil {
			fmt.Fprintf(os.Stderr, "sila facts rebaseline: backup failed: %v\n", err)
			os.Exit(1)
		}
	}

	rebaselined, err := st.RebaselineChain(reason)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "success": false})
		} else {
			fmt.Fprintf(os.Stderr, "sila facts rebaseline: %v\n", err)
		}
		os.Exit(1)
	}

	audit, err := st.VerifyChain("")
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error(), "success": false})
		} else {
			fmt.Fprintf(os.Stderr, "sila facts rebaseline (post-audit): %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"rebaselined_blocks": rebaselined,
			"intact":             audit.Intact,
			"total_blocks":       audit.TotalBlocks,
			"tip_hash":           audit.TipHash,
			"reason":             reason,
			"incidents":          audit.Incidents,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · facts rebaseline"))+" — "+ui.Dim("Fact Ledger Explicit Rebaseliner"))
	fmt.Fprintf(os.Stdout, "  %s %s across %d blocks\n",
		ui.Yellow("▲"),
		ui.Bold(fmt.Sprintf("Rebaselined %d fact ledger blocks (provenance recorded in fact_incidents)", rebaselined)),
		audit.TotalBlocks,
	)
	fmt.Fprintf(os.Stdout, "  Reason:         %s\n", reason)
	fmt.Fprintf(os.Stdout, "  New Ledger Tip: %s\n", ui.Bold(audit.TipHash))
	fmt.Fprintln(os.Stdout, "  "+ui.Yellow("SECURITY NOTICE: Explicit rebaseline resets cryptographic hashes to match current content."))
	fmt.Fprintln(os.Stdout, "  "+ui.Dim("It proves internal consistency from this point forward; it DOES NOT recover original truth or prove historical authenticity."))
}

var (
	factsDryRun bool
	factsYes    bool
)

func parseRecoveryFlags(args []string) (dryRun, yes, allowNoBackup bool) {
	dryRun, yes = factsDryRun, factsYes
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--yes", "-y":
			yes = true
		case "--allow-no-backup":
			allowNoBackup = true
		}
	}
	return
}

func factsBackup(st *store.Store) (string, error) {
	dest := fmt.Sprintf("%s.backup-%s.db", st.Path, time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := st.Backup(dest); err != nil {
		return "", err
	}
	backup, err := store.OpenRO(dest)
	if err != nil {
		return "", fmt.Errorf("verify backup open: %w", err)
	}
	defer backup.Close()
	audit, err := backup.VerifyChain("")
	if err != nil {
		return "", fmt.Errorf("verify backup chain: %w", err)
	}
	if audit.TotalBlocks == 0 {
		return "", fmt.Errorf("backup verification returned zero blocks")
	}
	return dest, nil
}

func runFactsShow(ui render.UI, st *store.Store, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila facts show: specify a fact id")
		os.Exit(2)
	}
	id := args[0]
	facts, err := st.FactsAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila facts show: %v\n", err)
		os.Exit(1)
	}
	var found *store.Fact
	for i := range facts {
		if facts[i].ID == id {
			found = &facts[i]
			break
		}
	}
	if found == nil {
		if jsonOut {
			emitJSON(map[string]any{"error": "fact not found", "id": id})
		} else {
			fmt.Fprintf(os.Stderr, "sila facts show: no fact with id %s\n", id)
		}
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(found)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · fact"))+" "+ui.Dim(found.ID))
	fmt.Fprintf(os.Stdout, "  Project:     %s\n", ui.Bold(found.ProjectID))
	fmt.Fprintf(os.Stdout, "  Claim:       %s\n", found.Claim)
	if found.Kind != "" {
		fmt.Fprintf(os.Stdout, "  Kind:        %s\n", found.Kind)
	}
	fmt.Fprintf(os.Stdout, "  First Seen:  %s\n", found.FirstSeen)
	fmt.Fprintf(os.Stdout, "  Fact Hash:   %s\n", ui.Bold(found.FactHash))
	fmt.Fprintf(os.Stdout, "  Prev Hash:   %s\n", ui.Dim(found.PrevHash))
}

func runFactsList(ui render.UI, st *store.Store, project string, jsonOut, toonOut, verbose bool) {
	facts, err := st.FactsByProject(project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila facts list: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(facts)
		return
	}
	if toonOut {
		var rows [][]string
		for _, f := range facts {
			h := f.FactHash
			if len(h) > 12 {
				h = h[:12] + "…"
			}
			rows = append(rows, []string{f.ID, f.ProjectID, f.Claim, h})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("facts", []string{"id", "project", "claim", "hash"}, rows))
		return
	}
	printFacts(ui, st, facts, false, false, verbose)
}

// runFactsHygiene classifies stale file references, re-points moved files, and
// archives deleted/generated/ambiguous claims with the non-hashed is_stale
// marker. It never deletes a fact and never mutates a hashed field.
func runFactsHygiene(ui render.UI, st *store.Store, project string, jsonOut bool, args []string) {
	dryRun, yes, limit := false, false, 0
	for _, a := range args {
		switch {
		case a == "--dry-run" || a == "--preview" || a == "-n":
			dryRun = true
		case a == "--yes" || a == "-y":
			yes = true
		case strings.HasPrefix(a, "--limit="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--limit=")); err == nil && n > 0 {
				limit = n
			}
		}
	}
	if !dryRun && !yes {
		fmt.Fprintln(os.Stderr, "sila facts hygiene: specify --dry-run to classify or --yes to repair/archive")
		os.Exit(2)
	}

	res, err := st.RepairStaleReferences(project, dryRun, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila facts hygiene: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(res)
		if !dryRun && (res.After > 0 || len(res.Errors) > 0) {
			os.Exit(1)
		}
		return
	}

	mode := ""
	if dryRun {
		mode = "dry-run"
	}
	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "facts hygiene", Scope: project, Mode: mode}))
	counts := map[string]int{}
	for _, item := range res.Items {
		counts[item.Classification]++
	}
	classes := make([]string, 0, len(counts))
	for c := range counts {
		classes = append(classes, c)
	}
	sort.Strings(classes)
	fmt.Fprintf(os.Stdout, "  Active stale claims: %d\n", res.Before)
	for _, c := range classes {
		fmt.Fprintf(os.Stdout, "  %-20s %d\n", c+":", counts[c])
	}
	if dryRun {
		fmt.Fprintf(os.Stdout, "  %s no files changed; re-run with --yes to apply\n", ui.Dim("→"))
		return
	}
	fmt.Fprintf(os.Stdout, "  Repaired: %d · Archived: %d · Remaining: %d\n", res.Repaired, res.Archived, res.After)
	for _, e := range res.Errors {
		fmt.Fprintf(os.Stderr, "  %s %s\n", ui.Red("✖"), e)
	}
	if res.After > 0 || len(res.Errors) > 0 {
		os.Exit(1)
	}
}
