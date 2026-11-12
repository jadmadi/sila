package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

const continuityReportKeep = 12

// reportMarkdown is a bool-like flag: --markdown selects the default path and
// --markdown=<path> selects an explicit one.
type reportMarkdown struct {
	Path    string
	Enabled bool
}

func (m *reportMarkdown) String() string { return m.Path }

func (m *reportMarkdown) Set(value string) error {
	m.Enabled = true
	if value == "" || value == "true" {
		m.Path = ""
		return nil
	}
	m.Path = value
	return nil
}

func (m *reportMarkdown) IsBoolFlag() bool { return true }

func runReport(ui render.UI, dbPath, project, sinceFlag string, jsonOut bool, markdown *reportMarkdown, args []string) {
	spec, err := parseHumanStandupWindow(args, sinceFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila report: %v\n", err)
		os.Exit(2)
	}
	since, label, err := parseActivitySince(spec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila report: %v\n", err)
		os.Exit(2)
	}
	projectID := project
	if strings.EqualFold(strings.TrimSpace(projectID), "all") {
		projectID = ""
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila report: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	rep, err := dream.BuildContinuityReport(context.Background(), st, projectID, since, time.Now().UTC())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila report: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(rep)
		return
	}
	if markdown != nil && markdown.Enabled {
		path, writeErr := writeContinuityMarkdown(rep, markdown.Path)
		if writeErr != nil {
			fmt.Fprintf(os.Stderr, "sila report: %v: %v\n", path, writeErr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "wrote %s\n", path)
		return
	}
	printContinuityHuman(ui, rep, label)
}

func writeContinuityMarkdown(rep dream.ContinuityReport, requested string) (string, error) {
	path := requested
	if path == "" {
		path = filepath.Join(".sila", "reports", "continuity-"+rep.Until.Format("2006-01-02")+".md")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return path, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(dream.RenderContinuityMarkdown(rep)), 0o600); err != nil {
		return path, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return path, err
	}
	pruneContinuityReports(dir)
	return path, nil
}

func pruneContinuityReports(dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "continuity-*.md"))
	if err != nil || len(matches) <= continuityReportKeep {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-continuityReportKeep] {
		_ = os.Remove(old)
	}
}

func printContinuityHuman(ui render.UI, rep dream.ContinuityReport, label string) {
	fmt.Fprintf(os.Stdout, "%s  %s\n\n", ui.Cyan(ui.Bold("sila · continuity report")), ui.Dim(label))

	fmt.Fprintln(os.Stdout, ui.Bold("Harnesses"))
	fmt.Fprintln(os.Stdout, ui.Dim("Sessions and messages ingested per tool in the window."))
	if len(rep.Harnesses) == 0 {
		fmt.Fprintln(os.Stdout, "  none")
	} else {
		rows := make([][]string, 0, len(rep.Harnesses))
		for _, h := range rep.Harnesses {
			rows = append(rows, []string{h.Harness, fmt.Sprint(h.Sessions), fmt.Sprint(h.Messages)})
		}
		fmt.Fprint(os.Stdout, ui.Table([]string{"harness", "sessions", "messages"}, rows, []string{"left", "right", "right"}))
	}
	fmt.Fprintln(os.Stdout)

	fmt.Fprintln(os.Stdout, ui.Bold("Continuity"))
	fmt.Fprintln(os.Stdout, ui.Dim("How many sessions were primed, and how many handoffs crossed tools."))
	if rep.Coverage.Eligible == 0 {
		fmt.Fprintln(os.Stdout, "  none")
	} else {
		fmt.Fprintf(os.Stdout, "  coverage: %d%% (%d of %d primed sessions)\n", rep.Coverage.Percent, rep.Coverage.Primed, rep.Coverage.Eligible)
		if len(rep.CrossHarness) == 0 {
			fmt.Fprintln(os.Stdout, "  cross-harness handoffs: none")
		} else {
			rows := make([][]string, 0, len(rep.CrossHarness))
			for _, edge := range rep.CrossHarness {
				rows = append(rows, []string{edge.Writer, edge.Reader, edge.Record})
			}
			fmt.Fprint(os.Stdout, ui.Table([]string{"writer", "reader", "record"}, rows, []string{"left", "left", "left"}))
		}
	}
	fmt.Fprintln(os.Stdout)

	fmt.Fprintln(os.Stdout, ui.Bold("Knowledge"))
	fmt.Fprintln(os.Stdout, ui.Dim("New decisions, lessons, closed goals, and handoffs written."))
	fmt.Fprintf(os.Stdout, "  memos: %d  lessons: %d  goals closed: %d  handoffs: %d\n\n",
		rep.Knowledge.Memos, rep.Knowledge.Lessons, rep.Knowledge.GoalsClosed, rep.Knowledge.Handoffs)

	fmt.Fprintln(os.Stdout, ui.Bold("Health"))
	fmt.Fprintln(os.Stdout, ui.Dim("Stale facts, disputed directives, expired memos, and broken chains."))
	fmt.Fprintf(os.Stdout, "  stale facts: %d  disputed: %d  expired memos: %d  broken chains: %d\n\n",
		rep.Health.StaleFacts, rep.Health.DisputedDirectives, rep.Health.ExpiredMemos, rep.Health.BrokenChains)

	fmt.Fprintln(os.Stdout, ui.Bold("Open handoffs"))
	fmt.Fprintln(os.Stdout, ui.Dim("At most three open handoffs, newest first."))
	if len(rep.OpenHandoffs) == 0 {
		fmt.Fprintln(os.Stdout, "  none")
	} else {
		for _, h := range rep.OpenHandoffs {
			fmt.Fprintf(os.Stdout, "  %s (%d open): %s\n", h.Agent, h.OpenItems, h.Summary)
		}
	}
	fmt.Fprintln(os.Stdout)

	fmt.Fprintln(os.Stdout, ui.Bold("Verdict"))
	fmt.Fprintln(os.Stdout, ui.Dim("One sentence generated from the numbers above."))
	fmt.Fprintf(os.Stdout, "  %s\n", rep.Verdict)
}
