package clean

import (
	"context"
	"os"
	"path/filepath"
)

// BunCleaner handles Bun package manager cache and local residue.
type BunCleaner struct{}

func NewBunCleaner() *BunCleaner {
	return &BunCleaner{}
}

func (c *BunCleaner) Name() string {
	return "bun"
}

func (c *BunCleaner) DisplayName() string {
	return "Bun"
}

func (c *BunCleaner) Aliases() []string {
	return nil
}

func (c *BunCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "bun.lockb")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "bun.lock")); err == nil {
		return true
	}
	return false
}

func (c *BunCleaner) DetectGlobal() bool {
	return CommandExists("bun")
}

func (c *BunCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	home, _ := os.UserHomeDir()

	// 1. Global Cache
	if (scope == ScopeGlobal || scope == ScopeAll) && home != "" {
		bunCache := filepath.Join(home, ".bun", "install", "cache")
		if size := PathSize(bunCache); size > 0 {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "Bun Package Cache",
				Category:    CategoryGlobalCache,
				Path:        bunCache,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Global package download cache for Bun",
				Command:     "bun pm cache rm",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					if CommandExists("bun") {
						_, err := RunCmd(ctx, "", "bun", "pm", "cache", "rm")
						if err == nil {
							return nil
						}
					}
					_, err := RemovePathSafe(bunCache, home)
					return err
				},
			})
		}
	}

	// 2. Repository Local Residue
	if (scope == ScopeRepo || scope == ScopeAll) && dir != "" {
		cacheDir := filepath.Join(dir, "node_modules", ".cache")
		if fi, err := os.Stat(cacheDir); err == nil && fi.IsDir() {
			size := PathSize(cacheDir)
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "Bun node_modules/.cache",
				Category:    CategoryRepoResidue,
				Path:        cacheDir,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Intermediate tool cache inside node_modules",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					_, err := RemovePathSafe(cacheDir, dir)
					return err
				},
			})
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

func (c *BunCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
	rep, err := c.Inspect(ctx, dir, scope, heavy)
	if err != nil {
		return nil, err
	}
	res := &Result{Toolchain: c.Name(), DisplayName: c.DisplayName(), DryRun: dryRun}
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
