package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func runPause(ui render.UI, dbPath, project, reason, agent string, global, jsonOut bool) {
	projectID := project
	if !global && projectID == "" {
		cwd, _ := os.Getwd()
		projectID = store.ProjectID(cwd)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila pause: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := st.PauseCapture(projectID, reason, agent, global); err != nil {
		fmt.Fprintf(os.Stderr, "sila pause: %v\n", err)
		os.Exit(1)
	}
	state, _ := st.CaptureState(projectID)
	if jsonOut {
		emitJSON(map[string]any{"paused": true, "global": state.Global, "project": projectID, "since": state.Since, "reason": state.Reason})
		return
	}
	target := projectID
	if global {
		target = "all projects"
	}
	since := state.Since.Format("2006-01-02 15:04")
	fmt.Fprintf(os.Stdout, "capture paused for %s since %s\n", target, since)
	if state.Reason != "" {
		fmt.Fprintf(os.Stdout, "reason: %s\n", state.Reason)
	}
}

func runResume(ui render.UI, dbPath, project string, global, jsonOut bool) {
	projectID := project
	if !global && projectID == "" {
		cwd, _ := os.Getwd()
		projectID = store.ProjectID(cwd)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila resume: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := st.ResumeCapture(projectID, global); err != nil {
		fmt.Fprintf(os.Stderr, "sila resume: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"paused": false, "global": global, "project": projectID})
		return
	}
	target := projectID
	if global {
		target = "all projects"
	}
	fmt.Fprintf(os.Stdout, "capture resumed for %s\n", target)
}

// hookEventWrites reports whether an event may write capture rows. Gate,
// status, install, and drain events are never blocked by a pause.
func hookEventWrites(event string) bool {
	event = strings.ToLower(strings.TrimSpace(event))
	switch {
	case strings.HasPrefix(event, "pre-commit"), strings.HasPrefix(event, "pre-push"),
		strings.Contains(event, "status"), strings.Contains(event, "install"),
		strings.Contains(event, "uninstall"), strings.Contains(event, "drain"),
		strings.Contains(event, "doctor"), strings.Contains(event, "spool"):
		return false
	}
	return true
}

func hookCapturePaused(dbPath, project string) (bool, error) {
	st, err := store.Open(dbPath)
	if err != nil {
		return false, err
	}
	defer st.Close()
	state, err := st.CaptureState(project)
	if err != nil {
		return false, err
	}
	return state.Paused || state.Forgotten, nil
}

// captureStateJSON is the machine-readable capture state for status output.
type captureStateJSON struct {
	State  string `json:"state"`
	Since  string `json:"since,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func captureStateFor(st *store.Store, projectID string) captureStateJSON {
	out := captureStateJSON{State: "ACTIVE"}
	if st == nil {
		return out
	}
	state, err := st.CaptureState(projectID)
	if err != nil {
		return out
	}
	if state.Forgotten {
		out.State = "FORGOTTEN"
		if !state.ForgottenAt.IsZero() {
			out.Since = state.ForgottenAt.Format(time.RFC3339)
		}
		return out
	}
	if state.Paused {
		out.State = "PAUSED"
		if !state.Since.IsZero() {
			out.Since = state.Since.Format(time.RFC3339)
		}
		out.Reason = state.Reason
	}
	return out
}

func captureLine(c captureStateJSON) string {
	switch c.State {
	case "PAUSED":
		line := "PAUSED"
		if c.Since != "" {
			line += " since " + c.Since
		}
		if c.Reason != "" {
			line += " (" + c.Reason + ")"
		}
		return line
	default:
		return c.State
	}
}
