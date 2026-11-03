package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
)

// mcpBridgeDiagnostic reports the Sila-side Command Code bridge contract and
// the last observed proxy failure. It never starts a server or mutates state.
type mcpBridgeDiagnostic struct {
	ExpectedEnvelope string `json:"expected_envelope"`
	ContinuityTools  int    `json:"continuity_tools"`
	SchemaDigest     string `json:"schema_digest"`
	Profile          string `json:"profile"`
	LastProxyError   string `json:"last_proxy_error"`
}

// buildMCPBridgeDiagnostic inspects the local Command Code log for the last
// mcp__sila__ "Invalid params" line. A missing log is not a failure.
func buildMCPBridgeDiagnostic(homeDir string) mcpBridgeDiagnostic {
	diag := mcpBridgeDiagnostic{
		ExpectedEnvelope: `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"<tool>","arguments":{...}}}`,
		ContinuityTools:  mcp.ContinuityToolCount,
		SchemaDigest:     mcp.SchemaDigest(),
		Profile:          mcp.ProfileContinuity,
		LastProxyError:   "none recorded",
	}
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}
	logPath := filepath.Join(homeDir, ".commandcode", "logs", "command.log")
	f, err := os.Open(logPath)
	if err != nil {
		return diag
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "mcp__sila__") && strings.Contains(strings.ToLower(line), "invalid params") {
			diag.LastProxyError = line
		}
	}
	return diag
}

// isSilaMCPServer reports whether the process at pid runs `sila mcp`.
func isSilaMCPServer(pid int) bool {
	return store.IsSilaMCPServer(pid)
}

// appendMCPFreshnessCheck contributes the running-server check to a doctor
// report and refreshes the summary so the result reflects it.
func appendMCPFreshnessCheck(rep *store.DoctorReport) {
	store.AppendMCPFreshnessCheck(rep)
}
