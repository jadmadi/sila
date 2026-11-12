//go:build linux

// Command resguard protects a machine during long autonomous agent runs.
//
// It keeps Go build temporaries off tmpfs, serializes heavy builds with a file
// lock, watches disk and memory while a command runs, and stops the command
// before the host runs out of space or memory.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	tmpfsMagic = 0x01021994
	lockName   = "heavy.lock"
	maxRuns    = 10
	minRootGB  = 5.0
)

type limits struct {
	MinDiskGB float64
	MinMemGB  float64
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "status":
		err = statusCmd(os.Args[2:])
	case "preflight":
		err = preflightCmd(os.Args[2:])
	case "clean":
		err = cleanCmd(os.Args[2:])
	case "run":
		err = runCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		var ec exitCodeError
		if errors.As(err, &ec) {
			os.Exit(ec.code)
		}
		fmt.Fprintln(os.Stderr, "resguard:", err)
		os.Exit(1)
	}
}

type exitCodeError struct{ code int }

func (e exitCodeError) Error() string { return fmt.Sprintf("exit code %d", e.code) }

func usage() {
	fmt.Fprint(os.Stderr, `resguard — bound disk and memory during autonomous Go runs

Usage:
  resguard status [--temp-root=DIR]
  resguard preflight [--temp-root=DIR] [--min-disk-gb=8] [--min-mem-gb=2]
  resguard clean [--temp-root=DIR] [--age=2h] [--cache]
  resguard run [flags] -- command [args...]

run flags:
  --temp-root=DIR     disk-backed temp root (default $HOME/.cache/sila-run)
  --min-disk-gb=8     abort when the temp filesystem has less free space
  --min-mem-gb=2      abort when MemAvailable drops below this
  --max-parallel=2    adds -p=N to GOFLAGS for go commands
  --wait=30m          how long to wait for the heavy-build lock
  --keep              keep the run directory even on success
`)
}

func defaultTempRoot() string {
	if v := os.Getenv("RESGUARD_TEMP_ROOT"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".sila-run"
	}
	return filepath.Join(home, ".cache", "sila-run")
}

func statusCmd(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	tempRoot := fs.String("temp-root", defaultTempRoot(), "disk-backed temp root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return printStatus(*tempRoot)
}

func preflightCmd(args []string) error {
	fs := flag.NewFlagSet("preflight", flag.ContinueOnError)
	tempRoot := fs.String("temp-root", defaultTempRoot(), "disk-backed temp root")
	minDisk := fs.Float64("min-disk-gb", 8, "minimum free GB on the temp disk")
	minMem := fs.Float64("min-mem-gb", 2, "minimum available memory in GB")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := ensureDir(*tempRoot); err != nil {
		return err
	}
	m, err := readMemInfo()
	if err != nil {
		return err
	}
	return preflight(*tempRoot, limits{MinDiskGB: *minDisk, MinMemGB: *minMem}, m)
}

func cleanCmd(args []string) error {
	fs := flag.NewFlagSet("clean", flag.ContinueOnError)
	tempRoot := fs.String("temp-root", defaultTempRoot(), "disk-backed temp root")
	age := fs.Duration("age", 2*time.Hour, "remove entries older than this")
	cache := fs.Bool("cache", false, "also run go clean -cache")
	if err := fs.Parse(args); err != nil {
		return err
	}
	removed, err := cleanStale(*tempRoot, *age)
	if err != nil {
		return err
	}
	fmt.Printf("removed %d stale directorie(s) older than %s\n", removed, *age)
	if *cache {
		cmd := exec.Command("go", "clean", "-cache", "-testcache")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go clean: %w", err)
		}
		fmt.Println("cleared the Go build and test caches")
	}
	return nil
}

func runCmd(args []string) error {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep == len(args)-1 {
		return errors.New("run requires -- followed by a command")
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	tempRoot := fs.String("temp-root", defaultTempRoot(), "disk-backed temp root")
	minDisk := fs.Float64("min-disk-gb", 8, "minimum free GB on the temp disk")
	minMem := fs.Float64("min-mem-gb", 2, "minimum available memory in GB")
	maxParallel := fs.Int("max-parallel", 2, "value for GOFLAGS -p")
	wait := fs.Duration("wait", 30*time.Minute, "how long to wait for the heavy-build lock")
	keep := fs.Bool("keep", false, "keep the run directory on success")
	if err := fs.Parse(args[:sep]); err != nil {
		return err
	}
	command := args[sep+1:]

	if err := ensureDir(*tempRoot); err != nil {
		return err
	}
	m, err := readMemInfo()
	if err != nil {
		return err
	}
	if err := preflight(*tempRoot, limits{MinDiskGB: *minDisk, MinMemGB: *minMem}, m); err != nil {
		return err
	}

	unlock, err := acquireLock(*tempRoot, *wait, 2*time.Second)
	if err != nil {
		return err
	}
	defer unlock()

	runDir := filepath.Join(*tempRoot, fmt.Sprintf("%d-%d", time.Now().Unix(), os.Getpid()))
	goTmp := filepath.Join(runDir, "go-tmp")
	sysTmp := filepath.Join(runDir, "tmp")
	for _, d := range []string{goTmp, sysTmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = append(os.Environ(),
		"GOTMPDIR="+goTmp,
		"TMPDIR="+sysTmp,
		"GOFLAGS="+goFlags(*maxParallel),
	)
	cmd.Stdin = os.Stdin
	logPath := filepath.Join(runDir, "output.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	out := &capWriter{w: io.MultiWriter(os.Stdout, logFile), max: 128 << 20}
	errOut := &capWriter{w: io.MultiWriter(os.Stderr, logFile), max: 128 << 20}
	cmd.Stdout, cmd.Stderr = out, errOut
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	fmt.Printf("resguard: temp=%s parallel=%d\n", runDir, *maxParallel)
	if err := cmd.Start(); err != nil {
		return err
	}

	stop := make(chan string, 1)
	done := make(chan struct{})
	go watch(cmd.Process.Pid, limits{MinDiskGB: *minDisk, MinMemGB: *minMem}, *tempRoot, stop, done)

	waitErr := cmd.Wait()
	close(done)
	select {
	case reason := <-stop:
		fmt.Fprintf(os.Stderr, "resguard: aborted: %s\n", reason)
		waitErr = errAborted
	default:
	}

	pruneRuns(*tempRoot)
	if waitErr == nil && !*keep {
		_ = os.RemoveAll(runDir)
	} else {
		fmt.Fprintf(os.Stderr, "resguard: log kept at %s\n", logPath)
	}
	if waitErr == nil {
		return nil
	}
	if errors.Is(waitErr, errAborted) {
		return exitCodeError{code: 124}
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitCodeError{code: exitErr.ExitCode()}
	}
	return waitErr
}

var errAborted = errors.New("aborted by resource watchdog")

func goFlags(parallel int) string {
	existing := os.Getenv("GOFLAGS")
	if strings.Contains(existing, "-p=") || strings.Contains(existing, " -p ") {
		return existing
	}
	if existing == "" {
		return fmt.Sprintf("-p=%d", parallel)
	}
	return fmt.Sprintf("%s -p=%d", existing, parallel)
}

type capWriter struct {
	w   io.Writer
	max int64
	n   int64
}

func (c *capWriter) Write(p []byte) (int, error) {
	if c.n >= c.max {
		return len(p), nil
	}
	remain := c.max - c.n
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.n >= c.max {
		_, _ = io.WriteString(c.w, "\n[resguard] output capped\n")
	}
	return n, err
}

func watch(pid int, lim limits, tempRoot string, stop chan<- string, done <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			m, err := readMemInfo()
			if err != nil {
				continue
			}
			if reason := breach(tempRoot, lim, m); reason != "" {
				stop <- reason
				terminate(pid)
				return
			}
		}
	}
}

func terminate(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func preflight(tempRoot string, lim limits, m memInfo) error {
	freeGB, err := diskFreeGB(tempRoot)
	if err != nil {
		return err
	}
	if freeGB < lim.MinDiskGB {
		return fmt.Errorf("temp disk %s has %.1f GB free, need %.1f GB", tempRoot, freeGB, lim.MinDiskGB)
	}
	if rootGB, err := diskFreeGB("/"); err == nil && rootGB < minRootGB {
		return fmt.Errorf("root disk has %.1f GB free, need %.1f GB for the build cache", rootGB, minRootGB)
	}
	if m.AvailableGB() < lim.MinMemGB {
		return fmt.Errorf("only %.1f GB memory available, need %.1f GB", m.AvailableGB(), lim.MinMemGB)
	}
	if tmpfs, err := isTmpfs(tempRoot); err == nil && tmpfs {
		return fmt.Errorf("temp root %s is tmpfs (RAM-backed); choose a disk-backed path", tempRoot)
	}
	if used := m.SwapUsedPct(); used > 95 {
		return fmt.Errorf("swap is %.0f%% used; free swap before a long run", used)
	}
	return nil
}

func breach(tempRoot string, lim limits, m memInfo) string {
	if freeGB, err := diskFreeGB(tempRoot); err == nil && freeGB < lim.MinDiskGB {
		return fmt.Sprintf("temp disk free %.1f GB below %.1f GB", freeGB, lim.MinDiskGB)
	}
	if rootGB, err := diskFreeGB("/"); err == nil && rootGB < minRootGB {
		return fmt.Sprintf("root disk free %.1f GB below %.1f GB (build cache)", rootGB, minRootGB)
	}
	if m.AvailableGB() < lim.MinMemGB {
		return fmt.Sprintf("memory available %.1f GB below %.1f GB", m.AvailableGB(), lim.MinMemGB)
	}
	if used := m.SwapUsedPct(); used > 95 {
		return fmt.Sprintf("swap %.0f%% used", used)
	}
	return ""
}

func printStatus(tempRoot string) error {
	if err := ensureDir(tempRoot); err != nil {
		return err
	}
	m, err := readMemInfo()
	if err != nil {
		return err
	}
	freeGB, err := diskFreeGB(tempRoot)
	if err != nil {
		return err
	}
	tmpfs, _ := isTmpfs(tempRoot)
	fmt.Printf("temp root:   %s (tmpfs=%v)\n", tempRoot, tmpfs)
	fmt.Printf("temp free:   %.1f GB\n", freeGB)
	if rootGB, err := diskFreeGB("/"); err == nil {
		fmt.Printf("root free:   %.1f GB (build cache)\n", rootGB)
	}
	fmt.Printf("memory:      %.1f GB available of %.1f GB\n", m.AvailableGB(), m.TotalGB())
	fmt.Printf("swap:        %.0f%% used\n", m.SwapUsedPct())
	if warn := breach(tempRoot, limits{MinDiskGB: 8, MinMemGB: 2}, m); warn != "" {
		fmt.Printf("warning:     %s\n", warn)
	}
	return nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return os.Chmod(dir, 0o700)
}

func acquireLock(tempRoot string, wait, poll time.Duration) (func(), error) {
	path := filepath.Join(tempRoot, lockName)
	deadline := time.Now().Add(wait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			_, _ = fmt.Fprintf(f, "%d %s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		_ = f.Close()
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another heavy run holds %s", path)
		}
		time.Sleep(poll)
	}
}

func cleanStale(tempRoot string, age time.Duration) (int, error) {
	cutoff := time.Now().Add(-age)
	removed := 0
	// Leaked go-build dirs live in the system temp dir, often tmpfs.
	if entries, err := os.ReadDir(os.TempDir()); err == nil {
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "go-build") {
				continue
			}
			full := filepath.Join(os.TempDir(), e.Name())
			if info, err := os.Stat(full); err == nil && info.ModTime().Before(cutoff) {
				if err := os.RemoveAll(full); err == nil {
					removed++
				}
			}
		}
	}
	if entries, err := os.ReadDir(tempRoot); err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "go-tmp" {
				continue
			}
			full := filepath.Join(tempRoot, e.Name())
			if info, err := os.Stat(full); err == nil && info.ModTime().Before(cutoff) {
				if err := os.RemoveAll(full); err == nil {
					removed++
				}
			}
		}
	}
	return removed, nil
}

func pruneRuns(tempRoot string) {
	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(tempRoot, e.Name()))
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		a, _ := os.Stat(dirs[i])
		b, _ := os.Stat(dirs[j])
		if a == nil || b == nil {
			return dirs[i] > dirs[j]
		}
		return a.ModTime().After(b.ModTime())
	})
	for i := maxRuns; i < len(dirs); i++ {
		_ = os.RemoveAll(dirs[i])
	}
}

type memInfo struct {
	TotalKB     int64
	AvailableKB int64
	SwapTotalKB int64
	SwapFreeKB  int64
}

func (m memInfo) TotalGB() float64     { return float64(m.TotalKB) / 1024 / 1024 }
func (m memInfo) AvailableGB() float64 { return float64(m.AvailableKB) / 1024 / 1024 }

func (m memInfo) SwapUsedPct() float64 {
	if m.SwapTotalKB == 0 {
		return 0
	}
	return 100 * float64(m.SwapTotalKB-m.SwapFreeKB) / float64(m.SwapTotalKB)
}

func readMemInfo() (memInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return memInfo{}, err
	}
	defer f.Close()
	return parseMemInfo(f)
}

func parseMemInfo(r io.Reader) (memInfo, error) {
	var m memInfo
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			m.TotalKB = v
		case "MemAvailable":
			m.AvailableKB = v
		case "SwapTotal":
			m.SwapTotalKB = v
		case "SwapFree":
			m.SwapFreeKB = v
		}
	}
	if err := sc.Err(); err != nil {
		return memInfo{}, err
	}
	if m.TotalKB == 0 {
		return memInfo{}, errors.New("cannot read MemTotal")
	}
	return m, nil
}

func diskFreeGB(path string) (float64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return float64(st.Bavail) * float64(st.Bsize) / 1e9, nil
}

func isTmpfs(path string) (bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return false, err
	}
	return st.Type == tmpfsMagic, nil
}
