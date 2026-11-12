package mcp

import "os"

// init gives the package tests a deterministic identity. Anonymous behavior is
// exercised by tests that call t.Setenv("SILA_AGENT", "") explicitly.
func init() {
	if os.Getenv("SILA_AGENT") == "" && os.Getenv("SILA_ALLOW_ANONYMOUS") == "" {
		_ = os.Setenv("SILA_AGENT", "test-agent")
	}
}
