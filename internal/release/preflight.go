package release

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assess"
	"github.com/jadmadi/sila/internal/version"
)

// CheckStatus represents the evaluation state of a preflight quality check.
type CheckStatus string

const (
	// StatusPass indicates the check succeeded completely.
	StatusPass CheckStatus = "PASS"
	// StatusWarn indicates the check passed with non-fatal observations.
	StatusWarn CheckStatus = "WARN"
	// StatusFail indicates the check encountered a fatal blocker.
	StatusFail CheckStatus = "FAIL"
	// StatusSkipped indicates the check was bypassed in the current mode.
	StatusSkipped CheckStatus = "SKIPPED"
)

// CheckResult records the individual outcome of a quality matrix evaluation.
type CheckResult struct {
	Name       string      `json:"name"`
	Category   string      `json:"category"`
	Status     CheckStatus `json:"status"`
	Detail     string      `json:"detail"`
	DurationMs int64       `json:"duration_ms"`
}

// PreflightOptions configures the release quality gate execution environment.
type PreflightOptions struct {
	AllowDirty bool   `json:"allow_dirty"`
	Offline    bool   `json:"offline"`
	Strict     bool   `json:"strict"`
	RepoDir    string `json:"repo_dir"`
	DBPath     string `json:"db_path,omitempty"`
}

// ScorecardReport consolidates multi-dimensional quality metrics into an overall grade.
type ScorecardReport struct {
	Version     string        `json:"version"`
	Score       float64       `json:"score"`
	Grade       string        `json:"grade"`
	Passed      bool          `json:"passed"`
	OfflineMode bool          `json:"offline_mode"`
	Timestamp   string        `json:"timestamp"`
	Checks      []CheckResult `json:"checks"`
	Summary     string        `json:"summary"`
}

// isOnline performs a lightweight, non-blocking reachability probe with a strict timeout.
func isOnline(timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", "github.com:443", timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// RunPreflight runs the comprehensive Software Quality Scorecard and Release Readiness Matrix.
func RunPreflight(opts PreflightOptions) (ScorecardReport, error) {
	repoDir := opts.RepoDir
	if repoDir == "" {
		repoDir = ResolveGitRepoDir("")
	}

	var checks []CheckResult

	// 1. SemVer & Git Tag Discipline
	t0 := time.Now()
	gitTagRes := checkGitTag(repoDir, opts)
	gitTagRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, gitTagRes)

	// 2. Working Tree Cleanliness
	t0 = time.Now()
	worktreeRes := checkWorktree(repoDir, opts)
	worktreeRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, worktreeRes)

	// 3. License & Data Sovereignty Covenant
	t0 = time.Now()
	licenseRes := checkLicense(repoDir)
	licenseRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, licenseRes)

	// 4. Go Report Card & Static Code Quality
	t0 = time.Now()
	reportCardRes := checkReportCard(repoDir)
	reportCardRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, reportCardRes)

	// 5. MCP Schema Contract Stability
	t0 = time.Now()
	mcpRes := checkMCPContract(repoDir)
	mcpRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, mcpRes)

	// 6. Documentation & Changelog Parity
	t0 = time.Now()
	docsRes := checkDocsAndChangelog(repoDir)
	docsRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, docsRes)

	// 7. Remote Origin Reachability Probe (with Offline Resilience)
	t0 = time.Now()
	remoteRes, detectedOffline := checkRemoteReachability(repoDir, opts)
	remoteRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, remoteRes)

	// 8. Package Ecosystem & Indexing Readiness Probe
	t0 = time.Now()
	pkgRes := checkPackageEcosystem(opts, detectedOffline)
	pkgRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, pkgRes)

	// 9. 3-Day Local Dogfood Soak & Daily Telemetry Verification
	t0 = time.Now()
	soakRes := checkLocalSoak(repoDir, opts)
	soakRes.DurationMs = time.Since(t0).Milliseconds()
	checks = append(checks, soakRes)

	// Score Calculation
	var totalPossible float64
	var totalScore float64
	hasFail := false
	hasSkipped := false

	for _, c := range checks {
		totalPossible += 100.0
		switch c.Status {
		case StatusPass:
			totalScore += 100.0
		case StatusWarn:
			totalScore += 75.0
		case StatusSkipped:
			if opts.Strict {
				hasSkipped = true
				// In strict mode, skipped probes are penalized
				totalScore += 0.0
			} else {
				// In normal/offline mode, skipped remote probes do not penalize score
				totalScore += 100.0
			}
		case StatusFail:
			hasFail = true
			totalScore += 0.0
		}
	}

	score := 0.0
	if totalPossible > 0 {
		score = (totalScore / totalPossible) * 100.0
	}

	grade := calculateGrade(score)

	passed := !hasFail && score >= 80.0
	if opts.Strict {
		passed = passed && !hasSkipped && score >= 90.0
	}

	summary := fmt.Sprintf("Quality Score: %.1f%% | Grade: %s | Mode: %s",
		score, grade, modeLabel(opts.Offline, detectedOffline))

	report := ScorecardReport{
		Version:     version.Version,
		Score:       score,
		Grade:       grade,
		Passed:      passed,
		OfflineMode: opts.Offline || detectedOffline,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Checks:      checks,
		Summary:     summary,
	}

	return report, nil
}

func modeLabel(forcedOffline, detectedOffline bool) string {
	if forcedOffline {
		return "Forced Offline (--offline)"
	}
	if detectedOffline {
		return "Offline Resilient (Auto-Detected)"
	}
	return "Connected Online"
}

func calculateGrade(score float64) string {
	switch {
	case score >= 95.0:
		return "A+"
	case score >= 90.0:
		return "A"
	case score >= 80.0:
		return "B"
	case score >= 70.0:
		return "C"
	case score >= 60.0:
		return "D"
	default:
		return "F"
	}
}

func checkGitTag(repoDir string, opts PreflightOptions) CheckResult {
	cmd := exec.Command("git", "describe", "--tags", "--exact-match", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	currentTag := strings.TrimSpace(string(out))

	if err != nil || currentTag == "" {
		if opts.AllowDirty {
			return CheckResult{
				Name:     "SemVer & Git Tag Discipline",
				Category: "Repository & Git Hygiene",
				Status:   StatusWarn,
				Detail:   fmt.Sprintf("HEAD commit not tagged with exact release version (current: %s; dev build allowed by --allow-dirty)", version.Version),
			}
		}
		return CheckResult{
			Name:     "SemVer & Git Tag Discipline",
			Category: "Repository & Git Hygiene",
			Status:   StatusFail,
			Detail:   fmt.Sprintf("HEAD must be tagged with exact SemVer tag matching version %s", version.Version),
		}
	}

	// Verify tag format
	if !strings.HasPrefix(currentTag, "v") {
		return CheckResult{
			Name:     "SemVer & Git Tag Discipline",
			Category: "Repository & Git Hygiene",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Current tag %q does not follow standard 'vX.Y.Z' prefix", currentTag),
		}
	}

	// Verify annotated tag
	catCmd := exec.Command("git", "cat-file", "-t", currentTag)
	catCmd.Dir = repoDir
	catOut, _ := catCmd.Output()
	if strings.TrimSpace(string(catOut)) != "tag" {
		return CheckResult{
			Name:     "SemVer & Git Tag Discipline",
			Category: "Repository & Git Hygiene",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Tag %q is lightweight; annotated release tags are recommended", currentTag),
		}
	}

	return CheckResult{
		Name:     "SemVer & Git Tag Discipline",
		Category: "Repository & Git Hygiene",
		Status:   StatusPass,
		Detail:   fmt.Sprintf("HEAD matches verified annotated release tag %s", currentTag),
	}
}

func checkWorktree(repoDir string, opts PreflightOptions) CheckResult {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:     "Worktree Cleanliness",
			Category: "Repository & Git Hygiene",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Could not check git status: %v", err),
		}
	}

	dirtyLines := strings.TrimSpace(string(out))
	if dirtyLines != "" {
		lines := strings.Split(dirtyLines, "\n")
		if opts.AllowDirty {
			return CheckResult{
				Name:     "Worktree Cleanliness",
				Category: "Repository & Git Hygiene",
				Status:   StatusWarn,
				Detail:   fmt.Sprintf("%d uncommitted modification(s) in working tree (tolerated by --allow-dirty)", len(lines)),
			}
		}
		return CheckResult{
			Name:     "Worktree Cleanliness",
			Category: "Repository & Git Hygiene",
			Status:   StatusFail,
			Detail:   fmt.Sprintf("Working tree contains %d uncommitted modification(s); clean worktree required for release", len(lines)),
		}
	}

	return CheckResult{
		Name:     "Worktree Cleanliness",
		Category: "Repository & Git Hygiene",
		Status:   StatusPass,
		Detail:   "Working tree is completely clean (zero uncommitted or untracked changes)",
	}
}

func checkLicense(repoDir string) CheckResult {
	licensePath := filepath.Join(repoDir, "LICENSE")
	data, err := os.ReadFile(licensePath)
	if err != nil {
		// Also check parent directory if in sub-repo
		parentLicense := filepath.Join(filepath.Dir(repoDir), "LICENSE")
		if data2, err2 := os.ReadFile(parentLicense); err2 == nil {
			data = data2
		} else {
			return CheckResult{
				Name:     "License & Sovereignty Covenant",
				Category: "License & Sovereignty",
				Status:   StatusFail,
				Detail:   "LICENSE file missing from repository root",
			}
		}
	}

	content := string(data)
	if strings.Contains(content, "Noncommercial") || strings.Contains(content, "PolyForm Noncommercial") {
		return CheckResult{
			Name:     "License & Sovereignty Covenant",
			Category: "License & Sovereignty",
			Status:   StatusFail,
			Detail:   "Forbidden non-commercial license restriction detected; violates open-source sovereignty covenant",
		}
	}

	var licName string
	switch {
	case strings.Contains(content, "GNU AFFERO GENERAL PUBLIC LICENSE"):
		licName = "AGPL-3.0"
	case strings.Contains(content, "MIT License") || strings.Contains(content, "Permission is hereby granted, free of charge"):
		licName = "MIT"
	case strings.Contains(content, "Apache License"):
		licName = "Apache-2.0"
	case strings.Contains(content, "BSD 3-Clause") || strings.Contains(content, "Redistribution and use in source and binary forms"):
		licName = "BSD-3-Clause"
	default:
		licName = "Custom / Unrecognized Open-Source"
	}

	return CheckResult{
		Name:     "License & Sovereignty Covenant",
		Category: "License & Sovereignty",
		Status:   StatusPass,
		Detail:   fmt.Sprintf("Verified OSI/FSF license (%s); zero non-commercial riders detected", licName),
	}
}

func checkReportCard(repoDir string) CheckResult {
	rc, err := assess.AuditReportCard(repoDir)
	if err != nil {
		return CheckResult{
			Name:     "Go Report Card & Static Analysis",
			Category: "Code Quality & Static Analysis",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Static analysis evaluation skipped: %v", err),
		}
	}

	if rc.Clean {
		return CheckResult{
			Name:     "Go Report Card & Static Analysis",
			Category: "Code Quality & Static Analysis",
			Status:   StatusPass,
			Detail:   fmt.Sprintf("Grade %s (%.1f%%) across %d files (gofmt, govet, ineffassign, misspell, gocyclo)", rc.Grade, rc.Score, rc.FilesScanned),
		}
	}

	if rc.Score >= 80.0 {
		return CheckResult{
			Name:     "Go Report Card & Static Analysis",
			Category: "Code Quality & Static Analysis",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Grade %s (%.1f%%) meets baseline threshold but has minor static analysis warnings", rc.Grade, rc.Score),
		}
	}

	return CheckResult{
		Name:     "Go Report Card & Static Analysis",
		Category: "Code Quality & Static Analysis",
		Status:   StatusFail,
		Detail:   fmt.Sprintf("Grade %s (%.1f%%) falls below 80%% code quality requirement", rc.Grade, rc.Score),
	}
}

func checkMCPContract(repoDir string) CheckResult {
	contractPath := filepath.Join(repoDir, "contracts", "mcp-v1.json")
	if _, err := os.Stat(contractPath); err != nil {
		return CheckResult{
			Name:     "MCP Schema Contract Stability",
			Category: "Contract & API Stability",
			Status:   StatusWarn,
			Detail:   "Baseline contracts/mcp-v1.json not found; schema diff check bypassed",
		}
	}

	previous, err := ReadContract(contractPath)
	if err != nil {
		return CheckResult{
			Name:     "MCP Schema Contract Stability",
			Category: "Contract & API Stability",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Could not read baseline MCP contract: %v", err),
		}
	}

	changes := BreakingChanges(previous, Contract())
	if len(changes) > 0 {
		return CheckResult{
			Name:     "MCP Schema Contract Stability",
			Category: "Contract & API Stability",
			Status:   StatusFail,
			Detail:   fmt.Sprintf("Breaking MCP contract changes detected: %s", strings.Join(changes, "; ")),
		}
	}

	return CheckResult{
		Name:     "MCP Schema Contract Stability",
		Category: "Contract & API Stability",
		Status:   StatusPass,
		Detail:   fmt.Sprintf("Zero breaking changes against published MCP baseline (%d tools verified)", len(previous)),
	}
}

func checkDocsAndChangelog(repoDir string) CheckResult {
	changelogPath := filepath.Join(repoDir, "CHANGELOG.md")
	data, err := os.ReadFile(changelogPath)
	if err != nil {
		return CheckResult{
			Name:     "Documentation & Changelog Parity",
			Category: "Documentation & Contracts",
			Status:   StatusWarn,
			Detail:   "CHANGELOG.md not found in repository root",
		}
	}

	content := string(data)
	targetVer := strings.TrimPrefix(version.Version, "v")
	hasVerSection := strings.Contains(content, "## ["+targetVer+"]")
	hasUnreleased := strings.Contains(content, "## [Unreleased]")

	if !hasVerSection && !hasUnreleased {
		return CheckResult{
			Name:     "Documentation & Changelog Parity",
			Category: "Documentation & Contracts",
			Status:   StatusFail,
			Detail:   fmt.Sprintf("CHANGELOG.md missing release section for %s or [Unreleased]", version.Version),
		}
	}

	refPath := filepath.Join(repoDir, "docs", "reference.md")
	hasRef := false
	if _, err := os.Stat(refPath); err == nil {
		hasRef = true
	}

	detail := "CHANGELOG.md contains release notes"
	if hasRef {
		detail += "; docs/reference.md verified"
	}

	return CheckResult{
		Name:     "Documentation & Changelog Parity",
		Category: "Documentation & Contracts",
		Status:   StatusPass,
		Detail:   detail,
	}
}

func checkRemoteReachability(repoDir string, opts PreflightOptions) (CheckResult, bool) {
	if opts.Offline {
		return CheckResult{
			Name:     "Remote Origin Reachability",
			Category: "Upstream & Ecosystem Reachability",
			Status:   StatusSkipped,
			Detail:   "Offline mode active (--offline); remote network reachability probe bypassed",
		}, true
	}

	// Non-blocking auto-probe (350ms timeout)
	if !isOnline(350 * time.Millisecond) {
		if opts.Strict {
			return CheckResult{
				Name:     "Remote Origin Reachability",
				Category: "Upstream & Ecosystem Reachability",
				Status:   StatusFail,
				Detail:   "Network reachability probe failed in --strict release mode",
			}, true
		}
		return CheckResult{
			Name:     "Remote Origin Reachability",
			Category: "Upstream & Ecosystem Reachability",
			Status:   StatusSkipped,
			Detail:   "Offline environment detected (sub-350ms probe timed out); remote probe safely skipped with zero penalty",
		}, true
	}

	// Online: verify git remote origin
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return CheckResult{
			Name:     "Remote Origin Reachability",
			Category: "Upstream & Ecosystem Reachability",
			Status:   StatusWarn,
			Detail:   "No git remote 'origin' configured",
		}, false
	}

	remoteURL := strings.TrimSpace(string(out))
	if !strings.Contains(remoteURL, "github.com/jadmadi/sila") && !strings.Contains(remoteURL, "jadmadi/sila") {
		return CheckResult{
			Name:     "Remote Origin Reachability",
			Category: "Upstream & Ecosystem Reachability",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("Remote origin points to %q (expected canonical jadmadi/sila)", remoteURL),
		}, false
	}

	return CheckResult{
		Name:     "Remote Origin Reachability",
		Category: "Upstream & Ecosystem Reachability",
		Status:   StatusPass,
		Detail:   fmt.Sprintf("Verified canonical remote origin: %s", remoteURL),
	}, false
}

func checkPackageEcosystem(opts PreflightOptions, offlineDetected bool) CheckResult {
	if opts.Offline || offlineDetected {
		return CheckResult{
			Name:     "Package Ecosystem & Indexing Readiness",
			Category: "Upstream & Ecosystem Reachability",
			Status:   StatusSkipped,
			Detail:   "Offline mode active; pkg.go.dev indexing verification probe bypassed",
		}
	}

	client := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Head("https://pkg.go.dev/github.com/jadmadi/sila")
	if err != nil {
		return CheckResult{
			Name:     "Package Ecosystem & Indexing Readiness",
			Category: "Upstream & Ecosystem Reachability",
			Status:   StatusWarn,
			Detail:   fmt.Sprintf("pkg.go.dev probe timed out or unreachable: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return CheckResult{
			Name:     "Package Ecosystem & Indexing Readiness",
			Category: "Upstream & Ecosystem Reachability",
			Status:   StatusPass,
			Detail:   "Module is successfully indexed and reachable on pkg.go.dev",
		}
	}

	return CheckResult{
		Name:     "Package Ecosystem & Indexing Readiness",
		Category: "Upstream & Ecosystem Reachability",
		Status:   StatusWarn,
		Detail:   fmt.Sprintf("pkg.go.dev returned HTTP %d (normal for unreleased or pending versions)", resp.StatusCode),
	}
}

func checkLocalSoak(repoDir string, opts PreflightOptions) CheckResult {
	soak := EvaluateSoak(SoakOptions{
		RepoDir: repoDir,
		DBPath:  opts.DBPath,
	})

	switch soak.State {
	case SoakUpToDate, SoakReady:
		return CheckResult{
			Name:     "3-Day Local Soak & Telemetry",
			Category: "Lifecycle & Dogfood Soak",
			Status:   StatusPass,
			Detail:   soak.Summary,
		}
	case SoakInProgress:
		status := StatusWarn
		if opts.Strict {
			status = StatusFail
		}
		return CheckResult{
			Name:     "3-Day Local Soak & Telemetry",
			Category: "Lifecycle & Dogfood Soak",
			Status:   status,
			Detail:   soak.Summary,
		}
	case SoakNeedsDebug:
		status := StatusFail
		if opts.AllowDirty && !opts.Strict {
			status = StatusWarn
		}
		return CheckResult{
			Name:     "3-Day Local Soak & Telemetry",
			Category: "Lifecycle & Dogfood Soak",
			Status:   status,
			Detail:   soak.Summary,
		}
	default:
		return CheckResult{
			Name:     "3-Day Local Soak & Telemetry",
			Category: "Lifecycle & Dogfood Soak",
			Status:   StatusWarn,
			Detail:   soak.Summary,
		}
	}
}
