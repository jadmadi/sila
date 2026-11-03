package andon

import (
	"errors"
	"fmt"
	"strings"
)

// Well-known Andon halt error codes.
const (
	CodeMigrationHalt       = "ERR_ANDON_MIGRATION_HALT"
	CodeInvariantViolation  = "ERR_ANDON_INVARIANT_VIOLATION"
	CodeSchemaCorruption    = "ERR_ANDON_SCHEMA_CORRUPTION"
	CodeGateBreach          = "ERR_ANDON_GATE_BREACH"
	CodeMutationSandboxFail = "ERR_ANDON_MUTATION_SANDBOX_FAIL"
	CodeRollbackPanic       = "ERR_ANDON_ROLLBACK_PANIC"
)

// HaltError represents an immediate execution abort triggered by an Andon cord pull.
type HaltError struct {
	Code              string          `json:"code"`
	Operation         string          `json:"operation"`
	Severity          Severity        `json:"severity"`
	Reason            string          `json:"reason"`
	Invariant         string          `json:"invariant"`
	Remediation       string          `json:"remediation"`
	Context           map[string]any  `json:"context,omitempty"`
	Dump              *DiagnosticDump `json:"dump,omitempty"`
	RollbacksExecuted int             `json:"rollbacks_executed"`
	RollbackErrors    []string        `json:"rollback_errors,omitempty"`
	Cause             error           `json:"-"`
}

// Error implements the standard error interface.
func (e *HaltError) Error() string {
	if e == nil {
		return "<nil>"
	}
	base := fmt.Sprintf("andon halt [%s] during %s: %s (violated invariant: %s)", e.Code, e.Operation, e.Reason, e.Invariant)
	if e.Cause != nil {
		base += fmt.Sprintf(" (cause: %v)", e.Cause)
	}
	return base
}

// Unwrap returns the underlying cause of the halt, if any.
func (e *HaltError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// FormatAndon produces a high-visibility terminal banner representing the Andon cord pull.
func (e *HaltError) FormatAndon(noColor bool) string {
	if e == nil {
		return ""
	}

	red := func(s string) string {
		if noColor {
			return s
		}
		return "\033[1;31m" + s + "\033[0m"
	}
	yellow := func(s string) string {
		if noColor {
			return s
		}
		return "\033[1;33m" + s + "\033[0m"
	}
	cyan := func(s string) string {
		if noColor {
			return s
		}
		return "\033[1;36m" + s + "\033[0m"
	}
	bold := func(s string) string {
		if noColor {
			return s
		}
		return "\033[1m" + s + "\033[0m"
	}
	dim := func(s string) string {
		if noColor {
			return s
		}
		return "\033[2m" + s + "\033[0m"
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(red("═══════════════════════════════════════════════════════════════════════════════\n"))
	b.WriteString(red(fmt.Sprintf("🚨 ANDON CORD HALT TRIGGERED [%s] 🚨\n", e.Severity)))
	b.WriteString(red("═══════════════════════════════════════════════════════════════════════════════\n"))
	b.WriteString(fmt.Sprintf("  %s %s\n", bold("Operation:"), e.Operation))
	b.WriteString(fmt.Sprintf("  %s      %s\n", bold("Code:"), yellow(e.Code)))
	b.WriteString(fmt.Sprintf("  %s %s\n", bold("Invariant:"), cyan(e.Invariant)))
	b.WriteString(fmt.Sprintf("  %s    %s\n", bold("Reason:"), e.Reason))
	if e.Cause != nil {
		b.WriteString(fmt.Sprintf("  %s     %v\n", bold("Cause:"), e.Cause))
	}
	if e.RollbacksExecuted > 0 {
		b.WriteString(fmt.Sprintf("  %s  %d rollback actions executed cleanly\n", bold("Rollback:"), e.RollbacksExecuted))
	}
	if len(e.RollbackErrors) > 0 {
		b.WriteString(red(fmt.Sprintf("  ⚠️ Rollback Errors (%d):\n", len(e.RollbackErrors))))
		for _, rErr := range e.RollbackErrors {
			b.WriteString(fmt.Sprintf("    - %s\n", rErr))
		}
	}
	if e.Remediation != "" {
		b.WriteString("\n")
		b.WriteString(yellow("  👉 REMEDIATION / RECOVERY:\n"))
		b.WriteString(fmt.Sprintf("     %s\n", e.Remediation))
	}
	if e.Dump != nil && len(e.Dump.FailedPreconditions) > 0 {
		b.WriteString("\n")
		b.WriteString(dim("  Failed Preconditions:\n"))
		for _, f := range e.Dump.FailedPreconditions {
			b.WriteString(dim(fmt.Sprintf("    ✗ %s\n", f)))
		}
	}
	b.WriteString(red("═══════════════════════════════════════════════════════════════════════════════\n"))

	return b.String()
}

// AsHaltError extracts *HaltError from err if present.
func AsHaltError(err error) (*HaltError, bool) {
	if err == nil {
		return nil, false
	}
	var he *HaltError
	if errors.As(err, &he) {
		return he, true
	}
	return nil, false
}
