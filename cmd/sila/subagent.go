package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/session"
	"github.com/jadmadi/sila/internal/store"
)

func runSubagent(ui render.UI, dbPath string, jsonOut, toonOut bool, args []string) {
	action := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		action = strings.ToLower(args[0])
		args = args[1:]
	}
	cwd, _ := os.Getwd()
	projectID := store.ProjectID(cwd)
	parent, subagent, goal, lane, paths, handoff, charterPath, checkPath := "", "", "", "", "", "parent-only", "", ""
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--parent-session="):
			parent = strings.TrimPrefix(arg, "--parent-session=")
		case strings.HasPrefix(arg, "--subagent="):
			subagent = strings.TrimPrefix(arg, "--subagent=")
		case strings.HasPrefix(arg, "--goal="):
			goal = strings.TrimPrefix(arg, "--goal=")
		case strings.HasPrefix(arg, "--lane="):
			lane = strings.TrimPrefix(arg, "--lane=")
		case strings.HasPrefix(arg, "--paths="):
			paths = strings.TrimPrefix(arg, "--paths=")
		case strings.HasPrefix(arg, "--handoff="):
			handoff = strings.TrimPrefix(arg, "--handoff=")
		case strings.HasPrefix(arg, "--charter="):
			charterPath = strings.TrimPrefix(arg, "--charter=")
		case strings.HasPrefix(arg, "--path="):
			checkPath = strings.TrimPrefix(arg, "--path=")
		}
	}
	dir := filepath.Join(cwd, ".sila", "state", "charters")
	fail := func(err error) {
		if typed, ok := err.(*session.CharterError); ok {
			fmt.Fprintf(os.Stderr, "sila subagent: %s: %s\n", typed.Code, typed.Message)
			os.Exit(2)
		}
		fmt.Fprintf(os.Stderr, "sila subagent: %v\n", err)
		os.Exit(1)
	}

	switch action {
	case "charter":
		var allow []string
		if paths != "" {
			for _, p := range strings.Split(paths, ",") {
				if p = strings.TrimSpace(p); p != "" {
					allow = append(allow, p)
				}
			}
		}
		charter, path, err := session.NewSubagentCharter(cwd, projectID, parent, subagent, "command-code", "", goal, lane, allow, handoff)
		if err != nil {
			fail(err)
		}
		if jsonOut {
			emitJSON(charter)
			return
		}
		fmt.Fprintf(os.Stdout, "✔ charter %s written to %s\n", charter.CharterID, path)
		fmt.Fprintf(os.Stdout, "  parent: %s · goal: %s · lane: %s · handoff: %s\n", charter.ParentSessionID, charter.GoalSlug, charter.Lane, charter.HandoffPolicy)
	case "verify":
		charter, err := session.LoadCharter(charterPath)
		if err != nil {
			fail(err)
		}
		key, err := session.LoadOrCreateCharterKey(cwd)
		if err != nil {
			fail(err)
		}
		if err := session.VerifyCharter(charter, key); err != nil {
			fail(err)
		}
		if checkPath != "" {
			if err := charter.ValidatePath(checkPath); err != nil {
				fail(err)
			}
		}
		if jsonOut {
			emitJSON(map[string]any{"valid": true, "charter": charter, "path": checkPath})
			return
		}
		fmt.Fprintf(os.Stdout, "✔ charter valid: %s\n", charter.Summary())
		if checkPath != "" {
			fmt.Fprintf(os.Stdout, "  path allowed: %s\n", checkPath)
		}
	case "list":
		files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
		sort.Strings(files)
		var charters []session.SubagentCharter
		for _, file := range files {
			charter, err := session.LoadCharter(file)
			if err != nil {
				continue
			}
			if parent != "" && charter.ParentSessionID != parent {
				continue
			}
			charters = append(charters, charter)
		}
		if jsonOut {
			emitJSON(charters)
			return
		}
		fmt.Fprint(os.Stdout, render.RenderHeader(ui, render.Header{Command: "subagent list", Scope: projectID}))
		for _, c := range charters {
			fmt.Fprintf(os.Stdout, "  %s\n", c.Summary())
		}
		if len(charters) == 0 {
			fmt.Fprintln(os.Stdout, "  no charters")
		}
	case "revoke":
		if charterPath == "" {
			fmt.Fprintln(os.Stderr, "sila subagent revoke: --charter=<id|file> is required")
			os.Exit(2)
		}
		file := charterPath
		if !strings.ContainsRune(file, filepath.Separator) {
			file = filepath.Join(dir, strings.TrimSuffix(file, ".json")+".json")
		}
		charter, err := session.LoadCharter(file)
		if err != nil {
			fail(err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fail(err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "revoked.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fail(err)
		}
		fmt.Fprintf(f, `{"charter_id":%q,"subagent_id":%q,"revoked_at":%q}`+"\n", charter.CharterID, charter.SubagentID, time.Now().UTC().Format(time.RFC3339))
		f.Close()
		if err := os.Remove(file); err != nil {
			fail(err)
		}
		if jsonOut {
			emitJSON(map[string]any{"revoked": true, "charter_id": charter.CharterID})
			return
		}
		fmt.Fprintf(os.Stdout, "✔ revoked %s\n", charter.CharterID)
	case "status":
		charter, err := session.LoadCharter(charterPath)
		if err != nil {
			fail(err)
		}
		key, err := session.LoadOrCreateCharterKey(cwd)
		if err != nil {
			fail(err)
		}
		err = session.VerifyCharter(charter, key)
		valid := err == nil
		if jsonOut {
			emitJSON(map[string]any{"valid": valid, "charter": charter, "error": errorString(err)})
			return
		}
		fmt.Fprintf(os.Stdout, "charter: %s\n  parent: %s\n  goal: %s\n  lane: %s\n  handoff: %s\n  valid: %v\n",
			charter.CharterID, charter.ParentSessionID, charter.GoalSlug, charter.Lane, charter.HandoffPolicy, valid)
		if err != nil {
			fail(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "sila subagent: unknown action %q (expected charter, verify, list, revoke, status)\n", action)
		os.Exit(2)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// charterForWrite loads a signed charter from --charter or SILA_CHARTER.
func charterForWrite(args []string) (session.SubagentCharter, bool, error) {
	charter, _, err := session.CharterFromEnvOrArgs(args)
	if err != nil {
		return session.SubagentCharter{}, false, err
	}
	if charter.SubagentID == "" {
		return session.SubagentCharter{}, false, nil
	}
	return charter, true, nil
}

func charterFail(err error) {
	if typed, ok := err.(*session.CharterError); ok {
		fmt.Fprintf(os.Stderr, "sila: %s: %s\n", typed.Code, typed.Message)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "sila: %v\n", err)
	os.Exit(2)
}
