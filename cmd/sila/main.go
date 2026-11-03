package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/cli"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"

	"github.com/mattn/go-isatty"
)

func main() {
	if len(os.Args) < 2 {
		// Bare `sila` in an interactive terminal: offer the quick launcher
		// instead of a wall of help text. Piped/CI invocations keep usage+2.
		if isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd()) {
			runQuickLauncher()
			return
		}
		usage()
		os.Exit(2)
	}

	// Rewrite conversational operational phrases into canonical subcommands
	// using the structured CCLI rewrite and precedence pipeline.
	if os.Getenv("SILA_CCLI_CHILD") == "1" {
		defer recoverCCLIChildPanic(os.Args[1:])
	}
	os.Args = applyCCLIDispatch(os.Args)

	// alias: sila [-lp|--list-projects] [flags]  ==  sila projects [flags]
	if os.Args[1] == "-lp" || os.Args[1] == "--list-projects" {
		os.Args[1] = "projects"
	}

	// Accept leading global flags (--no-color, --version, --help) before the
	// subcommand, in addition to the canonical `<sub> ... --no-color` form.
	globalNoColor := false
	sub := ""
	rest := os.Args[1:]
loop:
	for i, a := range rest {
		switch a {
		case "--version", "-version", "-V":
			fmt.Println(version.String())
			return
		case "-v":
			fmt.Fprintln(os.Stderr, "sila: -v means verbose; use -V or --version to print the version")
			fmt.Fprintln(os.Stderr, "run 'sila --help' for usage.")
			os.Exit(2)
		case "--help", "-h":
			for _, a := range rest[i+1:] {
				if a == "--all" {
					usageAll()
					return
				}
			}
			usage()
			return
		case "help":
			if len(rest) > i+1 && !strings.HasPrefix(rest[i+1], "-") {
				sub = rest[i+1]
				if sub == "all" {
					usageAll()
					return
				}
				if sub == "phrases" || sub == "ccli" {
					runCCLIHelpPhrases(os.Stdout)
					return
				}
				if sub == "do" {
					printDoHelp(os.Stdout)
					return
				}
				if !isValidSubcommand(sub) {
					unknownHelpTopic(sub)
					os.Exit(2)
				}
				ui := render.New(os.Stdout, true)
				subcommandUsageTo(sub, ui, os.Stdout)
				return
			}
			usage()
			return
		case "--upgrade", "-upgrade", "--update", "-update":
			sub = "upgrade"
			if len(rest) > i+1 {
				rest = rest[i+1:]
			} else {
				rest = nil
			}
			break loop
		case "--check-update-bg":
			version.RunBackgroundUpdateCheck()
			return
		case "--no-color":
			globalNoColor = true
			continue
		case "--no-ccli", "--no-ccli=true", "--no-ccli=false", "--no-ccli=1", "--no-ccli=0":
			continue
		}
		if strings.HasPrefix(a, "-") {
			// A leading flag is rejected, never silently dropped: `rest` is
			// sliced from the subcommand onward, so a dropped flag never
			// reaches the subcommand's parser and the command runs as if the
			// flag were unset. For --db that means the live store instead of
			// the named one.
			rejectLeadingFlag(a, rest[i+1:])
		}
		sub = a
		if len(rest) > i+1 {
			rest = rest[i+1:]
		} else {
			rest = nil
		}
		break
	}
	if strings.EqualFold(sub, "version") {
		fmt.Println(version.String())
		return
	}
	if !isValidSubcommand(sub) {
		unknownCommandError(sub)
		os.Exit(2)
	}

	ui := render.New(os.Stdout, globalNoColor)
	for _, a := range rest {
		if a == "--help" || a == "-h" {
			subcommandUsageTo(sub, render.New(os.Stdout, true), os.Stdout)
			return
		}
	}
	defer version.MaybeCheckForUpdate(isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()), globalNoColor, sub)

	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	// Explicit -h/--help is handled before parsing. Suppressing the implicit
	// Usage call keeps a bad flag value from scrolling a full help block.
	fs.Usage = func() {}

	rf := newRootFlags(fs, sub)

	// `--json=full` is a documented spelling of `--json-envelope`.
	for i, a := range rest {
		if a == "--json=full" {
			rest[i] = "--json-envelope"
		}
	}

	// Go's flag package stops at the first positional arg, so flags placed after
	// the tool (e.g. `ingest codex --json`) would be silently ignored. Reorder
	// so flags (with their space-separated values) precede positionals.
	flagArgs, pos, hasUnknown := reorderFlagsForSub(fs, sub, rest)
	parseArgs := append([]string{}, flagArgs...)
	if hasUnknown {
		// Terminate root flag parsing so subcommand-owned flags reach the
		// subcommand parser instead of failing in the root FlagSet.
		parseArgs = append(parseArgs, "--")
	}
	parseArgs = append(parseArgs, pos...)
	if err := fs.Parse(parseArgs); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, renderFlagParseError(fs, sub, err))
		os.Exit(2)
	}

	render.SetWidthOverride(*rf.widthFlag)
	dispatchStarted := time.Now()
	rf.dispatch(sub, ui, fs, globalNoColor)
	recordCLIAdoption(*rf.dbPath, sub, rest, dispatchStarted)
}

func isValidSubcommand(sub string) bool {
	_, ok := cli.Lookup(sub)
	return ok
}

// isReadOnlyCLIInvocation reports whether a CLI subcommand and its post-flag
// arguments perform a non-mutating inspection/query that must never open or
// write to sila.db for adoption telemetry.
func isReadOnlyCLIInvocation(sub string, args []string) bool {
	cmd := strings.ToLower(strings.TrimSpace(sub))
	if cmd == "" {
		return true
	}
	for _, a := range args {
		trimmed := strings.ToLower(strings.TrimSpace(a))
		switch trimmed {
		case "--dry-run", "--dry-run=true", "--dry-run=1", "--help", "-h", "--check", "--check=true":
			return true
		}
	}

	firstPos := firstNonFlagArg(args)

	switch cmd {
	case "help", "version", "completion", "mcp", "serve",
		"salam", "status", "doctor", "assess", "search", "recall",
		"handoffs", "inspect", "guide", "route", "export", "digest", "adoption":
		return true
	case "facts", "fact":
		switch firstPos {
		case "rebaseline", "repair", "prune":
			return false
		default:
			return true
		}
	case "projects", "project":
		if firstPos == "prune" {
			return false
		}
		return true
	case "skill", "skills":
		switch firstPos {
		case "create", "new", "init", "scaffold", "install", "add", "deploy":
			return false
		default:
			return true
		}
	case "goals", "goal":
		switch firstPos {
		case "", "ready", "list", "ls", "show", "get", "validate", "check", "status", "implemented":
			return true
		default:
			return false
		}
	case "onboard":
		switch firstPos {
		case "check", "status":
			return true
		default:
			return false
		}
	}

	if spec, ok := cli.Lookup(cmd); ok && !spec.Mutating {
		return true
	}
	return false
}

func firstNonFlagArg(args []string) string {
	for i := 0; i < len(args); i++ {
		a := strings.TrimSpace(args[i])
		if a == "" || a == "--" {
			continue
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") {
				switch a {
				case "--db", "-db", "--project", "-project", "--branch", "-branch",
					"--agent", "-agent", "--format", "-format", "--tools", "-tools",
					"--policy", "-policy", "--project-root", "--out", "--since", "--limit":
					i++
				}
			}
			continue
		}
		return strings.ToLower(a)
	}
	return ""
}

// recordCLIAdoption logs a best-effort CLI invocation into the tool_events
// adoption ledger for mutating commands when the target database already exists.
func recordCLIAdoption(dbPath, command string, args []string, start time.Time) {
	if strings.ToLower(strings.TrimSpace(os.Getenv("SILA_TOOL_LEDGER"))) == "off" {
		return
	}
	if isReadOnlyCLIInvocation(command, args) {
		return
	}
	effDB := strings.TrimSpace(dbPath)
	if effDB == "" {
		effDB = store.DefaultDBPath()
	}
	if effDB == "" {
		return
	}
	if _, err := os.Stat(effDB); err != nil {
		return
	}
	st, err := store.Open(effDB)
	if err != nil {
		return
	}
	defer st.Close()
	cwd, _ := os.Getwd()
	project := os.Getenv("SILA_PROJECT_ID")
	if project == "" && cwd != "" {
		project = store.ProjectID(cwd)
	}
	agent := os.Getenv("SILA_AGENT")
	if agent == "" {
		agent = "cli"
	}
	session := os.Getenv("SILA_SESSION_ID")
	_ = st.RecordToolEvent(store.ToolEvent{
		ProjectID: project, SessionID: session, Agent: agent, Source: "cli",
		ToolName: command, Category: "cli", Outcome: "ok",
		DurationMS: time.Since(start).Milliseconds(), CreatedAt: time.Now().UTC(),
	})
}
