package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/importmem"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func importUsage() {
	fmt.Fprintln(os.Stderr, "usage: sila import [options]")
	fmt.Fprintln(os.Stderr, "  sila import                          dry-run inventory; writes nothing")
	fmt.Fprintln(os.Stderr, "  sila import --from=claude --yes      import Claude Code memory files")
	fmt.Fprintln(os.Stderr, "  sila import --dry-run --json         machine-readable preview")
	fmt.Fprintln(os.Stderr, "options: --from=claude|codex|grok|all --project=<id> --dry-run --yes")
	fmt.Fprintln(os.Stderr, "         --json --toon --again --force-similar --max-files=N")
}

type importCLIOptions struct {
	from                string
	project             string
	dryRun              bool
	yes                 bool
	again               bool
	forceSimilar        bool
	includeInstructions bool
	maxFiles            int
	jsonOut             bool
	toonOut             bool
}

// validateImportCLIOptions converts flags into library options and rejects
// contradictory or refused combinations before any store is opened.
func validateImportCLIOptions(opts importCLIOptions) (importmem.ImportOptions, error) {
	if opts.includeInstructions {
		return importmem.ImportOptions{}, &importmem.ImportError{
			Code:    importmem.CodeInstructionsRefused,
			Message: "instruction and rule files are never imported as memory",
		}
	}
	if opts.yes && opts.dryRun {
		return importmem.ImportOptions{}, fmt.Errorf("--yes and --dry-run are mutually exclusive")
	}
	if opts.maxFiles < 0 || opts.maxFiles > importmem.DefaultMaxFiles {
		return importmem.ImportOptions{}, &importmem.ImportError{
			Code:    importmem.CodeBound,
			Message: fmt.Sprintf("--max-files must be 1-%d", importmem.DefaultMaxFiles),
		}
	}
	if _, err := importmem.ParseSources(opts.from); err != nil {
		return importmem.ImportOptions{}, err
	}
	return importmem.ImportOptions{
		From:         opts.from,
		DryRun:       !opts.yes,
		Again:        opts.again,
		ForceSimilar: opts.forceSimilar,
		MaxFiles:     opts.maxFiles,
	}, nil
}

func resolveImportProject(st *store.Store, selector string) (store.Project, error) {
	if strings.TrimSpace(selector) != "" {
		if project, err := st.ResolveProject(selector); err == nil {
			return project, nil
		}
		if project, ok, err := st.ProjectForDir(selector); err == nil && ok {
			return project, nil
		}
		return store.Project{}, &importmem.ImportError{
			Code:    importmem.CodeProject,
			Message: "--project does not match exactly one registered project",
		}
	}
	cwd, _ := os.Getwd()
	project, ok, err := st.ProjectForDir(cwd)
	if err != nil {
		return store.Project{}, &importmem.ImportError{Code: importmem.CodeProject, Message: "cannot resolve the current project"}
	}
	if !ok {
		return store.Project{}, &importmem.ImportError{
			Code:    importmem.CodeProject,
			Message: "the current directory is not a registered project; pass --project",
		}
	}
	return project, nil
}

// runImport returns a process exit code so tests never have to fork.
func runImport(ui render.UI, dbPath string, opts importCLIOptions) int {
	libraryOpts, err := validateImportCLIOptions(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila import: %v\n", err)
		return 2
	}

	var st *store.Store
	if libraryOpts.DryRun {
		st, err = store.OpenRO(dbPath)
	} else {
		st, err = store.Open(dbPath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila import: %v\n", err)
		return 1
	}
	defer st.Close()

	project, err := resolveImportProject(st, opts.project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila import: %v\n", err)
		return 2
	}
	home, _ := os.UserHomeDir()
	libraryOpts.ProjectID = project.ID
	libraryOpts.RepoRoot = project.Path
	libraryOpts.HomeDir = home

	report, runErr := importmem.Run(context.Background(), st, libraryOpts)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "sila import: %v\n", runErr)
		if importmem.CodeOf(runErr) == importmem.CodeProject {
			return 2
		}
		return 1
	}
	renderImportReport(ui, report, opts)
	return 0
}

func renderImportReport(ui render.UI, report *importmem.ImportReport, opts importCLIOptions) {
	if opts.jsonOut {
		emitJSON(report)
		return
	}
	if opts.toonOut {
		rows := [][]string{
			{"status", report.Status},
			{"project", report.ProjectID},
			{"dry_run", fmt.Sprintf("%t", report.DryRun)},
			{"files_scanned", fmt.Sprintf("%d", report.FilesScanned)},
			{"files_skipped", fmt.Sprintf("%d", len(report.FilesSkipped))},
			{"candidates", fmt.Sprintf("%d", report.Candidates)},
			{"created", fmt.Sprintf("%d", report.Created)},
			{"replayed", fmt.Sprintf("%d", report.Replayed)},
			{"planned", fmt.Sprintf("%d", report.Planned)},
			{"duplicates", fmt.Sprintf("%d", report.Duplicates)},
			{"similar", fmt.Sprintf("%d", report.Similar)},
			{"failed", fmt.Sprintf("%d", report.Failed)},
			{"bytes_read", fmt.Sprintf("%d", report.BytesRead)},
			{"duration_ms", fmt.Sprintf("%d", report.DurationMS)},
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("import", []string{"metric", "value"}, rows))
		return
	}

	mode := "import"
	if report.DryRun {
		mode = "import (dry run)"
	}
	fmt.Fprintf(os.Stdout, "%s  %s\n", ui.Cyan(ui.Bold("sila · "+mode)), ui.Dim(report.ProjectID))
	sources := make([]string, 0, len(report.Sources))
	for _, source := range report.Sources {
		sources = append(sources, fmt.Sprintf("%s %s=%d", source.Source, source.Status, source.Files))
	}
	fmt.Fprintf(os.Stdout, "  Sources:    %s\n", strings.Join(sources, " · "))
	fmt.Fprintf(os.Stdout, "  Files:      %d scanned · %d skipped\n", report.FilesScanned, len(report.FilesSkipped))
	fmt.Fprintf(os.Stdout, "  Candidates: %d · created=%d replayed=%d planned=%d duplicates=%d similar=%d failed=%d\n",
		report.Candidates, report.Created, report.Replayed, report.Planned,
		report.Duplicates, report.Similar, report.Failed)
	redactions := make([]string, 0, len(report.Redactions))
	keys := make([]string, 0, len(report.Redactions))
	for key := range report.Redactions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		redactions = append(redactions, fmt.Sprintf("%s=%d", key, report.Redactions[key]))
	}
	fmt.Fprintf(os.Stdout, "  Redactions: %s\n", strings.Join(redactions, " · "))
	fmt.Fprintf(os.Stdout, "  Bytes:      %s\n", render.FormatBytes(report.BytesRead))

	shown := 0
	for _, item := range report.Items {
		if item.MemoryID == "" {
			continue
		}
		if shown >= 20 {
			break
		}
		fmt.Fprintf(os.Stdout, "  %s %-20s %-22s %s\n", ui.Green("●"), item.MemoryID, item.Kind, item.Label)
		shown++
	}
	for _, skip := range report.FilesSkipped {
		fmt.Fprintf(os.Stdout, "  %s %-34s %s (%s)\n", ui.Yellow("▲"), skip.Code, skip.Reason, skip.Label)
	}
	if report.DryRun {
		fmt.Fprintln(os.Stdout, actionLine(ui, "sila import --yes", "write these memories to the store"))
	}
}
