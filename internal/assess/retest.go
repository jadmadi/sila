package assess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/jadmadi/sila/internal/dream"
)

var (
	// ErrReTestCommandUnresolved indicates missing or ambiguous test runbook commands.
	ErrReTestCommandUnresolved = errors.New("RETEST_COMMAND_UNRESOLVED")
	// ErrReTestCommandUnsupported indicates shell operators, quotes, or unsupported executables.
	ErrReTestCommandUnsupported = errors.New("RETEST_COMMAND_UNSUPPORTED")
)

// ReTestCommand specifies a direct argv invocation resolved from the repository runbook.
type ReTestCommand struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Source     string   `json:"source"`
}

// ResolveReTestCommand discovers and validates the repository's test command using dream.DetectRunbook.
// It enforces direct argv syntax without shell interpretation or silent fallbacks.
func ResolveReTestCommand(rootDir string) (ReTestCommand, error) {
	summary := dream.DetectRunbook(rootDir)
	return resolveReTestCommandFromSummary(summary, rootDir)
}

func resolveReTestCommandFromSummary(summary *dream.RunbookSummary, rootDir string) (ReTestCommand, error) {
	if summary == nil || len(summary.Entries) == 0 {
		return ReTestCommand{}, fmt.Errorf("%w: no runbook entries found in %s; action: configure a single test command in AGENTS.md or manifest", ErrReTestCommandUnresolved, rootDir)
	}

	var testEntries []dream.RunbookEntry
	for _, e := range summary.Entries {
		if strings.EqualFold(e.Phase, "Test") && strings.TrimSpace(e.Command) != "" {
			testEntries = append(testEntries, e)
		}
	}

	if len(testEntries) == 0 {
		return ReTestCommand{}, fmt.Errorf("%w: no test-phase command detected in %s; action: configure a single test command in AGENTS.md or manifest", ErrReTestCommandUnresolved, rootDir)
	}

	distinct := make(map[string]dream.RunbookEntry)
	for _, e := range testEntries {
		distinct[strings.TrimSpace(e.Command)] = e
	}

	if len(distinct) > 1 {
		var cmds []string
		for cmd := range distinct {
			cmds = append(cmds, fmt.Sprintf("%q", cmd))
		}
		return ReTestCommand{}, fmt.Errorf("%w: multiple distinct test commands detected in %s (%s); action: specify exactly one authoritative test command", ErrReTestCommandUnresolved, rootDir, strings.Join(cmds, ", "))
	}

	var selected dream.RunbookEntry
	for _, e := range distinct {
		selected = e
	}
	cmdStr := strings.TrimSpace(selected.Command)

	// Reject quotes, shell operators ; & | < >, substitutions, backticks, and newlines
	if strings.ContainsAny(cmdStr, "\r\n") ||
		strings.ContainsAny(cmdStr, "'\"") ||
		strings.ContainsAny(cmdStr, ";&|<>") ||
		strings.Contains(cmdStr, "$(") ||
		strings.Contains(cmdStr, "${") ||
		strings.ContainsRune(cmdStr, '`') {
		return ReTestCommand{}, fmt.Errorf("%w: command %q contains unsupported shell syntax; source: %s; action: configure direct argv command without shell operators or quotes", ErrReTestCommandUnsupported, cmdStr, selected.Source)
	}

	fields := strings.Fields(cmdStr)
	if len(fields) == 0 {
		return ReTestCommand{}, fmt.Errorf("%w: empty test command in %s; action: configure valid test command", ErrReTestCommandUnresolved, rootDir)
	}

	// Reject parameter expansions starting with $
	for _, f := range fields {
		if strings.HasPrefix(f, "$") {
			return ReTestCommand{}, fmt.Errorf("%w: command %q contains shell variable substitution %q; source: %s; action: configure direct argv command without shell substitutions", ErrReTestCommandUnsupported, cmdStr, f, selected.Source)
		}
	}

	// Reject leading environment assignments (e.g. FOO=bar)
	if strings.Contains(fields[0], "=") {
		return ReTestCommand{}, fmt.Errorf("%w: command %q contains leading environment assignment %q; source: %s; action: configure direct argv command without inline env vars", ErrReTestCommandUnsupported, cmdStr, fields[0], selected.Source)
	}

	// Validate allowed direct argv commands:
	// go test, pnpm test, pnpm run test, aubr test, aubr run test, cargo test, pytest, python/python3
	isAllowed := false
	switch {
	case len(fields) >= 2 && fields[0] == "go" && fields[1] == "test":
		isAllowed = true
	case len(fields) >= 3 && fields[0] == "pnpm" && fields[1] == "run" && fields[2] == "test":
		isAllowed = true
	case len(fields) >= 2 && fields[0] == "pnpm" && fields[1] == "test":
		isAllowed = true
	case len(fields) >= 3 && fields[0] == "aubr" && fields[1] == "run" && fields[2] == "test":
		isAllowed = true
	case len(fields) >= 2 && fields[0] == "aubr" && fields[1] == "test":
		isAllowed = true
	case len(fields) >= 2 && fields[0] == "cargo" && fields[1] == "test":
		isAllowed = true
	case len(fields) >= 1 && fields[0] == "pytest":
		isAllowed = true
	case len(fields) >= 2 && (fields[0] == "python3" || fields[0] == "python"):
		isAllowed = true
	}

	if !isAllowed {
		return ReTestCommand{}, fmt.Errorf("%w: command %q is not a supported direct argv command (supported: go test, pnpm test, pnpm run test, aubr test, cargo test, pytest); source: %s; action: configure supported direct argv command", ErrReTestCommandUnsupported, cmdStr, selected.Source)
	}

	return ReTestCommand{
		Executable: fields[0],
		Args:       fields[1:],
		Source:     selected.Source,
	}, nil
}

// DefaultReTestRunner executes the repository's resolved test command in rootDir.
func DefaultReTestRunner(ctx context.Context, rootDir string) (bool, string, error) {
	cmdSpec, err := ResolveReTestCommand(rootDir)
	if err != nil {
		return false, "", err
	}

	cmd := exec.CommandContext(ctx, cmdSpec.Executable, cmdSpec.Args...)
	cmd.Dir = rootDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, string(out), err
	}
	return true, string(out), nil
}
