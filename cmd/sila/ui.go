package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/mattn/go-isatty"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/web"
)

func runUI(ui render.UI, dbPath, project string, port int, host string, noOpen, expose, jsonOut bool, args []string) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runUIContext(ctx, ui, dbPath, project, port, host, noOpen, expose, jsonOut, args); err != nil {
		fmt.Fprintf(os.Stderr, "sila ui: %v\n", err)
		os.Exit(1)
	}
}

func runUIContext(ctx context.Context, ui render.UI, dbPath, project string, port int, host string, noOpen, expose, jsonOut bool, args []string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	cwd, _ := os.Getwd()
	if project == "" {
		project = store.ProjectID(cwd)
	}

	cfg := web.Config{
		Host:       host,
		Port:       port,
		ProjectDir: cwd,
		ProjectID:  project,
		NoOpen:     noOpen,
		Expose:     expose,
		Store:      st,
		OnShutdown: cancel,
	}

	srv, err := web.NewServer(cfg)
	if err != nil {
		return fmt.Errorf("init server: %w", err)
	}

	if err := srv.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}

	url := srv.URL()
	if jsonOut {
		out := map[string]any{
			"url":         url,
			"port":        srv.Port(),
			"host":        host,
			"project_id":  project,
			"project_dir": cwd,
			"status":      "running",
		}
		// An exposed server gates mutations behind the token, so a scripted
		// launch needs it in the machine-readable output too.
		if web.HostIsRemote(host) {
			out["token"] = srv.Token()
		}
		emitJSON(out)
	} else {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Cyan("sila · decision cockpit"))+" is running at "+ui.Bold(ui.Green(url)))
		if web.HostIsRemote(host) {
			fmt.Fprintf(os.Stdout, "  🔑 Launch token (required for mutations): %s\n", ui.Yellow(srv.Token()))
		}
		fmt.Fprintf(os.Stdout, "  📁 Project: %s (%s)\n", ui.Bold(project), ui.Dim(cwd))
		fmt.Fprintf(os.Stdout, "  ⚡ Realtime: %s (Server-Sent Events active)\n", ui.Green("Live SSE"))
		fmt.Fprintln(os.Stdout, ui.Dim("  Press 'q' or Ctrl+C to stop."))
	}

	if !noOpen {
		// An exposed server needs the token for mutations; opening the token
		// URL seeds the launch cookie so the cockpit works without the user
		// pasting anything.
		openBrowser(launchURL(url, srv, host))
	}

	// Enable terminal cbreak mode so single 'q' quits without pressing Enter (interactive TTY only)
	if isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd()) {
		restoreTerm := enableTerminalCbreak()
		defer restoreTerm()

		go func() {
			buf := make([]byte, 1)
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				n, err := os.Stdin.Read(buf)
				if err != nil {
					return
				}
				if n > 0 && (buf[0] == 'q' || buf[0] == 'Q' || buf[0] == 3) {
					cancel()
					return
				}
			}
		}()
	}

	// Wait until context is canceled (via signal, keyboard 'q', or /api/shutdown)
	<-ctx.Done()

	if !jsonOut {
		fmt.Fprintln(os.Stdout, "\n"+ui.Dim("Shutting down web cockpit..."))
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}

// launchURL appends the launch token when the server is exposed, so the
// browser receives it once and keeps it in the SameSite launch cookie.
func launchURL(url string, srv *web.Server, host string) string {
	if !web.HostIsRemote(host) {
		return url
	}
	return url + "/?token=" + srv.Token()
}

var openBrowserCmd = func(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

func openBrowser(targetURL string) {
	_ = openBrowserCmd(targetURL)
}
