package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/ast"
	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

type memoOptions struct {
	text    string
	since   string
	claim   string
	limit   int
	jsonOut bool
	toonOut bool
}

type memoListEntry struct {
	store.Fact
	Agent string `json:"agent"`
}

func memoUsage() {
	fmt.Fprintln(os.Stderr, "usage: sila memo \"<decision>\"")
	fmt.Fprintln(os.Stderr, "       sila memo add \"<decision>\"")
	fmt.Fprintln(os.Stderr, "       sila memo --text=\"<decision>\"")
	fmt.Fprintln(os.Stderr, "       sila memo list [--since=<duration|RFC3339>] [--limit=N] [--claim=<text>] [--json|--toon]")
	fmt.Fprintln(os.Stderr, "       sila memo show <fact-id> [--json]")
}

func runMemo(ui render.UI, dbPath, project, agent, files, decision string, opts memoOptions, args []string) {
	// Parse inline flags that may follow the action.
	var positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "--charter="):
			// Consumed by the signed subagent charter helper.
		case strings.HasPrefix(a, "--decision="):
			decision = strings.TrimPrefix(a, "--decision=")
		case strings.HasPrefix(a, "-d="):
			decision = strings.TrimPrefix(a, "-d=")
		case strings.HasPrefix(a, "--text="):
			opts.text = strings.TrimPrefix(a, "--text=")
		case strings.HasPrefix(a, "--since="):
			opts.since = strings.TrimPrefix(a, "--since=")
		case strings.HasPrefix(a, "--claim="):
			opts.claim = strings.TrimPrefix(a, "--claim=")
		case a == "--claim":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				fmt.Fprintln(os.Stderr, "sila memo: --claim requires a value")
				os.Exit(2)
			}
			opts.claim = strings.TrimSpace(args[i+1])
			i++
		case strings.HasPrefix(a, "--claim-text="):
			opts.claim = strings.TrimPrefix(a, "--claim-text=")
		case strings.HasPrefix(a, "--limit="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--limit=")); err == nil {
				opts.limit = n
			}
		case a == "--json":
			opts.jsonOut = true
		case a == "--toon":
			opts.toonOut = true
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "sila memo: unknown flag %q\n", a)
				memoUsage()
				os.Exit(2)
			}
			positional = append(positional, a)
		}
	}

	action := "add"
	rest := positional
	if len(positional) > 0 {
		switch positional[0] {
		case "add":
			rest = positional[1:]
		case "list", "ls":
			action = "list"
			rest = positional[1:]
		case "show", "get":
			action = "show"
			rest = positional[1:]
		case "delete", "rm", "prune":
			memoUsage()
			os.Exit(2)
		case "help":
			memoUsage()
			return
		}
	}
	if opts.text != "" {
		action = "add"
		rest = []string{opts.text}
	}
	if action == "add" && len(rest) == 0 {
		memoUsage()
		os.Exit(2)
	}

	readOnly := action == "list" || action == "show"
	var st *store.Store
	var err error
	if readOnly {
		st, err = store.OpenRO(dbPath)
	} else {
		st, err = store.Open(dbPath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	effectiveDir := cwd
	if rDir, err := goals.ResolveProjectDir(st, cwd); err == nil && rDir != "" {
		effectiveDir = rDir
	}
	requestedProject := strings.TrimSpace(project)
	if project != "*" {
		if project != "" {
			p, err := st.ResolveScope(project, effectiveDir)
			if err == nil && p.ID != "" {
				project = p.ID
				if p.Path != "" {
					effectiveDir = p.Path
				}
			}
		} else {
			p, err := st.ResolveScope("", effectiveDir)
			if err == nil && p.ID != "" {
				project = p.ID
				if p.Path != "" {
					effectiveDir = p.Path
				}
			} else {
				project = store.ProjectID(effectiveDir)
			}
		}
	}

	// Writes must key facts to a registered project. Without this, the first
	// memo creates facts under a bare derived id, and the next read treats that
	// id as a collision and disambiguates it with a path hash.
	if action == "add" && project != "*" {
		targetDir := ""
		switch {
		case requestedProject == "":
			targetDir = effectiveDir
		default:
			if fi, statErr := os.Stat(requestedProject); statErr == nil && fi.IsDir() {
				targetDir = requestedProject
			}
		}
		if targetDir != "" {
			p, regErr := st.EnsureProject(targetDir)
			if regErr != nil {
				fmt.Fprintf(os.Stderr, "sila memo: register project: %v\n", regErr)
				os.Exit(1)
			}
			project = p.ID
		}
	}

	switch action {
	case "list":
		limit := opts.limit
		if limit <= 0 {
			limit = 50
		}
		if limit > 500 {
			limit = 500
		}
		if opts.claim != "" && limit < 500 {
			limit = 500 // the diagnostic must span the historical records, not just the newest page
		}
		since := parseSince(opts.since)
		memos, err := st.ListFactsByKind("memo", project, since, limit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila memo list: %v\n", err)
			os.Exit(1)
		}
		// Historical memos may be keyed by the bare derived id because the write
		// path did not register the project. Surface them only for an
		// unregistered working directory, never across registered projects.
		if len(memos) == 0 {
			if _, registered, regErr := st.ProjectForDir(cwd); regErr == nil && !registered {
				raw := store.ProjectID(store.CanonicalProjectPath(cwd))
				if raw != "" && raw != project {
					if legacy, legacyErr := st.ListFactsByKind("memo", raw, since, limit); legacyErr == nil && len(legacy) > 0 {
						memos = legacy
						project = raw
					}
				}
			}
		}
		if opts.claim != "" {
			filtered := memos[:0]
			for _, memo := range memos {
				if strings.TrimSpace(memo.Claim) == strings.TrimSpace(opts.claim) {
					filtered = append(filtered, memo)
				}
			}
			memos = filtered
		}
		if opts.jsonOut {
			entries := make([]memoListEntry, 0, len(memos))
			for _, memo := range memos {
				entries = append(entries, memoListEntry{Fact: memo, Agent: memo.ToolName})
			}
			emitJSON(map[string]any{
				"project":         project,
				"project_display": cardProjectLabel(st, project),
				"count":           len(entries),
				"memos":           entries,
			})
			return
		}
		if opts.toonOut {
			rows := make([][]string, 0, len(memos))
			for _, memo := range memos {
				rows = append(rows, []string{memo.ID, memo.ProjectID, memo.FirstSeen, truncateMemo(memo.Claim, 80), strconv.Itoa(len(memo.RepoFiles)), memo.ToolName})
			}
			fmt.Fprint(os.Stdout, render.FormatTOONTable("memos", []string{"id", "project", "age", "claim", "files", "agent"}, rows))
			return
		}
		if len(memos) == 0 {
			fmt.Fprintf(os.Stdout, "%s No memos recorded for %s. Record one with `sila memo \"<directive>\"`.\n",
				ui.Yellow("▲"), ui.Bold(cardProjectLabel(st, project)))
			return
		}
		headers := []string{"id", "project", "age", "claim", "files", "agent"}
		rows := make([][]string, 0, len(memos))
		for _, memo := range memos {
			rows = append(rows, []string{memo.ID, memo.ProjectID, shortAge(memo.FirstSeen), truncateMemo(memo.Claim, 68), strconv.Itoa(len(memo.RepoFiles)), memo.ToolName})
		}
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "memos", Scope: cardProjectLabel(st, project), Title: fmt.Sprintf("%d recorded", len(memos))}))
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "r", "l", "r", "l"}))
		return

	case "show":
		if len(rest) == 0 {
			memoUsage()
			os.Exit(2)
		}
		fact, err := st.FactByID(rest[0])
		if err != nil || fact == nil {
			if opts.jsonOut {
				emitJSON(map[string]any{"error": "memo not found", "id": rest[0]})
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "memo not found: %s\n", rest[0])
			os.Exit(1)
		}
		if opts.jsonOut {
			emitJSON(fact)
			return
		}
		fmt.Fprintf(os.Stdout, "%s %s\n\n", ui.Bold(fact.ID), ui.Dim("· "+fact.ProjectID+" · "+fact.FirstSeen))
		_ = render.RenderMarkdown(os.Stdout, fact.Claim, ui)
		fmt.Fprintf(os.Stdout, "\nFiles: %v\nTool: %s\nClient: %s\n", fact.RepoFiles, fact.ToolName, fact.ClientKind)
		return

	case "add":
		content := strings.Join(rest, " ")
		var fileList []string
		if files != "" {
			for f := range strings.SplitSeq(files, ",") {
				if f = strings.TrimSpace(f); f != "" {
					fileList = append(fileList, f)
				}
			}
		}
		if decision != "" {
			if _, canonical, err := store.ParseAddress(decision); err == nil {
				fileList = append(fileList, "decision:"+canonical)
				if !strings.Contains(content, "[decision:") {
					content = fmt.Sprintf("[decision: %s] %s", canonical, content)
				}
			}
		}
		var fact *store.Fact
		if charter, ok, cerr := charterForWrite(os.Args[1:]); cerr != nil {
			charterFail(cerr)
		} else if ok {
			for _, f := range fileList {
				if perr := charter.ValidatePath(f); perr != nil {
					charterFail(perr)
				}
			}
			project, agent = charter.ProjectID, charter.OriginRole()
			fact, err = st.InsertMemoWithProvenance(project, content, agent, cwd, fileList, "", store.Provenance{
				SessionRef: charter.ParentSessionID, OriginRole: charter.OriginRole(),
			})
		} else if sessionRef := resolveWriteSession(project); sessionRef != "" {
			fact, err = st.InsertMemoWithSession(project, content, agent, cwd, fileList, sessionRef)
		} else {
			fact, err = st.InsertMemo(project, content, agent, cwd, fileList)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		recordMemoryEvent(st, project, agent, "memo", "ok", "", "cli", 0)
		if opts.jsonOut {
			fact.RepoFiles = jsonCardFiles(cwd, fact.RepoFiles)
			emitJSON(fact)
			return
		}
		fmt.Fprint(os.Stdout, render.RenderCard(ui, render.Card{
			Verb:   "Recorded",
			Object: "memo",
			ID:     fact.ID,
			Fields: []render.Field{
				{Label: "Directive", Value: strings.ReplaceAll(content, "\n", " ")},
				{Label: "Project", Value: cardProjectLabel(st, project)},
				{Label: "Files", Value: displayCardFiles(cwd, fileList)},
			},
		}))
	}
}

// cardProjectLabel resolves one project selector to the canonical human label:
// "<friendly> (<id>)" when both exist, otherwise the id or the raw selector.
func cardProjectLabel(st *store.Store, project string) string {
	if st != nil && project != "" {
		cwd, _ := os.Getwd()
		if p, err := st.ResolveScope(project, cwd); err == nil && p.ID != "" {
			friendly := ""
			if p.Path != "" {
				friendly = filepath.Base(p.Path)
			}
			return render.DisplayProject(friendly, p.ID)
		}
	}
	return render.DisplayProject("", project)
}

// jsonCardFiles applies --relative-paths to a JSON file list.
func jsonCardFiles(root string, files []string) []string {
	if !relativePathsMode {
		return files
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, render.DisplayPath(root, f))
	}
	return out
}

// displayCardFiles renders a file list with repository-relative paths.
func displayCardFiles(root string, files []string) string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, render.DisplayPath(root, f))
	}
	return strings.Join(out, ", ")
}

func isSingleWordToken(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && !strings.ContainsAny(s, " \t\n")
}

func unknownVerbUsage(verb, token string) {
	fmt.Fprintf(os.Stderr, "sila %s: unknown subcommand %q\n", verb, token)
	fmt.Fprintf(os.Stderr, "usage: sila %s [add|list|show]\n", verb)
	os.Exit(2)
}

func truncateMemo(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

func shortAge(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return "?"
	}
	age := time.Since(t)
	switch {
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	}
}

// runNote routes `sila note` subcommands: add, list, sync.
func runNote(ui render.UI, dbPath, project string, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "sila note: specify a subcommand: add, list, or sync")
		fmt.Fprintln(os.Stdout, "help: sila note add \"cli/internal/store::Open\" \"Always set busy_timeout(5000)\" --kind=invariant")
		os.Exit(2)
	}

	st, err := store.Open(dbPath)
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
		} else if !st.ReadOnly {
			if proj, err := st.EnsureProject(cwd); err == nil && proj.ID != "" {
				project = proj.ID
			} else {
				project = store.ProjectID(cwd)
			}
		}
	} else {
		p, err := st.ResolveScope("", cwd)
		if err == nil && p.ID != "" {
			project = p.ID
		} else if !st.ReadOnly {
			if proj, err := st.EnsureProject(cwd); err == nil && proj.ID != "" {
				project = proj.ID
			} else {
				project = store.ProjectID(cwd)
			}
		} else {
			project = store.ProjectID(cwd)
		}
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "add":
		runNoteAdd(ui, st, project, cwd, jsonOut, subArgs)
	case "list", "ls":
		runNoteList(ui, st, project, jsonOut, subArgs)
	case "sync":
		runNoteSync(ui, st, project, cwd, jsonOut, subArgs)
	default:
		// If first argument looks like a target, route dynamically
		if len(args) >= 2 && !strings.HasPrefix(args[0], "-") {
			runNoteAdd(ui, st, project, cwd, jsonOut, args)
		} else {
			runNoteList(ui, st, project, jsonOut, args)
		}
	}
}

func runNoteAdd(ui render.UI, st *store.Store, project, cwd string, jsonOut bool, args []string) {
	kind := "memo"
	target := ""
	var positional []string

	for _, a := range args {
		if strings.HasPrefix(a, "--kind=") {
			kind = strings.TrimPrefix(a, "--kind=")
		} else if strings.HasPrefix(a, "-k=") {
			kind = strings.TrimPrefix(a, "-k=")
		} else if strings.HasPrefix(a, "--symbol=") {
			target = strings.TrimPrefix(a, "--symbol=")
		} else if strings.HasPrefix(a, "-s=") {
			target = strings.TrimPrefix(a, "-s=")
		} else if strings.HasPrefix(a, "--target=") {
			target = strings.TrimPrefix(a, "--target=")
		} else if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
		}
	}

	if target == "" {
		if len(positional) < 2 {
			fmt.Fprintln(os.Stderr, "sila note add: target symbol and note text required")
			fmt.Fprintln(os.Stdout, "usage: sila note add <target> \"<text>\" [--kind=memo|lesson|invariant]")
			os.Exit(2)
		}
		target = positional[0]
		positional = positional[1:]
	} else {
		if len(positional) < 1 {
			fmt.Fprintln(os.Stderr, "sila note add: note text required")
			fmt.Fprintln(os.Stdout, "usage: sila note add --symbol=<target> \"<text>\" [--kind=memo|lesson|invariant]")
			os.Exit(2)
		}
	}

	text := strings.Join(positional, " ")

	note := &store.SymbolNote{
		ProjectID:   project,
		CanonicalID: target,
		SymbolName:  target,
		NoteText:    text,
		NoteKind:    kind,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	// Try extracting rich symbol metadata from AST if local files match
	if strings.Contains(target, "::") {
		parts := strings.Split(target, "::")
		pkgOrFile := parts[0]
		symName := parts[1]
		note.SymbolName = symName

		var candidateFile string
		if strings.HasSuffix(pkgOrFile, ".go") {
			candidateFile = filepath.Join(cwd, pkgOrFile)
		}
		if candidateFile != "" {
			if syms, err := ast.ExtractSymbolsFromFile(candidateFile, cwd); err == nil {
				for _, s := range syms {
					if s.SymbolName == symName || s.CanonicalID == target || ast.MatchSymbolTarget(s.CanonicalID, target) {
						note.CanonicalID = s.CanonicalID
						note.PackagePath = s.PackagePath
						note.FilePath = s.FilePath
						note.SymbolKind = string(s.SymbolKind)
						note.Receiver = s.Receiver
						note.SpanStartLine = s.StartLine
						note.SpanEndLine = s.EndLine
						break
					}
				}
			}
		} else {
			dir := filepath.Join(cwd, pkgOrFile)
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				if syms, err := ast.ExtractSymbolsFromDir(dir, cwd); err == nil {
					for _, s := range syms {
						if s.SymbolName == symName || s.CanonicalID == target || ast.MatchSymbolTarget(s.CanonicalID, target) {
							note.CanonicalID = s.CanonicalID
							note.PackagePath = s.PackagePath
							note.FilePath = s.FilePath
							note.SymbolKind = string(s.SymbolKind)
							note.Receiver = s.Receiver
							note.SpanStartLine = s.StartLine
							note.SpanEndLine = s.EndLine
							break
						}
					}
				}
			}
		}
	}

	// Gather git metadata
	note.GitSHA = gitOutput(cwd, "rev-parse", "--short", "HEAD")
	note.GitBranch = gitOutput(cwd, "branch", "--show-current")
	note.CommitterDate = gitOutput(cwd, "log", "-1", "--format=%cI")
	if note.CommitterDate == "" {
		note.CommitterDate = time.Now().UTC().Format(time.RFC3339)
	}

	if err := st.InsertSymbolNote(note); err != nil {
		fmt.Fprintf(os.Stderr, "sila note add: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(note)
		return
	}

	fmt.Fprintf(os.Stdout, "%s Recorded symbol %s for %s\n", ui.Green("✔"), ui.Bold(kind), ui.Cyan(note.CanonicalID))
	fmt.Fprintf(os.Stdout, "  Text: %s\n", note.NoteText)
	if note.FilePath != "" && note.SpanStartLine > 0 {
		fmt.Fprintf(os.Stdout, "  Span: %s:%d-%d\n", note.FilePath, note.SpanStartLine, note.SpanEndLine)
	}
}

func runNoteList(ui render.UI, st *store.Store, project string, jsonOut bool, args []string) {
	target := ""
	for _, a := range args {
		if strings.HasPrefix(a, "--target=") {
			target = strings.TrimPrefix(a, "--target=")
		} else if strings.HasPrefix(a, "--symbol=") {
			target = strings.TrimPrefix(a, "--symbol=")
		} else if strings.HasPrefix(a, "-s=") {
			target = strings.TrimPrefix(a, "-s=")
		} else if !strings.HasPrefix(a, "-") {
			target = a
		}
	}

	notes, err := st.ListSymbolNotes(project, target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila note list: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(notes)
		return
	}

	if len(notes) == 0 {
		if target != "" {
			fmt.Fprintf(os.Stdout, "%s No symbol notes found matching %q.\n", ui.Yellow("▲"), target)
		} else {
			fmt.Fprintf(os.Stdout, "%s No symbol notes registered in project.\n", ui.Yellow("▲"))
		}
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · note"))+ui.Dim(fmt.Sprintf(" · %d %s", len(notes), plural(int64(len(notes)), "note"))))
	fmt.Fprintln(os.Stdout)
	for _, n := range notes {
		span := ""
		if n.FilePath != "" && n.SpanStartLine > 0 {
			span = fmt.Sprintf(" (%s:%d-%d)", n.FilePath, n.SpanStartLine, n.SpanEndLine)
		}
		fmt.Fprintf(os.Stdout, "  📌 [%s] %s%s\n", ui.Bold(n.NoteKind), ui.Cyan(n.CanonicalID), ui.Dim(span))
		fmt.Fprintf(os.Stdout, "     %s\n", n.NoteText)
		meta := []string{}
		if n.GitSHA != "" {
			meta = append(meta, "commit "+n.GitSHA)
		}
		if n.GitBranch != "" {
			meta = append(meta, n.GitBranch)
		}
		if n.CreatedAt != "" && len(n.CreatedAt) >= 10 {
			meta = append(meta, n.CreatedAt[:10])
		}
		if len(meta) > 0 {
			fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim(strings.Join(meta, " · ")))
		}
		fmt.Fprintln(os.Stdout)
	}
}

func runNoteSync(ui render.UI, st *store.Store, project, cwd string, jsonOut bool, args []string) {
	exportFlag := false
	importFlag := false
	for _, a := range args {
		if a == "--export" || a == "-e" {
			exportFlag = true
		} else if a == "--import" || a == "-i" {
			importFlag = true
		}
	}

	notesFile := filepath.Join(cwd, ".sila", "notes.json")

	if importFlag && !exportFlag {
		data, err := os.ReadFile(notesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila note sync: read %s: %v\n", notesFile, err)
			os.Exit(1)
		}
		var notes []store.SymbolNote
		if err := json.Unmarshal(data, &notes); err != nil {
			fmt.Fprintf(os.Stderr, "sila note sync: parse %s: %v\n", notesFile, err)
			os.Exit(1)
		}
		count, err := st.ImportSymbolNotes(project, notes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila note sync: import: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s Imported %d symbol notes from %s\n", ui.Green("✔"), count, notesFile)
		return
	}

	if exportFlag && !importFlag {
		notes, err := st.ExportSymbolNotes(project)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila note sync: export: %v\n", err)
			os.Exit(1)
		}
		if err := os.MkdirAll(filepath.Dir(notesFile), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "sila note sync: mkdir: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(notes, "", "  ")
		if err := os.WriteFile(notesFile, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "sila note sync: write %s: %v\n", notesFile, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "%s Exported %d symbol notes to %s\n", ui.Green("✔"), len(notes), notesFile)
		return
	}

	// Two-way sync: import first if exists, then export
	imported := 0
	if data, err := os.ReadFile(notesFile); err == nil {
		var notes []store.SymbolNote
		if err := json.Unmarshal(data, &notes); err == nil {
			imported, _ = st.ImportSymbolNotes(project, notes)
		}
	}

	allNotes, err := st.ExportSymbolNotes(project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila note sync: export: %v\n", err)
		os.Exit(1)
	}
	_ = os.MkdirAll(filepath.Dir(notesFile), 0755)
	data, _ := json.MarshalIndent(allNotes, "", "  ")
	_ = os.WriteFile(notesFile, data, 0644)

	fmt.Fprintf(os.Stdout, "%s Synchronized symbol notes (imported %d, total %d active in %s)\n",
		ui.Green("✔"), imported, len(allNotes), notesFile)
}

func gitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
