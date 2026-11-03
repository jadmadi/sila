package clean

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// NpmCleaner handles npm global cache and workspace debug log residue.
type NpmCleaner struct{}

func NewNpmCleaner() *NpmCleaner {
	return &NpmCleaner{}
}

func (c *NpmCleaner) Name() string {
	return "npm"
}

func (c *NpmCleaner) DisplayName() string {
	return "npm"
}

func (c *NpmCleaner) Aliases() []string {
	return nil
}

func (c *NpmCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err == nil {
		return true
	}
	return false
}

func (c *NpmCleaner) DetectGlobal() bool {
	return CommandExists("npm")
}

func (c *NpmCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	home, _ := os.UserHomeDir()

	// 1. Global Cache
	if (scope == ScopeGlobal || scope == ScopeAll) && home != "" {
		npmCache := filepath.Join(home, ".npm", "_cacache")
		if size := PathSize(npmCache); size > 0 {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "npm Content Cache",
				Category:    CategoryGlobalCache,
				Path:        npmCache,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "HTTP and tarball download cache for npm",
				Command:     "npm cache clean --force",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					if CommandExists("npm") {
						out, err := RunCmd(ctx, "", "npm", "cache", "clean", "--force")
						if err != nil && IsLockError(err, out) {
							return &LockError{
								Toolchain: c.DisplayName(),
								ItemName:  "npm Content Cache",
								Output:    out,
								Err:       err,
							}
						}
					}
					if _, err := os.Stat(npmCache); err == nil {
						_, err := RemovePathSafe(npmCache, home)
						if err != nil && IsLockError(err, "") {
							return &LockError{
								Toolchain: c.DisplayName(),
								ItemName:  "npm Content Cache",
								Err:       err,
							}
						}
						return err
					}
					return nil
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
				Name:        "npm node_modules/.cache",
				Category:    CategoryRepoResidue,
				Path:        cacheDir,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Intermediate Babel/Webpack/Jest cache inside node_modules",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					_, err := RemovePathSafe(cacheDir, dir)
					return err
				},
			})
		}

		// Clean npm debug logs
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasPrefix(e.Name(), "npm-debug.log") {
					p := filepath.Join(dir, e.Name())
					info, err := e.Info()
					var size int64
					if err == nil {
						size = info.Size()
					}
					rep.Items = append(rep.Items, CleanItem{
						Toolchain:   c.Name(),
						Name:        "npm Debug Log (" + e.Name() + ")",
						Category:    CategoryRepoResidue,
						Path:        p,
						Bytes:       size,
						Tier:        TierSafe,
						Description: "Old npm execution failure log",
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

func (c *NpmCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
	rep, err := c.Inspect(ctx, dir, scope, heavy)
	if err != nil {
		return nil, err
	}
	res := &Result{Toolchain: c.Name(), DisplayName: c.DisplayName(), DryRun: dryRun}
	for _, item := range rep.Items {
		ir := ItemResult{Item: item}
		if item.Action != nil {
			var beforeSize int64
			if !dryRun {
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
				} else {
					afterSize := PathSize(item.Path)
					diff := beforeSize - afterSize
					if diff > 0 {
						ir.ReclaimedBytes = diff
						res.TotalReclaimed += diff
					} else {
						ir.ReclaimedBytes = 0
					}
				}
			}
		}
		res.Results = append(res.Results, ir)
	}
	return res, nil
}
