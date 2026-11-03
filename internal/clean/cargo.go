package clean

import (
	"context"
	"os"
	"path/filepath"
)

// CargoCleaner handles Rust Cargo build targets and global registry cache.
type CargoCleaner struct{}

// NewCargoCleaner creates a new Cargo toolchain cleaner.
func NewCargoCleaner() *CargoCleaner {
	return &CargoCleaner{}
}

func (c *CargoCleaner) Name() string {
	return "cargo"
}

func (c *CargoCleaner) DisplayName() string {
	return "Rust / Cargo"
}

func (c *CargoCleaner) Aliases() []string {
	return []string{"rust"}
}

func (c *CargoCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.lock")); err == nil {
		return true
	}
	return false
}

func (c *CargoCleaner) DetectGlobal() bool {
	return CommandExists("cargo")
}

func (c *CargoCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	home, _ := os.UserHomeDir()

	// 1. Global Caches
	if (scope == ScopeGlobal || scope == ScopeAll) && home != "" {
		cargoHome := os.Getenv("CARGO_HOME")
		if cargoHome == "" {
			cargoHome = filepath.Join(home, ".cargo")
		}

		regCache := filepath.Join(cargoHome, "registry", "cache")
		if size := PathSize(regCache); size > 0 {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "Cargo Registry Download Cache",
				Category:    CategoryGlobalCache,
				Path:        regCache,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Downloaded .crate archive packages",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					_, err := RemovePathSafe(regCache, cargoHome)
					return err
				},
			})
		}

		gitDb := filepath.Join(cargoHome, "git", "db")
		if size := PathSize(gitDb); size > 0 && heavy {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "Cargo Git Dependencies Cache",
				Category:    CategoryGlobalStore,
				Path:        gitDb,
				Bytes:       size,
				Tier:        TierHeavy,
				Description: "Cloned git dependency repository database",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					_, err := RemovePathSafe(gitDb, cargoHome)
					return err
				},
			})
		}
	}

	// 2. Repository Local Residue
	if (scope == ScopeRepo || scope == ScopeAll) && dir != "" {
		targetDir := filepath.Join(dir, "target")
		if fi, err := os.Stat(targetDir); err == nil && fi.IsDir() {
			size := PathSize(targetDir)
			if size > 0 {
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "Cargo Build Output (target/)",
					Category:    CategoryRepoResidue,
					Path:        targetDir,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Compiled Rust binaries, debug symbols, and incremental compilation cache",
					Command:     "cargo clean",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						if CommandExists("cargo") {
							_, err := RunCmd(ctx, dir, "cargo", "clean")
							if err == nil {
								return nil
							}
						}
						_, err := RemovePathSafe(targetDir, dir)
						return err
					},
				})
			}
		}
	}

	// Calculate totals
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

func (c *CargoCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
	rep, err := c.Inspect(ctx, dir, scope, heavy)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Toolchain:   c.Name(),
		DisplayName: c.DisplayName(),
		DryRun:      dryRun,
	}

	for _, item := range rep.Items {
		ir := ItemResult{Item: item}
		if item.Action != nil {
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
				ir.ReclaimedBytes = item.Bytes
				res.TotalReclaimed += item.Bytes
			}
		}
		res.Results = append(res.Results, ir)
	}

	return res, nil
}
