package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// resolveDocDir locates the documentation directory containing index.html.
func resolveDocDir(customDir string) (string, error) {
	candidates := []string{}
	if customDir != "" {
		candidates = append(candidates, customDir)
	}

	cwd, _ := os.Getwd()
	candidates = append(candidates,
		filepath.Join(cwd, "html-docs"),
		filepath.Join(cwd, "..", "html-docs"),
	)

	// Search parent directories if running from subdirectories or worktrees
	dir := cwd
	for range 4 {
		candidates = append(candidates, filepath.Join(dir, "html-docs"))
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(filepath.Join(cand, "index.html")); err == nil && !fi.IsDir() {
			abs, err := filepath.Abs(cand)
			if err == nil {
				return abs, nil
			}
			return cand, nil
		}
	}

	return "", fmt.Errorf("html-docs directory with index.html not found in %s", cwd)
}

// runDocServe implements `sila doc serve`: zero-dependency HTTP preview of the local documentation portal.
func runDocServe(ui render.UI, port int, host string, noOpen, jsonOut bool, customDir string) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runDocServeContext(ctx, ui, port, host, noOpen, jsonOut, customDir); err != nil {
		if jsonOut {
			emitJSON(map[string]any{"error": err.Error()})
		} else {
			fmt.Fprintf(os.Stderr, "sila doc serve: %v\n", err)
		}
		os.Exit(1)
	}
}

func runDocServeContext(ctx context.Context, ui render.UI, port int, host string, noOpen, jsonOut bool, customDir string) error {
	docDir, err := resolveDocDir(customDir)
	if err != nil {
		return err
	}

	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 7452
	}

	addr := fmt.Sprintf("%s:%d", host, port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// Fallback to random available port if port is busy
		listener, err = net.Listen("tcp", fmt.Sprintf("%s:0", host))
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", host, err)
		}
	}
	defer listener.Close()

	actualPort := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://%s:%d", host, actualPort)

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(docDir)))

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	if jsonOut {
		emitJSON(map[string]any{
			"url":       url,
			"directory": docDir,
			"status":    "serving",
			"port":      actualPort,
			"host":      host,
		})
	} else {
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · doc serve"))+" — offline documentation portal")
		fmt.Fprintf(os.Stdout, "  %-12s %s\n", "URL:", ui.Green(ui.Bold(url)))
		fmt.Fprintf(os.Stdout, "  %-12s %s\n", "Directory:", ui.Dim(docDir))
		fmt.Fprintf(os.Stdout, "  %-12s %s\n", "Runtime:", ui.Dim("pure-Go (zero Python/Node dependencies)"))
		fmt.Fprintf(os.Stdout, "  %-12s %s\n", "Status:", ui.Bold("serving · press Ctrl+C to stop"))
	}

	if !noOpen {
		openBrowser(url)
	}

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
		if !jsonOut {
			fmt.Fprintln(os.Stdout, ui.Dim("\nShutting down doc server..."))
		}
		return nil
	case err := <-serverErr:
		return err
	}
}
