package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/cloud"
	"github.com/jadmadi/sila/internal/heal"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runCloud handles `sila cloud [pull|status|push]`.
func runCloud(ui render.UI, dbPath, project string, limit int, jsonOut, toonOut bool, args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	switch sub {
	case "status":
		runCloudStatus(ui, dbPath, project, jsonOut, toonOut)
	case "pull", "drain":
		runCloudPull(ui, dbPath, project, limit, jsonOut, toonOut)
	case "push", "publish":
		runCloudPush(ui, dbPath, project, jsonOut, toonOut)
	case "help", "--help", "-h":
		printCloudHelp(ui)
	default:
		fmt.Fprintf(os.Stderr, "unknown cloud subcommand: %s\n\n", sub)
		printCloudHelp(ui)
		os.Exit(2)
	}
}

func printCloudHelp(ui render.UI) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila cloud"))+" — D1 edge bridge and cloud synchronization")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Usage:"))
	fmt.Fprintln(os.Stdout, "  sila cloud status [--project=<slug>] [--json] [--toon]")
	fmt.Fprintln(os.Stdout, "  sila cloud pull [--limit=N] [--project=<slug>] [--json] [--toon]")
	fmt.Fprintln(os.Stdout, "  sila cloud push [--project=<slug>] [--json] [--toon]")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Commands:"))
	fmt.Fprintln(os.Stdout, "  status   Probe edge bridge health, pending items, and last sync time")
	fmt.Fprintln(os.Stdout, "  pull     Drain, digest, and acknowledge pending inbound conversations")
	fmt.Fprintln(os.Stdout, "  push     Publish redacted verified facts, memos, and goals to edge read replica")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Options:"))
	fmt.Fprintln(os.Stdout, "  --limit=N      Maximum items to pull in one batch (default: 50)")
	fmt.Fprintln(os.Stdout, "  --project=id   Filter to a specific project")
	fmt.Fprintln(os.Stdout, "  --json         Emit machine-readable JSON output")
	fmt.Fprintln(os.Stdout, "  --toon         Emit Token-Oriented Object Notation (TOON)")
}

func runCloudStatus(ui render.UI, dbPath, project string, jsonOut, toonOut bool) {
	// 1. Consent Gate: zero network calls if disabled or unconsented
	client := cloud.ActiveCloudFor("status", project)
	if client == nil {
		client = cloud.ActiveCloudFor("pull", project)
	}

	var lastSync string
	if st, err := store.OpenRO(dbPath); err == nil {
		if val, err := st.GetState("cloud:last_pull"); err == nil {
			lastSync = val
		}
		st.Close()
	}

	if client == nil {
		if jsonOut {
			emitJSON(map[string]any{
				"enabled":   false,
				"status":    "disabled",
				"last_sync": lastSync,
				"message":   "Sila Cloud is disabled or not configured. Set cloud_enabled, cloud_endpoint, and cloud_token.",
			})
			return
		}
		if toonOut {
			fmt.Fprintf(os.Stdout, "cloud_status{enabled,status,endpoint,last_sync}:\n  false,disabled,,%s\n", lastSync)
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud status")))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Status:", ui.Dim("disabled (zero network calls)"))
		if lastSync != "" {
			fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Last Sync:", ui.Dim(lastSync))
		}
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Enable:", ui.Yellow("Set cloud_enabled=true, cloud_endpoint, and cloud_token in ~/.local/share/sila/config.json"))
		return
	}

	// 2. Query edge status
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := client.Status(ctx)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"enabled":   true,
				"status":    "offline",
				"endpoint":  client.Endpoint(),
				"last_sync": lastSync,
				"warning":   err.Error(),
			})
			return
		}
		if toonOut {
			fmt.Fprintf(os.Stdout, "cloud_status{enabled,status,endpoint,last_sync,warning}:\n  true,offline,%s,%s,%q\n", client.Endpoint(), lastSync, err.Error())
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud status")))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Status:", ui.Yellow("offline (network warning)"))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Endpoint:", client.Endpoint())
		if lastSync != "" {
			fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Last Sync:", lastSync)
		}
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Warning:", ui.Dim(err.Error()))
		return
	}

	if jsonOut {
		emitJSON(map[string]any{
			"enabled":       true,
			"status":        status.Status,
			"service":       status.Service,
			"pending_count": status.PendingCount,
			"endpoint":      status.Endpoint,
			"last_sync":     lastSync,
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "cloud_status{enabled,status,service,pending,endpoint,last_sync}:\n  true,%s,%s,%d,%s,%s\n",
			status.Status, status.Service, status.PendingCount, status.Endpoint, lastSync)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud status")))
	fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Status:", ui.Green(status.Status))
	fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Service:", status.Service)
	fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Pending Items:", ui.Bold(fmt.Sprintf("%d", status.PendingCount)))
	fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Endpoint:", client.Endpoint())
	if lastSync != "" {
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Last Sync:", ui.Dim(lastSync))
	} else {
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Last Sync:", ui.Dim("never"))
	}
}

func runCloudPull(ui render.UI, dbPath, project string, limit int, jsonOut, toonOut bool) {
	if limit <= 0 {
		limit = 50
	}

	// 1. Consent Gate: zero network calls if unconsented
	client := cloud.ActiveCloudFor("pull", project)
	if client == nil {
		if jsonOut {
			emitJSON(map[string]any{
				"enabled": false,
				"status":  "disabled",
				"error":   "Sila Cloud pull is disabled or not consented for this project. Check cloud_enabled, cloud_scopes, and cloud_projects.",
			})
			return
		}
		if toonOut {
			fmt.Fprintln(os.Stdout, "cloud_pull{enabled,status,error}:\n  false,disabled,\"Cloud pull not consented\"")
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud pull")))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Status:", ui.Dim("disabled (zero network calls)"))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Hint:", ui.Yellow("Ensure cloud_enabled=true, cloud_scopes includes 'pull', and cloud_projects allows this project."))
		return
	}

	// 2. Open local SQLite store
	st, err := store.Open(dbPath)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": fmt.Sprintf("failed to open knowledge store: %v", err)})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red(fmt.Sprintf("Failed to open store: %v", err)))
		}
		os.Exit(1)
	}
	defer st.Close()

	// 3. Drain and digest
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	digester := cloud.NewDigester(st)
	summary, err := digester.DrainAndDigest(ctx, client, project, limit)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"enabled":  true,
				"status":   "offline",
				"endpoint": client.Endpoint(),
				"warning":  err.Error(),
			})
			return
		}
		if toonOut {
			fmt.Fprintf(os.Stdout, "cloud_pull{status,warning}:\n  offline,%q\n", err.Error())
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud pull")))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Status:", ui.Yellow("offline / network warning"))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Warning:", ui.Dim(err.Error()))
		return
	}

	// Record last pull timestamp
	now := time.Now().UTC().Format(time.RFC3339)
	_ = st.SetState("cloud:last_pull", now)

	if jsonOut {
		emitJSON(summary)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "cloud_pull{fetched,ingested,skipped,acked,errors}:\n  %d,%d,%d,%d,%d\n",
			summary.TotalFetched, summary.IngestedMessages, summary.SkippedMessages, summary.AckedItems, len(summary.Errors))
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud pull")))
	fmt.Fprintf(os.Stdout, "  %-18s %d item(s)\n", "Fetched:", summary.TotalFetched)
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Ingested:", ui.Green(fmt.Sprintf("%d message(s)", summary.IngestedMessages)))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Skipped/Merged:", ui.Dim(fmt.Sprintf("%d message(s)", summary.SkippedMessages)))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Acknowledged:", ui.Bold(fmt.Sprintf("%d item(s)", summary.AckedItems)))
	if len(summary.Errors) > 0 {
		fmt.Fprintln(os.Stdout, ui.Yellow("\nWarnings during ingestion:"))
		for _, e := range summary.Errors {
			fmt.Fprintf(os.Stdout, "  ! %s\n", ui.Dim(e))
		}
	}
}

func runCloudPush(ui render.UI, dbPath, project string, jsonOut, toonOut bool) {
	// 1. Consent Gate: zero network calls if disabled or unconsented
	client := cloud.ActiveCloudFor("push", project)
	if client == nil {
		if jsonOut {
			emitJSON(map[string]any{
				"enabled": false,
				"status":  "disabled",
				"error":   "Sila Cloud push is disabled or not consented for this project. Check cloud_enabled, cloud_scopes, and cloud_projects.",
			})
			return
		}
		if toonOut {
			fmt.Fprintln(os.Stdout, "cloud_push{enabled,status,error}:\n  false,disabled,\"Cloud push not consented\"")
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud push")))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Status:", ui.Dim("disabled (zero network calls)"))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Hint:", ui.Yellow("Ensure cloud_enabled=true, cloud_scopes includes 'push', and cloud_projects allows this project."))
		return
	}

	// 2. Open local SQLite store
	st, err := store.OpenRO(dbPath)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": fmt.Sprintf("failed to open knowledge store: %v", err)})
		} else {
			fmt.Fprintln(os.Stderr, ui.Red(fmt.Sprintf("Failed to open store: %v", err)))
		}
		os.Exit(1)
	}
	defer st.Close()

	repoRoot := heal.RepoRootPath("")
	payload, err := cloud.BuildPublishPayload(st, repoRoot, project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build publish payload: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.PublishKnowledge(ctx, payload)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{
				"enabled":  true,
				"status":   "failed",
				"endpoint": client.Endpoint(),
				"error":    err.Error(),
			})
			return
		}
		if toonOut {
			fmt.Fprintf(os.Stdout, "cloud_push{status,error}:\n  failed,%q\n", err.Error())
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud push")))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Status:", ui.Red("failed"))
		fmt.Fprintf(os.Stdout, "  %-16s %s\n", "Error:", ui.Dim(err.Error()))
		return
	}

	// Update last push state in store
	if rwSt, err := store.Open(dbPath); err == nil {
		now := time.Now().UTC().Format(time.RFC3339)
		_ = rwSt.SetState("cloud:last_push", now)
		rwSt.Close()
	}

	if jsonOut {
		emitJSON(map[string]any{
			"enabled":   true,
			"status":    "published",
			"endpoint":  client.Endpoint(),
			"published": resp.Published,
		})
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "cloud_push{status,facts,memos,goals,endpoint}:\n  published,%d,%d,%d,%s\n",
			resp.Published.Facts, resp.Published.Memos, resp.Published.Goals, client.Endpoint())
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · cloud push")))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Status:", ui.Green("published to edge"))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Facts:", ui.Bold(fmt.Sprintf("%d item(s)", resp.Published.Facts)))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Memos:", ui.Bold(fmt.Sprintf("%d directive(s)", resp.Published.Memos)))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Goals:", ui.Bold(fmt.Sprintf("%d package(s)", resp.Published.Goals)))
	fmt.Fprintf(os.Stdout, "  %-18s %s\n", "Endpoint:", client.Endpoint())
}
