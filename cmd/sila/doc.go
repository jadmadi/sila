package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/ingest"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// docExitFunc allows overriding os.Exit during testing.
var docExitFunc = os.Exit

// DocInputKind classifies the user's positional argument to `sila doc`.
type DocInputKind int

// DocInputKind constants classify positional inputs provided to `sila doc`.
const (
	DocInputFileOrDir DocInputKind = iota
	DocInputURL
	DocInputBareDomain
	DocInputExplicitPathMissing
	DocInputSearchQuery
)

// docFileExtensions lists common document and code extensions to disambiguate
// explicit file paths from bare domains or search terms.
var docFileExtensions = map[string]bool{
	".md":       true,
	".markdown": true,
	".mdown":    true,
	".mkd":      true,
	".html":     true,
	".htm":      true,
	".txt":      true,
	".text":     true,
	".pdf":      true,
	".rst":      true,
	".adoc":     true,
	".asciidoc": true,
	".json":     true,
	".yaml":     true,
	".yml":      true,
	".xml":      true,
	".go":       true,
	".py":       true,
	".ts":       true,
	".js":       true,
	".rs":       true,
	".c":        true,
	".cpp":      true,
	".h":        true,
	".java":     true,
	".rb":       true,
	".php":      true,
	".sh":       true,
}

// classifyDocInput evaluates target and determines its intended category.
func classifyDocInput(target string) DocInputKind {
	target = strings.TrimSpace(target)
	if target == "" {
		return DocInputSearchQuery
	}

	// 1. Existing local file or directory
	if _, err := os.Stat(target); err == nil {
		return DocInputFileOrDir
	}

	// 2. Explicit HTTP/HTTPS scheme
	lower := strings.ToLower(target)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return DocInputURL
	}

	// 3. Explicit path notation (relative or absolute) that failed os.Stat
	if strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") ||
		strings.HasPrefix(target, "/") || strings.HasPrefix(target, "~/") ||
		strings.HasPrefix(target, `.\`) || strings.HasPrefix(target, `..\`) {
		return DocInputExplicitPathMissing
	}

	// Explicit document or code extension that does not exist on disk
	ext := strings.ToLower(filepath.Ext(target))
	if docFileExtensions[ext] {
		return DocInputExplicitPathMissing
	}

	// 4. Bare domain without scheme (e.g. docs.stripe.com, golang.org)
	if isBareDomain(target) {
		return DocInputBareDomain
	}

	// 5. If it contains path separators and was not recognized as a domain, treat as missing path
	if strings.Contains(target, "/") || strings.Contains(target, `\`) {
		return DocInputExplicitPathMissing
	}

	// 6. Otherwise, treat as search query / topic
	return DocInputSearchQuery
}

// isBareDomain verifies if a string matches a domain name without URL scheme.
func isBareDomain(s string) bool {
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	host := s
	if idx := strings.IndexAny(s, "/?#"); idx != -1 {
		host = s[:idx]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
		if part[0] == '-' || part[len(part)-1] == '-' {
			return false
		}
		for _, r := range part {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	tld := parts[len(parts)-1]
	if len(tld) < 2 {
		return false
	}
	for _, r := range tld {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	if docFileExtensions["."+strings.ToLower(tld)] {
		return false
	}
	return true
}

// reportAutoIngest surfaces the outcome of a --auto-ingest write.
func reportAutoIngest(ui render.UI, openErr error, saved int, err error, label, project string) {
	switch {
	case openErr != nil:
		fmt.Fprintln(os.Stderr, ui.Red("✖")+fmt.Sprintf(" --auto-ingest could not open the store: %v", openErr))
	case err != nil:
		fmt.Fprintln(os.Stderr, ui.Red("✖")+fmt.Sprintf(" --auto-ingest failed, nothing was saved: %v", err))
	default:
		fmt.Fprintln(os.Stdout, ui.Green("✔")+fmt.Sprintf(" Auto-ingested %s into %s.", label, ui.Bold(project)))
	}
}

// runDocDashboard renders the structured bare `sila doc` overview.
func runDocDashboard(ui render.UI, dbPath, project string, jsonOut bool) {
	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}

	var totalDocs int
	var totalBytes int64
	var recentDocs []store.Doc

	if _, err := os.Stat(dbPath); err == nil {
		if st, err := store.OpenRO(dbPath); err == nil {
			defer st.Close()
			totalDocs, totalBytes, _ = st.DocStats(project)
			recentDocs, _ = st.DocsByProject(project, 5)
		}
	}

	cfg, _ := config.Load("")
	docSites := cfg.DocSites

	docDir, docErr := resolveDocDir("")
	docServerStatus := "available (run: sila doc serve)"
	if docErr != nil {
		docServerStatus = "not built (html-docs not found)"
	}

	if jsonOut {
		emitJSON(map[string]any{
			"project":     project,
			"total_docs":  totalDocs,
			"total_bytes": totalBytes,
			"total_size":  render.FormatBytes(totalBytes),
			"recent_docs": recentDocs,
			"doc_sites":   docSites,
			"doc_portal": map[string]any{
				"directory": docDir,
				"status":    docServerStatus,
				"command":   "sila doc serve",
			},
		})
		return
	}

	fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "doc", Title: "reference documentation engine"}))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Project:", ui.Bold(project))
	fmt.Fprintf(os.Stdout, "  %-18s %s (%s)\n", "Indexed Docs:", render.FormatNumber(int64(totalDocs)), render.FormatBytes(totalBytes))
	if docErr == nil {
		fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Doc Portal:", ui.Green("available")+" ("+ui.Dim(docDir)+")")
	} else {
		fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Doc Portal:", ui.Dim("not found"))
	}
	fmt.Fprintln(os.Stdout)

	if len(recentDocs) > 0 {
		fmt.Fprintln(os.Stdout, ui.Bold("Recently Indexed Documents:"))
		headers := []string{"id", "title", "url / source", "created"}
		var rows [][]string
		for _, d := range recentDocs {
			ts := d.CreatedAt
			if t, err := time.Parse(time.RFC3339, d.CreatedAt); err == nil {
				ts = t.Format("2006-01-02 15:04")
			}
			source := d.URL
			if source == "" {
				source = "(local file)"
			}
			rows = append(rows, []string{d.ID, ui.Bold(d.Title), ui.Dim(trunc(source, 36)), ui.Dim(ts)})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))
		fmt.Fprintln(os.Stdout)
	}

	if len(docSites) > 0 {
		fmt.Fprintln(os.Stdout, ui.Bold("Registered Documentation Sites:"))
		for _, s := range docSites {
			fmt.Fprintf(os.Stdout, "  • %s\n", s)
		}
		fmt.Fprintln(os.Stdout)
	}

	fmt.Fprintln(os.Stdout, ui.Bold("Commands & Usage:"))
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc <url|domain>", "Ingest or crawl documentation site")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc <file|dir>", "Ingest local markdown, HTML or export directory")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc ingest <path>", "Ingest wiki export (--format=auto|notion|confluence|gitbook)")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc <query>", "Search indexed documents (e.g. sila doc react)")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc search <query>", "Full-text search indexed documentation")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc list", "List all indexed documents for project")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc get <id>", "View full document in terminal")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc serve", "Start local offline documentation web portal")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc web-search <query>", "Search web documentation (--auto-ingest)")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc code-search <query>", "Search GitHub code via Sourcegraph/GitHub")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc pkg <package>", "Fetch package documentation (Go, Rust, NPM, Python)")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc so <query>", "Search Stack Overflow solutions (--auto-ingest)")
	fmt.Fprintf(os.Stdout, "  %-32s %s\n", "sila doc sites [add|rm] <site>", "Manage preferred documentation sites")
}

// runDoc ingests, lists, gets, crawls, or searches external reference docs.
func runDoc(ui render.UI, dbPath, project, customTitle, lang, repo, ext, eco, tag string, crawl bool, depth, maxPages, top int, autoIngest bool, port int, host string, noOpen, jsonOut, verbose bool, format string, args []string) {
	if len(args) == 0 {
		runDocDashboard(ui, dbPath, project, jsonOut)
		return
	}

	sub := args[0]
	switch sub {
	case "ingest":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila doc ingest <path> [--format=auto|notion|confluence|gitbook]")
			docExitFunc(2)
			return
		}
		target := args[1]
		st, err := store.Open(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			docExitFunc(1)
			return
		}
		defer st.Close()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}

		res, err := ingest.IngestExport(st, target, ingest.IngestExportOptions{
			Format:      ingest.ParseExportFormat(format),
			ProjectID:   project,
			CustomTitle: customTitle,
		})
		if err != nil {
			fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("doc ingest failed: %v", err), "sila doc --help"))
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(res)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+fmt.Sprintf(" Ingested and indexed %d documents from %s export into %s.", res.TotalFiles, ui.Bold(string(res.DetectedFormat)), ui.Bold(project)))
		return

	case "serve":
		customDir := ""
		if len(args) > 1 {
			customDir = args[1]
		}
		runDocServe(ui, port, host, noOpen, jsonOut, customDir)
		return
	case "sites":
		cfg, _ := config.Load("")
		if len(args) == 1 || (len(args) > 1 && args[1] == "list") {
			if jsonOut {
				emitJSON(cfg.DocSites)
				return
			}
			if len(cfg.DocSites) == 0 {
				fmt.Fprintln(os.Stdout, ui.Dim("No registered documentation sites. Add with: sila doc sites add <url>"))
				return
			}
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("Registered Documentation Sites:")))
			for _, s := range cfg.DocSites {
				fmt.Fprintf(os.Stdout, "  • %s\n", s)
			}
			return
		}
		action := args[1]
		if action == "add" {
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "Usage: sila doc sites add <url|domain>")
				docExitFunc(2)
				return
			}
			site := args[2]
			if err := config.AddDocSite("", site); err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				docExitFunc(1)
				return
			}
			fmt.Fprintln(os.Stdout, ui.Green("✔")+" Added documentation site: "+ui.Bold(site))
			return
		}
		if action == "rm" || action == "remove" {
			if len(args) < 3 {
				fmt.Fprintln(os.Stderr, "Usage: sila doc sites rm <url|domain>")
				docExitFunc(2)
				return
			}
			site := args[2]
			if err := config.RemoveDocSite("", site); err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				docExitFunc(1)
				return
			}
			fmt.Fprintln(os.Stdout, ui.Green("✔")+" Removed documentation site: "+ui.Bold(site))
			return
		}

	case "web-search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila doc web-search <query> [--auto-ingest]")
			docExitFunc(2)
			return
		}
		query := strings.Join(args[1:], " ")
		cfg, _ := config.Load("")
		results, providerName, err := ingest.SearchWeb(query, 5, cfg.DocSites, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila web-search error (%s): %v\n", providerName, err)
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(results)
			return
		}
		if len(results) == 0 {
			fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("No web search results found via %s.", providerName)))
			return
		}

		var st *store.Store
		var openErr error
		if autoIngest {
			st, openErr = store.Open(dbPath)
		} else {
			st, _ = store.OpenRO(dbPath)
		}
		defer func() {
			if st != nil {
				st.Close()
			}
		}()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}

		if autoIngest {
			var n int
			var err error
			if st != nil {
				docs, ierr := ingest.IngestSearchResults(st, results, project)
				n, err = len(docs), ierr
			}
			reportAutoIngest(ui, openErr, n, err, fmt.Sprintf("%d reference pages", n), project)
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("Web Search Results for %q (via %s):", query, providerName))))
		for i, r := range results {
			fmt.Fprintf(os.Stdout, "\n  %d. %s\n     %s\n     %s\n", i+1, ui.Bold(r.Title), ui.Dim(r.Link), r.Snippet)
		}

	case "code-search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila doc code-search <query> [--lang=go] [--repo=owner/repo] [--ext=go] [--auto-ingest]")
			docExitFunc(2)
			return
		}
		query := strings.Join(args[1:], " ")
		cfg, _ := config.Load("")
		topN := 5
		if top > 0 {
			topN = top
		}
		q := ingest.CodeSearchQuery{
			Query:     query,
			Language:  lang,
			Repo:      repo,
			Extension: ext,
			Top:       topN,
		}
		results, providerName, err := ingest.SearchCode(q, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila code-search error (%s): %v\n", providerName, err)
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(results)
			return
		}
		if len(results) == 0 {
			fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("No code results found via %s.", providerName)))
			return
		}

		var st *store.Store
		var openErr error
		if autoIngest {
			st, openErr = store.Open(dbPath)
		} else {
			st, _ = store.OpenRO(dbPath)
		}
		defer func() {
			if st != nil {
				st.Close()
			}
		}()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}

		if autoIngest {
			var n int
			var err error
			if st != nil {
				docs, ierr := ingest.IngestCodeSearchResults(st, results, project)
				n, err = len(docs), ierr
			}
			reportAutoIngest(ui, openErr, n, err, fmt.Sprintf("%d code files", n), project)
		}

		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("Code Search Results for %q (via %s):", query, providerName))))
		for i, r := range results {
			fmt.Fprintf(os.Stdout, "\n  %d. %s  %s\n     %s\n", i+1, ui.Bold(r.Repo), ui.Cyan(r.Path), ui.Dim(r.HTMLURL))
			if r.Snippet != "" {
				fmt.Fprintf(os.Stdout, "     %s\n", r.Snippet)
			}
		}

	case "pkg", "package":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila doc pkg <package-name> [--eco=auto|go|rust|npm|python] [--auto-ingest]")
			docExitFunc(2)
			return
		}
		pkgName := args[1]
		pkgDoc, err := ingest.FetchPackageDoc(pkgName, eco)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila pkg error: %v\n", err)
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(pkgDoc)
			return
		}
		var st *store.Store
		var openErr error
		if autoIngest {
			st, openErr = store.Open(dbPath)
		} else {
			st, _ = store.OpenRO(dbPath)
		}
		defer func() {
			if st != nil {
				st.Close()
			}
		}()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}
		if autoIngest {
			var err error
			if st != nil {
				_, err = ingest.IngestPackageDoc(st, pkgDoc, project)
			}
			reportAutoIngest(ui, openErr, 0, err, fmt.Sprintf("package doc for %s", ui.Bold(pkgDoc.Name)), project)
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("📦 Package Doc: %s (%s - v%s)", pkgDoc.Name, strings.ToUpper(pkgDoc.Ecosystem), pkgDoc.Version))))
		if pkgDoc.Summary != "" {
			fmt.Fprintf(os.Stdout, "Summary: %s\n", pkgDoc.Summary)
		}
		if pkgDoc.DocURL != "" {
			fmt.Fprintf(os.Stdout, "Doc URL: %s\n", ui.Dim(pkgDoc.DocURL))
		}
		if pkgDoc.RepoURL != "" {
			fmt.Fprintf(os.Stdout, "Repo:    %s\n", ui.Dim(pkgDoc.RepoURL))
		}

	case "so", "stackoverflow", "qa":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: sila doc so <query> [--tag=tag] [--auto-ingest]")
			docExitFunc(2)
			return
		}
		query := strings.Join(args[1:], " ")
		cfg, _ := config.Load("")
		topN := 5
		if top > 0 {
			topN = top
		}
		results, err := ingest.SearchStackOverflow(query, tag, topN, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila stackoverflow error: %v\n", err)
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(results)
			return
		}
		if len(results) == 0 {
			fmt.Fprintln(os.Stdout, ui.Dim("No Stack Overflow threads found."))
			return
		}
		var st *store.Store
		var openErr error
		if autoIngest {
			st, openErr = store.Open(dbPath)
		} else {
			st, _ = store.OpenRO(dbPath)
		}
		defer func() {
			if st != nil {
				st.Close()
			}
		}()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}
		if autoIngest {
			var n int
			var err error
			if st != nil {
				docs, ierr := ingest.IngestStackOverflowResults(st, results, project)
				n, err = len(docs), ierr
			}
			reportAutoIngest(ui, openErr, n, err, fmt.Sprintf("%d Stack Overflow solution threads", n), project)
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("Stack Overflow QA for %q:", query))))
		for i, r := range results {
			ansMarker := ui.Dim("[unanswered]")
			if r.IsAnswered {
				ansMarker = ui.Green("[✔ answered]")
			}
			fmt.Fprintf(os.Stdout, "\n  %d. %s %s (Score: %d)\n     %s\n     %s\n", i+1, ui.Bold(r.Title), ansMarker, r.Score, ui.Dim(r.Link), r.Snippet)
		}

	case "list":
		st, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			docExitFunc(1)
			return
		}
		defer st.Close()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}
		docs, err := st.DocsByProject(project, 50)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(docs)
			return
		}
		if len(docs) == 0 {
			fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("No attached documents found for project %q.", project)))
			return
		}
		headers := []string{"id", "title", "url", "created"}
		var rows [][]string
		for _, d := range docs {
			ts := d.CreatedAt
			if t, err := time.Parse(time.RFC3339, d.CreatedAt); err == nil {
				ts = t.Format("2006-01-02 15:04")
			}
			rows = append(rows, []string{d.ID, ui.Bold(d.Title), ui.Dim(trunc(d.URL, 40)), ui.Dim(ts)})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))

	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sila doc search: specify search query")
			docExitFunc(2)
			return
		}
		query := strings.Join(args[1:], " ")
		st, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			docExitFunc(1)
			return
		}
		defer st.Close()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}
		docs, err := st.SearchDocsFTS(query, project, 10)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(docs)
			return
		}
		if len(docs) == 0 {
			fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("No documents match %q.", query)))
			return
		}
		headers := []string{"id", "project", "title", "content snippet"}
		var rows [][]string
		for _, d := range docs {
			snippet := trunc(strings.ReplaceAll(store.ExtractDocSnippet(d.Content, query, 160), "\n", " "), 60)
			rows = append(rows, []string{d.ID, d.ProjectID, ui.Bold(d.Title), snippet})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))

	case "get":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sila doc get: specify document ID")
			docExitFunc(2)
			return
		}
		docID := args[1]
		st, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			docExitFunc(1)
			return
		}
		defer st.Close()
		doc, err := st.GetDoc(docID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			docExitFunc(1)
			return
		}
		if doc == nil {
			fmt.Fprintln(os.Stderr, "Document not found.")
			docExitFunc(1)
			return
		}
		if jsonOut {
			emitJSON(doc)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("# "+doc.Title)))
		if doc.URL != "" {
			fmt.Fprintf(os.Stdout, "Source: %s\n\n", ui.Dim(doc.URL))
		}
		_ = render.RenderMarkdown(os.Stdout, doc.Content, ui)
		fmt.Fprintln(os.Stdout)

	default:
		target := sub
		kind := classifyDocInput(target)

		switch kind {
		case DocInputExplicitPathMissing:
			fmt.Fprintf(os.Stderr, "sila doc: file or directory not found: %s\n", target)
			fmt.Fprintf(os.Stderr, "Tip: To search indexed documents, run: %s\n", ui.Bold("sila doc search "+target))
			fmt.Fprintf(os.Stderr, "Tip: To search the web, run: %s\n", ui.Bold(fmt.Sprintf("sila doc web-search %q --auto-ingest", target)))
			docExitFunc(1)
			return

		case DocInputSearchQuery:
			query := strings.Join(args, " ")
			cwd, _ := os.Getwd()
			if project == "" {
				project = store.ProjectID(cwd)
			}

			if _, err := os.Stat(dbPath); err != nil {
				if jsonOut {
					emitJSON([]store.Doc{})
					return
				}
				fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("No indexed documents match %q.", query)))
				fmt.Fprintln(os.Stdout)
				fmt.Fprintf(os.Stdout, "Tip: To search the web for this topic, run:\n  %s\n", ui.Bold(fmt.Sprintf("sila doc web-search %q --auto-ingest", query)))
				return
			}

			st, err := store.OpenRO(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				docExitFunc(1)
				return
			}
			defer st.Close()

			docs, err := st.SearchDocsFTS(query, project, 10)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				docExitFunc(1)
				return
			}
			if jsonOut {
				emitJSON(docs)
				return
			}
			if len(docs) == 0 {
				fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("No indexed documents match %q.", query)))
				fmt.Fprintln(os.Stdout)
				fmt.Fprintf(os.Stdout, "Tip: To search the web for this topic, run:\n  %s\n", ui.Bold(fmt.Sprintf("sila doc web-search %q --auto-ingest", query)))
				return
			}
			fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("Indexed Document Results for %q:", query))))
			headers := []string{"id", "project", "title", "content snippet"}
			var rows [][]string
			for _, d := range docs {
				snippet := trunc(strings.ReplaceAll(store.ExtractDocSnippet(d.Content, query, 160), "\n", " "), 60)
				rows = append(rows, []string{d.ID, d.ProjectID, ui.Bold(d.Title), snippet})
			}
			fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"l", "l", "l", "l"}))
			fmt.Fprintf(os.Stdout, "\nTip: View document with: %s\n", ui.Bold("sila doc get <id>"))
			return

		case DocInputBareDomain:
			target = "https://" + target
			if !jsonOut {
				fmt.Fprintln(os.Stdout, ui.Dim("Interpreting bare domain as "+target+" ..."))
			}
			fallthrough

		case DocInputURL:
			st, err := store.Open(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %v\n", err)
				docExitFunc(1)
				return
			}
			defer st.Close()
			cwd, _ := os.Getwd()
			if project == "" {
				project = store.ProjectID(cwd)
			}

			if crawl {
				opts := ingest.DefaultCrawlOptions()
				opts.MaxDepth = depth
				opts.MaxPages = maxPages
				opts.CustomTitle = customTitle
				sigCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
				defer cancel()
				docs, err := ingest.CrawlAndIngestContext(sigCtx, st, target, project, opts)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						fmt.Fprintln(os.Stderr, "\nsila crawl: cancelled by user.")
						docExitFunc(130)
						return
					}
					fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("doc crawl failed: %v", err), "sila doc <url>"))
					docExitFunc(1)
					return
				}
				if jsonOut {
					emitJSON(docs)
					return
				}
				fmt.Fprintln(os.Stdout, ui.Green("✔")+fmt.Sprintf(" Crawled and ingested %d documentation pages into %s.", len(docs), ui.Bold(project)))
				return
			}

			doc, err := ingest.IngestDoc(st, target, project, customTitle)
			if err != nil {
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("doc write failed: %v", err), "sila doc --help"))
				docExitFunc(1)
				return
			}
			if jsonOut {
				emitJSON(doc)
				return
			}
			fmt.Fprint(os.Stdout, render.RenderCard(ui, render.Card{
				Verb:   "Ingested",
				Object: "document",
				ID:     doc.ID,
				Fields: []render.Field{
					{Label: "Title", Value: doc.Title},
					{Label: "Project", Value: cardProjectLabel(st, project)},
					{Label: "URL", Value: doc.URL},
					{Label: "Format", Value: doc.Format},
				},
			}))

		case DocInputFileOrDir:
			fi, statErr := os.Stat(target)
			if statErr != nil {
				fmt.Fprint(os.Stderr, render.RenderError(ui,
					fmt.Sprintf("sila doc: cannot read %q", target), "check the path or pass a URL"))
				docExitFunc(2)
				return
			}
			isDir := fi.IsDir()
			st, err := store.Open(dbPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila: %s\n", friendlyStoreError(err))
				docExitFunc(1)
				return
			}
			defer st.Close()
			cwd, _ := os.Getwd()
			if project == "" {
				project = store.ProjectID(cwd)
			}

			res, err := ingest.IngestExport(st, target, ingest.IngestExportOptions{
				Format:      ingest.ParseExportFormat(format),
				ProjectID:   project,
				CustomTitle: customTitle,
			})
			if err != nil {
				fmt.Fprint(os.Stderr, render.RenderError(ui, fmt.Sprintf("sila doc: write failed: %s", friendlyStoreError(err)), "sila doc --help"))
				docExitFunc(1)
				return
			}
			if res.TotalFiles == 0 && len(res.Docs) == 0 {
				code := "no_documents"
				message := fmt.Sprintf("no documents found in %q", target)
				fix := "check the path or pass --format=<export>"
				exitCode := 1
				if isDir {
					code = "directory_target"
					message = fmt.Sprintf("%q is a directory with no recognized export layout", target)
					fix = `ingest one file, a URL, or use 'sila doc search "<query>"' (or pass --format=<export>)`
					exitCode = 2
				}
				if jsonOut {
					emitJSON(map[string]any{"error": map[string]any{
						"code": code, "message": message, "fix": fix,
					}})
					docExitFunc(exitCode)
					return
				}
				fmt.Fprint(os.Stderr, render.RenderError(ui, "sila doc: "+message, fix))
				docExitFunc(exitCode)
				return
			}
			if jsonOut {
				if res.TotalFiles == 1 && len(res.Docs) > 0 {
					emitJSON(res.Docs[0])
				} else {
					emitJSON(res)
				}
				return
			}
			if isDir || res.TotalFiles > 1 {
				fmt.Fprintln(os.Stdout, ui.Green("✔")+fmt.Sprintf(" Ingested and indexed %d documents from %s export into %s.", res.TotalFiles, ui.Bold(string(res.DetectedFormat)), ui.Bold(project)))
			} else if len(res.Docs) > 0 {
				fmt.Fprintln(os.Stdout, ui.Green("✔")+" Ingested and indexed "+ui.Bold(res.Docs[0].Title)+" into "+ui.Bold(project)+".")
			}
		}
	}
}
