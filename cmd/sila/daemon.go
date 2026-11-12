package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/daemon"
	"github.com/jadmadi/sila/internal/ingest"
	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/release"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

func daemonSocketFrom(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--socket=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, "--socket="))
		}
	}
	return daemon.DefaultSocketPath()
}

func daemonDBFrom(args []string, fallback string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "--db=") {
			return strings.TrimSpace(strings.TrimPrefix(arg, "--db="))
		}
	}
	return fallback
}

// runDaemonCmd implements start, stop, and status.
func runDaemonCmd(ui render.UI, dbPath string, jsonOut bool, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: sila daemon <start|stop|status> [--foreground] [--socket=<path>] [--db=<path>]")
		os.Exit(2)
	}
	sub := args[0]
	socket := daemonSocketFrom(args)
	db := daemonDBFrom(args, dbPath)
	pidPath := filepath.Join(filepath.Dir(socket), "sila.pid")
	foreground := false
	for _, arg := range args[1:] {
		if arg == "--foreground" {
			foreground = true
		}
	}
	switch sub {
	case "start":
		if client, err := daemon.Dial(socket, 300*time.Millisecond); err == nil {
			_ = client.Close()
			printDaemonRunning(ui, jsonOut)
			return
		}
		if !daemon.SameOwner(pidPath) {
			fmt.Fprintln(os.Stderr, "sila daemon: pid file belongs to another user")
			os.Exit(1)
		}
		if _, err := os.Stat(socket); err == nil {
			lock, lockErr := daemon.AcquireLock(pidPath)
			if lockErr == daemon.ErrLocked {
				printDaemonRunning(ui, jsonOut)
				return
			}
			if lockErr == nil {
				lock.Release()
			}
			_ = os.Remove(socket)
		}
		if foreground {
			runDaemonForeground(ui, db, socket, pidPath, jsonOut)
			return
		}
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila daemon: %v\n", err)
			os.Exit(1)
		}
		child := exec.Command(exe, "daemon", "start", "--foreground", "--socket="+socket, "--db="+db)
		child.Stdout = nil
		child.Stderr = nil
		daemon.Detach(child)
		if err := child.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "sila daemon: %v\n", err)
			os.Exit(1)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if client, err := daemon.Dial(socket, 200*time.Millisecond); err == nil {
				_ = client.Close()
				if jsonOut {
					emitJSON(map[string]any{"pid": child.Process.Pid, "socket": socket, "status": "started"})
				} else {
					fmt.Fprintf(os.Stdout, "daemon started pid=%d socket=%s\n", child.Process.Pid, socket)
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		fmt.Fprintln(os.Stderr, "sila daemon: start timed out")
		os.Exit(1)
	case "stop":
		client, err := daemon.Dial(socket, 500*time.Millisecond)
		if err != nil {
			fmt.Fprintln(os.Stdout, "daemon not running")
			_ = os.Remove(pidPath)
			return
		}
		_, callErr := client.Call(context.Background(), "shutdown", nil)
		_ = client.Close()
		if callErr != nil {
			fmt.Fprintf(os.Stderr, "sila daemon: %v\n", callErr)
			os.Exit(1)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := daemon.Dial(socket, 100*time.Millisecond); err != nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		_ = os.Remove(socket)
		_ = os.Remove(pidPath)
		if jsonOut {
			emitJSON(map[string]any{"stopped": true})
		} else {
			fmt.Fprintln(os.Stdout, "daemon stopped")
		}
	case "status":
		client, err := daemon.Dial(socket, 500*time.Millisecond)
		if err != nil {
			if jsonOut {
				emitJSON(map[string]any{"running": false, "socket": socket})
			} else {
				fmt.Fprintln(os.Stdout, "daemon not running")
			}
			return
		}
		defer client.Close()
		raw, err := client.Call(context.Background(), "status", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sila daemon: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			fmt.Fprintln(os.Stdout, string(raw))
			return
		}
		var status map[string]any
		_ = json.Unmarshal(raw, &status)
		fmt.Fprintf(os.Stdout, "pid: %v\nversion: %v\nuptime: %vs\nclients: %v\nsocket: %v\nsync_runs: %v\nlast_sync_at: %v\n",
			status["pid"], status["version"], status["uptime_seconds"], status["clients"], status["socket"], status["sync_runs"], status["last_sync_at"])
	default:
		fmt.Fprintf(os.Stderr, "sila daemon: unknown subcommand %q\n", sub)
		os.Exit(2)
	}
}

func printDaemonRunning(ui render.UI, jsonOut bool) {
	if jsonOut {
		emitJSON(map[string]any{"running": true, "status": "daemon already running"})
		return
	}
	fmt.Fprintln(os.Stdout, "daemon already running")
}

func runDaemonForeground(ui render.UI, db, socket, pidPath string, jsonOut bool) {
	lock, err := daemon.AcquireLock(pidPath)
	if err == daemon.ErrLocked {
		printDaemonRunning(ui, jsonOut)
		return
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila daemon: %v\n", err)
		os.Exit(1)
	}
	defer lock.Release()
	defer os.Remove(pidPath)
	if err := lock.WritePID(os.Getpid()); err != nil {
		fmt.Fprintf(os.Stderr, "sila daemon: %v\n", err)
		os.Exit(1)
	}
	st, err := openMCPStore(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila daemon: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	server := daemon.NewServerWithStore(socket, version.Version, st)
	server.AddLifecycleHandlers()
	if err := server.Listen(); err != nil {
		fmt.Fprintf(os.Stderr, "sila daemon: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		cancel()
	}()
	go runDaemonSyncLoop(ctx, st, db, server)
	_ = server.Serve(ctx)
}

func runDaemonSyncLoop(ctx context.Context, st *store.Store, dbPath string, server *daemon.Server) {
	if os.Getenv("SILA_DAEMON_SYNC") == "0" {
		return
	}
	interval := 30 * time.Second
	if raw := strings.TrimSpace(os.Getenv("SILA_DAEMON_SYNC_INTERVAL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 100*time.Millisecond {
			interval = d
		}
	}
	runTick := func() {
		if ctx.Err() != nil {
			return
		}
		if spooler, err := loaders.NewSpooler(loaders.DefaultSpoolDir()); err == nil {
			_, _ = spooler.Drain(st, nil)
		}
		results, err := ingest.Run(st, ingest.Options{
			Ctx:   ctx,
			Since: time.Now().UTC().Add(-6 * time.Hour),
			Out:   io.Discard,
		})
		if err != nil {
			return
		}
		var newRec, merged int64
		for _, r := range results {
			if r.Error == nil {
				newRec += int64(r.NewRecords)
				merged += int64(r.Merged)
			}
		}
		var produced int
		if newRec > 0 || merged > 0 {
			produced, _, _, _, _ = distill(st, "")
		}
		release.SyncSoakDailyIfDue("", dbPath)
		server.NotifySync(newRec, produced)
	}

	initDelay := time.NewTimer(1 * time.Second)
	defer initDelay.Stop()
	select {
	case <-ctx.Done():
		return
	case <-initDelay.C:
		runTick()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runTick()
		}
	}
}

// ensureDaemonRunning auto-starts the background daemon when operating against
// the default store outside of unit-test binaries.
func ensureDaemonRunning(dbPath string) {
	if os.Getenv("SILA_NO_AUTO_DAEMON") == "1" || strings.EqualFold(strings.TrimSpace(os.Getenv("SILA_DAEMON")), "off") {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	base := filepath.Base(exe)
	if strings.HasSuffix(base, ".test") || strings.Contains(base, "test") {
		return
	}
	defaultDB := store.DefaultDBPath()
	if dbPath != "" && filepath.Clean(dbPath) != filepath.Clean(defaultDB) {
		return
	}
	socket := daemon.DefaultSocketPath()
	if client, err := daemon.Dial(socket, 50*time.Millisecond); err == nil {
		_ = client.Close()
		return
	}
	pidPath := filepath.Join(filepath.Dir(socket), "sila.pid")
	if !daemon.SameOwner(pidPath) {
		return
	}
	child := exec.Command(exe, "daemon", "start", "--foreground", "--socket="+socket, "--db="+defaultDB)
	child.Stdout = nil
	child.Stderr = nil
	daemon.Detach(child)
	_ = child.Start()
}
