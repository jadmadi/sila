package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/release"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func openMCPStore(dbPath string) (*store.Store, error) {
	return store.Open(dbPath)
}

// runMCP starts the Model Context Protocol stdio server.
func runMCP(dbPath string, upstreams, noUpstreams bool, profile string, traceArgs bool) {
	st, err := openMCPStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila mcp: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cfg, _ := config.Load("")
	if noUpstreams {
		cfg.MCPUpstreamsEnabled = false
	} else if upstreams {
		cfg.MCPUpstreamsEnabled = true
	}
	upstreamMgr := mcp.NewUpstreamManager(st, cfg)
	defer upstreamMgr.Close()

	srv := mcp.NewServer(st, os.Stdin, os.Stdout)
	srv.SetUpstreamManager(upstreamMgr)
	srv.SetTraceArgs(traceArgs || os.Getenv("SILA_MCP_TRACE_ARGS") == "1")
	if profile != "" {
		srv.SetProfile(profile)
	} else {
		srv.SetProfile(mcp.ProfileContinuity)
	}
	if err := srv.Run(); err != nil && err != io.EOF {
		fmt.Fprintf(os.Stderr, "sila mcp: %v\n", err)
		os.Exit(1)
	}
}

type primeRunOptions struct {
	Strict             bool
	Full               bool
	MaxTokensStr       string
	PressureStr        string
	IncludeMarkdown    bool
	CachePinned        bool
	AllGoals           bool
	DisableCodebaseMap bool
	CodebaseMapChars   int
}

// runPrime outputs a concise session start context briefing block with optional context compression and degradation.
func runPrime(ui render.UI, dbPath, project string, jsonOut, toonOut bool, top int, budgetStr string, extraArgs ...string) {
	opts := primeRunOptions{}
	if len(extraArgs) > 0 {
		opts.MaxTokensStr = extraArgs[0]
	}
	if len(extraArgs) > 1 {
		opts.PressureStr = extraArgs[1]
	}
	runPrimeWithOptions(ui, dbPath, project, jsonOut, toonOut, top, budgetStr, opts)
}

func runPrimeWithOptions(ui render.UI, dbPath, project string, jsonOut, toonOut bool, top int, budgetStr string, opts primeRunOptions) {
	st, storeCreated, err := openReadStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	defer release.SyncSoakDailyIfDue(cwd, dbPath)
	maxTokensStr := opts.MaxTokensStr
	pressureStr := opts.PressureStr
	if pressureStr == "" {
		pressureStr = "auto"
	}

	budget := dream.ParseBudget(budgetStr)
	maxTokens := dream.ParseBudget(maxTokensStr)
	if budget == 0 && maxTokens > 0 && !jsonOut {
		budget = maxTokens
	}
	hardMaxTokens := maxTokens
	if !jsonOut && !toonOut {
		hardMaxTokens = 0
	}

	forcedPressure := store.WindowPressure("")
	if pressureStr != "" && strings.ToLower(pressureStr) != "auto" {
		if p, ok := store.ParsePressure(pressureStr); ok {
			forcedPressure = p
		}
	}

	summary, err := dream.BuildPrimeBriefWithOptions(st, dream.PrimeOptions{
		ProjectSel:         project,
		ProjectDir:         cwd,
		MaxFacts:           top,
		Budget:             budget,
		MaxTokens:          hardMaxTokens,
		IncludeMarkdown:    opts.IncludeMarkdown || (!jsonOut && !toonOut),
		AllGoals:           opts.AllGoals,
		CachePinned:        opts.CachePinned,
		ForcedPressure:     forcedPressure,
		DisableCodebaseMap: opts.DisableCodebaseMap,
		CodebaseMapChars:   opts.CodebaseMapChars,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	summary.Initialized = !storeCreated
	if opts.Strict && !summary.Initialized {
		if jsonOut {
			emitJSON(summary)
			os.Exit(3)
		}
		fmt.Fprintln(os.Stderr, "sila prime: no pre-existing knowledge store; strict mode requires `sila sync` first")
		os.Exit(3)
	}

	if summary.Error != "" {
		if jsonOut {
			emitJSON(summary)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "sila prime: %s\n", summary.Error)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(summary)
		return
	}
	if toonOut {
		var factRows [][]string
		for _, f := range summary.TopFacts {
			desc := f.Distilled
			if desc == "" {
				desc = f.Claim
			}
			desc = trunc(strings.ReplaceAll(desc, "\n", " "), 90)
			factRows = append(factRows, []string{
				f.ID,
				fmt.Sprintf("%t", f.FileRefsExist || f.Verified),
				desc,
			})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("prime_facts", []string{"id", "grounded", "claim"}, factRows))
		if summary.ActiveHandoff != nil {
			var handoffFields [][2]string
			handoffFields = append(handoffFields, [2]string{"summary", summary.ActiveHandoff.Summary})
			handoffFields = append(handoffFields, [2]string{"branch", summary.ActiveHandoff.Branch})
			handoffFields = append(handoffFields, [2]string{"agent", summary.ActiveHandoff.Agent})
			handoffFields = append(handoffFields, [2]string{"open_items", strings.Join(summary.ActiveHandoff.OpenItems, "; ")})
			fmt.Fprint(os.Stdout, render.FormatTOONObject("active_handoff", handoffFields))
		}
		if summary.Shrink != nil && (summary.Shrink.Pressure != store.PressureNormal || len(summary.Shrink.SectionsPruned) > 0) {
			prunedTokens := summary.Shrink.OriginalTokens - summary.Shrink.FinalTokens
			if prunedTokens < 0 {
				prunedTokens = 0
			}
			var shrinkFields [][2]string
			shrinkFields = append(shrinkFields, [2]string{"pressure", string(summary.Shrink.Pressure)})
			shrinkFields = append(shrinkFields, [2]string{"original_tokens", fmt.Sprintf("%d", summary.Shrink.OriginalTokens)})
			shrinkFields = append(shrinkFields, [2]string{"final_tokens", fmt.Sprintf("%d", summary.Shrink.FinalTokens)})
			shrinkFields = append(shrinkFields, [2]string{"pruned_tokens", fmt.Sprintf("%d", prunedTokens)})
			if len(summary.Shrink.SectionsPruned) > 0 {
				shrinkFields = append(shrinkFields, [2]string{"sections_pruned", strings.Join(summary.Shrink.SectionsPruned, ", ")})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONObject("window_pressure", shrinkFields))
		} else if summary.Compression != nil && summary.Compression.PrunedTokens > 0 {
			var compFields [][2]string
			compFields = append(compFields, [2]string{"original_tokens", fmt.Sprintf("%d", summary.Compression.OriginalTokens)})
			compFields = append(compFields, [2]string{"compressed_tokens", fmt.Sprintf("%d", summary.Compression.CompressedTokens)})
			compFields = append(compFields, [2]string{"pruned_tokens", fmt.Sprintf("%d", summary.Compression.PrunedTokens)})
			compFields = append(compFields, [2]string{"reduction_pct", fmt.Sprintf("%.1f%%", summary.Compression.SavingsRatio*100)})
			fmt.Fprint(os.Stdout, render.FormatTOONObject("context_compression", compFields))
		}
		if summary.CodebaseMap != nil && len(summary.CodebaseMap.Directories) > 0 {
			var mapRows [][]string
			for _, d := range summary.CodebaseMap.Directories {
				mapRows = append(mapRows, []string{
					d.Dir,
					strings.Join(d.Symbols, ", "),
				})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("codebase_map", []string{"dir", "symbols"}, mapRows))
		}
		fmt.Fprintf(os.Stdout, "grounding:\n  verified: %d/%d\n", summary.TotalVerified, summary.TotalClaims)
		fmt.Fprintln(os.Stdout, "help[1]: Run `sila handoff` to inspect tasks or `sila opinions` for project taste")
		return
	}

	if summary.Shrink != nil && (summary.Shrink.Pressure != store.PressureNormal || len(summary.Shrink.SectionsPruned) > 0 || (pressureStr != "" && strings.ToLower(pressureStr) != "auto")) {
		prunedTokens := summary.Shrink.OriginalTokens - summary.Shrink.FinalTokens
		if prunedTokens < 0 {
			prunedTokens = 0
		}
		fmt.Fprintf(os.Stdout, "Pressure: %s (Original: %s, Pruned: %s -> %s)\n\n",
			summary.Shrink.Pressure,
			formatTokenCount(summary.Shrink.OriginalTokens),
			formatTokenCount(prunedTokens),
			formatTokenCount(summary.Shrink.FinalTokens),
		)
	}

	md := summary.Markdown
	if !opts.Full {
		md = stripPrimeContract(md)
	}
	if opts.CachePinned {
		md = mcp.FormatCachePinnedPrimeMarkdown(summary)
	}

	// Surface symbol notes for modified repository files
	if modifiedFiles := getModifiedGoFiles(cwd); len(modifiedFiles) > 0 {
		var pinnedNotes []store.SymbolNote
		seenNoteIDs := make(map[string]bool)
		for _, mf := range modifiedFiles {
			if notes, err := st.GetSymbolNotesByFile(summary.ProjectID, mf); err == nil {
				for _, n := range notes {
					if !seenNoteIDs[n.ID] {
						seenNoteIDs[n.ID] = true
						pinnedNotes = append(pinnedNotes, n)
					}
				}
			}
		}
		if len(pinnedNotes) > 0 {
			var sb strings.Builder
			sb.WriteString("\n## Symbol-Pinned Invariants & Directives\n")
			for _, pn := range pinnedNotes {
				span := ""
				if pn.SpanStartLine > 0 {
					span = fmt.Sprintf(" (%s:%d-%d)", pn.FilePath, pn.SpanStartLine, pn.SpanEndLine)
				}
				sb.WriteString(fmt.Sprintf("- 📌 [%s] `%s`%s: %s\n", pn.NoteKind, pn.CanonicalID, span, pn.NoteText))
			}
			md += sb.String()
		}
	}

	// Surface negative signal warnings for unasserted or silenced architectural invariants
	if signals, err := st.DetectNegativeSignals(context.Background(), summary.ProjectID, 7*24*time.Hour); err == nil && len(signals) > 0 {
		var sb strings.Builder
		sb.WriteString("\n## ⚠ Negative Drift Alerts: Unasserted Invariants\n")
		for _, s := range signals {
			sb.WriteString(fmt.Sprintf("- ⚠ [%s] `%s` (%.1fd since last assertion): %s\n", s.Status, s.RuleID, s.DaysSinceAssertion, s.RuleText))
		}
		md += sb.String()
	}

	fmt.Fprint(os.Stdout, render.WrapAt(md, render.TerminalWidth()))
}

// stripPrimeContract removes the repeated nine-verb contract from the default
// briefing; `sila prime --full` keeps it.
func stripPrimeContract(md string) string {
	const startMarker = "**Working Contract (9-Verb Memory Loop)**:"
	const endMarker = "_Note: If `sila` CLI is not in `$PATH`"
	start := strings.Index(md, startMarker)
	if start < 0 {
		return md
	}
	rel := strings.Index(md[start:], endMarker)
	if rel < 0 {
		return md
	}
	end := start + rel
	if nl := strings.Index(md[end:], "\n"); nl >= 0 {
		end += nl + 1
	} else {
		end = len(md)
	}
	return md[:start] + md[end:]
}

func getModifiedGoFiles(cwd string) []string {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) < 3 {
			continue
		}
		path := strings.TrimSpace(line[2:])
		if idx := strings.Index(path, "->"); idx != -1 {
			path = strings.TrimSpace(path[idx+2:])
		}
		path = strings.Trim(path, "\"")
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
	}
	return files
}
