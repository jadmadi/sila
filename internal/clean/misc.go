package clean

import (
	"context"
	"os"
	"path/filepath"
)

// ZigCleaner handles Zig build cache and local artifact directories.
type ZigCleaner struct{}

func NewZigCleaner() *ZigCleaner {
	return &ZigCleaner{}
}

func (c *ZigCleaner) Name() string {
	return "zig"
}

func (c *ZigCleaner) DisplayName() string {
	return "Zig"
}

func (c *ZigCleaner) Aliases() []string {
	return nil
}

func (c *ZigCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "build.zig")); err == nil {
		return true
	}
	return false
}

func (c *ZigCleaner) DetectGlobal() bool {
	return CommandExists("zig")
}

func (c *ZigCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	home, _ := os.UserHomeDir()

	// 1. Global Cache
	if (scope == ScopeGlobal || scope == ScopeAll) && home != "" {
		zigCache := filepath.Join(home, ".cache", "zig")
		if size := PathSize(zigCache); size > 0 {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "Zig Global Cache",
				Category:    CategoryGlobalCache,
				Path:        zigCache,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Global Zig compiler intermediate cache",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					_, err := RemovePathSafe(zigCache, home)
					return err
				},
			})
		}
	}

	// 2. Repo Artifacts
	if (scope == ScopeRepo || scope == ScopeAll) && dir != "" {
		for _, name := range []string{"zig-cache", "zig-out"} {
			p := filepath.Join(dir, name)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				size := PathSize(p)
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "Zig Build Output (" + name + "/)",
					Category:    CategoryRepoResidue,
					Path:        p,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Local Zig compilation artifacts and build output",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						_, err := RemovePathSafe(p, dir)
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

func (c *ZigCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
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

// MiseCleaner handles mise toolchain download and metadata cache.
type MiseCleaner struct{}

func NewMiseCleaner() *MiseCleaner {
	return &MiseCleaner{}
}

func (c *MiseCleaner) Name() string {
	return "mise"
}

func (c *MiseCleaner) DisplayName() string {
	return "mise"
}

func (c *MiseCleaner) Aliases() []string {
	return nil
}

func (c *MiseCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	for _, f := range []string{"mise.toml", ".mise.toml", ".mise.local.toml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

func (c *MiseCleaner) DetectGlobal() bool {
	return CommandExists("mise")
}

func (c *MiseCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	home, _ := os.UserHomeDir()
	if (scope == ScopeGlobal || scope == ScopeAll) && home != "" {
		miseCache := filepath.Join(home, ".local", "share", "mise", "cache")
		if size := PathSize(miseCache); size > 0 {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "mise Toolchain Cache",
				Category:    CategoryGlobalCache,
				Path:        miseCache,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Cached tool downloads and tarballs managed by mise",
				Command:     "mise cache clean",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					if CommandExists("mise") {
						_, err := RunCmd(ctx, "", "mise", "cache", "clean")
						if err == nil {
							return nil
						}
					}
					_, err := RemovePathSafe(miseCache, home)
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

func (c *MiseCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
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
