package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jadmadi/sila/internal/hookconfig"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

type primeHarnessStatus struct {
	Last   *hookconfig.PrimeSummary `json:"last,omitempty"`
	Counts map[string]int           `json:"counts_24h,omitempty"`
}

func primeStatusMap(dbPath string) map[string]primeHarnessStatus {
	out := map[string]primeHarnessStatus{}
	st, _, err := openReadStore(dbPath)
	if err != nil {
		return out
	}
	defer st.Close()
	events, err := st.ListPrimeEvents(store.PrimeEventFilter{Limit: 10000})
	if err != nil {
		return out
	}
	now := time.Now().UTC()
	for _, e := range events {
		harness := e.Harness
		if harness == "" {
			harness = e.Agent
		}
		if harness == "" {
			continue
		}
		cur := out[harness]
		if cur.Counts == nil {
			cur.Counts = map[string]int{}
		}
		cur.Last = &hookconfig.PrimeSummary{
			At: e.CreatedAt.Format(time.RFC3339), Status: e.Status,
			ErrorClass: e.ErrorClass, SessionID: e.SessionID, Source: e.Source,
		}
		if now.Sub(e.CreatedAt) <= 24*time.Hour {
			cur.Counts[e.Status]++
		}
		out[harness] = cur
	}
	return out
}

func runPrimeStatus(ui render.UI, dbPath, project, harness string, jsonOut, toonOut, strict bool) {
	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}
	hooks := hookconfig.StatusAll(cwd)
	prime := primeStatusMap(dbPath)
	if harness != "" {
		filtered := map[string]primeHarnessStatus{}
		if p, ok := prime[harness]; ok {
			filtered[harness] = p
		}
		prime = filtered
	}
	if jsonOut || toonOut {
		emitJSON(map[string]any{
			"project":   project,
			"harnesses": prime,
			"coverage":  hookCoverage(hooks),
		})
	} else {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · prime status")))
		ids := make([]string, 0, len(prime))
		for id := range prime {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			fmt.Fprintln(os.Stdout, "  no prime events recorded")
		}
		for _, id := range ids {
			p := prime[id]
			last := "never"
			if p.Last != nil {
				last = fmt.Sprintf("%s %s", p.Last.Status, p.Last.At)
			}
			fmt.Fprintf(os.Stdout, "  %-16s last=%s ok=%d failed=%d\n",
				id, last, p.Counts["ok"], p.Counts["failed"])
		}
	}
	if !strict {
		return
	}
	ids := make([]string, 0, len(hooks))
	for id := range hooks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := hooks[id]
		p := prime[id]
		if !st.Capabilities["session-start"] && p.Last == nil {
			continue
		}
		if !st.Installed && !st.Configured && p.Last == nil {
			continue
		}
		if p.Last == nil || p.Last.Status != "ok" {
			fmt.Fprintf(os.Stderr, "sila prime status: %s has no successful prime event\n", id)
			os.Exit(1)
		}
	}
}

func runPrimeVerify(dbPath, project, sessionID string, jsonOut bool) int {
	if project == "" {
		cwd, _ := os.Getwd()
		project = store.ProjectID(cwd)
	}
	st, _, err := openReadStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila prime --verify: %v\n", err)
		return 2
	}
	defer st.Close()
	events, err := st.ListPrimeEvents(store.PrimeEventFilter{ProjectID: project, SessionID: sessionID, Limit: 10000})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila prime --verify: %v\n", err)
		return 2
	}
	if len(events) == 0 {
		if jsonOut {
			emitJSON(map[string]any{"project": project, "session_id": sessionID, "verified": false, "status": "missing"})
		} else {
			fmt.Fprintln(os.Stdout, "prime: no matching event")
		}
		return 1
	}
	last := events[len(events)-1]
	if jsonOut {
		emitJSON(last)
	} else {
		fmt.Fprintf(os.Stdout, "prime: %s (%s) session=%s\n", last.Status, last.ErrorClass, last.SessionID)
	}
	if last.Status == "ok" {
		return 0
	}
	return 1
}
