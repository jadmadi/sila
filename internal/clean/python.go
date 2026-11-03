package clean

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

// PythonCleaner handles uv, pip, and Python project caches.
type PythonCleaner struct{}

func NewPythonCleaner() *PythonCleaner {
	return &PythonCleaner{}
}

func (c *PythonCleaner) Name() string {
	return "python"
}

func (c *PythonCleaner) DisplayName() string {
	return "Python (uv / pip)"
}

func (c *PythonCleaner) Aliases() []string {
	return []string{"py", "uv", "pip"}
}

func (c *PythonCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	markers := []string{"pyproject.toml", "uv.lock", "requirements.txt", "setup.py", "Pipfile", ".venv"}
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			return true
		}
	}
	return false
}

func (c *PythonCleaner) DetectGlobal() bool {
	return CommandExists("uv") || CommandExists("pip") || CommandExists("python3")
}

func (c *PythonCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	home, _ := os.UserHomeDir()

	// 1. Global Caches (uv & pip)
	if (scope == ScopeGlobal || scope == ScopeAll) && home != "" {
		// uv cache
		uvCache := filepath.Join(home, ".cache", "uv")
		if size := PathSize(uvCache); size > 0 {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "uv Package & Wheel Cache",
				Category:    CategoryGlobalCache,
				Path:        uvCache,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Cached Python wheels, source distributions, and git checkouts",
				Command:     "uv cache clean",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					if CommandExists("uv") {
						_, err := RunCmd(ctx, "", "uv", "cache", "clean")
						if err == nil {
							return nil
						}
					}
					_, err := RemovePathSafe(uvCache, home)
					return err
				},
			})
		}

		// pip cache
		pipCache := filepath.Join(home, ".cache", "pip")
		if size := PathSize(pipCache); size > 0 {
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "pip HTTP & Wheel Cache",
				Category:    CategoryGlobalCache,
				Path:        pipCache,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Cached downloads and built wheels for pip",
				Command:     "pip cache purge",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					if CommandExists("pip") {
						_, err := RunCmd(ctx, "", "pip", "cache", "purge")
						if err == nil {
							return nil
						}
					}
					_, err := RemovePathSafe(pipCache, home)
					return err
				},
			})
		}
	}

	// 2. Repository Local Residue
	if (scope == ScopeRepo || scope == ScopeAll) && dir != "" {
		localCaches := []string{".pytest_cache", ".ruff_cache", ".mypy_cache", "htmlcov"}
		for _, name := range localCaches {
			p := filepath.Join(dir, name)
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				size := PathSize(p)
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "Python Linter/Test Cache (" + name + "/)",
					Category:    CategoryRepoResidue,
					Path:        p,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Disposable test runner or linter cache directory",
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

		// Single files like .coverage
		covFile := filepath.Join(dir, ".coverage")
		if fi, err := os.Stat(covFile); err == nil && !fi.IsDir() {
			size := fi.Size()
			rep.Items = append(rep.Items, CleanItem{
				Toolchain:   c.Name(),
				Name:        "Python Coverage Data (.coverage)",
				Category:    CategoryRepoResidue,
				Path:        covFile,
				Bytes:       size,
				Tier:        TierSafe,
				Description: "Raw test coverage database file",
				Action: func(ctx context.Context, dryRun bool) error {
					if dryRun {
						return nil
					}
					_, err := RemovePathSafe(covFile, dir)
					return err
				},
			})
		}

		// Find __pycache__ up to depth 3
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			// Don't traverse into .git or virtualenvs
			if d.IsDir() {
				base := d.Name()
				if base == ".git" || base == "node_modules" || base == ".venv" || base == "venv" {
					return filepath.SkipDir
				}
				if base == "__pycache__" {
					size := PathSize(p)
					rel, _ := filepath.Rel(dir, p)
					rep.Items = append(rep.Items, CleanItem{
						Toolchain:   c.Name(),
						Name:        "Python Bytecode (" + rel + "/)",
						Category:    CategoryRepoResidue,
						Path:        p,
						Bytes:       size,
						Tier:        TierSafe,
						Description: "Compiled .pyc bytecode cache",
						Action: func(ctx context.Context, dryRun bool) error {
							if dryRun {
								return nil
							}
							_, err := RemovePathSafe(p, dir)
							return err
						},
					})
					return filepath.SkipDir
				}
			}
			return nil
		})
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

func (c *PythonCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
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
