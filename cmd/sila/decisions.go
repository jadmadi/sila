package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runDecision(ui render.UI, dbPath, project, agent, goalSlug string, jsonOut bool, args []string) {
	isWrite := false
	preActionCreate := false
	if len(args) > 0 {
		sub := args[0]
		if sub == "create" || sub == "new" || sub == "add" || sub == "resolve" || sub == "close" || sub == "defer" {
			isWrite = true
		}
		if strings.HasPrefix(sub, "-") {
			for _, a := range args {
				if a == "create" || a == "new" || a == "add" {
					preActionCreate = true
					isWrite = true
					break
				}
			}
		}
	}
	var st *store.Store
	var err error
	if isWrite {
		st, err = store.Open(dbPath)
	} else {
		st, err = store.OpenRO(dbPath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if project != "" {
		p, err := st.ResolveScope(project, cwd)
		if err == nil && p.ID != "" {
			project = p.ID
		}
	} else {
		p, err := st.ResolveScope("", cwd)
		if err == nil && p.ID != "" {
			project = p.ID
		} else {
			project = store.ProjectID(cwd)
		}
	}

	if len(args) == 0 {
		printDecisionsSummary(ui, st, project, jsonOut)
		return
	}

	if preActionCreate {
		action := -1
		for i, a := range args {
			if a == "create" || a == "new" || a == "add" {
				action = i
				break
			}
		}
		subArgs := append(append([]string{}, args[:action]...), args[action+1:]...)
		createDecisionCmd(ui, st, project, agent, goalSlug, jsonOut, subArgs)
		return
	}

	sub := args[0]
	subArgs := args[1:]

	// If the argument looks like an address (e.g. "D1" or "1"), treat as view
	if _, _, err := store.ParseAddress(sub); err == nil && sub != "check" && sub != "create" && sub != "resolve" && sub != "list" {
		viewDecision(ui, st, project, sub, jsonOut)
		return
	}

	switch sub {
	case "create", "new", "add":
		createDecisionCmd(ui, st, project, agent, goalSlug, jsonOut, subArgs)
	case "defer":
		deferDecisionCmd(ui, st, project, agent, jsonOut, subArgs)
	case "resolve", "close":
		resolveDecisionCmd(ui, st, project, agent, jsonOut, subArgs)
	case "list", "ls":
		listDecisionsCmd(ui, st, project, goalSlug, jsonOut, subArgs)
	case "view", "show", "get":
		if len(subArgs) == 0 {
			if jsonOut {
				emitErrorEnvelope("USAGE", "decision address is required", "sila decision list")
				os.Exit(2)
			}
			fmt.Fprintln(os.Stderr, "Usage: sila decision view <D#>")
			os.Exit(2)
		}
		viewDecision(ui, st, project, subArgs[0], jsonOut)
	case "check", "verify", "audit":
		checkDecisionsCmd(ui, st, project, jsonOut, subArgs)
	case "help", "--help", "-h":
		printDecisionHelp(ui)
	default:
		fmt.Fprintf(os.Stderr, "unknown decision subcommand %q\n", sub)
		printDecisionHelp(ui)
		os.Exit(2)
	}
}

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func createDecisionCmd(ui render.UI, st *store.Store, project, agent, goalSlug string, jsonOut bool, args []string) {
	fs := flag.NewFlagSet("sila decision create", flag.ContinueOnError)
	var optFlags stringSlice
	fs.Var(&optFlags, "option", "Lettered option (e.g. -option 'A: Pure-Go SQLite')")
	fs.Var(&optFlags, "o", "Shorthand for -option")
	optionsRaw := fs.String("options", "", "Semicolon- or comma-separated options (e.g. 'A: Opt 1; B: Opt 2')")
	recFlag := fs.String("recommend", "", "Explicit recommendation letter (e.g. 'A')")
	fs.StringVar(recFlag, "r", "", "Shorthand for -recommend")
	rationaleFlag := fs.String("rationale", "", "Rationale for recommendation")
	contextFlag := fs.String("context", "", "Background context or motivation")
	goalFlag := fs.String("goal", goalSlug, "Associated goal slug")
	addressFlag := fs.String("address", "", "Optional explicit address (e.g. 'D1') for reservations")

	var flagArgs []string
	var posArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				switch strings.TrimLeft(a, "-") {
				case "o", "option", "r", "recommend", "rationale", "context", "goal", "address", "options":
					i++
					flagArgs = append(flagArgs, args[i])
				}
			}
		} else {
			posArgs = append(posArgs, a)
		}
	}

	fs.SetOutput(os.Stderr)
	fs.Usage = func() {}
	if err := fs.Parse(flagArgs); err != nil {
		fmt.Fprintf(os.Stderr, "sila decision create: %v\nrun 'sila decision --help' for usage\n", err)
		os.Exit(2)
	}

	title := strings.TrimSpace(strings.Join(append(posArgs, fs.Args()...), " "))
	if strings.TrimSpace(title) == "" {
		fmt.Fprintln(os.Stderr, "sila decision create: title is required (e.g. sila decision create 'Database Driver' -o 'A: SQLite' -o 'B: Postgres' -r A)")
		os.Exit(2)
	}

	var parsedOptions []store.DecisionOption
	// Parse options from repeated flags
	for _, optStr := range optFlags {
		opt := parseOptionString(optStr)
		if opt != nil {
			parsedOptions = append(parsedOptions, *opt)
		}
	}
	// Parse options from --options string
	if *optionsRaw != "" {
		parts := splitOptionsString(*optionsRaw)
		for _, part := range parts {
			opt := parseOptionString(part)
			if opt != nil {
				parsedOptions = append(parsedOptions, *opt)
			}
		}
	}

	if len(parsedOptions) < 2 {
		fmt.Fprintln(os.Stderr, "sila decision create: every decision requires at least two lettered options (use -o 'A: Title' -o 'B: Title')")
		os.Exit(2)
	}

	if strings.TrimSpace(*recFlag) == "" {
		fmt.Fprintln(os.Stderr, "sila decision create: explicit recommendation is required (use --recommend=A or -r A)")
		os.Exit(2)
	}

	if agent == "" {
		agent = "user"
	}

	d, err := st.CreateDecision(&store.Decision{
		ProjectID:      project,
		Address:        *addressFlag,
		Title:          title,
		Context:        *contextFlag,
		Options:        parsedOptions,
		Recommendation: *recFlag,
		Rationale:      *rationaleFlag,
		CreatedBy:      agent,
		GoalSlug:       *goalFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila decision create error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(d)
		return
	}

	fmt.Fprintf(os.Stdout, "%s Created addressed decision %s (%s)\n", ui.Green("✔"), ui.Bold(d.Address), d.Title)
	fmt.Fprintf(os.Stdout, "  Recommendation: Option %s\n", ui.Bold(d.Recommendation))
	for _, opt := range d.Options {
		tradeOff := ""
		if opt.TradeOff != "" {
			tradeOff = " (" + opt.TradeOff + ")"
		}
		marker := " "
		if opt.Letter == d.Recommendation {
			marker = "★"
		}
		fmt.Fprintf(os.Stdout, "  %s Option %s: %s%s\n", marker, opt.Letter, opt.Title, tradeOff)
	}
	if d.Rationale != "" {
		fmt.Fprintf(os.Stdout, "  Rationale: %s\n", d.Rationale)
	}
}

func deferDecisionCmd(ui render.UI, st *store.Store, project, agent string, jsonOut bool, args []string) {
	fs := flag.NewFlagSet("sila decision defer", flag.ContinueOnError)
	notesFlag := fs.String("notes", "", "Deferral notes")
	fs.StringVar(notesFlag, "m", "", "Shorthand for -notes")
	byFlag := fs.String("by", agent, "Deferring actor")

	var flagArgs []string
	var posArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				switch strings.TrimLeft(a, "-") {
				case "m", "notes", "by":
					i++
					flagArgs = append(flagArgs, args[i])
				}
			}
		} else {
			posArgs = append(posArgs, a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(2)
	}
	extra := append(posArgs, fs.Args()...)
	if len(extra) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: sila decision defer <D#> [--notes='...'] [--by=<actor>]")
		os.Exit(2)
	}
	actor := *byFlag
	if actor == "" {
		actor = "user"
	}
	d, err := st.DeferDecision(project, extra[0], *notesFlag, actor)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila decision defer error: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(d)
		return
	}
	fmt.Fprintf(os.Stdout, "%s Deferred decision %s (%s)\n", ui.Yellow("·"), ui.Bold(d.Address), d.Title)
	if d.ResolutionNotes != "" {
		fmt.Fprintf(os.Stdout, "  Notes: %s\n", d.ResolutionNotes)
	}
	fmt.Fprintf(os.Stdout, "  Deferred by %s\n", d.ResolvedBy)
}

func resolveDecisionCmd(ui render.UI, st *store.Store, project, agent string, jsonOut bool, args []string) {
	fs := flag.NewFlagSet("sila decision resolve", flag.ContinueOnError)
	choiceFlag := fs.String("choice", "", "Chosen option letter (e.g. 'A') or resolution summary")
	fs.StringVar(choiceFlag, "c", "", "Shorthand for -choice")
	notesFlag := fs.String("notes", "", "Resolution notes")
	fs.StringVar(notesFlag, "m", "", "Shorthand for -notes")
	byFlag := fs.String("by", agent, "Resolver identifier")
	userQuoteFlag := fs.String("user-quote", "", "Verbatim user quote authorizing the resolution")
	fs.StringVar(userQuoteFlag, "q", "", "Shorthand for -user-quote")
	routeNoteFlag := fs.String("route-note", "", "Routing note recorded with the resolution")
	requireQuoteFlag := fs.Bool("require-quote", false, "Reject resolution without a verbatim user quote")

	var flagArgs []string
	var posArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagArgs = append(flagArgs, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				switch strings.TrimLeft(a, "-") {
				case "c", "choice", "m", "notes", "by", "q", "user-quote", "route-note":
					i++
					flagArgs = append(flagArgs, args[i])
				}
			}
		} else {
			posArgs = append(posArgs, a)
		}
	}

	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(2)
	}

	extra := append(posArgs, fs.Args()...)
	if len(extra) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: sila decision resolve <D#> --choice=A [--notes='...']")
		os.Exit(2)
	}

	address := extra[0]
	choice := *choiceFlag
	notes := *notesFlag
	if choice == "" && len(extra) > 1 {
		choice = extra[1]
	}
	if notes == "" && len(extra) > 2 {
		notes = strings.Join(extra[2:], " ")
	}

	if choice == "" {
		fmt.Fprintln(os.Stderr, "sila decision resolve: choice is required (use --choice=A or -c A)")
		os.Exit(2)
	}

	resolvedBy := *byFlag
	if resolvedBy == "" {
		resolvedBy = "user"
	}

	d, err := st.ResolveDecisionWithOptions(project, address, choice, notes, resolvedBy, *userQuoteFlag, *routeNoteFlag, *requireQuoteFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila decision resolve error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(d)
		return
	}

	fmt.Fprintf(os.Stdout, "%s Resolved decision %s (%s): selected %s\n", ui.Green("✔"), ui.Bold(d.Address), d.Title, ui.Bold(d.Resolution))
	if d.ResolutionNotes != "" {
		fmt.Fprintf(os.Stdout, "  Notes: %s\n", d.ResolutionNotes)
	}
	if d.UserQuote != "" {
		fmt.Fprintf(os.Stdout, "  User Quote: %s\n", d.UserQuote)
	}
	if d.RouteNote != "" {
		fmt.Fprintf(os.Stdout, "  Route Note: %s\n", d.RouteNote)
	}
	fmt.Fprintf(os.Stdout, "  Resolved by %s at %s\n", d.ResolvedBy, d.ResolvedAt)
}

func listDecisionsCmd(ui render.UI, st *store.Store, project, goalSlug string, jsonOut bool, args []string) {
	fs := flag.NewFlagSet("sila decision list", flag.ContinueOnError)
	statusFlag := fs.String("status", "all", "Filter by status: open, resolved, all")
	goalFilter := fs.String("goal", goalSlug, "Filter by goal slug")
	limitFlag := fs.Int("limit", 0, "Limit number of results")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	decisions, err := st.ListDecisions(store.DecisionFilter{
		ProjectID: project,
		Status:    *statusFlag,
		GoalSlug:  *goalFilter,
		Limit:     *limitFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila decision list error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(decisions)
		return
	}

	if outModes.porcelain {
		for _, d := range decisions {
			choice := "-"
			if d.Resolution != "" {
				choice = d.Resolution
			}
			fmt.Fprintf(os.Stdout, "%s\t%s\t%s\t%s\t%s\n", d.Address, d.Status, d.Recommendation, choice, sanitizePorcelain(d.Title))
		}
		return
	}

	if len(decisions) == 0 {
		fmt.Fprintf(os.Stdout, "No decisions found matching filter (status=%s, project=%s)\n", *statusFlag, project)
		return
	}

	fmt.Fprintf(os.Stdout, "%s Epistemic Decisions for %s (%d total):\n\n", ui.Cyan("·"), ui.Bold(project), len(decisions))
	fmt.Fprintf(os.Stdout, "  %-6s %-10s %-5s %-7s %s\n", "ADDR", "STATUS", "REC", "CHOICE", "TITLE")
	fmt.Fprintf(os.Stdout, "  %-6s %-10s %-5s %-7s %s\n", "----", "------", "---", "------", "-----")
	for _, d := range decisions {
		choice := "-"
		if d.Resolution != "" {
			choice = d.Resolution
		}
		statusColor := d.Status
		if d.Status == "open" {
			statusColor = ui.Yellow("open")
		} else if d.Status == "resolved" {
			statusColor = ui.Green("resolved")
		}
		fmt.Fprintf(os.Stdout, "  %-6s %-10s %-5s %-7s %s\n", d.Address, statusColor, d.Recommendation, choice, d.Title)
	}
}

func viewDecision(ui render.UI, st *store.Store, project, address string, jsonOut bool) {
	d, err := st.GetDecision(project, address)
	if err != nil {
		if jsonOut {
			emitErrorEnvelope("DECISION_NOT_FOUND", err.Error(), "sila decision list")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "sila decision view error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(d)
		return
	}

	statusStr := d.Status
	if d.Status == "open" {
		statusStr = ui.Yellow("open")
	} else if d.Status == "deferred" {
		statusStr = ui.Yellow("deferred")
	} else if d.Status == "resolved" {
		statusStr = ui.Green("resolved")
	}

	fmt.Fprintf(os.Stdout, "\n  %s %s: %s\n", ui.Cyan("·"), ui.Bold(d.Address), ui.Bold(d.Title))
	fmt.Fprintf(os.Stdout, "  Status:         %s\n", statusStr)
	if d.GoalSlug != "" {
		fmt.Fprintf(os.Stdout, "  Goal:           %s\n", d.GoalSlug)
	}
	if d.Context != "" {
		fmt.Fprintf(os.Stdout, "  Context:        %s\n", d.Context)
	}
	fmt.Fprintf(os.Stdout, "  Recommendation: Option %s\n", ui.Bold(d.Recommendation))
	if d.Rationale != "" {
		fmt.Fprintf(os.Stdout, "  Rationale:      %s\n", d.Rationale)
	}
	fmt.Fprintln(os.Stdout, "\n  Options:")
	for _, opt := range d.Options {
		tradeOff := ""
		if opt.TradeOff != "" {
			tradeOff = " (" + opt.TradeOff + ")"
		}
		marker := " "
		if opt.Letter == d.Recommendation {
			marker = "★"
		}
		if opt.Letter == d.Resolution {
			marker = "✔"
		}
		fmt.Fprintf(os.Stdout, "    %s [%s] %s%s\n", marker, opt.Letter, opt.Title, tradeOff)
	}

	if d.UserQuote != "" {
		fmt.Fprintf(os.Stdout, "\n  User Quote:\n    %s\n", d.UserQuote)
	}
	if d.RouteNote != "" {
		fmt.Fprintf(os.Stdout, "  Route Note: %s\n", d.RouteNote)
	}

	if d.Status == "resolved" {
		fmt.Fprintln(os.Stdout, "\n  Resolution:")
		fmt.Fprintf(os.Stdout, "    Selected:    %s\n", ui.Bold(d.Resolution))
		if d.ResolutionNotes != "" {
			fmt.Fprintf(os.Stdout, "    Notes:       %s\n", d.ResolutionNotes)
		}
		fmt.Fprintf(os.Stdout, "    Resolved By: %s\n", d.ResolvedBy)
		fmt.Fprintf(os.Stdout, "    Resolved At: %s\n", d.ResolvedAt)
	}
	fmt.Fprintln(os.Stdout)
}

func checkDecisionsCmd(ui render.UI, st *store.Store, project string, jsonOut bool, args []string) {
	fs := flag.NewFlagSet("sila decision check", flag.ContinueOnError)
	failOnOpen := fs.Bool("fail-on-open", false, "Exit non-zero if any open decisions remain")
	failOnDeferred := fs.Bool("fail-on-deferred", false, "Exit non-zero if any deferred decisions remain")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	anomalies, err := st.CheckDecisionCollisions(project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila decision check error: %v\n", err)
		os.Exit(1)
	}

	decisions, err := st.ListDecisions(store.DecisionFilter{ProjectID: project})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila decision check error: %v\n", err)
		os.Exit(1)
	}

	openCount := 0
	deferredCount := 0
	for _, d := range decisions {
		if d.Status == "open" {
			openCount++
		}
		if d.Status == "deferred" {
			deferredCount++
		}
	}
	failDeferred := *failOnDeferred || *failOnOpen

	res := map[string]any{
		"project":   project,
		"total":     len(decisions),
		"open":      openCount,
		"deferred":  deferredCount,
		"anomalies": anomalies,
		"clean":     len(anomalies) == 0 && (!*failOnOpen || openCount == 0) && (!failDeferred || deferredCount == 0),
	}

	if jsonOut {
		emitJSON(res)
		if len(anomalies) > 0 || (*failOnOpen && openCount > 0) || (failDeferred && deferredCount > 0) {
			os.Exit(1)
		}
		return
	}

	if len(anomalies) > 0 {
		fmt.Fprintf(os.Stdout, "%s Detected %d decision collision(s) or invariant violation(s):\n", ui.Red("✖"), len(anomalies))
		for _, a := range anomalies {
			fmt.Fprintf(os.Stdout, "  - %s\n", a)
		}
		os.Exit(1)
	}

	fmt.Fprintf(os.Stdout, "%s Decision log integrity verified (%d decisions, 0 collisions, %d open, %d deferred)\n", ui.Green("✔"), len(decisions), openCount, deferredCount)
	if *failOnOpen && openCount > 0 {
		fmt.Fprintf(os.Stderr, "failed: %d open decision(s) pending resolution\n", openCount)
		os.Exit(1)
	}
	if failDeferred && deferredCount > 0 {
		fmt.Fprintf(os.Stderr, "failed: %d deferred decision(s) pending resolution\n", deferredCount)
		os.Exit(1)
	}
}

func printDecisionsSummary(ui render.UI, st *store.Store, project string, jsonOut bool) {
	decisions, err := st.ListDecisions(store.DecisionFilter{ProjectID: project})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error listing decisions: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(decisions)
		return
	}

	if len(decisions) == 0 {
		fmt.Fprintf(os.Stdout, "%s No addressed decisions recorded for %s.\n", ui.Cyan("·"), ui.Bold(project))
		fmt.Fprintln(os.Stdout, "  Create one: sila decision create 'Title' -o 'A: Opt 1' -o 'B: Opt 2' -r A")
		return
	}

	listDecisionsCmd(ui, st, project, "", false, nil)
}

func printDecisionHelp(ui render.UI) {
	fmt.Fprintln(os.Stdout, ui.Bold(ui.Cyan("sila decision"))+" — Addressed Epistemic Decision Log & Dispute Resolution Engine")
	fmt.Fprintln(os.Stdout, "\nUsage:")
	fmt.Fprintln(os.Stdout, "  sila decision create <title> -o 'A: Opt 1' -o 'B: Opt 2' -r A [--rationale='...'] [--context='...'] [--goal=<slug>]")
	fmt.Fprintln(os.Stdout, "  sila decision resolve <D#> --choice=A [--notes='...'] [--user-quote='...'] [--route-note='...'] [--require-quote]")
	fmt.Fprintln(os.Stdout, "  sila decision defer <D#> [--notes='...'] [--by=<actor>]")
	fmt.Fprintln(os.Stdout, "  sila decision list [--status=open|deferred|resolved|all] [--goal=<slug>]")
	fmt.Fprintln(os.Stdout, "  sila decision view <D#>")
	fmt.Fprintln(os.Stdout, "  sila decision check [--fail-on-open] [--fail-on-deferred]")
}

func parseOptionString(s string) *store.DecisionOption {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	idx := strings.Index(s, ":")
	if idx < 0 {
		return nil
	}
	letter := strings.ToUpper(strings.TrimSpace(s[:idx]))
	rest := strings.TrimSpace(s[idx+1:])

	// Extract optional trade-off in parentheses or brackets
	tradeOff := ""
	if openP := strings.LastIndex(rest, "("); openP > 0 && strings.HasSuffix(rest, ")") {
		tradeOff = strings.TrimSuffix(rest[openP+1:], ")")
		rest = strings.TrimSpace(rest[:openP])
	} else if openB := strings.LastIndex(rest, "["); openB > 0 && strings.HasSuffix(rest, "]") {
		tradeOff = strings.TrimSuffix(rest[openB+1:], "]")
		rest = strings.TrimSpace(rest[:openB])
	}

	return &store.DecisionOption{
		Letter:   letter,
		Title:    rest,
		TradeOff: tradeOff,
	}
}

func splitOptionsString(s string) []string {
	if strings.Contains(s, ";") {
		return strings.Split(s, ";")
	}
	return strings.Split(s, ",")
}
