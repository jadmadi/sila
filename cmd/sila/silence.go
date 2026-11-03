package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/sentry"
	"github.com/jadmadi/sila/internal/store"
)

// runSilence handles the `sila silence` subcommand routing.
func runSilence(ui render.UI, dbPath, project string, jsonOut, toonOut bool, args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila silence: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	proj, err := st.ResolveScope(project, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila silence: %v\n", err)
		os.Exit(1)
	}

	switch sub {
	case "status", "check":
		runSilenceStatus(ui, st, proj.ID, cwd, jsonOut, toonOut)
	case "list", "clusters":
		runSilenceList(ui, st, proj.ID, jsonOut, toonOut)
	case "override":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: sila silence override <signature> [--reason=...]\n")
			os.Exit(2)
		}
		sig := args[1]
		reason := "Manual developer override"
		for _, a := range args[2:] {
			if strings.HasPrefix(a, "--reason=") {
				reason = strings.TrimPrefix(a, "--reason=")
			}
		}
		runSilenceOverride(ui, st, proj.ID, sig, reason, jsonOut)
	default:
		fmt.Fprintf(os.Stderr, "sila silence: unknown subcommand %q (expected status, list, or override)\n", sub)
		os.Exit(2)
	}
}

func runSilenceStatus(ui render.UI, st *store.Store, projectID, cwd string, jsonOut, toonOut bool) {
	clusters, err := st.ListDiagnosticClusters(projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila silence: %v\n", err)
		os.Exit(1)
	}

	tracker := sentry.DefaultPageHinkleyTracker()
	var anomalies []*sentry.SilenceAnomaly

	for _, c := range clusters {
		freqs, err := st.GetClusterFrequencySeries(projectID, c.Signature)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila silence: %v\n", err)
			os.Exit(1)
		}
		aci, _ := sentry.CalculateACIFromGit(cwd, c.TargetFile, c.TargetSymbol)
		cluster := &sentry.DiagnosticCluster{
			Signature:       c.Signature,
			Category:        c.Category,
			Template:        c.Template,
			TargetFile:      c.TargetFile,
			TargetSymbol:    c.TargetSymbol,
			OccurrenceCount: c.OccurrenceCount,
			FirstSeen:       c.FirstSeen,
			LastSeen:        c.LastSeen,
		}
		anomaly := sentry.EvaluateSilence(cluster, freqs, tracker, aci)
		if isOverridden, _ := st.IsSilenceOverridden(projectID, c.Signature); isOverridden {
			anomaly.Status = "overridden"
		}
		anomalies = append(anomalies, anomaly)
	}

	if jsonOut {
		emitJSON(map[string]any{
			"project_id": projectID,
			"clusters":   len(clusters),
			"anomalies":  anomalies,
		})
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · silence sentry")))
	fmt.Fprintf(os.Stdout, "Project: %s | Tracked Clusters: %d\n\n", projectID, len(clusters))

	if len(anomalies) == 0 {
		fmt.Fprintln(os.Stdout, ui.Green("✔ Zero uncoupled silence anomalies detected."))
		return
	}

	for _, a := range anomalies {
		badge := ui.Yellow("●")
		if a.Status == "silence_anomaly" {
			badge = ui.Red("✖")
		} else if a.Status == "verified_fix" {
			badge = ui.Green("✔")
		}
		fmt.Fprintf(os.Stdout, "%s [%s] %s (%s::%s)\n", badge, a.Status, a.Signature, a.TargetFile, a.TargetSymbol)
		fmt.Fprintf(os.Stdout, "    %s\n", a.Explanation)
		fmt.Fprintf(os.Stdout, "    Template: %s\n\n", a.Template)
	}
}

func runSilenceList(ui render.UI, st *store.Store, projectID string, jsonOut, toonOut bool) {
	clusters, err := st.ListDiagnosticClusters(projectID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila silence: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(clusters)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · diagnostic clusters")))
	for _, c := range clusters {
		fmt.Fprintf(os.Stdout, "- [%s] %s (%dx) %s::%s\n  %s\n",
			c.Category, c.Signature, c.OccurrenceCount, c.TargetFile, c.TargetSymbol, c.Template)
	}
}

func runSilenceOverride(ui render.UI, st *store.Store, projectID, signature, reason string, jsonOut bool) {
	actor := os.Getenv("USER")
	if actor == "" {
		actor = "developer"
	}
	createdAt := time.Now().UTC().Format(time.RFC3339)

	proof, err := store.GenerateEd25519OverrideProof(projectID, signature, reason, actor, createdAt, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila silence: generate override proof: %v\n", err)
		os.Exit(1)
	}

	ov := store.SilenceOverride{
		Signature:      signature,
		ProjectID:      projectID,
		Reason:         reason,
		Actor:          actor,
		SignatureProof: proof,
		CreatedAt:      createdAt,
	}

	if err := st.RecordSilenceOverride(ov); err != nil {
		fmt.Fprintf(os.Stderr, "sila silence: record override: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		emitJSON(ov)
		return
	}

	fmt.Fprintln(os.Stdout, ui.Green("✔ Silence anomaly override recorded in SQLite isnad ledger."))
	fmt.Fprintf(os.Stdout, "  Signature: %s\n  Actor:     %s\n  Proof:     %s\n  Reason:    %s\n",
		signature, actor, proof, reason)
}
