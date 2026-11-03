package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
)

// rejectLeadingFlag prints why a flag placed before the subcommand is not
// accepted and exits 2 without running the command.
func rejectLeadingFlag(flag string, after []string) {
	fmt.Fprintln(os.Stderr, leadingFlagRejection(flag, after))
	os.Exit(2)
}

// leadingFlagRejection names the leading flag, states the order contract, and
// shows the corrected command. The example uses the first non-flag token only
// when every token after it is also a flag — otherwise that token is the
// flag's value (as in `--db /tmp/x status`), and `sila /tmp/x --db` would be a
// wrong suggestion.
func leadingFlagRejection(flag string, after []string) string {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("sila: flag %s before the command is not accepted — flags come after the subcommand", flag))
	for i, a := range after {
		if strings.HasPrefix(a, "-") {
			continue
		}
		suggest := true
		for _, b := range after[i+1:] {
			if !strings.HasPrefix(b, "-") {
				suggest = false
				break
			}
		}
		if suggest {
			msg.WriteString(fmt.Sprintf(" (try: sila %s %s)", a, flag))
		}
		break
	}
	return msg.String()
}

// actionLine renders a prominent, copyable next-step command with a dim note.
// Used across assess/status/ingest to surface the recommended action.
func actionLine(ui render.UI, cmd, note string) string {
	return "  " + ui.Bold(ui.Cyan("▶ ")) + ui.Bold(cmd) + "   " + ui.Dim(note)
}

// firstNonEmpty returns a if non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseSince turns a --since flag into a time: RFC3339, YYYY-MM-DD, day/week duration (e.g. "90d", "2w"),
// or a Go duration relative to now (e.g. "24h"). Invalid/empty -> zero time (no filter).
func parseSince(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t
	}
	// Check for 'd' (days) suffix, e.g. "90d", "7d"
	if before, ok := strings.CutSuffix(s, "d"); ok {
		if days, err := strconv.Atoi(before); err == nil {
			return time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		}
	}
	// Check for 'w' (weeks) suffix, e.g. "2w"
	if before, ok := strings.CutSuffix(s, "w"); ok {
		if weeks, err := strconv.Atoi(before); err == nil {
			return time.Now().Add(-time.Duration(weeks) * 7 * 24 * time.Hour)
		}
	}
	// Check for 'mo' (months) suffix, e.g. "1mo"
	if before, ok := strings.CutSuffix(s, "mo"); ok {
		if mos, err := strconv.Atoi(before); err == nil {
			return time.Now().Add(-time.Duration(mos) * 30 * 24 * time.Hour)
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d)
	}
	fmt.Fprintf(os.Stderr, "sila: ignoring invalid --since %q (wanted RFC3339, YYYY-MM-DD, or duration e.g. 24h, 7d, 90d, 1mo)\n", s)
	return time.Time{}
}

// fmtAge renders a duration from a timestamp as a short relative string.
func fmtAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	}
}

func verifiedCell(ui render.UI, n int) string {
	if n > 0 {
		return ui.Green(fmt.Sprint(n))
	}
	return ui.Dim("0")
}

// jsonProject is the machine-readable project listing shape.
type jsonProject struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Messages int    `json:"messages"`
	Facts    int    `json:"facts"`
	Verified int    `json:"verified"`
}

// resolveTools maps the first positional (or "all") to a target tool list.
func resolveTools(fsArgs []string) []string {
	toolName := "all"
	if len(fsArgs) > 0 {
		toolName = fsArgs[0]
	}
	specName := loaders.ResolveTool(toolName)
	if specName == "" {
		fmt.Fprintf(os.Stderr, "sila: unknown tool %q\n", toolName)
		os.Exit(1)
	}
	if specName == "all" {
		return nil
	}
	return []string{specName}
}

func shortParent(p string) string {
	i := strings.LastIndex(p, "/")
	parent := "/"
	if i >= 0 {
		parent = p[:i]
	} else if p != "" {
		parent = p
	}
	if h := os.Getenv("HOME"); h != "" {
		switch {
		case parent == h:
			parent = "~"
		case strings.HasPrefix(parent, h+"/"):
			parent = "~" + strings.TrimPrefix(parent, h)
		}
	}
	if r := []rune(parent); len(r) > 38 {
		parent = "…" + string(r[len(r)-37:])
	}
	if parent == "" {
		parent = "/"
	}
	return parent
}

// trunc shortens s to n runes (never splitting a multi-byte char) and adds an
// ellipsis when truncated. A n<=0 disables truncation for --verbose.
func trunc(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// jsonFact is the stable machine-readable fact shape.
type jsonFact struct {
	ProjectID     string   `json:"project_id"`
	Claim         string   `json:"claim"`
	Kind          string   `json:"kind,omitempty"`
	Confidence    string   `json:"confidence"`
	Corroboration string   `json:"corroboration"`
	Verified      bool     `json:"verified"`
	FileRefsExist bool     `json:"file_refs_exist"`
	IsFact        bool     `json:"is_fact"`
	IsInvariant   bool     `json:"is_invariant"`
	Distilled     string   `json:"distilled,omitempty"`
	DistillStatus string   `json:"distill_status,omitempty"`
	IsStale       bool     `json:"is_stale,omitempty"`
	RepoFiles     []string `json:"repo_files"`
	Tools         []string `json:"tools"`
	SessionRef    string   `json:"session_ref,omitempty"`
	FirstSeen     string   `json:"first_seen"`
	RefsCheckedAt string   `json:"refs_checked_at,omitempty"`
}

func emitJSON(v any) {
	if v == nil {
		v = []any{}
	}
	if outModes.envelope {
		if err := render.EmitJSONSuccess(os.Stdout, v, nil, nil); err != nil {
			fmt.Fprintf(os.Stderr, "sila: %v\n", err)
			os.Exit(1)
		}
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(render.NormalizeSlices(v)); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
}

// emitJSONLine writes one compact JSON object per line (JSON Lines) without
// indentation. Used by `sila watch --json` so every stdout line parses
// independently; emitJSON stays pretty-printed for all other commands.
func emitJSONLine(v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, string(raw))
}

// overlapCell colors a count: yellow when tripped, dim otherwise.
func overlapCell(ui render.UI, n int) string {
	s := fmt.Sprint(n)
	if n > 0 {
		return ui.Yellow(s)
	}
	return ui.Dim(s)
}

// jsonResult is the stable, human-readable ingest output shape.
type jsonResult struct {
	Tool       string `json:"tool"`
	Messages   int    `json:"messages"`
	NewRecords int    `json:"new_records"`
	Merged     int    `json:"merged"`
	Overlaps   int    `json:"overlaps"`
	Error      string `json:"error,omitempty"`
}

// jsonAssessReport is the stable assess output shape.
type jsonAssessReport struct {
	Tool         string `json:"tool"`
	Source       string `json:"source,omitempty"`
	Exists       bool   `json:"exists"`
	Size         int64  `json:"size,omitempty"`
	ModTime      string `json:"mod_time,omitempty"`
	HasWatermark bool   `json:"has_watermark"`
	Total        int64  `json:"total"`
	New          int64  `json:"new"`
	Error        string `json:"error,omitempty"`
}

// plural returns a pluralized noun for a count.
func plural(n int64, word string) string {
	if n == 1 {
		return word
	}
	lower := strings.ToLower(word)
	switch {
	case strings.HasSuffix(lower, "s"), strings.HasSuffix(lower, "sh"),
		strings.HasSuffix(lower, "ch"), strings.HasSuffix(lower, "x"),
		strings.HasSuffix(lower, "z"):
		return word + "es"
	case strings.HasSuffix(lower, "y") && len(lower) > 1:
		prev := lower[len(lower)-2]
		if !strings.ContainsRune("aeiou", rune(prev)) {
			return word[:len(word)-1] + "ies"
		}
	}
	return word + "s"
}

// reorderFlags splits argv into root-known flag tokens (and their values)
// followed by positional tokens. Unknown flags are preserved in positionals so
// subcommand-owned flags (for example `decision create -o A`) reach the
// subcommand parser instead of failing in the root FlagSet.
func reorderFlags(fs *flag.FlagSet, args []string) (flags, positionals []string) {
	flags, positionals, _ = reorderFlagsForSub(fs, "", args)
	return flags, positionals
}

func reorderFlagsForSub(fs *flag.FlagSet, sub string, args []string) (flags, positionals []string, hasUnknown bool) {
	owned := subcommandOwnedFlags(sub)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		name := strings.TrimLeft(a, "-")
		if idx := strings.IndexByte(name, '='); idx >= 0 {
			name = name[:idx]
		}
		if owned[name] || fs == nil || fs.Lookup(name) == nil {
			hasUnknown = true
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		if !strings.Contains(a, "=") {
			isBool := false
			if f := fs.Lookup(name); f != nil {
				if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
					isBool = true
				}
			}
			if !isBool && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		}
	}
	return flags, positionals, hasUnknown
}

// subcommandOwnedFlags lists flags whose meaning belongs to a subcommand even
// when the root FlagSet happens to register the same spelling for another
// command (for example --recommend is an audit flag globally but a decision
// recommendation for `sila decision`).
func subcommandOwnedFlags(sub string) map[string]bool {
	switch sub {
	case "decision", "decisions", "adr":
		return map[string]bool{
			"o": true, "option": true, "options": true,
			"r": true, "recommend": true, "rationale": true,
			"context": true, "goal": true, "address": true,
		}
	case "memo", "memos":
		// `--claim` is a global boolean for `goals reopen`; in the memo scope it
		// filters by claim text, so the root parser must not consume it.
		return map[string]bool{"claim": true}
	default:
		return nil
	}
}

var flagInvalidValueRE = regexp.MustCompile(`invalid value "([^"]*)" for flag -([a-zA-Z0-9-]+): (.*)`)

// flagKind describes the value a flag expects without exposing Go types.
func flagKind(f *flag.Flag) string {
	if f == nil {
		return "a value"
	}
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
		return "true or false"
	}
	if _, err := strconv.Atoi(f.DefValue); err == nil {
		return "a number"
	}
	return "a value"
}

// renderFlagParseError turns the flag package's boilerplate into one cause
// line plus a help hint, and never prints the full usage block.
func renderFlagParseError(fs *flag.FlagSet, sub string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if os.Getenv("SILA_DEBUG") == "1" {
		return fmt.Sprintf("sila %s: %s", sub, msg)
	}
	if m := flagInvalidValueRE.FindStringSubmatch(msg); m != nil {
		kind := "a value"
		if fs != nil {
			kind = flagKind(fs.Lookup(m[2]))
		}
		return fmt.Sprintf("sila %s: --%s expects %s, got %q (run 'sila %s -h' for usage)", sub, m[2], kind, m[1], sub)
	}
	if strings.HasPrefix(msg, "flag provided but not defined: -") {
		name := strings.TrimPrefix(msg, "flag provided but not defined: -")
		return fmt.Sprintf("sila %s: unknown flag --%s (run 'sila %s -h' for usage)", sub, name, sub)
	}
	if strings.HasPrefix(msg, "flag needs an argument: -") {
		name := strings.TrimPrefix(msg, "flag needs an argument: -")
		return fmt.Sprintf("sila %s: --%s requires a value (run 'sila %s -h' for usage)", sub, name, sub)
	}
	return fmt.Sprintf("sila %s: %s (run 'sila %s -h' for usage)", sub, msg, sub)
}

// friendlyStoreError maps SQLite/andon internals to a plain-English cause.
func friendlyStoreError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if os.Getenv("SILA_DEBUG") == "1" {
		return msg
	}
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "sqlite_busy"), strings.Contains(low, "database is locked"), strings.Contains(low, "database table is locked"):
		return "the knowledge store is busy; another sila process is using it; retry in a moment (the store was not modified)"
	case strings.Contains(low, "err_andon_"):
		return "the knowledge store failed a consistency check; the store was not modified; run `sila doctor`"
	}
	return msg
}

func formatTokenCount(tokens int) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk tok", float64(tokens)/1000.0)
	}
	return fmt.Sprintf("%d tok", tokens)
}
