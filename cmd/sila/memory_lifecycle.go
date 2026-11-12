package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func isMemoryHardDeleteRequested(life memoryLifecycleOptions) bool { return life.hardDelete }

func refuseMemoryHardDelete(jsonOut bool) {
	err := store.RefuseMemoryHardDelete()
	if jsonOut {
		emitJSON(map[string]any{"schema": "sila/memory-lifecycle/v1", "ok": false, "diagnostics": memoryDiagnostics(err)})
	}
	fmt.Fprintf(os.Stderr, "sila memory: %v\n", err)
	os.Exit(memoryExit(err))
}

func resolveMemoryProject(st *store.Store, selector string) string {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return ""
	}
	if p, err := st.ResolveProject(selector); err == nil && p.ID != "" {
		return p.ID
	}
	return selector
}

func memoryArchive(ui render.UI, dbPath, project, memoryID, agent string, life memoryLifecycleOptions, jsonOut bool) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory archive: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	record, err := st.ArchiveMemory(context.Background(), resolveMemoryProject(st, project), memoryID,
		store.ArchiveAuthority(life.authority), store.ArchiveReason(life.reason), life.note, agent, life.duplicateOf)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"schema": "sila/memory-lifecycle/v1", "ok": false, "diagnostics": memoryDiagnostics(err)})
		}
		fmt.Fprintf(os.Stderr, "sila memory archive: %v\n", err)
		os.Exit(memoryExit(err))
	}
	payload := record.RedactSensitive()
	if jsonOut {
		emitJSON(map[string]any{"schema": "sila/memory-lifecycle/v1", "ok": true, "memory": payload})
		return
	}
	fmt.Fprintf(os.Stdout, "%s memory %s archived\n", ui.Green("✔"), record.MemoryID)
	fmt.Fprintf(os.Stdout, "  archive   %s\n", record.ArchiveID)
	fmt.Fprintf(os.Stdout, "  state     %s\n", record.State)
	fmt.Fprintf(os.Stdout, "  reason    %s\n", record.Reason)
	fmt.Fprintf(os.Stdout, "  authority %s\n", record.Authority)
	fmt.Fprintf(os.Stdout, "  actor     %s\n", record.Actor)
	if record.RestoreDeadline != "" {
		fmt.Fprintf(os.Stdout, "  deadline  %s (agent restorable: %t)\n", record.RestoreDeadline, record.AgentRestorable)
	}
	if record.Sensitive {
		fmt.Fprintln(os.Stdout, "  content   [redacted]")
	}
}

func memoryRestore(ui render.UI, dbPath, project, idOrArchive, agent string, life memoryLifecycleOptions, jsonOut bool) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory restore: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	authority := store.ArchiveAuthorityUserRequest
	if strings.TrimSpace(life.authority) != "" {
		parsed, ok := store.ParseRestoreAuthority(life.authority)
		if !ok {
			authErr := &store.MemoryError{Code: store.MemoryCodeArchiveAuthority, Field: "authority", Message: "authority must be user_request or agent_verified"}
			if jsonOut {
				emitJSON(map[string]any{"schema": "sila/memory-lifecycle/v1", "ok": false, "diagnostics": memoryDiagnostics(authErr)})
			}
			fmt.Fprintf(os.Stderr, "sila memory restore: %v\n", authErr)
			os.Exit(2)
		}
		authority = parsed
	}
	actor := agent
	if actor == "" {
		actor = "user"
	}
	result, err := st.RestoreMemory(context.Background(), resolveMemoryProject(st, project), idOrArchive, actor, authority, time.Now().UTC())
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"schema": "sila/memory-lifecycle/v1", "ok": false, "diagnostics": memoryDiagnostics(err)})
		}
		fmt.Fprintf(os.Stderr, "sila memory restore: %v\n", err)
		os.Exit(memoryExit(err))
	}
	if jsonOut {
		emitJSON(map[string]any{"schema": "sila/memory-lifecycle/v1", "ok": true, "restore": result})
		return
	}
	if !result.Restored {
		fmt.Fprintf(os.Stdout, "%s memory %s is already current (idempotent no-op)\n", ui.Green("✔"), result.MemoryID)
		return
	}
	fmt.Fprintf(os.Stdout, "%s memory %s restored to %s\n", ui.Green("✔"), result.MemoryID, result.State)
}

func memoryArchiveList(ui render.UI, dbPath, project string, jsonOut, toonOut, revealSensitive bool) {
	st, _, err := store.OpenInspection(dbPath)
	if err != nil || st == nil {
		fmt.Fprintf(os.Stderr, "sila memory archive list: %v\n", firstNonEmptyError(err))
		os.Exit(1)
	}
	defer st.Close()
	records, err := st.ListArchivedMemories(resolveMemoryProject(st, project))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory archive list: %v\n", err)
		os.Exit(1)
	}
	stats := st.ComputeArchiveStats(records, time.Now().UTC())
	reveal := revealSensitive && ui.IsTerminal()
	visible := make([]store.ArchiveRecord, 0, len(records))
	for _, record := range records {
		if !reveal {
			record = record.RedactSensitive()
		}
		visible = append(visible, record)
	}
	if jsonOut {
		emitJSON(map[string]any{"schema": "sila/memory-archive-list/v1", "records": visible, "summary": stats})
		return
	}
	if toonOut {
		rows := make([][]string, 0, len(visible))
		for _, record := range visible {
			rows = append(rows, []string{record.MemoryID, record.State, record.Reason, record.Authority, record.Actor, fmt.Sprintf("%t", record.AgentRestorable), record.Title})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("archived_memories", []string{"id", "state", "reason", "authority", "actor", "agent_restorable", "title"}, rows))
		return
	}
	if len(visible) == 0 {
		fmt.Fprintln(os.Stdout, "no archived or superseded memories")
		return
	}
	fmt.Fprintf(os.Stdout, "%s  %s\n", ui.Cyan(ui.Bold("sila · memory archive")), ui.Dim(fmt.Sprintf("%d record(s)", len(visible))))
	for _, record := range visible {
		deadline := ""
		if record.RestoreDeadline != "" {
			deadline = " · deadline " + record.RestoreDeadline
		}
		fmt.Fprintf(os.Stdout, "  %-24s %-12s %-14s %-16s %s%s\n", record.MemoryID, record.State, record.Reason, record.Authority, record.Title, deadline)
	}
	fmt.Fprintf(os.Stdout, "\n  archived=%d superseded=%d restorable=%d agent_restorable=%d expired_undo=%d sensitive=%d chain_healthy=%t\n",
		stats.Archived, stats.Superseded, stats.Restorable, stats.AgentRestorable, stats.ExpiredUndo, stats.Sensitive, stats.ChainHealthy)
}

func memoryHistory(ui render.UI, dbPath, project, memoryID string, life memoryLifecycleOptions, jsonOut bool) {
	st, _, err := store.OpenInspection(dbPath)
	if err != nil || st == nil {
		fmt.Fprintf(os.Stderr, "sila memory history: %v\n", firstNonEmptyError(err))
		os.Exit(1)
	}
	defer st.Close()
	resolvedProject := resolveMemoryProject(st, project)
	history, err := st.VerifyMemoryHistory(context.Background(), resolvedProject, memoryID)
	if life.verify && err != nil {
		if jsonOut {
			emitJSON(map[string]any{"schema": "sila/memory-history/v1", "ok": false, "history": history, "diagnostics": memoryDiagnostics(err)})
		}
		fmt.Fprintf(os.Stderr, "sila memory history: %v\n", err)
		os.Exit(memoryExit(err))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory history: %v\n", err)
		os.Exit(memoryExit(err))
	}
	reveal := life.revealSensitive && ui.IsTerminal()
	if !reveal {
		for i := range history.Events {
			if history.Events[i].Authority == string(store.ArchiveAuthoritySensitive) {
				history.Events[i].Note = ""
			}
		}
	}
	if jsonOut {
		emitJSON(map[string]any{"schema": "sila/memory-history/v1", "ok": true, "history": history})
		return
	}
	if len(history.Events) == 0 {
		fmt.Fprintf(os.Stdout, "memory %s has no lifecycle events\n", memoryID)
		return
	}
	status := "verified"
	if !history.Verified {
		status = "BROKEN at " + history.BrokenEventID
	}
	fmt.Fprintf(os.Stdout, "%s %s (%s)\n", ui.Cyan(ui.Bold(memoryID)), ui.Dim("lifecycle"), status)
	for _, event := range history.Events {
		note := ""
		if event.Note != "" {
			note = " · note: " + event.Note
		}
		fmt.Fprintf(os.Stdout, "  %s %s → %s %s/%s by %s%s\n", event.CreatedAt, event.FromState, event.ToState, event.Action, event.Authority, event.Actor, note)
	}
}

func memoryUnmerge(ui render.UI, dbPath, project, mergedID, agent string, jsonOut bool) {
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory unmerge: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	_ = project
	_ = agent
	result, err := dream.UnmergeConsolidatedFact(context.Background(), st, mergedID, time.Now().UTC())
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"schema": "sila/memory-unmerge/v1", "ok": false, "error": err.Error()})
		}
		fmt.Fprintf(os.Stderr, "sila memory unmerge: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"schema": "sila/memory-unmerge/v1", "ok": true, "unmerge": result})
		return
	}
	fmt.Fprintf(os.Stdout, "%s memory %s unmerged\n", ui.Green("✔"), result.MergedID)
	fmt.Fprintf(os.Stdout, "  archived  %s\n", result.MergedID)
	fmt.Fprintf(os.Stdout, "  restored  %s\n", result.RestoredID)
}
