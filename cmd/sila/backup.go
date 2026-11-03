package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runBackup(ui render.UI, dbPath, dest string, jsonOut bool) {
	if _, err := os.Stat(dbPath); err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": "knowledge store not found", "store": dbPath})
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No knowledge store found at "+dbPath))
		}
		return
	}

	// VACUUM INTO writes a new file and is permitted on a read-only source
	// connection, so the backup never takes write locks on the live store.
	st, err := store.OpenRO(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila backup: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if dest == "" {
		dest = dbPath + "." + time.Now().UTC().Format("20060102T150405Z") + ".bak"
	}

	start := time.Now()
	if err := st.Backup(dest); err != nil {
		// A valid snapshot whose last_backup marker could not be recorded is
		// still a usable backup: warn rather than exit nonzero, which would
		// make a caller discard a good file.
		if errors.Is(err, store.ErrBackupMarkerUnrecorded) {
			if jsonOut {
				emitJSON(map[string]any{"warning": err.Error(), "store": dbPath, "destination": dest})
			} else {
				fmt.Fprintln(os.Stderr, ui.Yellow("sila backup: "+err.Error()))
			}
		} else {
			if jsonOut {
				emitJSON(map[string]any{"error": err.Error(), "store": dbPath, "destination": dest})
			} else {
				fmt.Fprintln(os.Stderr, ui.Red("sila backup: "+err.Error()))
			}
			os.Exit(1)
		}
	}

	var size int64
	if fi, err := os.Stat(dest); err == nil {
		size = fi.Size()
	}
	if jsonOut {
		emitJSON(map[string]any{
			"store":       dbPath,
			"destination": dest,
			"size_bytes":  size,
			"duration_ms": time.Since(start).Milliseconds(),
			"status":      "ok",
		})
		return
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · backup")))
	fmt.Fprintf(os.Stdout, "  Snapshot: %s (%s · consistency guaranteed under active writers)\n", dest, render.FormatBytes(size))
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, actionLine(ui, "sila doctor --db="+dest, "verify the snapshot, then keep it off this machine"))
}
