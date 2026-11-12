package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/memory"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func memoryUsage() {
	fmt.Fprintln(os.Stderr, "usage: sila memory add --file=<json|-> [--project=<id>]")
	fmt.Fprintln(os.Stderr, "       sila memory validate --file=<json|-> [--project=<id>]")
	fmt.Fprintln(os.Stderr, "       sila memory show <id> [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory list [--kind=<kind>] [--since=<dur>] [--limit=N] [--json|--toon]")
	fmt.Fprintln(os.Stderr, "       sila memory digest <id> [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory archive <id> --reason=<reason> --authority=<authority> [--note=<text>] [--duplicate-of=<id>] [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory archive list [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory restore <id|archive-id> [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory unmerge <merged-fact-id> [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory history <id> [--verify] [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory feedback <memory-id> --signal=<signal> [--retrieval=<id>|--unbound] [--note=<text>] [--agent=<id>] [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory feedback list [--memory=<id>] [--signal=<signal>] [--since=<dur|RFC3339>] [--json|--toon]")
	fmt.Fprintln(os.Stderr, "       sila memory report [--since=7d] [--json]")
	fmt.Fprintln(os.Stderr, "       sila memory prune --yes [--older-than=72h]")
}

// memoryLifecycleOptions carries archive/restore/history flags from the root
// parser into the memory subcommands.
type memoryLifecycleOptions struct {
	reason          string
	authority       string
	note            string
	duplicateOf     string
	verify          bool
	hardDelete      bool
	revealSensitive bool
	includeArchived bool
}

func runMemory(ui render.UI, dbPath, project, since, olderThan, file, kind, agent string, limit int, yes, jsonOut, toonOut bool, life memoryLifecycleOptions, args []string) {
	sub := "report"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}

	switch sub {
	case "add", "validate", "show", "list", "digest":
		runTypedMemory(ui, dbPath, project, since, file, kind, agent, limit, jsonOut, toonOut, sub, args, life.includeArchived)
		return
	case "archive":
		if isMemoryHardDeleteRequested(life) {
			refuseMemoryHardDelete(jsonOut)
		}
		if len(args) > 0 && strings.EqualFold(args[0], "list") {
			memoryArchiveList(ui, dbPath, project, jsonOut, toonOut, life.revealSensitive)
			return
		}
		if len(args) == 0 {
			memoryUsage()
			os.Exit(2)
		}
		memoryArchive(ui, dbPath, project, args[0], agent, life, jsonOut)
		return
	case "restore":
		if isMemoryHardDeleteRequested(life) {
			refuseMemoryHardDelete(jsonOut)
		}
		if len(args) == 0 {
			memoryUsage()
			os.Exit(2)
		}
		memoryRestore(ui, dbPath, project, args[0], agent, life, jsonOut)
		return
	case "unmerge":
		if len(args) == 0 {
			memoryUsage()
			os.Exit(2)
		}
		memoryUnmerge(ui, dbPath, project, args[0], agent, jsonOut)
		return
	case "history":
		if isMemoryHardDeleteRequested(life) {
			refuseMemoryHardDelete(jsonOut)
		}
		if len(args) == 0 {
			memoryUsage()
			os.Exit(2)
		}
		memoryHistory(ui, dbPath, project, args[0], life, jsonOut)
		return
	case "feedback":
		memoryFeedbackRun(ui, dbPath, project, since, agent, life.note, limit, jsonOut, toonOut, args)
		return
	case "help":
		memoryUsage()
		return
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	switch sub {
	case "report":
		runMemoryReport(ui, st, project, since, jsonOut, toonOut)
	case "prune":
		if !yes {
			fmt.Fprintln(os.Stderr, "sila memory prune: pass --yes to delete events")
			os.Exit(2)
		}
		age, err := parseSessionAge(olderThan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila memory prune: %v\n", err)
			os.Exit(2)
		}
		deleted, err := st.PruneMemoryEvents(time.Now().UTC().Add(-age))
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila memory prune: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"deleted": deleted, "older_than": age.String()})
			return
		}
		fmt.Fprintf(os.Stdout, "%s Pruned %d memory event(s) older than %s\n", ui.Green("✔"), deleted, age)
	default:
		fmt.Fprintf(os.Stderr, "sila memory: unknown subcommand %q (valid: add, validate, show, list, digest, archive, restore, history, feedback, report, prune)\n", sub)
		os.Exit(2)
	}
}

func runTypedMemory(ui render.UI, dbPath, project, since, file, kind, agent string, limit int, jsonOut, toonOut bool, sub string, args []string, includeArchived bool) {
	switch sub {
	case "add":
		memoryAdd(ui, dbPath, project, file, agent, jsonOut)
	case "validate":
		memoryValidate(dbPath, project, file, jsonOut)
	case "show":
		if len(args) == 0 {
			memoryUsage()
			os.Exit(2)
		}
		memoryShow(ui, dbPath, args[0], jsonOut)
	case "list":
		memoryList(ui, dbPath, project, since, kind, limit, jsonOut, toonOut, includeArchived)
	case "digest":
		if len(args) == 0 {
			memoryUsage()
			os.Exit(2)
		}
		memoryDigest(ui, dbPath, args[0], jsonOut)
	}
}

func readMemoryEnvelopeInput(file string) ([]byte, error) {
	if file == "" || file == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(file)
}

func memoryExit(err error) int {
	switch store.MemoryCode(err) {
	case store.MemoryCodeJSONInvalid, store.MemoryCodeSchemaUnknown, store.MemoryCodeSchemaVersion,
		store.MemoryCodeFieldUnknown, store.MemoryCodeLimit, store.MemoryCodeEnum, store.MemoryCodeBound,
		store.MemoryCodeEvidenceRef, store.MemoryCodeEvidencePath, store.MemoryCodeEvidenceDigest,
		store.MemoryCodeRelationTarget, store.MemoryCodeRelationCycle, store.MemoryCodeIdempotency,
		store.MemoryCodeDuplicate, store.MemoryCodeProjectUnresolved,
		store.MemoryCodeStateConflict, store.MemoryCodeStateInvalid, store.MemoryCodeArchiveAuthority,
		store.MemoryCodeRestoreWindow, store.MemoryCodeSensitiveRestore, store.MemoryCodeSupersededRestore,
		store.MemoryCodeDuplicateTarget, store.MemoryCodeHardDeleteRefused, store.MemoryCodeArchiveNote,
		store.MemoryCodeArchiveProject, store.MemoryCodeFeedbackSignal, store.MemoryCodeFeedbackNotReturned,
		store.MemoryCodeFeedbackExpired, store.MemoryCodeFeedbackSecret, store.MemoryCodeFeedbackNoteLimit,
		store.MemoryCodeFeedbackBound, store.MemoryCodeFeedbackSuperseded, store.MemoryCodeFeedbackProject:
		return 2
	}
	return 1
}

func memoryDiagnostics(err error) []store.MemoryDiagnostic {
	if err == nil {
		return nil
	}
	var ve *store.MemoryValidationError
	if ok := asMemoryValidationError(err, &ve); ok {
		return ve.Diagnostics
	}
	code := store.MemoryCode(err)
	if code == "" {
		return []store.MemoryDiagnostic{{Code: "MEMORY_IO", Message: err.Error()}}
	}
	var me *store.MemoryError
	field := ""
	if ok := asMemoryError(err, &me); ok {
		field = me.Field
	}
	return []store.MemoryDiagnostic{{Code: code, Field: field, Message: err.Error()}}
}

func asMemoryValidationError(err error, target **store.MemoryValidationError) bool {
	for err != nil {
		if ve, ok := err.(*store.MemoryValidationError); ok {
			*target = ve
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

func asMemoryError(err error, target **store.MemoryError) bool {
	for err != nil {
		if me, ok := err.(*store.MemoryError); ok {
			*target = me
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

func memoryAdd(ui render.UI, dbPath, project, file, agent string, jsonOut bool) {
	data, err := readMemoryEnvelopeInput(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory add: %v\n", err)
		os.Exit(1)
	}
	env, err := store.DecodeMemoryEnvelope(data)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"created": false, "diagnostics": memoryDiagnostics(err)})
		}
		fmt.Fprintf(os.Stderr, "sila memory add: %v\n", err)
		os.Exit(memoryExit(err))
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory add: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	effectiveDir := cwd
	if resolved, rErr := goals.ResolveProjectDir(st, cwd); rErr == nil && resolved != "" {
		effectiveDir = resolved
	}
	if env.ProjectID == "" {
		if strings.TrimSpace(project) != "" {
			if p, rErr := st.ResolveProject(project); rErr == nil && p.ID != "" {
				env.ProjectID = p.ID
			} else {
				env.ProjectID = project
			}
		} else {
			p, registered, rErr := st.ProjectForDir(effectiveDir)
			if rErr != nil || !registered {
				err := fmt.Errorf("%s: no registered project for %s; pass --project", store.MemoryCodeProjectUnresolved, effectiveDir)
				if jsonOut {
					emitJSON(map[string]any{"created": false, "diagnostics": memoryDiagnostics(err)})
				}
				fmt.Fprintf(os.Stderr, "sila memory add: %v\n", err)
				os.Exit(2)
			}
			env.ProjectID = p.ID
		}
	} else if strings.TrimSpace(project) != "" {
		if p, rErr := st.ResolveProject(project); rErr == nil && p.ID != "" && p.ID != env.ProjectID {
			err := fmt.Errorf("%s: envelope project_id %q conflicts with --project %q", store.MemoryCodeBound, env.ProjectID, p.ID)
			if jsonOut {
				emitJSON(map[string]any{"created": false, "diagnostics": memoryDiagnostics(err)})
			}
			fmt.Fprintf(os.Stderr, "sila memory add: %v\n", err)
			os.Exit(2)
		}
	}
	prov := store.Provenance{}
	if env.Provenance != nil {
		prov = store.Provenance{
			ToolName:     env.Provenance.ToolName,
			ClientKind:   env.Provenance.ClientKind,
			OriginEntity: env.Provenance.OriginEntity,
			SessionRef:   env.Provenance.SessionRef,
			OriginRole:   "user",
		}
		if env.Provenance.Agent != "" {
			agent = env.Provenance.Agent
		}
	}
	if agent == "" {
		agent = "user"
	}
	rec, created, err := st.SaveMemoryEnvelope(context.Background(), env.ProjectID, env, prov)
	if err != nil {
		if jsonOut {
			emitJSON(map[string]any{"created": false, "diagnostics": memoryDiagnostics(err)})
		}
		fmt.Fprintf(os.Stderr, "sila memory add: %v\n", err)
		os.Exit(memoryExit(err))
	}
	if jsonOut {
		emitJSON(map[string]any{"created": created, "memory": rec, "diagnostics": rec.Diagnostics})
		return
	}
	if created {
		fmt.Fprintf(os.Stdout, "%s memory %s created\n", ui.Green("✔"), rec.ID)
	} else {
		fmt.Fprintf(os.Stdout, "%s memory %s already exists (idempotent replay)\n", ui.Green("✔"), rec.ID)
	}
	fmt.Fprintf(os.Stdout, "  digest  %s\n", rec.Digest)
	fmt.Fprintf(os.Stdout, "  project %s\n", rec.ProjectID)
	fmt.Fprintf(os.Stdout, "  kind    %s\n", rec.Kind)
	for _, d := range rec.Diagnostics {
		fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Yellow(d.Code), d.Message)
	}
}

func memoryValidate(dbPath, project, file string, jsonOut bool) {
	data, err := readMemoryEnvelopeInput(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory validate: %v\n", err)
		os.Exit(1)
	}
	env, err := store.DecodeMemoryEnvelope(data)
	if err != nil {
		emitMemoryValidation(jsonOut, nil, memoryDiagnostics(err))
		os.Exit(memoryExit(err))
	}
	var st *store.Store
	if info, statErr := os.Stat(dbPath); statErr == nil && !info.IsDir() {
		if opened, _, openErr := store.OpenInspection(dbPath); openErr == nil && opened != nil {
			st = opened
			defer st.Close()
		}
	}
	projectID := env.ProjectID
	if projectID == "" && st != nil {
		cwd, _ := os.Getwd()
		if resolved, rErr := goals.ResolveProjectDir(st, cwd); rErr == nil && resolved != "" {
			cwd = resolved
		}
		if p, registered, rErr := st.ProjectForDir(cwd); rErr == nil && registered {
			projectID = p.ID
		}
	}
	if projectID == "" {
		projectID = project
	}
	projectDir := ""
	if st != nil {
		if p, rErr := st.ResolveScope(projectID, ""); rErr == nil && p.Path != "" {
			projectDir = p.Path
		}
	}
	if projectDir == "" {
		cwd, _ := os.Getwd()
		projectDir = cwd
	}
	diags := store.ValidateMemoryEnvelope(env, store.MemoryValidateOptions{ProjectDir: projectDir})
	emitMemoryValidation(jsonOut, env, diags)
	if len(diags) > 0 {
		os.Exit(2)
	}
}

func emitMemoryValidation(jsonOut bool, env *store.MemoryEnvelope, diags []store.MemoryDiagnostic) {
	valid := len(diags) == 0
	if jsonOut {
		payload := map[string]any{"schema": "sila/memory-validation/v1", "valid": valid, "diagnostics": diags}
		if env != nil {
			payload["memory_id"] = env.MemoryID
			payload["project_id"] = env.ProjectID
		}
		emitJSON(payload)
		return
	}
	if valid {
		id := ""
		if env != nil {
			id = env.MemoryID
		}
		fmt.Fprintf(os.Stdout, "valid memory envelope%s\n", firstNonEmptySuffix(id))
		return
	}
	fmt.Fprintf(os.Stderr, "invalid memory envelope (%d diagnostics)\n", len(diags))
	for _, d := range diags {
		fmt.Fprintf(os.Stderr, "  %s %s\n", d.Code, d.Message)
	}
}

func firstNonEmptySuffix(id string) string {
	if id == "" {
		return ""
	}
	return " " + id
}

func memoryShow(ui render.UI, dbPath, id string, jsonOut bool) {
	st, _, err := store.OpenInspection(dbPath)
	if err != nil || st == nil {
		fmt.Fprintf(os.Stderr, "sila memory show: %v\n", firstNonEmptyError(err))
		os.Exit(1)
	}
	defer st.Close()
	rec, err := st.MemoryRecordByID(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory show: %v\n", err)
		os.Exit(memoryExit(err))
	}
	if jsonOut {
		emitJSON(rec)
		return
	}
	fmt.Fprintf(os.Stdout, "%s  %s\n", ui.Cyan(ui.Bold(rec.ID)), rec.Title)
	fmt.Fprintf(os.Stdout, "  kind       %s\n", rec.Kind)
	fmt.Fprintf(os.Stdout, "  project    %s\n", rec.ProjectID)
	fmt.Fprintf(os.Stdout, "  authority  %s\n", rec.Authority)
	fmt.Fprintf(os.Stdout, "  state      %s\n", rec.State)
	fmt.Fprintf(os.Stdout, "  grounded   %t\n", rec.Grounded)
	fmt.Fprintf(os.Stdout, "  superseded %t\n", rec.Superseded)
	fmt.Fprintf(os.Stdout, "  digest     %s\n", rec.Digest)
	fmt.Fprintf(os.Stdout, "  fact       %s\n", rec.FactID)
	fmt.Fprintf(os.Stdout, "  created    %s\n", rec.CreatedAt)
	if rec.Envelope != nil {
		fmt.Fprintln(os.Stdout)
		raw, _ := json.MarshalIndent(rec.Envelope, "", "  ")
		fmt.Fprintln(os.Stdout, string(raw))
	}
}

func memoryList(ui render.UI, dbPath, project, since, kind string, limit int, jsonOut, toonOut, includeArchived bool) {
	st, _, err := store.OpenInspection(dbPath)
	if err != nil || st == nil {
		fmt.Fprintf(os.Stderr, "sila memory list: %v\n", firstNonEmptyError(err))
		os.Exit(1)
	}
	defer st.Close()
	opts := store.MemoryListOptions{Kind: kind, Limit: limit, IncludeArchived: includeArchived}
	if project != "" && project != "*" {
		opts.ProjectID = project
		if p, rErr := st.ResolveProject(project); rErr == nil && p.ID != "" {
			opts.ProjectID = p.ID
		}
	}
	if strings.TrimSpace(since) != "" {
		opts.Since = parseSince(since)
	}
	records, err := st.ListMemoryRecords(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory list: %v\n", err)
		os.Exit(1)
	}
	stats := store.ComputeMemoryStats(records)
	if jsonOut {
		emitJSON(map[string]any{"schema": "sila/memory-list/v1", "memories": records, "summary": stats})
		return
	}
	if toonOut {
		rows := make([][]string, 0, len(records))
		for _, rec := range records {
			grounded := "yes"
			if !rec.Grounded {
				grounded = "no"
			}
			state := rec.State
			if state == "" {
				state = "current"
			}
			rows = append(rows, []string{rec.ID, rec.Kind, rec.Authority, grounded, state, rec.Title})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("memories", []string{"id", "kind", "authority", "grounded", "state", "title"}, rows))
		return
	}
	if len(records) == 0 {
		fmt.Fprintln(os.Stdout, "no typed memories")
		return
	}
	fmt.Fprintf(os.Stdout, "%s  %s\n", ui.Cyan(ui.Bold("sila · memory")), ui.Dim(fmt.Sprintf("%d record(s)", stats.Total)))
	for _, rec := range records {
		marker := ui.Green("●")
		if rec.Superseded || rec.State == "archived" || rec.State == "superseded" {
			marker = ui.Dim("○")
		} else if !rec.Grounded {
			marker = ui.Yellow("●")
		}
		state := rec.State
		if state == "" {
			state = "current"
		}
		fmt.Fprintf(os.Stdout, "  %s %-24s %-20s %-12s %-10s %s\n", marker, rec.ID, rec.Kind, rec.Authority, state, rec.Title)
	}
	fmt.Fprintf(os.Stdout, "\n  grounded=%d ungrounded=%d agent_save=%d user_attested=%d missing_at_write=%d\n",
		stats.Grounded, stats.Ungrounded, stats.AgentSave, stats.UserAttested, stats.MissingAtWrite)
}

func memoryDigest(ui render.UI, dbPath, id string, jsonOut bool) {
	st, _, err := store.OpenInspection(dbPath)
	if err != nil || st == nil {
		fmt.Fprintf(os.Stderr, "sila memory digest: %v\n", firstNonEmptyError(err))
		os.Exit(1)
	}
	defer st.Close()
	rec, err := st.MemoryRecordByID(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory digest: %v\n", err)
		os.Exit(memoryExit(err))
	}
	if rec.Envelope == nil {
		fmt.Fprintf(os.Stderr, "sila memory digest: stored envelope is unreadable\n")
		os.Exit(1)
	}
	canonical, err := store.MemoryCanonicalJSON(rec.Envelope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory digest: %v\n", err)
		os.Exit(1)
	}
	digest, err := store.MemoryDigest(rec.Envelope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory digest: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(map[string]any{"memory_id": rec.ID, "digest": digest, "canonical": json.RawMessage(canonical)})
		return
	}
	fmt.Fprintf(os.Stdout, "%s %s\n", ui.Bold(rec.ID), digest)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, canonical, "", "  "); err == nil {
		fmt.Fprintln(os.Stdout, pretty.String())
	}
}

func firstNonEmptyError(err error) string {
	if err == nil {
		return "store not found"
	}
	return err.Error()
}

func runMemoryReport(ui render.UI, st *store.Store, project, sinceRaw string, jsonOut, toonOut bool) {
	since := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if strings.TrimSpace(sinceRaw) != "" {
		since = parseSince(sinceRaw)
	}
	until := time.Now().UTC()
	events, err := st.ListMemoryEvents(store.MemoryEventFilter{ProjectID: project, Since: since, Until: until, Limit: 100000})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory report: %v\n", err)
		os.Exit(1)
	}
	cwd, _ := os.Getwd()
	goals := memory.CollectGoalStats(cwd, since)
	report := memory.BuildReport(events, goals, project, since, until)
	report.Verdict = memory.Verdict(report)
	if jsonOut || toonOut {
		emitJSON(report)
		return
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · memory adoption"))+" — "+fmt.Sprintf("%s → %s", report.Window.Since, report.Window.Until))
	fmt.Fprintln(os.Stdout, "\n  Events")
	for _, verb := range []string{"prime", "recall", "memo", "lesson", "case", "facts", "gate", "handoff", "sync"} {
		counts := report.Events[verb]
		fmt.Fprintf(os.Stdout, "    %-10s ok=%d empty=%d error=%d skipped=%d\n", verb, counts["ok"], counts["empty"], counts["error"], counts["skipped"])
	}
	fmt.Fprintf(os.Stdout, "\n  Sessions: total=%d prime=%d (%.0f%%) recall=%d (%.0f%%) handoff=%d (%.0f%%)\n",
		report.Sessions.Total, report.Sessions.Prime, report.Sessions.PrimeRate*100,
		report.Sessions.Recall, report.Sessions.RecallRate*100, report.Sessions.Handoff, report.Sessions.HandoffRate*100)
	fmt.Fprintf(os.Stdout, "  Goals:    implemented=%d required=%d legacy=%d valid_memory=%d (%.0f%%)\n",
		report.Goals.Implemented, report.Goals.Required, report.Goals.Legacy,
		report.Goals.WithValidMemoryBlock, report.Goals.WriteComplianceRate*100)
	fmt.Fprintf(os.Stdout, "  Gate:     pass=%d fail=%d waiver=%d skip=%d\n",
		report.Gate.Pass, report.Gate.Fail, report.Gate.Waiver, report.Gate.Skip)
	fmt.Fprintf(os.Stdout, "  Unattributed events: %d\n", report.UnattributedEvents)
	fmt.Fprintf(os.Stdout, "  Verdict:  %s\n", strings.ToUpper(report.Verdict))
}

// memoryFeedbackOptions carries `sila memory feedback` subcommand flags that
// the root parser does not own.
type memoryFeedbackOptions struct {
	signal    string
	retrieval string
	note      string
	agent     string
	memory    string
	since     string
	unbound   bool
}

// parseMemoryFeedbackArgs scans flags in any position, because the root parser
// forwards unknown subcommand flags as positionals.
func parseMemoryFeedbackArgs(args []string) ([]string, memoryFeedbackOptions, error) {
	var opts memoryFeedbackOptions
	var positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-h" || a == "--help" {
			return nil, opts, flag.ErrHelp
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		value := ""
		hasValue := false
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			value, hasValue = name[idx+1:], true
			name = name[:idx]
		}
		take := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag --%s needs a value", name)
			}
			i++
			return args[i], nil
		}
		var err error
		switch name {
		case "signal":
			opts.signal, err = take()
		case "retrieval":
			opts.retrieval, err = take()
		case "note":
			opts.note, err = take()
		case "agent":
			opts.agent, err = take()
		case "memory":
			opts.memory, err = take()
		case "since":
			opts.since, err = take()
		case "unbound":
			opts.unbound = true
		case "json", "toon":
			// Root booleans; already applied before dispatch.
		case "limit":
			_, err = take()
		default:
			return nil, opts, fmt.Errorf("unknown flag %q", a)
		}
		if err != nil {
			return nil, opts, err
		}
	}
	return positionals, opts, nil
}

func memoryFeedbackRun(ui render.UI, dbPath, project, since, agent, note string, limit int, jsonOut, toonOut bool, args []string) {
	positionals, opts, err := parseMemoryFeedbackArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			memoryUsage()
			return
		}
		fmt.Fprintf(os.Stderr, "sila memory feedback: %v\n", err)
		memoryUsage()
		os.Exit(2)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory feedback: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if len(positionals) > 0 && strings.EqualFold(positionals[0], "list") {
		memoryFeedbackList(ui, st, project, since, limit, jsonOut, toonOut, opts)
		return
	}
	memoryFeedbackRecord(ui, st, project, agent, note, jsonOut, opts, positionals)
}

func memoryFeedbackRecord(ui render.UI, st *store.Store, project, agent, rootNote string, jsonOut bool, opts memoryFeedbackOptions, positionals []string) {
	memoryID := strings.TrimSpace(opts.memory)
	if memoryID == "" && len(positionals) > 0 {
		memoryID = strings.TrimSpace(positionals[0])
	}
	if memoryID == "" {
		fmt.Fprintln(os.Stderr, "sila memory feedback: <memory-id> is required")
		memoryUsage()
		os.Exit(2)
	}
	if strings.TrimSpace(opts.signal) == "" {
		fmt.Fprintln(os.Stderr, "sila memory feedback: --signal is required (helped, verified, less_important, irrelevant, wrong)")
		os.Exit(2)
	}
	if _, ok := store.ParseMemoryFeedbackSignal(opts.signal); !ok {
		emitMemoryFeedbackError(jsonOut, fmt.Errorf("%s: signal must be one of %s", store.MemoryCodeFeedbackSignal, strings.Join(store.MemoryFeedbackSignalValues(), ", ")))
		os.Exit(2)
	}

	projectID := ""
	if strings.TrimSpace(project) != "" {
		if p, err := st.ResolveProject(project); err == nil && p.ID != "" {
			projectID = p.ID
		} else {
			projectID = project
		}
	} else if cwd, err := os.Getwd(); err == nil {
		if p, registered, err := st.ProjectForDir(cwd); err == nil && registered {
			projectID = p.ID
		}
	}

	agentID := strings.TrimSpace(opts.agent)
	if agentID == "" {
		agentID = strings.TrimSpace(agent)
	}
	if agentID == "" {
		agentID = "agent"
	}
	note := opts.note
	if strings.TrimSpace(note) == "" {
		note = rootNote
	}

	outcome, err := st.RecordMemoryFeedback(context.Background(), store.MemoryFeedbackInput{
		ProjectID:   projectID,
		MemoryID:    memoryID,
		RetrievalID: opts.retrieval,
		Signal:      store.MemoryFeedbackSignal(strings.TrimSpace(opts.signal)),
		Note:        note,
		Agent:       agentID,
		Unbound:     opts.unbound,
	})
	if err != nil {
		emitMemoryFeedbackError(jsonOut, err)
		fmt.Fprintf(os.Stderr, "sila memory feedback: %v\n", err)
		os.Exit(memoryExit(err))
	}

	if jsonOut {
		emitJSON(map[string]any{
			"schema":             "sila/memory-feedback/v1",
			"ok":                 true,
			"created":            outcome.Created,
			"feedback":           outcome.Feedback,
			"adjustment":         outcome.Adjustment,
			"recommended_action": outcome.RecommendedAction,
		})
		return
	}

	verb := "recorded"
	if !outcome.Created {
		verb = "already recorded (no change)"
	}
	fmt.Fprintf(os.Stdout, "%s feedback %s %s for %s\n", ui.Green("✔"), outcome.Feedback.Signal, verb, outcome.Feedback.MemoryID)
	fmt.Fprintf(os.Stdout, "  weight      %+.1f\n", outcome.Feedback.Weight)
	fmt.Fprintf(os.Stdout, "  adjustment  %+.1f\n", outcome.Adjustment.Adjustment)
	scope := "bound"
	if outcome.Feedback.Unbound {
		scope = "unbound (half weight)"
	}
	fmt.Fprintf(os.Stdout, "  scope       %s\n", scope)
	if outcome.Feedback.RetrievalID != "" {
		fmt.Fprintf(os.Stdout, "  retrieval   %s\n", outcome.Feedback.RetrievalID)
	}
	if outcome.RecommendedAction == "archive" {
		fmt.Fprintf(os.Stdout, "  %s\n", ui.Yellow("advisory: this memory is contradicted; consider archiving it"))
		fmt.Fprintf(os.Stdout, "  suggested   sila memory archive %s --reason=wrong --authority=agent_verified --note=%q\n", outcome.Feedback.MemoryID, "contradicted by feedback")
	}
}

func memoryFeedbackList(ui render.UI, st *store.Store, project, rootSince string, limit int, jsonOut, toonOut bool, opts memoryFeedbackOptions) {
	projectID := ""
	if strings.TrimSpace(project) != "" {
		if p, err := st.ResolveProject(project); err == nil && p.ID != "" {
			projectID = p.ID
		} else {
			projectID = project
		}
	}
	signal := store.MemoryFeedbackSignal("")
	if strings.TrimSpace(opts.signal) != "" {
		parsed, ok := store.ParseMemoryFeedbackSignal(opts.signal)
		if !ok {
			fmt.Fprintf(os.Stderr, "sila memory feedback list: signal must be one of %s\n", strings.Join(store.MemoryFeedbackSignalValues(), ", "))
			os.Exit(2)
		}
		signal = parsed
	}
	sinceRaw := opts.since
	if strings.TrimSpace(sinceRaw) == "" {
		sinceRaw = rootSince
	}
	summary, err := st.ListMemoryFeedback(store.MemoryFeedbackListOptions{
		ProjectID: projectID,
		MemoryID:  strings.TrimSpace(opts.memory),
		Signal:    signal,
		Since:     parseSince(sinceRaw),
		Limit:     limit,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila memory feedback list: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(summary)
		return
	}
	if toonOut {
		rows := make([][]string, 0, len(summary.Feedback))
		for _, fb := range summary.Feedback {
			rows = append(rows, []string{
				fb.CreatedAt, string(fb.Signal), fb.MemoryID, fmt.Sprintf("%+.1f", fb.Weight),
				fmt.Sprintf("%t", fb.Unbound), fb.SupersededBy,
			})
		}
		fmt.Fprint(os.Stdout, render.FormatTOONTable("memory_feedback",
			[]string{"when", "signal", "memory", "weight", "unbound", "superseded_by"}, rows))
		return
	}

	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · memory feedback")))
	fmt.Fprintf(os.Stdout, "  total %d · accepted %d · superseded %d · bound %d · unbound %d\n",
		summary.Total, summary.Accepted, summary.Superseded, summary.Bound, summary.Unbound)
	if len(summary.Counts) > 0 {
		var signals []string
		for _, sig := range store.MemoryFeedbackSignalValues() {
			if count := summary.Counts[sig]; count > 0 {
				signals = append(signals, fmt.Sprintf("%s=%d", sig, count))
			}
		}
		fmt.Fprintf(os.Stdout, "  signals %s\n", strings.Join(signals, " "))
	}
	if len(summary.Feedback) == 0 {
		fmt.Fprintln(os.Stdout, "  no feedback recorded")
		return
	}
	fmt.Fprintln(os.Stdout)
	for _, fb := range summary.Feedback {
		scope := "bound"
		if fb.Unbound {
			scope = "unbound"
		}
		state := "accepted"
		if fb.SupersededBy != "" {
			state = "superseded"
		}
		fmt.Fprintf(os.Stdout, "  %s  %-14s %-34s %+6.1f  %s · %s\n",
			fb.CreatedAt, fb.Signal, fb.MemoryID, fb.Weight, scope, state)
		if fb.Note != "" {
			fmt.Fprintf(os.Stdout, "     %s\n", ui.Dim(fb.Note))
		}
	}
	if len(summary.TopAdjusted) > 0 {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "  top adjusted")
		for _, adj := range summary.TopAdjusted {
			fmt.Fprintf(os.Stdout, "    %-34s %+7.1f", adj.MemoryID, adj.Adjustment)
			if adj.RecommendedAction != "" {
				fmt.Fprintf(os.Stdout, "  · recommended %s", adj.RecommendedAction)
			}
			fmt.Fprintln(os.Stdout)
		}
	}
}

func emitMemoryFeedbackError(jsonOut bool, err error) {
	if !jsonOut {
		return
	}
	emitJSON(map[string]any{
		"schema":      "sila/memory-feedback/v1",
		"ok":          false,
		"diagnostics": memoryDiagnostics(err),
	})
}
