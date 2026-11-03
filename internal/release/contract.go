// Package release defines Sila's published compatibility metadata.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

// MCPContractVersion is the current version number for Sila MCP schema definitions.
const MCPContractVersion = 1

// Metadata is shipped with every release and binds its binary to its public
// MCP and database contracts.
type Metadata struct {
	Version              string `json:"version"`
	Commit               string `json:"commit"`
	MCPContractVersion   int    `json:"mcp_contract_version"`
	MCPContractSHA256    string `json:"mcp_contract_sha256"`
	SchemaVersion        int    `json:"schema_version"`
	MinimumSchemaVersion int    `json:"minimum_schema_version"`
}

// Contract returns a canonicalized snapshot of the executable MCP registry.
func Contract() []mcp.Tool {
	tools := mcp.ToolDefinitions()
	for i := range tools {
		sort.Strings(tools[i].InputSchema.Required)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// ContractJSON returns deterministic, indented contract JSON.
func ContractJSON() ([]byte, error) { return json.MarshalIndent(Contract(), "", "  ") }

// ContractDigest identifies exactly the published MCP shape.
func ContractDigest() (string, error) {
	b, err := ContractJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// CurrentMetadata returns metadata for the binary currently running.
func CurrentMetadata() (Metadata, error) {
	digest, err := ContractDigest()
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{Version: version.Version, Commit: version.Commit, MCPContractVersion: MCPContractVersion, MCPContractSHA256: digest, SchemaVersion: store.CurrentSchemaVersion(), MinimumSchemaVersion: store.MinimumSupportedSchemaVersion()}, nil
}

// WriteContract writes canonical MCP contract JSON to the target file path.
func WriteContract(path string) error {
	b, err := ContractJSON()
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

// WriteMetadata writes current binary release metadata JSON to the target file path.
func WriteMetadata(path string) error {
	m, err := CurrentMetadata()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ValidateMetadata refuses an artifact whose release metadata does not match
// the intended tag or a known contract shape.
func ValidateMetadata(m Metadata, expectedVersion string) error {
	if !version.IsRelease(m.Version) {
		return fmt.Errorf("release metadata has invalid version %q", m.Version)
	}
	if expectedVersion != "" && m.Version != expectedVersion {
		return fmt.Errorf("release metadata version %q does not match expected %q", m.Version, expectedVersion)
	}
	if m.MCPContractVersion != MCPContractVersion {
		return fmt.Errorf("unsupported MCP contract version %d", m.MCPContractVersion)
	}
	if len(m.MCPContractSHA256) != 64 || strings.Trim(m.MCPContractSHA256, "0123456789abcdefABCDEF") != "" {
		return fmt.Errorf("invalid MCP contract checksum")
	}
	if m.SchemaVersion < 1 || m.SchemaVersion > store.CurrentSchemaVersion() {
		return fmt.Errorf("unsupported schema version %d", m.SchemaVersion)
	}
	if m.MinimumSchemaVersion < 1 || m.MinimumSchemaVersion > m.SchemaVersion {
		return fmt.Errorf("invalid minimum schema version %d", m.MinimumSchemaVersion)
	}
	return nil
}
