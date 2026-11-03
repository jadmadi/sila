package clean

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// AubeCleaner handles aube global store pruning and workspace cache residue.
type AubeCleaner struct{}

// NewAubeCleaner creates a new aube toolchain cleaner.
func NewAubeCleaner() *AubeCleaner {
	return &AubeCleaner{}
}

func (c *AubeCleaner) Name() string {
	return "aube"
}

func (c *AubeCleaner) DisplayName() string {
	return "aube"
}

func (c *AubeCleaner) Aliases() []string {
	return nil
}

func (c *AubeCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "aube.json")); err == nil {
		return true
	}
	// If aube is installed and there's a package.json or pnpm-lock.yaml
	if CommandExists("aube") {
		if _, err := os.Stat(filepath.Join(dir, "pnpm-lock.yaml")); err == nil {
			return true
		}
	}
	return false
}

func (c *AubeCleaner) DetectGlobal() bool {
	return CommandExists("aube")
}

// AubeStorePath resolves the global store path for aube.
func AubeStorePath(ctx context.Context) string {
	if CommandExists("aube") {
		out, err := RunCmd(ctx, "", "aube", "store", "path")
		if err == nil {
			p := strings.TrimSpace(out)
			if p != "" {
				return p
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "aube", "store", "v1")
}

func (c *AubeCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	// 1. Global Store
	if scope == ScopeGlobal || scope == ScopeAll {
		storePath := AubeStorePath(ctx)
		if storePath != "" {
			if size := PathSize(storePath); size > 0 {
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "aube Global Package Store",
					Category:    CategoryGlobalStore,
					Path:        storePath,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Global content-addressable package store (prunes unreferenced packages)",
					Command:     "aube store prune",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						if CommandExists("aube") {
							home, _ := os.UserHomeDir()
							out, err := RunCmd(ctx, home, "aube", "store", "prune")
							if err != nil {
								if IsLockError(err, out) {
									return &LockError{
										Toolchain: c.DisplayName(),
										ItemName:  "aube Global Package Store",
										Output:    out,
										Err:       err,
									}
								}
								return err
							}
							if IsLockError(nil, out) {
								return &LockError{
									Toolchain: c.DisplayName(),
									ItemName:  "aube Global Package Store",
									Output:    out,
								}
							}
							return nil
						}
						return nil
					},
				})
			}
		}
	}

	// 2. Repository Local Residue
	if (scope == ScopeRepo || scope == ScopeAll) && dir != "" {
		cachePaths := []string{
			filepath.Join(dir, ".aube", "cache"),
			filepath.Join(dir, "aube-diag.jsonl"),
		}
		for _, cp := range cachePaths {
			if _, err := os.Stat(cp); err == nil {
				size := PathSize(cp)
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "aube Local Cache (" + filepath.Base(cp) + ")",
					Category:    CategoryRepoResidue,
					Path:        cp,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Project-level aube diagnostic or cache directory",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						_, err := RemovePathSafe(cp, dir)
						if err != nil && IsLockError(err, "") {
							return &LockError{
								Toolchain: c.DisplayName(),
								ItemName:  "aube Local Cache (" + filepath.Base(cp) + ")",
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
					Description: "Installed Node dependencies (requires aube install to restore)",
					Command:     "aube clean",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						if CommandExists("aube") {
							out, err := RunCmd(ctx, dir, "aube", "clean")
							if err != nil && IsLockError(err, out) {
								return &LockError{
									Toolchain: c.DisplayName(),
									ItemName:  "Project Dependencies (node_modules/)",
									Output:    out,
									Err:       err,
								}
							}
							if err == nil {
								return nil
							}
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

func (c *AubeCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
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
