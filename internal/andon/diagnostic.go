package andon

import (
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// DiagnosticDump encapsulates snapshot state captured when an Andon cord is pulled.
type DiagnosticDump struct {
	Timestamp            string            `json:"timestamp"`
	Operation            string            `json:"operation"`
	PreconditionsChecked []string          `json:"preconditions_checked,omitempty"`
	FailedPreconditions  []string          `json:"failed_preconditions,omitempty"`
	DatabaseState        map[string]any    `json:"database_state,omitempty"`
	CallTrace            string            `json:"call_trace,omitempty"`
	SystemInfo           map[string]string `json:"system_info,omitempty"`
	Extra                map[string]any    `json:"extra,omitempty"`
}

// CaptureDump constructs a DiagnosticDump capturing runtime system state and call trace.
func CaptureDump(operation string, preconditions, failed []string, dbState, extra map[string]any) *DiagnosticDump {
	dump := &DiagnosticDump{
		Timestamp:            time.Now().UTC().Format(time.RFC3339Nano),
		Operation:            operation,
		PreconditionsChecked: preconditions,
		FailedPreconditions:  failed,
		DatabaseState:        dbState,
		Extra:                extra,
		SystemInfo: map[string]string{
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"go_version": runtime.Version(),
			"num_cpu":    string(rune('0' + runtime.NumCPU())),
		},
	}

	// Capture sanitized call trace
	stack := string(debug.Stack())
	lines := strings.Split(stack, "\n")
	filtered := make([]string, 0, len(lines))
	for _, l := range lines {
		// Omit internal debug.Stack frames for cleaner output
		if strings.Contains(l, "runtime/debug.Stack") || strings.Contains(l, "CaptureDump") {
			continue
		}
		filtered = append(filtered, l)
	}
	dump.CallTrace = strings.Join(filtered, "\n")

	return dump
}
