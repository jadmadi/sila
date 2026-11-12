package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// runGoalsMergeSlot handles acquire, heartbeat, release, and status.
func runGoalsMergeSlot(ui render.UI, dbPath, project string, jsonOut bool, args []string, tokenArg string, forceArg bool) {
	token := tokenArg
	force := forceArg
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sila goals merge-slot <acquire|heartbeat|release|status> [flags]")
		os.Exit(2)
	}
	op := args[0]
	holder := strings.TrimSpace(os.Getenv("SILA_AGENT"))
	if holder == "" {
		holder = "cli"
	}
	if token == "" {
		token = ""
	}
	_ = force
	lease := 15 * time.Minute
	wait := time.Duration(0)
	for _, arg := range args[1:] {
		switch {
		case strings.HasPrefix(arg, "--holder="):
			holder = strings.TrimSpace(strings.TrimPrefix(arg, "--holder="))
		case strings.HasPrefix(arg, "--token="):
			token = strings.TrimSpace(strings.TrimPrefix(arg, "--token="))
		case strings.HasPrefix(arg, "--slot-token="):
			token = strings.TrimSpace(strings.TrimPrefix(arg, "--slot-token="))
		case strings.HasPrefix(arg, "--lease="):
			parsed, err := time.ParseDuration(strings.TrimPrefix(arg, "--lease="))
			if err != nil || parsed <= 0 {
				fmt.Fprintln(os.Stderr, "sila goals merge-slot: --lease must be a positive duration")
				os.Exit(2)
			}
			lease = parsed
		case strings.HasPrefix(arg, "--wait="):
			parsed, err := time.ParseDuration(strings.TrimPrefix(arg, "--wait="))
			if err != nil || parsed <= 0 {
				fmt.Fprintln(os.Stderr, "sila goals merge-slot: --wait must be a positive duration")
				os.Exit(2)
			}
			wait = parsed
		case arg == "--force":
			force = true
		case arg == "--json":
			jsonOut = true
		}
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila goals merge-slot: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	ctx := context.Background()
	cwd, _ := os.Getwd()
	projectID := project
	if projectID == "" || projectID == "all" {
		projectID = store.ProjectID(cwd)
	}
	now := time.Now().UTC()

	switch op {
	case "acquire":
		deadline := time.Now().Add(wait)
		for {
			slot, acquireErr := st.AcquireMergeSlot(ctx, projectID, holder, lease, time.Now().UTC())
			if acquireErr == nil {
				printMergeSlot(ui, *slot, jsonOut, true)
				return
			}
			var busy *store.MergeSlotBusyError
			if !strings.Contains(acquireErr.Error(), "busy") || !asBusy(acquireErr, &busy) {
				fmt.Fprintf(os.Stderr, "sila goals merge-slot: %v\n", acquireErr)
				os.Exit(1)
			}
			if wait == 0 || time.Now().After(deadline) {
				if jsonOut {
					emitJSON(map[string]any{"error": "merge_slot_busy", "holder": busy.Holder, "expiry": busy.Expiry})
				} else {
					fmt.Fprintf(os.Stderr, "merge slot busy: held by %s until %s\n", busy.Holder, busy.Expiry.Format(time.RFC3339))
				}
				os.Exit(3)
			}
			if !jsonOut {
				fmt.Fprintf(os.Stdout, "waiting: held by %s until %s\n", busy.Holder, busy.Expiry.Format(time.RFC3339))
			}
			time.Sleep(200 * time.Millisecond)
		}
	case "heartbeat":
		if token == "" {
			fmt.Fprintln(os.Stderr, "sila goals merge-slot heartbeat: --token is required")
			os.Exit(2)
		}
		slot, err := st.HeartbeatMergeSlot(ctx, projectID, token, lease, now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals merge-slot: %v\n", err)
			os.Exit(1)
		}
		printMergeSlot(ui, *slot, jsonOut, false)
	case "release":
		if err := st.ReleaseMergeSlot(ctx, projectID, token, force); err != nil {
			fmt.Fprintf(os.Stderr, "sila goals merge-slot: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"released": true, "project_id": projectID})
		} else {
			fmt.Fprintln(os.Stdout, "merge slot released")
		}
	case "status":
		slot, err := st.MergeSlot(ctx, projectID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila goals merge-slot: %v\n", err)
			os.Exit(1)
		}
		if slot == nil {
			if jsonOut {
				emitJSON(map[string]any{"project_id": projectID, "free": true})
			} else {
				fmt.Fprintln(os.Stdout, "merge slot free")
			}
			return
		}
		printMergeSlot(ui, *slot, jsonOut, false)
	default:
		fmt.Fprintf(os.Stderr, "sila goals merge-slot: unknown operation %q\n", op)
		os.Exit(2)
	}
}

func asBusy(err error, target **store.MergeSlotBusyError) bool {
	for err != nil {
		if busy, ok := err.(*store.MergeSlotBusyError); ok {
			*target = busy
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

func printMergeSlot(ui render.UI, slot store.MergeSlot, jsonOut, withToken bool) {
	if jsonOut {
		if !withToken {
			slot.Token = ""
		}
		emitJSON(slot)
		return
	}
	fmt.Fprintf(os.Stdout, "holder: %s\nexpiry: %s\n", slot.Holder, slot.Expiry.Format(time.RFC3339))
	if slot.ReclaimedFrom != "" {
		fmt.Fprintf(os.Stdout, "reclaimed from: %s\n", slot.ReclaimedFrom)
	}
	if withToken {
		fmt.Fprintf(os.Stdout, "token: %s\n", slot.Token)
	}
}

// runMergeSlotIfRequested routes the merge-slot subcommand before the goals
// parser consumes its flags.
func runMergeSlotIfRequested(ui render.UI, dbPath, project string, jsonOut bool, args []string, token string, force bool) bool {
	if len(args) == 0 || args[0] != "merge-slot" {
		return false
	}
	runGoalsMergeSlot(ui, dbPath, project, jsonOut, args[1:], token, force)
	return true
}
