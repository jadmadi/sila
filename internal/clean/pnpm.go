package clean

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PnpmCleaner handles pnpm global package store pruning and workspace cache residue.
type PnpmCleaner struct{}

// NewPnpmCleaner creates a new pnpm toolchain cleaner.
func NewPnpmCleaner() *PnpmCleaner {
	return &PnpmCleaner{}
}

func (c *PnpmCleaner) Name() string {
	return "pnpm"
}

func (c *PnpmCleaner) DisplayName() string {
	return "pnpm"
}

func (c *PnpmCleaner) Aliases() []string {
	return nil
}

func (c *PnpmCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "pnpm-workspace.yaml")); err == nil {
		return true
	}
	return false
}

func (c *PnpmCleaner) DetectGlobal() bool {
	return CommandExists("pnpm")
}

// PnpmStorePath resolves the default global content-addressable store location for pnpm.
func PnpmStorePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "pnpm", "store")
	case "windows":
		if localApp := os.Getenv("LocalAppData"); localApp != "" {
			return filepath.Join(localApp, "pnpm", "store")
		}
		return filepath.Join(home, "AppData", "Local", "pnpm", "store")
	default:
		// Linux/Unix
		if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
			return filepath.Join(xdgData, "pnpm", "store")
		}
		return filepath.Join(home, ".local", "share", "pnpm", "store")
	}
}

func (c *PnpmCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	// 1. Global Store
	if scope == ScopeGlobal || scope == ScopeAll {
		storePath := PnpmStorePath()
		if storePath != "" {
			home, _ := os.UserHomeDir()
			activePath := storePath
			if CommandExists("pnpm") && home != "" {
				if out, err := RunCmd(ctx, home, "pnpm", "store", "path"); err == nil {
					p := strings.TrimSpace(out)
					if p != "" {
						activePath = p
					}
				}
			}

			// Audit active store
			if size := PathSize(activePath); size > 0 {
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "pnpm Global Package Store",
					Category:    CategoryGlobalStore,
					Path:        activePath,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Global content-addressable package store (prunes unreferenced packages)",
					Command:     "pnpm store prune",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						if CommandExists("pnpm") {
							targetDir := home
							if targetDir == "" {
								targetDir = dir
							}
							out, err := RunCmd(ctx, targetDir, "pnpm", "store", "prune")
							if err != nil {
								if IsLockError(err, out) {
									return &LockError{
										Toolchain: c.DisplayName(),
										ItemName:  "pnpm Global Package Store",
										Output:    out,
										Err:       err,
									}
								}
								return err
							}
							if IsLockError(nil, out) {
								return &LockError{
									Toolchain: c.DisplayName(),
									ItemName:  "pnpm Global Package Store",
									Output:    out,
								}
							}
							return nil
						}
						return nil
					},
				})
			}

			// Audit legacy store versions (e.g. ~/.local/share/pnpm/store/v10 when v11 is active)
			storeParent := storePath
			if entries, err := os.ReadDir(storeParent); err == nil {
				for _, entry := range entries {
					if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
						vPath := filepath.Join(storeParent, entry.Name())
						if filepath.Clean(vPath) != filepath.Clean(activePath) {
							if vSize := PathSize(vPath); vSize > 0 {
								rep.Items = append(rep.Items, CleanItem{
									Toolchain:   c.Name(),
									Name:        "pnpm Legacy Store (" + entry.Name() + ")",
									Category:    CategoryGlobalStore,
									Path:        vPath,
									Bytes:       vSize,
									Tier:        TierSafe,
									Description: fmt.Sprintf("Orphaned %s package store from previous pnpm version (safe to prune)", entry.Name()),
									Action: func(ctx context.Context, dryRun bool) error {
										if dryRun {
											return nil
										}
										_, err := RemovePathSafe(vPath, storeParent)
										if err != nil && IsLockError(err, "") {
											return &LockError{
												Toolchain: c.DisplayName(),
												ItemName:  "pnpm Legacy Store (" + entry.Name() + ")",
												Err:       err,
											}
										}
										return err
									},
								})
							}
						}
					}
				}
			}
		}
	}

	// 2. Repository Local Residue
	if (scope == ScopeRepo || scope == ScopeAll) && dir != "" {
		cachePaths := []string{
			filepath.Join(dir, "node_modules", ".cache"),
			filepath.Join(dir, ".turbo"),
			filepath.Join(dir, ".pnpm-store"),
		}
		for _, cp := range cachePaths {
			if fi, err := os.Stat(cp); err == nil && fi.IsDir() {
				size := PathSize(cp)
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "Local Build Cache (" + filepath.Base(cp) + ")",
					Category:    CategoryRepoResidue,
					Path:        cp,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Project-level intermediate build and cache directory",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						_, err := RemovePathSafe(cp, dir)
						if err != nil && IsLockError(err, "") {
							return &LockError{
								Toolchain: c.DisplayName(),
								ItemName:  "Local Build Cache (" + filepath.Base(cp) + ")",
								Err:       err,
							}
						}
						return err
					},
				})
			}
		}

		if heavy {
			nm := filepath.Join(dir, "node_modules")
			if fi, err := os.Stat(nm); err == nil && fi.IsDir() {
				size := PathSize(nm)
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "Project Dependencies (node_modules/)",
					Category:    CategoryRepoResidue,
					Path:        nm,
					Bytes:       size,
					Tier:        TierHeavy,
					Description: "Installed Node dependencies (requires pnpm install to restore)",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						_, err := RemovePathSafe(nm, dir)
						if err != nil && IsLockError(err, "") {
							return &LockError{
								Toolchain: c.DisplayName(),
								ItemName:  "Project Dependencies (node_modules/)",
								Err:       err,
							}
						}
						return err
					},
				})
			}
		}
	}

	for _, it := range rep.Items {
		rep.TotalBytes += it.Bytes
		if it.Tier == TierSafe {
			rep.SafeBytes += it.Bytes
		} else {
			rep.HeavyBytes += it.Bytes
		}
	}

	return rep, nil
}

func (c *PnpmCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
	rep, err := c.Inspect(ctx, dir, scope, heavy)
	if err != nil {
		return nil, err
	}
	res := &Result{Toolchain: c.Name(), DisplayName: c.DisplayName(), DryRun: dryRun}
	for _, item := range rep.Items {
		ir := ItemResult{Item: item}
		if item.Action != nil {
			var beforeSize int64
			if item.Category == CategoryGlobalStore && !dryRun {
				beforeSize = PathSize(item.Path)
			}
			err := item.Action(ctx, dryRun)
			if err != nil {
				ir.Success = false
				if IsLockError(err, "") {
					ir.Locked = true
					ir.LockReason = FormatLockReason(c.DisplayName(), item.Name, err, "")
					ir.Error = ir.LockReason
				} else {
					ir.Error = err.Error()
				}
			} else {
				ir.Success = true
				if dryRun {
					ir.ReclaimedBytes = item.Bytes
					res.TotalReclaimed += item.Bytes
				} else if item.Category == CategoryGlobalStore {
					afterSize := PathSize(item.Path)
					diff := beforeSize - afterSize
					if diff > 0 {
						ir.ReclaimedBytes = diff
						res.TotalReclaimed += diff
					} else {
						ir.ReclaimedBytes = 0
					}
				} else {
					ir.ReclaimedBytes = item.Bytes
					res.TotalReclaimed += item.Bytes
				}
			}
		}
		res.Results = append(res.Results, ir)
	}
	return res, nil
}
