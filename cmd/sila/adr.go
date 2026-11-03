package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runAdr implements `sila adr [new|list|compile|doctor|from-memo]`.
func runAdr(ui render.UI, dbPath, project, decidersFlag, refsFlag, outFile string, jsonOut bool, args []string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	adrDir := dream.ADRDir

	switch sub {
	case "new":
		titleArgs := args[1:]
		if len(titleArgs) == 0 {
			fmt.Fprintln(os.Stderr, "sila adr new: specify a decision title (e.g. sila adr new \"Adopt pure-Go SQLite\")")
			os.Exit(2)
		}
		title := strings.Join(titleArgs, " ")
		var refs []string
		for r := range strings.SplitSeq(refsFlag, ",") {
			if r = strings.TrimSpace(r); r != "" {
				refs = append(refs, r)
			}
		}
		path, body, err := dream.CreateADR(adrDir, title, decidersFlag, refs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila adr new: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"path": path, "title": title, "body": body})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Created ADR draft: "+ui.Bold(path))
		fmt.Fprintf(os.Stdout, "  %s Fill in Context, Decision, Consequences, then run `sila adr doctor`.\n", ui.Dim("→"))

	case "list":
		adrs, err := dream.LoadADRs(adrDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(os.Stdout, ui.Dim("No ADRs found. Run `sila adr new <title>` to create one."))
				return
			}
			fmt.Fprintf(os.Stderr, "sila adr list: %v\n", err)
			os.Exit(1)
		}
		if len(adrs) == 0 {
			fmt.Fprintln(os.Stdout, ui.Dim("No ADRs found. Run `sila adr new <title>` to create one."))
			return
		}
		if jsonOut {
			emitJSON(adrs)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold(fmt.Sprintf("sila · adr · %d decisions", len(adrs)))))
		headers := []string{"#", "status", "date", "title", "refs"}
		rows := make([][]string, 0, len(adrs))
		for _, a := range adrs {
			rows = append(rows, []string{
				fmt.Sprintf("%04d", a.Number),
				statusBadge(ui, a.Status),
				a.Date,
				a.Title,
				fmt.Sprintf("%d", len(a.References)),
			})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"r", "l", "l", "l", "r"}))

	case "compile":
		content, err := dream.CompileIndex(adrDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "sila adr compile: no docs/adr directory found. Run `sila adr new <title>` first.")
				os.Exit(1)
			}
			fmt.Fprintf(os.Stderr, "sila adr compile: %v\n", err)
			os.Exit(1)
		}
		dest := outFile
		if dest == "" {
			dest = filepath.Join(adrDir, "README.md")
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "sila adr compile: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "sila adr compile: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]string{"path": dest, "content": content})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Compiled ADR index into "+ui.Bold(dest))

	case "doctor":
		report, err := dream.Doctor(adrDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(os.Stdout, ui.Dim("No docs/adr directory found. Run `sila adr new <title>` first."))
				return
			}
			fmt.Fprintf(os.Stderr, "sila adr doctor: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(report)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · adr doctor")))
		fmt.Fprintf(os.Stdout, "  %d ADRs · %d healthy · %s drifted · %d/%d refs grounded\n",
			report.Total, report.Healthy,
			redIf(ui, report.Drifted), report.TotalRefs-report.MissingRefs, report.TotalRefs)
		fmt.Fprintln(os.Stdout)
		if report.Total == 0 {
			fmt.Fprintln(os.Stdout, ui.Dim("No ADRs to check. Run `sila adr new <title>` to create one."))
			return
		}
		headers := []string{"#", "status", "title", "refs", "missing", "verdict"}
		rows := make([][]string, 0, len(report.Health))
		for _, h := range report.Health {
			verdict := ui.Green("✔ grounded")
			if !h.Healthy {
				verdict = ui.Red(fmt.Sprintf("✖ %d missing", h.Missing))
			}
			rows = append(rows, []string{
				fmt.Sprintf("%04d", h.Number),
				h.Status,
				h.Title,
				fmt.Sprintf("%d", h.TotalRefs),
				redIf(ui, h.Missing),
				verdict,
			})
		}
		fmt.Fprintln(os.Stdout, ui.Table(headers, rows, []string{"r", "l", "l", "r", "r", "l"}))
		if report.MissingRefs > 0 {
			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, ui.Bold(ui.Yellow("Drifted references:")))
			for _, h := range report.Health {
				for _, ref := range h.MissingRef {
					fmt.Fprintf(os.Stdout, "  ADR-%04d: %s\n", h.Number, ui.Red(ref))
				}
			}
			fmt.Fprintln(os.Stdout)
			fmt.Fprintln(os.Stdout, ui.Dim("Update the ADR's `## References` block or restore the file on disk."))
		}

	case "from-memo":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sila adr from-memo: specify a memo fact ID (e.g. sila adr from-memo <fact-id>)")
			os.Exit(2)
		}
		memoID := args[1]
		st, err := store.OpenRO(dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		defer st.Close()
		cwd, _ := os.Getwd()
		if project == "" {
			project = store.ProjectID(cwd)
		}
		path, adr, err := dream.FromMemo(st, adrDir, project, memoID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila adr from-memo: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"path": path, "adr": adr})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Green("✔")+" Promoted memo "+ui.Bold(memoID)+" into ADR draft: "+ui.Bold(path))
		fmt.Fprintf(os.Stdout, "  %s Edit Context/Decision/Consequences, then run `sila adr doctor`.\n", ui.Dim("→"))

	default:
		fmt.Fprintf(os.Stderr, "sila adr: unknown action %q\n", sub)
		fmt.Fprintln(os.Stdout, "help: valid actions for adr: new, list, compile, doctor, from-memo")
		os.Exit(2)
	}
}

// statusBadge renders a colored status label for an ADR.
func statusBadge(ui render.UI, s dream.ADRStatus) string {
	switch s {
	case dream.StatusAccepted:
		return ui.Green(string(s))
	case dream.StatusProposed:
		return ui.Yellow(string(s))
	case dream.StatusSuperceded:
		return ui.Dim(string(s))
	case dream.StatusDeprecated:
		return ui.Red(string(s))
	}
	return string(s)
}

// redIf renders n in red when positive, otherwise dim "0".
func redIf(ui render.UI, n int) string {
	if n > 0 {
		return ui.Red(fmt.Sprintf("%d", n))
	}
	return ui.Dim("0")
}
