package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/loaders"
	"github.com/jadmadi/sila/internal/render"
)

func runPreflight(ui render.UI, dbPath, project string, args []string, jsonOut bool) {
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	cwd, _ := os.Getwd()
	switch sub {
	case "status":
		policy := loaders.ResolvePreflightPolicy(cwd)
		if jsonOut {
			emitJSON(map[string]any{
				"policy": policy, "project_dir": cwd,
				"plan_mode": "Command Code skips hooks in plan mode; preflight cannot block there",
			})
			return
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · preflight status")))
		fmt.Fprintf(os.Stdout, "  Policy:    %s\n", policy)
		fmt.Fprintf(os.Stdout, "  Project:   %s\n", cwd)
		fmt.Fprintln(os.Stdout, "  Plan mode: hooks skipped; preflight cannot block")
	case "set":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sila preflight set: policy is required (off|warn|enforce)")
			os.Exit(2)
		}
		policy := loaders.NormalizePreflightPolicy(args[1])
		if policy == "" {
			fmt.Fprintf(os.Stderr, "sila preflight set: invalid policy %q\n", args[1])
			os.Exit(2)
		}
		scope := "project"
		for _, a := range args[2:] {
			if a == "--user" || a == "--global" {
				scope = "user"
			}
		}
		path := filepath.Join(cwd, ".sila", "config")
		if scope == "user" {
			dir, err := os.UserConfigDir()
			if err != nil {
				fmt.Fprintf(os.Stderr, "sila preflight set: resolve user config: %v\n", err)
				os.Exit(1)
			}
			path = filepath.Join(dir, "sila", "config")
		}
		if err := writePreflightPolicy(path, string(policy)); err != nil {
			fmt.Fprintf(os.Stderr, "sila preflight set: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			emitJSON(map[string]any{"policy": policy, "scope": scope, "path": path, "status": "written"})
			return
		}
		fmt.Fprintf(os.Stdout, "preflight policy: %s (%s: %s)\n", policy, scope, path)
	default:
		fmt.Fprintf(os.Stderr, "sila preflight: unknown subcommand %q (status|set)\n", sub)
		os.Exit(2)
	}
}

func writePreflightPolicy(path, policy string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(data) > 0 {
		_ = os.WriteFile(path+".bak", data, 0o600)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 1 && strings.TrimSpace(lines[0]) == "" {
		lines = nil
	}
	replaced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "preflight_policy") {
			lines[i] = fmt.Sprintf("preflight_policy = %q", policy)
			replaced = true
		}
	}
	if !replaced {
		section := -1
		for i, line := range lines {
			if strings.TrimSpace(line) == "[command_code]" {
				section = i
				break
			}
		}
		if section >= 0 {
			lines = append(lines[:section+1], append([]string{fmt.Sprintf("preflight_policy = %q", policy)}, lines[section+1:]...)...)
		} else {
			lines = append(lines, "", "[command_code]", fmt.Sprintf("preflight_policy = %q", policy))
		}
	}
	out := strings.Join(lines, "\n") + "\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
