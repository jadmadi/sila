package store

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var (
	goModVersionRegex = regexp.MustCompile(`(?m)^go\s+([0-9]+(?:\.[0-9]+)?)`)
	ciGoVersionRegex  = regexp.MustCompile(`(?m)^\s*go-version:\s*['"]?([0-9]+(?:\.[0-9]+(?:\.[0-9]+)?)?)['"]?`)
	ciGoFileRegex     = regexp.MustCompile(`(?m)^\s*go-version-file:\s*['"]?([^'"\r\n]+)['"]?`)
)

// NormalizeGoVersion normalizes Go version strings (e.g. "go1.26.0", "1.26.2", "v1.26") to major.minor ("1.26").
func NormalizeGoVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "go")
	v = strings.TrimPrefix(v, "v")
	v = strings.Trim(v, `"'^~`)
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

// FindGoMod searches for go.mod starting in dir and ascending up to 3 directory levels or until filesystem root.
func FindGoMod(startDir string) (string, string, error) {
	if startDir == "" {
		var err error
		startDir, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	curr, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", err
	}
	for range 4 {
		candidate := filepath.Join(curr, "go.mod")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			data, err := os.ReadFile(candidate)
			if err != nil {
				return "", "", err
			}
			m := goModVersionRegex.FindStringSubmatch(string(data))
			if len(m) > 1 {
				return candidate, m[1], nil
			}
			return candidate, "", nil
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return "", "", os.ErrNotExist
}

// WorkflowDrift records a detected drift in a workflow file.
type WorkflowDrift struct {
	Path     string
	FoundVer string
	Expected string
}

// FindWorkflowDrifts locates .github/workflows in or above goModDir and inspects Go version parity.
func FindWorkflowDrifts(goModPath, goModVer string) ([]string, []WorkflowDrift, error) {
	if goModPath == "" || goModVer == "" {
		return nil, nil, nil
	}
	normTarget := NormalizeGoVersion(goModVer)
	baseDir := filepath.Dir(goModPath)

	// Look for .github/workflows in baseDir or parent directory
	var wfDir string
	candidates := []string{
		filepath.Join(baseDir, ".github", "workflows"),
		filepath.Join(filepath.Dir(baseDir), ".github", "workflows"),
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			wfDir = c
			break
		}
	}
	if wfDir == "" {
		return nil, nil, nil
	}

	entries, err := os.ReadDir(wfDir)
	if err != nil {
		return nil, nil, err
	}

	var scannedFiles []string
	var drifts []WorkflowDrift

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		wfPath := filepath.Join(wfDir, name)
		scannedFiles = append(scannedFiles, wfPath)

		content, err := os.ReadFile(wfPath)
		if err != nil {
			continue
		}
		sContent := string(content)

		// If go-version-file is used, it points to go.mod directly
		if ciGoFileRegex.MatchString(sContent) {
			continue
		}

		matches := ciGoVersionRegex.FindAllStringSubmatch(sContent, -1)
		for _, m := range matches {
			if len(m) > 1 {
				foundVer := m[1]
				if NormalizeGoVersion(foundVer) != normTarget {
					drifts = append(drifts, WorkflowDrift{
						Path:     wfPath,
						FoundVer: foundVer,
						Expected: goModVer,
					})
				}
			}
		}
	}

	return scannedFiles, drifts, nil
}

// AuditToolchain checks for toolchain version drift between go.mod, local runtime, and CI workflows.
func AuditToolchain(dir string) CheckResult {
	goModPath, goModVer, err := FindGoMod(dir)
	if err != nil || goModPath == "" || goModVer == "" {
		return CheckResult{
			Category: "Toolchain",
			Name:     "Toolchain Version Drift",
			Status:   StatusPass,
			Message:  "No go.mod detected in current workspace (skipped).",
		}
	}

	localNorm := NormalizeGoVersion(runtime.Version())
	scannedWfs, drifts, _ := FindWorkflowDrifts(goModPath, goModVer)

	if len(drifts) > 0 {
		var details []string
		for _, d := range drifts {
			rel, err := filepath.Rel(dir, d.Path)
			if err != nil {
				rel = filepath.Base(d.Path)
			}
			details = append(details, fmt.Sprintf("%s pins Go %s (expected %s)", rel, d.FoundVer, d.Expected))
		}
		relFirst, err := filepath.Rel(dir, drifts[0].Path)
		if err != nil {
			relFirst = filepath.Base(drifts[0].Path)
		}
		return CheckResult{
			Category: "Toolchain",
			Name:     "Toolchain Version Drift",
			Status:   StatusWarn,
			Message:  fmt.Sprintf("Toolchain drift detected: %d CI workflow(s) mismatch go.mod (%s).", len(drifts), goModVer),
			Detail:   strings.Join(details, "; "),
			Repair:   fmt.Sprintf("Run `sila doctor --repair` to sync %s with go.mod (%s).", relFirst, goModVer),
		}
	}

	if len(scannedWfs) > 0 {
		return CheckResult{
			Category: "Toolchain",
			Name:     "Toolchain Version Drift",
			Status:   StatusPass,
			Message:  fmt.Sprintf("go.mod (%s), local compiler (%s), and CI workflows (%d checked) are synchronized.", goModVer, localNorm, len(scannedWfs)),
		}
	}

	return CheckResult{
		Category: "Toolchain",
		Name:     "Toolchain Version Drift",
		Status:   StatusPass,
		Message:  fmt.Sprintf("go.mod specifies Go %s (no CI workflows found to audit).", goModVer),
	}
}

// RepairToolchain updates mismatched go-version pins in CI workflow files to match go.mod.
func RepairToolchain(dir string) ([]string, error) {
	goModPath, goModVer, err := FindGoMod(dir)
	if err != nil || goModPath == "" || goModVer == "" {
		return nil, nil
	}

	_, drifts, err := FindWorkflowDrifts(goModPath, goModVer)
	if err != nil || len(drifts) == 0 {
		return nil, nil
	}

	var repaired []string
	seen := make(map[string]bool)

	replaceRegex := regexp.MustCompile(`(?m)(^\s*go-version:\s*['"]?)[0-9]+(?:\.[0-9]+(?:\.[0-9]+)?)?(['"]?)`)

	for _, d := range drifts {
		if seen[d.Path] {
			continue
		}
		seen[d.Path] = true

		content, err := os.ReadFile(d.Path)
		if err != nil {
			continue
		}

		updated := replaceRegex.ReplaceAllString(string(content), "${1}"+goModVer+"${2}")
		if updated != string(content) {
			if err := os.WriteFile(d.Path, []byte(updated), 0644); err == nil {
				rel, err := filepath.Rel(dir, d.Path)
				if err != nil {
					rel = filepath.Base(d.Path)
				}
				repaired = append(repaired, fmt.Sprintf("Synchronized %s Go version to match go.mod (%s)", rel, goModVer))
			}
		}
	}

	return repaired, nil
}
