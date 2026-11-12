package store

import (
	"encoding/json"
	"errors"

	"github.com/jadmadi/sila/internal/config"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RubbishSweepOptions controls a workspace .sila rubbish sweep.
type RubbishSweepOptions struct {
	CWD     string
	HomeDir string
	TempDir string
	DryRun  bool
	Now     time.Time
}

// RubbishSweepReport summarizes one sweep.
type RubbishSweepReport struct {
	FoundWorkspaceDBs       int
	RemovedWorkspaceDBs     int
	OrphanSidecarsRemoved   int
	StaleLocksRemoved       int
	NonRepoStateDirsRemoved int
	CanaryDirsRemoved       int
	EmptyDirsRemoved        int
	Drained                 DrainStats
	ReclaimedBytes          int64
	Paths                   []string
}

type workspaceFlock struct {
	file *os.File
	held bool
}

func lockWorkspaceStore(dbPath string) (*workspaceFlock, bool, error) {
	file, err := os.OpenFile(dbPath+".sync.flock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := tryFlockExclusive(file); err != nil {
		if errors.Is(err, errFlockBusy) {
			return &workspaceFlock{file: file, held: false}, false, nil
		}
		file.Close()
		return nil, false, err
	}
	return &workspaceFlock{file: file, held: true}, true, nil
}

func releaseWorkspaceLock(lock *workspaceFlock) {
	if lock == nil || lock.file == nil {
		return
	}
	if lock.held {
		_ = unlockFlock(lock.file)
	}
	_ = lock.file.Close()
}

func (l *workspaceFlock) release() {
	releaseWorkspaceLock(l)
}

// DrainAndRemoveWorkspaceStore drains a workspace store and removes it when no
// live writer holds its .sync.flock.
func (s *Store) DrainAndRemoveWorkspaceStore(wsDBPath string) (DrainStats, bool, error) {
	lock, held, err := lockWorkspaceStore(wsDBPath)
	if err != nil {
		return DrainStats{}, false, err
	}
	defer lock.release()
	stats, err := s.DrainWorkspaceStore(wsDBPath)
	if err != nil {
		return stats, false, err
	}
	if !held {
		return stats, false, nil
	}
	for _, path := range []string{wsDBPath, wsDBPath + "-wal", wsDBPath + "-shm", wsDBPath + ".sync.flock"} {
		_ = os.Remove(path)
	}
	if probes, globErr := filepath.Glob(filepath.Join(filepath.Dir(wsDBPath), ".write_probe_*")); globErr == nil {
		for _, probe := range probes {
			_ = os.Remove(probe)
		}
	}
	return stats, true, nil
}

// SweepWorkspaceRubbish removes stray workspace stores, orphan sidecars,
// non-repository state, dead locks, stale canaries, and empty .sila dirs.
func (s *Store) SweepWorkspaceRubbish(opts RubbishSweepOptions) (RubbishSweepReport, error) {
	var report RubbishSweepReport
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cwd := strings.TrimSpace(opts.CWD)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	home := strings.TrimSpace(opts.HomeDir)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	temp := strings.TrimSpace(opts.TempDir)
	if temp == "" {
		temp = os.TempDir()
	}
	seen := map[string]bool{}
	var silaDirs []string
	addSila := func(dir string) {
		if dir == "" {
			return
		}
		clean := filepath.Clean(dir)
		if seen[clean] {
			return
		}
		seen[clean] = true
		silaDirs = append(silaDirs, clean)
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		addSila(filepath.Join(dir, ".sila"))
		parent := filepath.Dir(dir)
		if parent == dir || parent == "" {
			break
		}
	}
	if projects, err := s.Projects(); err == nil {
		for _, project := range projects {
			if project.Path == "" {
				continue
			}
			base := project.Path
			addSila(filepath.Join(base, ".sila"))
			addSila(filepath.Join(filepath.Dir(base), ".sila"))
			addSila(filepath.Join(filepath.Dir(filepath.Dir(base)), ".sila"))
		}
	}
	if home != "" {
		addSila(filepath.Join(home, ".sila"))
		for _, sub := range []string{"Downloads", ".config", ".gemini", ".agents", "tmp", "temp", "scripts"} {
			root := filepath.Join(home, sub)
			_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil || !entry.IsDir() {
					return nil
				}
				if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == ".cache" {
					return filepath.SkipDir
				}
				if strings.HasSuffix(filepath.ToSlash(path), "/.local/share/sila") {
					return filepath.SkipDir
				}
				rel, relErr := filepath.Rel(root, path)
				if relErr == nil && strings.Count(filepath.ToSlash(rel), "/") >= 3 {
					return filepath.SkipDir
				}
				if entry.Name() == ".sila" {
					addSila(path)
					return filepath.SkipDir
				}
				return nil
			})
		}
	}
	sort.Strings(silaDirs)
	primarySila := filepath.Dir(s.Path)
	for _, silaDir := range silaDirs {
		if silaDir == primarySila {
			continue
		}
		dbPath := filepath.Join(silaDir, "sila.db")
		if info, statErr := os.Stat(dbPath); statErr == nil {
			report.FoundWorkspaceDBs++
			size := info.Size()
			if opts.DryRun {
				report.ReclaimedBytes += size
				report.Paths = append(report.Paths, dbPath)
			} else {
				stats, removed, drainErr := s.DrainAndRemoveWorkspaceStore(dbPath)
				report.Drained.Messages += stats.Messages
				report.Drained.Facts += stats.Facts
				report.Drained.Handoffs += stats.Handoffs
				report.Drained.Decisions += stats.Decisions
				report.Drained.Feedback += stats.Feedback
				report.Drained.MemoryRecords += stats.MemoryRecords
				report.Drained.MemoryLifecycle += stats.MemoryLifecycle
				if drainErr == nil && removed {
					report.RemovedWorkspaceDBs++
					report.ReclaimedBytes += size
					report.Paths = append(report.Paths, dbPath)
				}
			}
		}
		for _, sidecar := range []string{dbPath + "-wal", dbPath + "-shm", dbPath + ".sync.flock"} {
			if info, statErr := os.Stat(sidecar); statErr == nil {
				if opts.DryRun {
					report.ReclaimedBytes += info.Size()
					continue
				}
				if os.Remove(sidecar) == nil {
					report.OrphanSidecarsRemoved++
					report.ReclaimedBytes += info.Size()
				}
			}
		}
		if !opts.DryRun {
			cleaned, removed := sweepSilaState(silaDir, now)
			report.StaleLocksRemoved += cleaned
			report.NonRepoStateDirsRemoved += removed
		}
	}
	if temp != "" {
		if canaries, globErr := filepath.Glob(filepath.Join(temp, "sila-prime-canary-*")); globErr == nil {
			for _, canary := range canaries {
				info, statErr := os.Stat(canary)
				if statErr != nil || now.Sub(info.ModTime()) < time.Hour {
					continue
				}
				if opts.DryRun {
					report.CanaryDirsRemoved++
					continue
				}
				if os.RemoveAll(canary) == nil {
					report.CanaryDirsRemoved++
				}
			}
		}
	}
	if !opts.DryRun {
		for i := len(silaDirs) - 1; i >= 0; i-- {
			if silaDirs[i] == primarySila {
				continue
			}
			if empty, _ := dirIsEmpty(silaDirs[i]); empty {
				if os.Remove(silaDirs[i]) == nil {
					report.EmptyDirsRemoved++
				}
			}
		}
	}
	return report, nil
}

func sweepSilaState(silaDir string, now time.Time) (staleLocks, nonRepoState int) {
	parent := filepath.Dir(silaDir)
	if config.IsForbiddenLocalStateDir(parent) {
		stateDir := filepath.Join(silaDir, "state")
		if info, statErr := os.Stat(stateDir); statErr == nil && info.IsDir() {
			if err := os.RemoveAll(stateDir); err == nil {
				nonRepoState++
			}
		}
	}
	for _, pattern := range []string{"locks/*.json", "locks/*.lock", "charters/*.json", "state/charters/*.json"} {
		matches, _ := filepath.Glob(filepath.Join(silaDir, filepath.FromSlash(pattern)))
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || now.Sub(info.ModTime()) < 24*time.Hour {
				continue
			}
			pid := readLockPID(match)
			if pid > 0 && processAlive(pid) {
				continue
			}
			if os.Remove(match) == nil {
				staleLocks++
			}
		}
	}
	return staleLocks, nonRepoState
}

func readLockPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var payload struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal(data, &payload) == nil {
		return payload.PID
	}
	return 0
}

func dirIsEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
