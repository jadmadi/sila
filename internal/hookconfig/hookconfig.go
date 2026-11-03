// Package hookconfig installs, removes, and reports Sila-owned agent lifecycle
// hooks in documented harness settings files. It delegates to the declarative
// harness adapter registry in internal/harness/registry while preserving
// existing caller contracts.
package hookconfig

import (
	"github.com/jadmadi/sila/internal/harness/registry"
)

// MemoryContractVersion is shared by the session brief, hooks, and salam.
const MemoryContractVersion = "sila/memory-contract/v1"

// Status is the hook installation state for one harness.
type Status struct {
	Harness             string          `json:"harness"`
	DisplayName         string          `json:"display_name,omitempty"`
	Kind                string          `json:"kind,omitempty"`
	Path                string          `json:"path,omitempty"`
	Scope               string          `json:"scope,omitempty"`
	Supported           bool            `json:"supported"`
	Installed           bool            `json:"installed"`
	Configured          bool            `json:"configured,omitempty"`
	Trusted             bool            `json:"trusted"`
	TrustMode           string          `json:"trust_mode,omitempty"`
	ProjectHooksIgnored bool            `json:"project_hooks_ignored,omitempty"`
	Events              []string        `json:"events,omitempty"`
	Capabilities        map[string]bool `json:"capabilities,omitempty"`
	Limitations         []string        `json:"limitations,omitempty"`
	DocsURL             string          `json:"docs_url,omitempty"`
	Detail              string          `json:"detail,omitempty"`

	CompactionSupported bool   `json:"compaction_supported"`
	CompactionInstalled bool   `json:"compaction_installed"`
	CompactionDetail    string `json:"compaction_detail,omitempty"`

	BinaryPath     string `json:"binary_path,omitempty"`
	BinarySource   string `json:"binary_source,omitempty"`
	WrapperPath    string `json:"wrapper_path,omitempty"`
	BinaryResolved bool   `json:"binary_resolved"`

	Surfaces       []registry.SurfaceStatus `json:"surfaces,omitempty"`
	LastPrime      *PrimeSummary            `json:"last_prime,omitempty"`
	PrimeCounts24h map[string]int           `json:"prime_counts_24h,omitempty"`
}

// PrimeSummary is the last observed prime attempt for a harness.
type PrimeSummary struct {
	At         string `json:"at,omitempty"`
	Status     string `json:"status,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Source     string `json:"source,omitempty"`
}

// Result is the outcome of an install or uninstall.
type Result struct {
	Harness             string   `json:"harness"`
	Path                string   `json:"path,omitempty"`
	Installed           bool     `json:"installed"`
	Configured          bool     `json:"configured,omitempty"`
	Trusted             bool     `json:"trusted"`
	TrustRequired       bool     `json:"trust_required,omitempty"`
	ProjectHooksIgnored bool     `json:"project_hooks_ignored,omitempty"`
	Skipped             bool     `json:"skipped"`
	Refused             bool     `json:"refused"`
	Files               []string `json:"files,omitempty"`
	Plan                []string `json:"plan,omitempty"`
	Detail              string   `json:"detail,omitempty"`
}

// Normalize maps aliases to a canonical harness name.
func Normalize(harness string) string {
	return registry.NormalizeID(harness)
}

// StatusAll reports the supported and unsupported harness matrix for projectDir.
func StatusAll(projectDir string) map[string]Status {
	raw := registry.StatusAll(projectDir)
	out := make(map[string]Status, len(raw))
	for k, st := range raw {
		out[k] = fromRegistryStatus(st)
	}
	return out
}

// StatusFor reports one harness. Unsupported harnesses are explicit, never
// silently reported as installed.
func StatusFor(harness, projectDir string) Status {
	return fromRegistryStatus(registry.StatusFor(harness, projectDir))
}

// StatusForUser reports one harness's user-scope hook installation under homeDir.
func StatusForUser(harness, homeDir string) Status {
	return fromRegistryStatus(registry.StatusForUser(harness, homeDir))
}

// Install merges Sila hooks into the harness settings file.
func Install(harness, projectDir string, dryRun, replace bool) (Result, error) {
	res, err := registry.Install(harness, projectDir, dryRun, replace)
	return fromRegistryResult(res), err
}

// InstallUser merges Sila hooks into the harness user-scope settings file.
func InstallUser(harness, homeDir string, dryRun, replace bool) (Result, error) {
	res, err := registry.InstallUser(harness, homeDir, dryRun, replace)
	return fromRegistryResult(res), err
}

// InstallUserSurface installs a selected Antigravity surface (all by default).
func InstallUserSurface(harness, homeDir, surface string, dryRun, replace bool) (Result, error) {
	res, err := registry.InstallUserSurface(harness, homeDir, surface, dryRun, replace)
	return fromRegistryResult(res), err
}

// Uninstall removes only Sila-owned hooks from the harness settings file.
func Uninstall(harness, projectDir string, dryRun bool) (Result, error) {
	res, err := registry.Uninstall(harness, projectDir, dryRun)
	return fromRegistryResult(res), err
}

// UninstallUser removes only Sila-owned hooks from the user-scope settings file.
func UninstallUser(harness, homeDir string, dryRun bool) (Result, error) {
	res, err := registry.UninstallUser(harness, homeDir, dryRun)
	return fromRegistryResult(res), err
}

func fromRegistryStatus(st registry.Status) Status {
	return Status{
		Harness:             st.Harness,
		DisplayName:         st.DisplayName,
		Kind:                string(st.Kind),
		Path:                st.Path,
		Scope:               st.Scope,
		Supported:           st.Supported,
		Installed:           st.Installed,
		Configured:          st.Configured,
		Trusted:             st.Trusted,
		TrustMode:           st.TrustMode,
		ProjectHooksIgnored: st.ProjectHooksIgnored,
		Events:              st.Events,
		Capabilities:        st.Capabilities,
		Limitations:         st.Limitations,
		DocsURL:             st.DocsURL,
		Detail:              st.Detail,
		CompactionSupported: st.CompactionSupported,
		CompactionInstalled: st.CompactionInstalled,
		CompactionDetail:    st.CompactionDetail,
		BinaryPath:          st.BinaryPath,
		BinarySource:        st.BinarySource,
		WrapperPath:         st.WrapperPath,
		BinaryResolved:      st.BinaryResolved,
		Surfaces:            st.Surfaces,
	}
}

func fromRegistryResult(res registry.InstallResult) Result {
	return Result{
		Harness:             res.Harness,
		Path:                res.Path,
		Installed:           res.Installed,
		Configured:          res.Configured,
		Trusted:             res.Trusted,
		TrustRequired:       res.TrustRequired,
		ProjectHooksIgnored: res.ProjectHooksIgnored,
		Skipped:             res.Skipped,
		Refused:             res.Refused,
		Files:               res.Files,
		Plan:                res.Plan,
		Detail:              res.Detail,
	}
}
