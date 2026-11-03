package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// StorageReport is the stable `sila storage` shape, both for --json and for
// rendering. Sizes are what sila actually reads: its own store files, and each
// source tool's data on disk.
type StorageReport struct {
	Store          StorageStore  `json:"store"`
	Tools          []StorageTool `json:"tools"`
	SourceTotal    int64         `json:"source_total_bytes"`
	CombinedTotal  int64         `json:"combined_total_bytes"`
	ToolsInstalled int           `json:"tools_installed"`
	ToolsKnown     int           `json:"tools_known"`
}

// StorageStore is sila's own footprint: the database file plus its WAL/SHM
// sidecars, which are part of the store's live state (a copy without the WAL
// can miss committed transactions).
type StorageStore struct {
	Path           string `json:"path"`
	DBBytes        int64  `json:"db_bytes"`
	WALBytes       int64  `json:"wal_bytes"`
	SHMBytes       int64  `json:"shm_bytes"`
	TotalBytes     int64  `json:"total_bytes"`
	Messages       int64  `json:"messages"`
	Claims         int64  `json:"claims"`
	Docs           int64  `json:"docs"`
	Projects       int64  `json:"projects"`
	FootprintTrend []int  `json:"footprint_trend,omitempty"`
}

// StorageTool is one source tool's on-disk footprint.
type StorageTool struct {
	Name    string `json:"name"`
	Display string `json:"display"`
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
}

// runStorage implements `sila storage`: how much room sila's own knowledge
// store takes, and how much each source tool's data takes, with totals.
func runStorage(ui render.UI, dbPath string, jsonOut bool) {
	if _, err := os.Stat(dbPath); err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": "knowledge store not found", "store": dbPath})
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No knowledge store found at "+dbPath))
		}
		return
	}

	// Pure reads: the store opens read-only, and the source walks never write.
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila storage: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	report := buildStorageReport(st, loaders.AllSources())

	if jsonOut {
		emitJSON(report)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · storage")))

	s := report.Store
	fmt.Fprintln(os.Stdout, ui.Bold("Knowledge store"))
	fmt.Fprintf(os.Stdout, "  %-14s %10s   %s\n", "database", render.FormatBytes(s.DBBytes), s.Path)
	if s.WALBytes > 0 {
		fmt.Fprintf(os.Stdout, "  %-14s %10s\n", "wal", render.FormatBytes(s.WALBytes))
	}
	if s.SHMBytes > 0 {
		fmt.Fprintf(os.Stdout, "  %-14s %10s\n", "shm", render.FormatBytes(s.SHMBytes))
	}
	if ui.IsTerminal() && len(s.FootprintTrend) > 0 {
		spark := render.SparklineInts(s.FootprintTrend)
		fmt.Fprintf(os.Stdout, "  %-14s %10s   %s\n", "trend (7d)", spark, render.FormatBytes(s.TotalBytes))
	}
	fmt.Fprintf(os.Stdout, "  %s %s   %s messages · %s claims · %s docs · %s projects\n\n",
		ui.Bold("store total"), ui.Bold(render.FormatBytes(s.TotalBytes)),
		render.FormatNumber(s.Messages), render.FormatNumber(s.Claims),
		render.FormatNumber(s.Docs), render.FormatNumber(s.Projects))

	fmt.Fprintln(os.Stdout, ui.Bold("Source tools (read-only)"))
	headers := []string{"tool", "size", "path"}
	aligns := []string{"l", "r", "l"}
	rows := make([][]string, 0, len(report.Tools))
	for _, t := range report.Tools {
		rows = append(rows, []string{t.Display, render.FormatBytes(t.Bytes), shortenHome(t.Path)})
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stdout, ui.Dim("  (no source tools found on this machine)"))
	}
	fmt.Fprintln(os.Stdout, ui.Table(headers, rows, aligns))
	fmt.Fprintf(os.Stdout, "  %s %s   (%d of %d known tools installed)\n\n",
		ui.Bold("source total"), ui.Bold(render.FormatBytes(report.SourceTotal)),
		report.ToolsInstalled, report.ToolsKnown)

	fmt.Fprintf(os.Stdout, "  %s %s on disk across sila and its sources.\n",
		ui.Bold("Combined"), ui.Bold(render.FormatBytes(report.CombinedTotal)))
	fmt.Fprintln(os.Stdout, ui.Dim("  Sources are never written to; trimming sila means `sila vacuum` (its own store) or removing source data at the tool."))
}

// buildStorageReport assembles the footprint: sila's own files from a store
// handle, and each source tool's data by summing its storage roots.
func buildStorageReport(st *store.Store, sources map[string]*loaders.Source) StorageReport {
	rep := StorageReport{Tools: []StorageTool{}}

	rep.Store.Path = st.Path
	rep.Store.DBBytes = fileSize(st.Path)
	rep.Store.WALBytes = fileSize(st.Path + "-wal")
	rep.Store.SHMBytes = fileSize(st.Path + "-shm")
	rep.Store.TotalBytes = rep.Store.DBBytes + rep.Store.WALBytes + rep.Store.SHMBytes
	var msgs, claims, projects int
	msgs, claims, _, projects, _ = st.Counts()
	rep.Store.Messages = int64(msgs)
	rep.Store.Claims = int64(claims)
	rep.Store.Projects = int64(projects)
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM docs`).Scan(&rep.Store.Docs)

	if trend, _, _, err := st.DailyStorageGrowthTrend(7); err == nil {
		rep.Store.FootprintTrend = trend
	}

	for _, name := range loaders.SortedNames(sources) {
		src := sources[name]
		rep.ToolsKnown++
		if name == "git" {
			// Git is not a data store sila reads wholesale; its "storage" is
			// every repository on disk, which would be a misleading number.
			continue
		}
		path, ok := src.ResolvePath()
		if !ok {
			continue
		}
		rep.ToolsInstalled++
		var total int64
		for _, root := range storageRoots(name, path) {
			total += pathSize(root)
		}
		rep.Tools = append(rep.Tools, StorageTool{
			Name:    name,
			Display: storageDisplayName(name),
			Path:    path,
			Bytes:   total,
		})
		rep.SourceTotal += total
	}
	rep.CombinedTotal = rep.Store.TotalBytes + rep.SourceTotal
	return rep
}

// storageRoots returns everything a tool's loader actually reads. Most tools
// read exactly their registered path; Codex is the exception — its loader also
// walks the sibling sessions/ tree beside history.jsonl, which is where the
// bulk of Codex's data lives.
func storageRoots(name, path string) []string {
	roots := []string{path}
	if name == "codex" {
		sessions := filepath.Join(filepath.Dir(path), "sessions")
		if _, err := os.Stat(sessions); err == nil {
			roots = append(roots, sessions)
		}
	}
	if name == "zed" {
		zedShare := filepath.Dir(filepath.Dir(path))
		stableDB := filepath.Join(zedShare, "db", "0-stable", "db.sqlite")
		if _, err := os.Stat(stableDB); err == nil {
			roots = append(roots, stableDB)
		}
		globalDB := filepath.Join(zedShare, "db", "0-global", "db.sqlite")
		if _, err := os.Stat(globalDB); err == nil {
			roots = append(roots, globalDB)
		}
	}
	return roots
}

// pathSize sums the byte size of a file (with SQLite sidecars, which carry
// committed state) or of every file under a directory. Symlinks are not
// followed. Unreadable entries are skipped: a footprint report must not fail
// because one directory is root-owned.
func pathSize(path string) int64 {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if !fi.IsDir() {
		n := fi.Size()
		for _, sidecar := range []string{path + "-wal", path + "-shm", path + "-journal"} {
			n += fileSize(sidecar)
		}
		return n
	}
	var n int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func fileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// storageDisplayName maps a canonical tool name to the label shown in the
// table, matching `sila sources`. storage_display_sync_test.go keeps this map
// from drifting when tools are added.
var storageDisplayNames = map[string]string{
	"mimocode":     "MiMoCode",
	"opencode":     "OpenCode",
	"devin":        "Devin",
	"codex":        "Codex",
	"command-code": "Command Code",
	"codewhale":    "Codewhale",
	"agy":          "Antigravity (AGY)",
	"zcode":        "ZCode",
	"claude-code":  "Claude Code",
	"muse":         "Muse",
	"zed":          "Zed",
	"harness2":     "Harness2",
}

func storageDisplayName(name string) string {
	if d, ok := storageDisplayNames[name]; ok {
		return d
	}
	return name
}

// shortenHome renders ~ for the home directory in table paths.
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + strings.TrimPrefix(path, home)
}
