package clean

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// GoCleaner handles Go build/test cache and repository test/profile residue.
type GoCleaner struct{}

// NewGoCleaner creates a new Go toolchain cleaner.
func NewGoCleaner() *GoCleaner {
	return &GoCleaner{}
}

func (c *GoCleaner) Name() string {
	return "go"
}

func (c *GoCleaner) DisplayName() string {
	return "Go"
}

func (c *GoCleaner) Aliases() []string {
	return []string{"golang"}
}

func (c *GoCleaner) DetectWorkspace(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
		return true
	}
	// Check for top-level .go files
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				return true
			}
		}
	}
	return false
}

func (c *GoCleaner) DetectGlobal() bool {
	return CommandExists("go")
}

// GoCachePath returns the system GOCACHE directory path.
func GoCachePath() string {
	if val := os.Getenv("GOCACHE"); val != "" {
		return val
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", "go-build")
	case "windows":
		if appData := os.Getenv("LocalAppData"); appData != "" {
			return filepath.Join(appData, "go-build")
		}
		return filepath.Join(home, "AppData", "Local", "go-build")
	default:
		// Linux and Unix
		if xdgCache := os.Getenv("XDG_CACHE_HOME"); xdgCache != "" {
			return filepath.Join(xdgCache, "go-build")
		}
		return filepath.Join(home, ".cache", "go-build")
	}
}

// GoModCachePath returns the system GOMODCACHE directory path.
func GoModCachePath() string {
	if val := os.Getenv("GOMODCACHE"); val != "" {
		return val
	}
	if gopath := os.Getenv("GOPATH"); gopath != "" {
		return filepath.Join(gopath, "pkg", "mod")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
}

func (c *GoCleaner) Inspect(ctx context.Context, dir string, scope Scope, heavy bool) (*Report, error) {
	rep := &Report{
		Toolchain:      c.Name(),
		DisplayName:    c.DisplayName(),
		DetectedGlobal: c.DetectGlobal(),
		DetectedRepo:   c.DetectWorkspace(dir),
	}

	// 1. Global Caches
	if scope == ScopeGlobal || scope == ScopeAll {
		gocache := GoCachePath()
		if gocache != "" {
			size := PathSize(gocache)
			if size > 0 {
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "Build & Test Cache",
					Category:    CategoryGlobalCache,
					Path:        gocache,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Compiled object files, package hashes, and test result cache",
					Command:     "go clean -cache -testcache",
					Action: func(ctx context.Context, dryRun bool) error {
						if dryRun {
							return nil
						}
						if CommandExists("go") {
							_, err := RunCmd(ctx, "", "go", "clean", "-cache", "-testcache")
							return err
						}
						home, _ := os.UserHomeDir()
						_, err := RemovePathSafe(gocache, home)
						return err
					},
				})
			}
		}

		if heavy {
			modcache := GoModCachePath()
			if modcache != "" {
				size := PathSize(modcache)
				if size > 0 {
					rep.Items = append(rep.Items, CleanItem{
						Toolchain:   c.Name(),
						Name:        "Downloaded Module Cache",
						Category:    CategoryGlobalStore,
						Path:        modcache,
						Bytes:       size,
						Tier:        TierHeavy,
						Description: "Downloaded read-only dependency module archives",
						Command:     "go clean -modcache",
						Action: func(ctx context.Context, dryRun bool) error {
							if dryRun {
								return nil
							}
							if CommandExists("go") {
								_, err := RunCmd(ctx, "", "go", "clean", "-modcache")
								return err
							}
							home, _ := os.UserHomeDir()
							_, err := RemovePathSafe(modcache, home)
							return err
						},
					})
				}
			}
		}
	}

	// 2. Repository Local Residue
	if (scope == ScopeRepo || scope == ScopeAll) && dir != "" {
		coverageNames := []string{
			"coverage.out", "coverage.html", "coverage.txt", "cover.out", "profile.out",
			"cpu.pprof", "mem.pprof", "trace.out",
		}
		for _, name := range coverageNames {
			p := filepath.Join(dir, name)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				size := fi.Size()
				rep.Items = append(rep.Items, CleanItem{
					Toolchain:   c.Name(),
					Name:        "Test & Profile Dump (" + name + ")",
					Category:    CategoryRepoResidue,
					Path:        p,
					Bytes:       size,
					Tier:        TierSafe,
					Description: "Disposable coverage or profiling output artifact",
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

		// Check for compiled test binaries (*.test)
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".test") {
					p := filepath.Join(dir, e.Name())
					info, err := e.Info()
					var size int64
					if err == nil {
						size = info.Size()
					}
					rep.Items = append(rep.Items, CleanItem{
						Toolchain:   c.Name(),
						Name:        "Compiled Test Binary (" + e.Name() + ")",
						Category:    CategoryRepoResidue,
						Path:        p,
						Bytes:       size,
						Tier:        TierSafe,
						Description: "Compiled go test runner executable artifact",
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

func (c *GoCleaner) Clean(ctx context.Context, dir string, scope Scope, heavy bool, dryRun bool) (*Result, error) {
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
