package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DriftReport summarizes synchronization status across repo rules and target formats.
type DriftReport struct {
	HasDrift            bool           `json:"has_drift"`
	DriftKind           string         `json:"drift_kind"`
	DriftKinds          []string       `json:"drift_kinds,omitempty"`
	RootAgentsExists    bool           `json:"root_agents_exists"`
	ClaudeLinkExists    bool           `json:"claude_link_exists"`
	CursorRulesExists   bool           `json:"cursor_rules_exists"`
	CoreHubHash         string         `json:"core_hub_hash"`
	CurrentHash         string         `json:"current_hash"`
	ManagedHash         string         `json:"managed_hash"`
	HubManagedHash      string         `json:"hub_managed_hash"`
	ManagedRevision     int            `json:"managed_revision"`
	HashMatch           bool           `json:"hash_match"`
	LegacyBlock         bool           `json:"legacy_block"`
	PrinciplesHashMatch bool           `json:"principles_hash_match"`
	PrinciplesPresent   bool           `json:"principles_present"`
	TargetStatuses      []TargetStatus `json:"target_statuses"`
	MissingFiles        []string       `json:"missing_files,omitempty"`
	Details             []string       `json:"details,omitempty"`
}

// TargetStatus reports one projected target independently of the whole-file hash.
type TargetStatus struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Present   bool   `json:"present"`
	Managed   bool   `json:"managed"`
	HashMatch bool   `json:"hash_match"`
	Detail    string `json:"detail,omitempty"`
}

// DetectDrift audits the current repository against compiled Hub-and-Spoke standards.
func DetectDrift(repoRoot string, opts CompileOptions) (*DriftReport, error) {
	if repoRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		repoRoot = cwd
	}

	report := &DriftReport{
		MissingFiles:        []string{},
		Details:             []string{},
		TargetStatuses:      []TargetStatus{},
		DriftKinds:          []string{},
		PrinciplesHashMatch: true,
	}

	compiler := NewCompiler(repoRoot)
	expectedContent, err := compiler.Compile(opts)
	if err != nil {
		return nil, fmt.Errorf("compile expected rules: %w", err)
	}
	expectedManaged, err := compiler.CompileProjectManagedBlock(opts)
	if err != nil {
		return nil, fmt.Errorf("compile expected managed block: %w", err)
	}

	report.CoreHubHash = HashContent(expectedContent)
	report.HubManagedHash = HashManagedBody(expectedManaged)

	// 1. Audit AGENTS.md
	rootAgents := filepath.Join(repoRoot, "AGENTS.md")
	data, err := os.ReadFile(rootAgents)
	if err != nil {
		report.HasDrift = true
		report.RootAgentsExists = false
		report.MissingFiles = append(report.MissingFiles, "AGENTS.md")
		report.Details = append(report.Details, "AGENTS.md is missing from repository root")
		report.TargetStatuses = append(report.TargetStatuses, TargetStatus{Path: "AGENTS.md", Kind: "agents"})
	} else {
		report.RootAgentsExists = true
		report.CurrentHash = HashContent(string(data))
		_, meta, hasBlock, legacy := ExtractManagedBlock(string(data))
		report.LegacyBlock = hasBlock && legacy
		switch {
		case report.LegacyBlock:
			report.HasDrift = true
			report.Details = append(report.Details, "AGENTS.md managed block has no fingerprint (legacy); run 'sila agents sync' to migrate")
		case hasBlock:
			report.ManagedHash = meta.Hash
			report.ManagedRevision = meta.Revision
			report.HashMatch = meta.Revision == ManagedRevision && meta.Hash == report.HubManagedHash
			if !report.HashMatch {
				report.HasDrift = true
				report.Details = append(report.Details, fmt.Sprintf("AGENTS.md managed block is at rev=%d hash=%s; hub is rev=%d hash=%s", meta.Revision, meta.Hash, ManagedRevision, report.HubManagedHash))
			}
		default:
			report.Details = append(report.Details, "AGENTS.md has no managed block; run 'sila agents sync' to create it")
		}
		report.TargetStatuses = append(report.TargetStatuses, TargetStatus{
			Path: "AGENTS.md", Kind: "agents", Present: true, Managed: hasBlock && !legacy,
			HashMatch: report.HashMatch, Detail: "canonical rule file",
		})
		if hasBlock {
			existingPrinciples, hasPrinciples := ExtractPrinciplesBlock(string(data))
			report.PrinciplesPresent = hasPrinciples
			if opts.IncludePrinciples {
				expectedPrinciples := DefaultPrinciplesPack(opts.Stack).RenderMarkdown()
				report.PrinciplesHashMatch = hasPrinciples && HashContent(existingPrinciples) == HashContent(expectedPrinciples)
				if !report.PrinciplesHashMatch {
					report.Details = append(report.Details, "AGENTS.md principles block is missing or stale")
				}
			} else {
				report.PrinciplesHashMatch = true
			}
		} else {
			report.PrinciplesHashMatch = true
		}
		// Check whether the Core Hub safety rails exist in current AGENTS.md
		// (heading names across sila generations).
		if !strings.Contains(string(data), "## 0. Safety Rails") && !strings.Contains(string(data), "Hard Prohibitions") && !strings.Contains(string(data), "Hard Constraints") {
			report.HasDrift = true
			report.Details = append(report.Details, "AGENTS.md is missing Core Hub safety rails")
		}
	}

	// 2. Audit CLAUDE.md
	claudePath := filepath.Join(repoRoot, "CLAUDE.md")
	claudeStatus := TargetStatus{Path: "CLAUDE.md", Kind: "claude"}
	if fi, err := os.Lstat(claudePath); err != nil {
		report.ClaudeLinkExists = false
		if HasClaudeIntegration(repoRoot) {
			report.Details = append(report.Details, "CLAUDE.md is not linked or present")
			report.MissingFiles = append(report.MissingFiles, "CLAUDE.md")
		}
	} else {
		report.ClaudeLinkExists = true
		claudeStatus.Present = true
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(claudePath)
			claudeStatus.Managed = target == "AGENTS.md" || target == "./AGENTS.md"
			claudeStatus.HashMatch = claudeStatus.Managed
			if !claudeStatus.Managed {
				report.HasDrift = true
				report.Details = append(report.Details, fmt.Sprintf("CLAUDE.md links to %s instead of AGENTS.md", target))
			}
			claudeStatus.Detail = "symlink -> " + target
		} else {
			claudeStatus.Detail = "regular user file preserved"
		}
	}
	report.TargetStatuses = append(report.TargetStatuses, claudeStatus)

	geminiPath := filepath.Join(repoRoot, "GEMINI.md")
	geminiStatus := TargetStatus{Path: "GEMINI.md", Kind: "gemini"}
	if fi, err := os.Lstat(geminiPath); err != nil {
		if HasGeminiIntegration(repoRoot) {
			report.MissingFiles = append(report.MissingFiles, "GEMINI.md")
			report.Details = append(report.Details, "GEMINI.md is not linked or present")
		}
	} else {
		geminiStatus.Present = true
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(geminiPath)
			geminiStatus.Managed = target == "AGENTS.md" || target == "./AGENTS.md"
			geminiStatus.HashMatch = geminiStatus.Managed
			geminiStatus.Detail = "symlink -> " + target
		} else {
			geminiStatus.Detail = "regular user file preserved"
		}
	}
	report.TargetStatuses = append(report.TargetStatuses, geminiStatus)

	// 3. Audit Cursor rules
	cursorMDC := filepath.Join(repoRoot, ".cursor", "rules", "000-sila-core.mdc")
	cursorLegacy := filepath.Join(repoRoot, ".cursorrules")
	cursorStatus := TargetStatus{Path: ".cursor/rules/000-sila-core.mdc", Kind: "cursor"}
	if data, err := os.ReadFile(cursorMDC); err == nil {
		report.CursorRulesExists = true
		cursorStatus.Present = true
		cursorStatus.Managed = strings.Contains(string(data), OwnedBlockBegin)
		cursorStatus.HashMatch = cursorStatus.Managed
		cursorStatus.Detail = "generated Cursor projection"
		if !cursorStatus.Managed {
			cursorStatus.Detail = "foreign Cursor file preserved"
		}
	} else if _, err := os.Stat(cursorLegacy); err == nil {
		report.CursorRulesExists = true
		cursorStatus.Present = true
		cursorStatus.Detail = "legacy .cursorrules present"
	} else {
		report.CursorRulesExists = false
		if HasCursorIntegration(repoRoot) {
			report.Details = append(report.Details, "Cursor rules (.cursor/rules/000-sila-core.mdc) not configured")
			report.MissingFiles = append(report.MissingFiles, ".cursor/rules/000-sila-core.mdc")
		}
	}
	report.TargetStatuses = append(report.TargetStatuses, cursorStatus)

	devinStatus := TargetStatus{Path: ".devin/setup.sh", Kind: "devin"}
	if data, err := os.ReadFile(filepath.Join(repoRoot, ".devin", "setup.sh")); err == nil {
		devinStatus.Present = true
		devinStatus.Managed = strings.Contains(string(data), "Generated by 'sila install'")
		devinStatus.HashMatch = devinStatus.Managed
		devinStatus.Detail = "Devin setup script"
	} else if HasDevinIntegration(repoRoot) {
		report.MissingFiles = append(report.MissingFiles, ".devin/setup.sh")
		report.Details = append(report.Details, ".devin/setup.sh is missing")
	}
	report.TargetStatuses = append(report.TargetStatuses, devinStatus)

	classifyDrift(report)
	return report, nil
}

func classifyDrift(report *DriftReport) {
	has := func(kind string) bool {
		for _, k := range report.DriftKinds {
			if k == kind {
				return true
			}
		}
		return false
	}
	add := func(kind string) {
		if !has(kind) {
			report.DriftKinds = append(report.DriftKinds, kind)
		}
	}
	for _, missing := range report.MissingFiles {
		_ = missing
		add("missing_target")
	}
	if !report.PrinciplesHashMatch {
		add("principles_drift")
	}
	if (report.ManagedHash != "" && !report.HashMatch) ||
		(report.RootAgentsExists && report.ManagedHash == "" && !report.LegacyBlock) {
		add("managed_hash_drift")
	}
	if report.LegacyBlock {
		add("legacy_block")
	}
	for _, target := range report.TargetStatuses {
		if target.Present && !target.Managed && target.Kind == "cursor" {
			add("foreign_target")
		}
	}
	if len(report.DriftKinds) == 0 {
		report.DriftKind = "in_sync"
		report.HasDrift = false
		return
	}
	priority := []string{"error", "missing_target", "principles_drift", "managed_hash_drift", "legacy_block", "foreign_target"}
	for _, kind := range priority {
		if has(kind) {
			report.DriftKind = kind
			break
		}
	}
	report.HasDrift = report.DriftKind != "in_sync"
}

// HashContent returns a SHA256 hex digest of normalized content.
func HashContent(s string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n")
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])[:16]
}
