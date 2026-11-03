package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/demo"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/web"
)

// runDemo implements `sila demo`: provision an ephemeral, pre-populated
// sandbox knowledge store and launch the Web Cockpit against it. The sandbox
// (store + sample goals/ADRs) is deleted when the cockpit shuts down.
func runDemo(ui render.UI, port int, host string, noOpen, expose, jsonOut bool) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runDemoContext(ctx, ui, port, host, noOpen, expose, jsonOut); err != nil {
		fmt.Fprintf(os.Stderr, "sila demo: %v\n", err)
		os.Exit(1)
	}
}

func runDemoContext(ctx context.Context, ui render.UI, port int, host string, noOpen, expose, jsonOut bool) error {
	sb, st, err := demo.Create()
	if err != nil {
		return err
	}
	defer st.Close()
	defer sb.Cleanup()

	demoCtx, cancelDemo := context.WithCancel(ctx)
	defer cancelDemo()

	cfg := web.Config{
		Host:       host,
		Port:       port,
		ProjectDir: sb.Dir,
		ProjectID:  demo.ProjectName,
		NoOpen:     noOpen,
		Expose:     expose,
		Store:      st,
		// /api/shutdown must actually stop the demo so the deferred sandbox
		// cleanup runs; a no-op callback left the demo serving after "stop".
		OnShutdown: cancelDemo,
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
			"url":        url,
			"port":       srv.Port(),
			"host":       host,
			"project_id": demo.ProjectName,
			"store":      sb.DBPath,
			"ephemeral":  true,
			"status":     "running",
		}
		if web.HostIsRemote(host) {
			out["token"] = srv.Token()
		}
		emitJSON(out)
	} else {
		fmt.Fprintln(os.Stdout, ui.Bold(ui.Cyan("sila · demo playground"))+" is running at "+ui.Bold(ui.Green(url)))
		if web.HostIsRemote(host) {
			fmt.Fprintf(os.Stdout, "  🔑 Launch token (required for mutations; the opened URL carries it): %s\n", ui.Yellow(srv.Token()))
		}
		fmt.Fprintf(os.Stdout, "  🧪 Sandbox: %s (%s)\n", ui.Bold(demo.ProjectName), ui.Dim("ephemeral — deleted on shutdown"))
		fmt.Fprintf(os.Stdout, "  📦 Demo store: %s\n", ui.Dim(sb.DBPath))
		fmt.Fprintf(os.Stdout, "  🔍 Query it live: %s\n", ui.Dim("sila search payment --db="+sb.DBPath))
		fmt.Fprintln(os.Stdout, ui.Dim("  Press Ctrl+C (or use Stop Cockpit) to stop and discard the sandbox."))
	}

	if !noOpen {
		openBrowser(launchURL(url, srv, host))
	}

	<-demoCtx.Done()

	if !jsonOut {
		fmt.Fprintln(os.Stdout, "\n"+ui.Dim("Shutting down demo playground and discarding the sandbox..."))
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	return srv.Shutdown(shutdownCtx)
}
