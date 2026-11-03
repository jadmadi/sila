package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/render"
)

// outputModes carries the process-wide machine-output flags. Sila runs one
// command per process, so these are invocation settings rather than state.
type outputModes struct {
	envelope  bool
	quiet     bool
	porcelain bool
	sync      bool
	advisory  bool
}

var outModes outputModes

// relativePathsMode opts JSON payloads into repository-relative file paths.
var relativePathsMode bool

// validateOutputModes enforces the mutual-exclusion contract before dispatch.
func validateOutputModes(jsonOut, jsonEnvelope, toonOut, porcelain bool) error {
	if jsonEnvelope && toonOut {
		return fmt.Errorf("--json-envelope and --toon are mutually exclusive")
	}
	if jsonOut && toonOut {
		return fmt.Errorf("--json and --toon are mutually exclusive")
	}
	if porcelain {
		switch {
		case jsonOut:
			return fmt.Errorf("--porcelain and --json are mutually exclusive")
		case jsonEnvelope:
			return fmt.Errorf("--porcelain and --json-envelope are mutually exclusive")
		case toonOut:
			return fmt.Errorf("--porcelain and --toon are mutually exclusive")
		}
	}
	return nil
}

// sanitizePorcelain keeps one field on one tab-separated line.
func sanitizePorcelain(s string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
}

// emitEnvelope writes a success envelope. Mutations lists files the command
// changed as a side effect; it is always [] when nothing was written.
func emitEnvelope(data any, warnings, mutations []string) {
	if err := render.EmitJSONSuccess(os.Stdout, data, warnings, mutations); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
}

// emitErrorEnvelope writes a stable error envelope to stdout and leaves the
// caller to choose the exit code.
func emitErrorEnvelope(code, message, fix string) {
	if err := render.EmitJSONError(os.Stdout, code, message, fix); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(1)
	}
}
