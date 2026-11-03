package main

import "fmt"

// scopeFailureAction decides what `sila goals` does when the leading
// positional resolves to no project (typo'd name, bare goal slug, missing
// directory). It returns a stderr notice ("" for none) and whether the
// caller must abort nonzero.
//
// A scoped subcommand following the bad positional (e.g. `sila goals
// <project> claim <slug>`) is a hard error: proceeding would silently turn
// the mutation into a no-op CWD audit at exit 0. A bare positional keeps the
// historical CWD audit with a notice, except under --global where the global
// audit is unambiguous and needs no notice. This gives the positional path
// parity with --project, which already hard-errors on resolve failure.
func scopeFailureAction(scope string, hasSubcommand, isGlobal bool) (notice string, hard bool) {
	if hasSubcommand {
		return fmt.Sprintf("sila goals: unknown project %q", scope), true
	}
	if isGlobal {
		return "", false
	}
	return fmt.Sprintf("sila goals: unknown project %q, auditing current directory", scope), false
}
