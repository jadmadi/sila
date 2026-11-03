package clean

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Scope defines the boundary of the cleanup operation.
type Scope string

const (
	// ScopeRepo only audits and cleans active repository artifacts (coverage, pprof, build targets).
	ScopeRepo Scope = "repo"
	// ScopeGlobal only audits and cleans user/machine caches (compiler caches, package stores).
	ScopeGlobal Scope = "global"
	// ScopeAll audits and cleans both repository artifacts and global caches.
	ScopeAll Scope = "all"
)

// SafetyTier defines how safe the clean operation is.
type SafetyTier string

const (
	// TierSafe items are recompilable or unreferenced (compiler build caches, test caches, unreferenced store packages).
	// Zero network refetch is needed for subsequent builds.
	TierSafe SafetyTier = "safe"
	// TierHeavy items require downloading source dependencies again over the network (modcache, node_modules).
	// Strictly opt-in via flags.
	TierHeavy SafetyTier = "heavy"
)

// CleanCategory describes the nature of cleanable storage.
type CleanCategory string

const (
	CategoryGlobalCache CleanCategory = "global_cache"
	CategoryGlobalStore CleanCategory = "global_store"
	CategoryRepoResidue CleanCategory = "repo_residue"
)

// CleanItem is a single cleanable target identified by a cleaner.
type CleanItem struct {
	Toolchain   string                                       `json:"toolchain"`
	Name        string                                       `json:"name"`
	Category    CleanCategory                                `json:"category"`
	Path        string                                       `json:"path"`
	Bytes       int64                                        `json:"bytes"`
	Tier        SafetyTier                                   `json:"tier"`
	Description string                                       `json:"description"`
	Command     string                                       `json:"command,omitempty"`
	Action      func(ctx context.Context, dryRun bool) error `json:"-"`
}

// Report captures the inspection results for one toolchain cleaner.
type Report struct {
	Toolchain      string      `json:"toolchain"`
	DisplayName    string      `json:"display_name"`
	Items          []CleanItem `json:"items"`
	TotalBytes     int64       `json:"total_bytes"`
	SafeBytes      int64       `json:"safe_bytes"`
	HeavyBytes     int64       `json:"heavy_bytes"`
	DetectedGlobal bool        `json:"detected_global"`
	DetectedRepo   bool        `json:"detected_repo"`
}

// ItemResult records the outcome of cleaning a single CleanItem.
type ItemResult struct {
	Item           CleanItem `json:"item"`
	Success        bool      `json:"success"`
	ReclaimedBytes int64     `json:"reclaimed_bytes"`
	Error          string    `json:"error,omitempty"`
	Locked         bool      `json:"locked,omitempty"`
	LockReason     string    `json:"lock_reason,omitempty"`
}

// Result captures the execution results for one toolchain cleaner.
type Result struct {
	Toolchain      string       `json:"toolchain"`
	DisplayName    string       `json:"display_name"`
	Results        []ItemResult `json:"results"`
	TotalReclaimed int64        `json:"total_reclaimed_bytes"`
	DryRun         bool         `json:"dry_run"`
}

// ToolchainCleaner is the pluggable contract implemented by each supported toolchain.
type ToolchainCleaner interface {
	Name() string
	DisplayName() string
	Aliases() []string
	DetectWorkspace(dir string) bool
	DetectGlobal() bool
	Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error)
	Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error)
}

// Registry manages the set of available toolchain cleaners.
type Registry struct {
	mu       sync.RWMutex
	cleaners []ToolchainCleaner
}

// NewRegistry creates a new registry instance.
func NewRegistry() *Registry {
	return &Registry{
		cleaners: make([]ToolchainCleaner, 0),
	}
}

// DefaultRegistry returns a Registry pre-populated with all standard toolchain cleaners.
func DefaultRegistry() *Registry {
	reg := NewRegistry()
	reg.Register(NewGoCleaner())
	reg.Register(NewPnpmCleaner())
	reg.Register(NewAubeCleaner())
	reg.Register(NewNpmCleaner())
	reg.Register(NewCargoCleaner())
	reg.Register(NewPythonCleaner())
	reg.Register(NewBunCleaner())
	reg.Register(NewZigCleaner())
	reg.Register(NewMiseCleaner())
	return reg
}

// Register adds a toolchain cleaner to the registry.
func (r *Registry) Register(c ToolchainCleaner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleaners = append(r.cleaners, c)
}

// All returns all registered cleaners.
func (r *Registry) All() []ToolchainCleaner {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ToolchainCleaner, len(r.cleaners))
	copy(out, r.cleaners)
	return out
}

// Select resolves target names/aliases against registered cleaners.
// If targets is empty, it returns all cleaners that are detected either in the workspace or globally.
func (r *Registry) Select(dir string, targets []string) ([]ToolchainCleaner, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(targets) == 0 {
		var selected []ToolchainCleaner
		for _, c := range r.cleaners {
			if c.DetectWorkspace(dir) || c.DetectGlobal() {
				selected = append(selected, c)
			}
		}
		return selected, nil
	}

	selectedMap := make(map[string]ToolchainCleaner)
	for _, target := range targets {
		norm := strings.ToLower(strings.TrimSpace(target))
		if norm == "" {
			continue
		}

		matched := false
		for _, c := range r.cleaners {
			if strings.EqualFold(c.Name(), norm) {
				selectedMap[c.Name()] = c
				matched = true
				break
			}
			for _, alias := range c.Aliases() {
				if strings.EqualFold(alias, norm) {
					selectedMap[c.Name()] = c
					matched = true
					break
				}
			}
		}

		if !matched {
			return nil, fmt.Errorf("unknown toolchain target: %q (available: %s)", target, r.availableList())
		}
	}

	var selected []ToolchainCleaner
	for _, c := range r.cleaners {
		if _, ok := selectedMap[c.Name()]; ok {
			selected = append(selected, c)
		}
	}
	return selected, nil
}

func (r *Registry) availableList() string {
	names := make([]string, 0, len(r.cleaners))
	for _, c := range r.cleaners {
		names = append(names, c.Name())
	}
	return strings.Join(names, ", ")
}

// InspectAll runs inspection across the selected cleaners in parallel.
func (r *Registry) InspectAll(ctx context.Context, dir string, targets []string, scope Scope, heavy bool) ([]*Report, error) {
	cleaners, err := r.Select(dir, targets)
	if err != nil {
		return nil, err
	}

	type inspectRes struct {
		rep *Report
		err error
	}

	resChan := make(chan inspectRes, len(cleaners))
	var wg sync.WaitGroup

	for _, c := range cleaners {
		wg.Add(1)
		go func(cl ToolchainCleaner) {
			defer wg.Done()
			rep, err := cl.Inspect(ctx, dir, scope, heavy)
			resChan <- inspectRes{rep: rep, err: err}
		}(c)
	}

	wg.Wait()
	close(resChan)

	var reports []*Report
	for res := range resChan {
		if res.err != nil {
			return nil, res.err
		}
		if res.rep != nil {
			reports = append(reports, res.rep)
		}
	}

	return reports, nil
}

// CleanAll executes cleanup across the selected cleaners.
func (r *Registry) CleanAll(ctx context.Context, dir string, targets []string, scope Scope, heavy bool, dryRun bool) ([]*Result, error) {
	cleaners, err := r.Select(dir, targets)
	if err != nil {
		return nil, err
	}

	var results []*Result
	for _, c := range cleaners {
		res, err := c.Clean(ctx, dir, scope, heavy, dryRun)
		if err != nil {
			return nil, fmt.Errorf("%s clean: %w", c.DisplayName(), err)
		}
		if res != nil {
			results = append(results, res)
		}
	}

	return results, nil
}

// File and Directory Helpers

// PathSize returns total bytes of a file or directory tree, ignoring broken symlinks.
func PathSize(path string) int64 {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if !fi.IsDir() {
		return fi.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// RemovePathSafe verifies path safety and removes the file or directory.
func RemovePathSafe(target string, allowedRoot string) (int64, error) {
	cleanTarget := filepath.Clean(target)
	cleanRoot := filepath.Clean(allowedRoot)

	// Invariant: cleanTarget must be strictly within cleanRoot or equal to it (if cleanRoot is a directory under user home)
	if !strings.HasPrefix(cleanTarget, cleanRoot) {
		return 0, fmt.Errorf("safety violation: target %q is outside allowed boundary %q", target, allowedRoot)
	}

	// Double-check target is not a system root or dangerous top-level path
	if cleanTarget == "/" || cleanTarget == "." || cleanTarget == ".." {
		return 0, fmt.Errorf("safety violation: cannot delete root or relative parent %q", target)
	}

	home, _ := os.UserHomeDir()
	if home != "" && cleanTarget == filepath.Clean(home) {
		return 0, fmt.Errorf("safety violation: cannot delete user home directory")
	}

	bytes := PathSize(cleanTarget)
	err := os.RemoveAll(cleanTarget)
	if err != nil {
		return 0, err
	}
	return bytes, nil
}

// CommandExists checks if an executable exists on PATH.
func CommandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// RunCmd runs an external command with context and optional working directory.
func RunCmd(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s failed: %w (output: %s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// LockError indicates that an operation was skipped or prevented because a toolchain
// cache or package store is locked by an active process.
type LockError struct {
	Toolchain string
	ItemName  string
	Output    string
	Err       error
}

func (e *LockError) Error() string {
	msg := ""
	if e.Output != "" {
		msg = strings.TrimSpace(e.Output)
	} else if e.Err != nil {
		msg = e.Err.Error()
	}
	name := e.Toolchain
	if e.ItemName != "" {
		name = fmt.Sprintf("%s (%s)", e.Toolchain, e.ItemName)
	}
	if msg != "" {
		return fmt.Sprintf("%s is locked by an active process: %s", name, msg)
	}
	return fmt.Sprintf("%s is locked by an active process", name)
}

// IsLockError checks if an error or command output indicates a lock or active process contention.
func IsLockError(err error, output string) bool {
	if err != nil {
		if _, ok := err.(*LockError); ok {
			return true
		}
	}
	text := ""
	if err != nil {
		text += " " + err.Error()
	}
	if output != "" {
		text += " " + output
	}
	if text == "" {
		return false
	}
	text = strings.ToLower(text)
	lockSignatures := []string{
		"is locked by another process",
		"the store is locked",
		"store is locked",
		"database is locked",
		"database table is locked",
		"resource busy",
		"ebusy",
		"etxtbsy",
		"text file busy",
		"lock held",
		"waiting for file lock",
		"failed to acquire cache lock",
		"failed to acquire lock",
		"timed out waiting for lock",
		"already locked",
		"err_pnpm_lockfile",
		"another process is using",
		"another instance is running",
		"package cache lock",
		"cannot acquire lock",
		"waiting for lock",
	}
	for _, sig := range lockSignatures {
		if strings.Contains(text, sig) {
			return true
		}
	}
	return false
}

// FormatLockReason creates a clear user-facing message explaining the active lock.
func FormatLockReason(toolchain string, itemName string, err error, output string) string {
	name := toolchain
	if itemName != "" {
		name = fmt.Sprintf("%s (%s)", toolchain, itemName)
	}
	return fmt.Sprintf("%s is currently locked by an active process (someone or another task is working and using it). When that process is over, you can clean it.", name)
}
