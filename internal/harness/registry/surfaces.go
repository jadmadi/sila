package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// HookSurface is one detected or candidate configuration surface for a harness.
type HookSurface struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Detected bool   `json:"detected"`
}

// SurfaceStatus is the per-surface installation/trust state.
type SurfaceStatus struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	Detected     bool   `json:"detected"`
	Configured   bool   `json:"configured"`
	Installed    bool   `json:"installed"`
	SessionStart bool   `json:"session_start"`
	Detail       string `json:"detail,omitempty"`
}

// AntigravitySurfaces returns the documented app/CLI/IDE candidates. Only
// surfaces whose parent directory exists are Detected; the CLI primary path is
// always installable by explicit request.
func AntigravitySurfaces(home string) []HookSurface {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	type candidate struct{ name, path string }
	candidates := []candidate{
		{"cli", filepath.Join(home, ".gemini", "config", "hooks.json")},
		{"app", filepath.Join(home, ".config", "Antigravity", "hooks.json")},
		{"ide", filepath.Join(home, ".config", "antigravity-ide", "hooks.json")},
	}
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, candidate{"app", filepath.Join(home, "Library", "Application Support", "Antigravity", "hooks.json")})
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidates = append(candidates, candidate{"app", filepath.Join(appData, "Antigravity", "hooks.json")})
		}
	}
	seen := map[string]bool{}
	out := make([]HookSurface, 0, len(candidates))
	for _, c := range candidates {
		if seen[c.path] {
			continue
		}
		seen[c.path] = true
		out = append(out, HookSurface{Name: c.name, Path: c.path, Detected: pathDetected(c.path)})
	}
	return out
}

func pathDetected(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if fi, err := os.Stat(filepath.Dir(path)); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// InstallUserSurface installs a selected Antigravity surface (all by default).
// Non-Antigravity harnesses delegate to InstallUser and ignore surface.
func InstallUserSurface(harness, homeDir, surface string, dryRun, replace bool) (InstallResult, error) {
	sp, ok := Lookup(harness)
	if !ok {
		return InstallUser(harness, homeDir, dryRun, replace)
	}
	if sp.ID != "antigravity" || strings.TrimSpace(surface) == "" || strings.EqualFold(surface, "all") {
		res, err := InstallUser(harness, homeDir, dryRun, replace)
		if err != nil || sp.ID != "antigravity" {
			return res, err
		}
		for _, s := range AntigravitySurfaces(homeDir) {
			if s.Name == "cli" || !s.Detected {
				continue
			}
			extra, extraErr := installJSON(sp, homeDir, s.Path, dryRun, replace)
			if extraErr != nil {
				return res, extraErr
			}
			res.Configured = res.Configured || extra.Configured
			res.Installed = res.Installed && (extra.Installed || extra.Skipped || extra.Refused)
			res.Trusted = res.Trusted && extra.Trusted
			res.Files = append(res.Files, extra.Files...)
			res.Plan = append(res.Plan, extra.Plan...)
		}
		if res.Installed && res.Detail == "installed" {
			res.Detail = "installed all detected Antigravity surfaces"
		}
		return res, nil
	}
	var selected *HookSurface
	for _, s := range AntigravitySurfaces(homeDir) {
		if strings.EqualFold(s.Name, surface) {
			selected = &s
			break
		}
	}
	if selected == nil {
		return InstallResult{Harness: sp.ID, Refused: true, Detail: fmt.Sprintf("unknown Antigravity surface %q (app|cli|ide|all)", surface)}, nil
	}
	if !selected.Detected && !strings.EqualFold(surface, "cli") {
		return InstallResult{Harness: sp.ID, Refused: true, Detail: fmt.Sprintf("Antigravity surface %q is not detected at %s", surface, selected.Path)}, nil
	}
	if strings.EqualFold(surface, "cli") {
		return InstallUser(harness, homeDir, dryRun, replace)
	}
	return installJSON(sp, homeDir, selected.Path, dryRun, replace)
}

func antigravitySurfaceStatuses(sp HarnessSpec, homeDir string) []SurfaceStatus {
	surfaces := AntigravitySurfaces(homeDir)
	out := make([]SurfaceStatus, 0, len(surfaces))
	for _, s := range surfaces {
		st := inspectShellHookStatus(sp, baseHarnessStatus(sp), s.Path, homeDir)
		ss := SurfaceStatus{
			Name: s.Name, Path: s.Path, Detected: s.Detected,
			Configured: st.Configured, Installed: st.Installed, Detail: st.Detail,
		}
		for _, ev := range st.Events {
			if ev == "SessionStart" {
				ss.SessionStart = true
				break
			}
		}
		out = append(out, ss)
	}
	return out
}

// TrustApproved reports whether an explicit trust receipt exists for harness
// under baseDir. It is the pre-install gate for trust-required harnesses.
func TrustApproved(harness, baseDir string) bool {
	sp, ok := Lookup(harness)
	if !ok || strings.TrimSpace(baseDir) == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(baseDir, ".sila", "harness-trust", sp.ID+".json"))
	if err != nil {
		return false
	}
	var rec TrustReceipt
	if err := json.Unmarshal(raw, &rec); err != nil {
		return false
	}
	return rec.Approved
}
