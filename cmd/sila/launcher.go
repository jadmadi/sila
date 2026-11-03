package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// launcherOptions are the quick-launch choices offered on bare `sila` in an
// interactive terminal. Each maps to the argv dispatched on pick.
var launcherOptions = []struct {
	Key   string
	Label string
	Desc  string
	Args  []string // nil Args = prompt for search terms
}{
	{"1", "Launch Web Cockpit", "Interactive local decision cockpit (sila ui)", []string{"ui"}},
	{"2", "View Roadmap Matrix", "Goals audit: implemented, claimed, blocked (sila goals)", []string{"goals"}},
	{"3", "Search Transcripts", "Ranked search over all indexed agent sessions", nil},
	{"4", "Run System Doctor", "SQLite integrity, index sync, and health audit (sila doctor)", []string{"doctor"}},
}

// renderLauncher formats the bare-invocation quick-launch menu.
func renderLauncher(ui render.UI) string {
	var b strings.Builder
	fmt.Fprintln(&b, ui.Cyan(ui.Bold("sila"))+" (صِلَة) — quick launcher")
	fmt.Fprintln(&b)
	for _, opt := range launcherOptions {
		fmt.Fprintf(&b, "  %s  %s — %s\n", ui.Bold(ui.Cyan(opt.Key)), ui.Bold(opt.Label), ui.Dim(opt.Desc))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, ui.Dim("  Press a number to launch, or q to quit. Full CLI: sila --help"))
	return b.String()
}

// launcherArgs maps a launcher keystroke to the subcommand argv to dispatch.
// query is the prompted search terms for option 3. ok is false for quit or
// unrecognized keys.
func launcherArgs(key byte, query string) ([]string, bool) {
	switch key {
	case '1':
		return []string{"ui"}, true
	case '2':
		return []string{"goals"}, true
	case '3':
		q := strings.TrimSpace(query)
		if q == "" {
			return nil, false
		}
		return append([]string{"search"}, strings.Fields(q)...), true
	case '4':
		return []string{"doctor"}, true
	default:
		return nil, false
	}
}

// isQuitKey reports whether the pressed key quits the launcher.
func isQuitKey(key byte) bool {
	return key == 'q' || key == 'Q' || key == 3 || key == 27
}

// runQuickLauncher shows the bare-invocation quick launcher on an interactive
// TTY and dispatches the chosen subcommand by re-entering main with rewritten
// argv. Non-TTY stdin never reaches here (guarded at dispatch).
func runQuickLauncher() {
	ui := render.New(os.Stdout, os.Getenv("NO_COLOR") != "")
	fmt.Fprint(os.Stdout, renderLauncher(ui))

	restore := setupRawStdin()
	buf := make([]byte, 1)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		restore()
		return
	}
	if isQuitKey(buf[0]) {
		restore()
		return
	}

	// Search needs a query prompt: leave raw mode first so the user can type.
	var query string
	if buf[0] == '3' {
		restore()
		fmt.Fprint(os.Stdout, "Search transcripts for: ")
		query = readLine()
	} else {
		restore()
	}

	args, ok := launcherArgs(buf[0], query)
	if !ok {
		return
	}
	os.Args = append([]string{os.Args[0]}, args...)
	main()
}

// readLine reads one line from stdin.
func readLine() string {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 || buf[0] == '\n' {
			break
		}
		if buf[0] != '\r' {
			line = append(line, buf[0])
		}
	}
	return string(line)
}
