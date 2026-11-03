package main

import (
	"fmt"
	"os"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/sources"
	"github.com/jadmadi/sila/internal/store"
)

// jsonSourcesTool is the stable `sila sources --json` per-tool shape.
type jsonSourcesTool struct {
	Name     string        `json:"name"`
	Display  string        `json:"display"`
	Status   string        `json:"status"`
	Sessions int64         `json:"sessions"`
	Messages int64         `json:"messages"`
	Indexed  bool          `json:"indexed"`
	Paths    []sources.Hit `json:"paths"`
	Guidance string        `json:"guidance"`
}

// runSources implements `sila sources`: inspect the host OS environment,
// discover installed AI coding tools, or manage regulatory source catalogs.
func runSources(ui render.UI, dbPath string, jsonOut, verbose bool, args []string) {
	if len(args) > 0 {
		switch args[0] {
		case "check":
			runSourcesCheck(ui, dbPath, jsonOut, verbose, args[1:])
			return
		case "list":
			runSourcesList(ui, dbPath, jsonOut, verbose, args[1:])
			return
		case "add":
			runSourcesAdd(ui, dbPath, jsonOut, verbose, args[1:])
			return
		}
	}
	var st *store.Store
	if _, err := os.Stat(dbPath); err == nil {
		s, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer s.Close()
		st = s
	}

	matrix := sources.Discover(st)

	if jsonOut {
		type jsonSources struct {
			Host  sources.Host      `json:"host"`
			Tools []jsonSourcesTool `json:"tools"`
		}
		out := jsonSources{Host: matrix.Host}
		for _, t := range matrix.SortedTools() {
			out.Tools = append(out.Tools, jsonSourcesTool{
				Name: t.Name, Display: t.Display, Status: string(t.Status),
				Sessions: t.Sessions, Messages: t.Messages, Indexed: t.Indexed,
				Paths: t.Paths, Guidance: t.Guidance,
			})
		}
		emitJSON(out)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · sources")))
	fmt.Fprintf(os.Stdout, "  %s/%s · home %s · store %s\n",
		matrix.Host.OS, matrix.Host.Arch, matrix.Host.Home, matrix.Host.Store)
	if !matrix.Host.StoreExists {
		fmt.Fprintln(os.Stdout, ui.Dim("  (no knowledge store yet — run `sila sync` to create one)"))
	}
	fmt.Fprintln(os.Stdout)

	headers := []string{"tool", "status", "sessions", "messages"}
	aligns := []string{"l", "l", "r", "r"}
	rows := make([][]string, 0, len(matrix.Tools))
	var installed, indexed int64
	for _, t := range matrix.SortedTools() {
		if t.Status != sources.StatusMissing {
			installed++
		}
		if t.Indexed {
			indexed++
		}
		statusCol := render.Dim
		switch t.Status {
		case sources.StatusIndexed:
			statusCol = render.Green
		case sources.StatusReady:
			statusCol = render.Yellow
		case sources.StatusDetected:
			statusCol = render.Cyan
		}
		sessions := ui.Dim("-")
		if t.Sessions > 0 {
			sessions = fmt.Sprint(t.Sessions)
		}
		messages := ui.Dim("-")
		if t.Messages > 0 {
			messages = fmt.Sprint(t.Messages)
		}
		rows = append(rows, []string{
			t.Display,
			ui.Paint(statusCol, string(t.Status)),
			sessions,
			messages,
		})
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, aligns))

	// Setup guidance: one actionable line per non-missing tool.
	fmt.Fprintln(os.Stdout, ui.Bold("Guidance"))
	for _, t := range matrix.SortedTools() {
		if t.Status == sources.StatusMissing {
			continue
		}
		fmt.Fprintf(os.Stdout, "  %s: %s\n", t.Display, t.Guidance)
	}

	if verbose {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Bold("Detected paths"))
		for _, t := range matrix.SortedTools() {
			if len(t.Paths) == 0 {
				fmt.Fprintln(os.Stdout, ui.Dim("  "+t.Display+": <not present>"))
				continue
			}
			for _, p := range t.Paths {
				meta := render.FormatBytes(p.Size)
				if p.ModTime != "" {
					meta += " · modified " + p.ModTime
				}
				fmt.Fprintf(os.Stdout, "  %s [%s]: %s (%s)\n", t.Display, p.Role, p.Path, ui.Dim(meta))
			}
		}
	}

	fmt.Fprintln(os.Stdout)
	switch {
	case !matrix.Host.StoreExists:
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila sync", "create the store and index every ingestable tool"))
	case indexed == 0:
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila sync", "index the ingestable tools found on this host"))
	default:
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila status", "confirm sync health at a glance"))
	}
	fmt.Fprintln(os.Stdout, ui.Dim(fmt.Sprintf("%d of %d known tools installed · %d indexed by sila.", installed, len(matrix.Tools), indexed)))
}

func runSourcesCheck(ui render.UI, dbPath string, jsonOut, verbose bool, args []string) {
	cwd, _ := os.Getwd()
	var st *store.Store
	if _, err := os.Stat(dbPath); err == nil {
		if s, err := store.OpenRO(dbPath); err == nil {
			defer s.Close()
			st = s
		}
	}

	report, err := assess.ScanProvenance(cwd, st)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila sources check: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(report)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · sources check"))+" — Regulatory Source & Citation Provenance")
	fmt.Fprintf(os.Stdout, "  Scanned: %d file(s) · Total citations: %d · Verified: %d\n",
		report.ScannedFiles, report.TotalCitations, report.VerifiedSources)

	if len(report.Violations) > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Bold("Violations / Uncataloged Citations:"))
		for _, v := range report.Violations {
			icon := ui.Yellow("▲")
			if v.Severity == "error" {
				icon = ui.Red("✖")
			}
			fmt.Fprintf(os.Stdout, "  %s %s:%d: %s\n", icon, ui.Bold(v.Path), v.Line, v.Message)
		}
	}

	if verbose && len(report.Citations) > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, ui.Bold("Extracted Citations:"))
		for _, c := range report.Citations {
			status := ui.Green("✔ verified")
			if !c.Verified {
				status = ui.Yellow("▲ uncataloged")
			}
			quotable := ui.Dim("read-only")
			if c.Quotable {
				quotable = ui.Cyan("quotable")
			}
			fmt.Fprintf(os.Stdout, "  %s %s [%s, %s] (%s:%d)\n",
				status, ui.Bold(c.URL), c.License, quotable, c.Path, c.Line)
		}
	}

	fmt.Fprintln(os.Stdout)
	if report.Valid {
		fmt.Fprintf(os.Stdout, "%s All regulatory external citations verified against closed license taxonomy.\n", ui.Green("✔"))
	} else {
		fmt.Fprintf(os.Stdout, "%s Provenance violations detected — resolve license declarations or catalog sources.\n", ui.Red("✖"))
		os.Exit(1)
	}
}

func runSourcesList(ui render.UI, dbPath string, jsonOut, verbose bool, args []string) {
	var st *store.Store
	if _, err := os.Stat(dbPath); err == nil {
		if s, err := store.OpenRO(dbPath); err == nil {
			defer s.Close()
			st = s
		}
	}

	var customSources []store.SourceCatalogEntry
	if st != nil {
		customSources, _ = st.ListSources()
	}

	if jsonOut {
		type listOut struct {
			StandardAuthorities map[string]any             `json:"standard_authorities"`
			CustomCatalog       []store.SourceCatalogEntry `json:"custom_catalog"`
		}
		emitJSON(listOut{
			StandardAuthorities: map[string]any{
				"w3.org":      "W3C (cc-by, quotable)",
				"ietf.org":    "IETF (free-to-read, read-only)",
				"owasp.org":   "OWASP (cc-by, quotable)",
				"nist.gov":    "NIST (public-domain, quotable)",
				"iso.org":     "ISO (iso, read-only)",
				"go.dev":      "Go Authors (cc-by, quotable)",
				"mozilla.org": "MDN (cc-by, quotable)",
			},
			CustomCatalog: customSources,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · sources list"))+" — Authoritative Reference & License Catalog")
	fmt.Fprintln(os.Stdout)

	headers := []string{"source / domain", "publisher", "license", "quotable"}
	aligns := []string{"l", "l", "l", "l"}
	var rows [][]string

	// 1. Built-in standard authorities
	rows = append(rows, []string{"w3.org", "W3C", "cc-by", "yes"})
	rows = append(rows, []string{"ietf.org / rfc-editor.org", "IETF / RFC Editor", "free-to-read", "no"})
	rows = append(rows, []string{"owasp.org", "OWASP Foundation", "cc-by", "yes"})
	rows = append(rows, []string{"nist.gov", "NIST", "public-domain", "yes"})
	rows = append(rows, []string{"iso.org", "ISO Standards", "iso", "no"})
	rows = append(rows, []string{"go.dev / golang.org", "Go Authors", "cc-by", "yes"})
	rows = append(rows, []string{"developer.mozilla.org", "MDN Web Docs", "cc-by", "yes"})

	// 2. Custom sources from store
	for _, cs := range customSources {
		qStr := "no"
		if cs.Quotable {
			qStr = "yes"
		}
		rows = append(rows, []string{cs.URL, cs.Publisher, string(cs.License), qStr})
	}

	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, aligns))
	fmt.Fprintf(os.Stdout, "\n  %s standard authorities · %s custom cataloged sources\n",
		ui.Bold("7"), ui.Bold(fmt.Sprint(len(customSources))))
}

func runSourcesAdd(ui render.UI, dbPath string, jsonOut, verbose bool, args []string) {
	if len(args) < 4 {
		fmt.Fprintln(os.Stderr, "sila sources add: usage: sila sources add <url> <title> <publisher> <license>")
		fmt.Fprintln(os.Stderr, "  licenses: public-domain, cc-by, free-to-read, iso, paywalled")
		os.Exit(2)
	}
	urlStr := args[0]
	title := args[1]
	publisher := args[2]
	licStr := args[3]

	lic, err := assess.ValidateLicenseDeclaration(licStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila sources add: %v\n", err)
		os.Exit(1)
	}

	s, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila sources add: open store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	entry := &store.SourceCatalogEntry{
		URL:       urlStr,
		Title:     title,
		Publisher: publisher,
		License:   lic,
	}

	if err := s.SaveSource(entry); err != nil {
		fmt.Fprintf(os.Stderr, "sila sources add: save source: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(entry)
		return
	}

	fmt.Fprintf(os.Stdout, "%s Cataloged source %s (%s)\n",
		ui.Green("✔"), ui.Bold(entry.URL), entry.License)
	fmt.Fprintf(os.Stdout, "  Publisher: %s · Title: %s · Quotable: %v\n",
		entry.Publisher, entry.Title, entry.Quotable)
}
