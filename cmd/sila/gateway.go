package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/gateway"
	"github.com/jadmadi/sila/internal/render"
)

// SurfaceStatus reports the configuration and health state of a single gateway surface.
type SurfaceStatus struct {
	Surface  string `json:"surface"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Target   string `json:"target,omitempty"`
	ConfigOK bool   `json:"config_ok"`
}

// GatewayStatusResponse holds overall gateway telemetry for machine output.
type GatewayStatusResponse struct {
	MasterEnabled bool            `json:"master_enabled"`
	Surfaces      []SurfaceStatus `json:"surfaces"`
}

// runGateway routes and executes 'sila gateway' subcommands.
func runGateway(ui render.UI, dbPath, effProject string, jsonOut, toonOut bool, args []string) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}

	cfg, err := config.Load("")
	if err != nil {
		cfg = &config.Config{}
	}

	switch sub {
	case "status", "":
		if sub == "" && len(args) == 0 {
			gatewayStatus(ui, cfg, jsonOut, toonOut)
			return
		}
		if sub == "status" {
			gatewayStatus(ui, cfg, jsonOut, toonOut)
			return
		}
		gatewayUsage(ui)
	case "test":
		surfaceArg := ""
		if len(args) > 1 {
			surfaceArg = args[1]
		}
		gatewayTest(ui, cfg, surfaceArg, jsonOut, toonOut)
	case "send":
		gatewaySend(ui, cfg, effProject, jsonOut, toonOut, args[1:])
	case "approve":
		runApprove(ui, dbPath, effProject, false, "", "", false, jsonOut, toonOut, args[1:])
	case "help", "-h", "--help":
		gatewayUsage(ui)
		return
	default:
		gatewayUsage(ui)
		os.Exit(2)
	}
}

func gatewayUsage(ui render.UI) {
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila gateway"))+" — Pluggable external relay and multi-surface inbound gateways")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Usage:"))
	fmt.Fprintln(os.Stdout, "  sila gateway status [--json] [--toon]")
	fmt.Fprintln(os.Stdout, "  sila gateway test [<surface>] [--json] [--toon]")
	fmt.Fprintln(os.Stdout, "  sila gateway send --title=\"<text>\" --body=\"<text>\" [--surface=<name>] [--level=info] [--project=<id>]")
	fmt.Fprintln(os.Stdout, "  sila gateway approve [<id>] [--reject] [--by=...] [--reason=...]")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, ui.Bold("Surfaces:"))
	fmt.Fprintln(os.Stdout, "  discord   Discord Webhook notifications")
	fmt.Fprintln(os.Stdout, "  telegram  Telegram Bot API messages")
	fmt.Fprintln(os.Stdout, "  gemini    Gemini / Cloudflare D1 task bridge")
	fmt.Fprintln(os.Stdout, "  mcp       Remote Model Context Protocol gateway")
}

func gatewayStatus(ui render.UI, cfg *config.Config, jsonOut, toonOut bool) {
	redacted := cfg.Redacted()

	surfaces := []SurfaceStatus{
		{
			Surface:  string(gateway.SurfaceDiscord),
			Name:     "Discord Webhook",
			Enabled:  cfg.GatewayEnabled && cfg.GatewayDiscord.Enabled,
			Target:   redacted.GatewayDiscord.WebhookURL,
			ConfigOK: cfg.GatewayDiscord.WebhookURL != "",
		},
		{
			Surface:  string(gateway.SurfaceTelegram),
			Name:     "Telegram Bot",
			Enabled:  cfg.GatewayEnabled && cfg.GatewayTelegram.Enabled,
			Target:   fmt.Sprintf("chat_id: %s, token: %s", redacted.GatewayTelegram.ChatID, redacted.GatewayTelegram.BotToken),
			ConfigOK: cfg.GatewayTelegram.BotToken != "" && cfg.GatewayTelegram.ChatID != "",
		},
		{
			Surface:  string(gateway.SurfaceGemini),
			Name:     "Gemini Remote Bridge",
			Enabled:  cfg.GatewayEnabled && cfg.GatewayGemini.Enabled,
			Target:   redacted.GatewayGemini.Endpoint,
			ConfigOK: cfg.GatewayGemini.Endpoint != "",
		},
		{
			Surface:  string(gateway.SurfaceMCP),
			Name:     "Remote MCP Gateway",
			Enabled:  cfg.GatewayEnabled && cfg.GatewayMCP.Enabled,
			Target:   redacted.GatewayMCP.Endpoint,
			ConfigOK: cfg.GatewayMCP.Endpoint != "",
		},
	}

	resp := GatewayStatusResponse{
		MasterEnabled: cfg.GatewayEnabled,
		Surfaces:      surfaces,
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
		return
	}

	if toonOut {
		renderGatewayStatusTOON(resp)
		return
	}

	masterBadge := ui.Red(ui.Bold("[DISABLED]"))
	if cfg.GatewayEnabled {
		masterBadge = ui.Green(ui.Bold("[ENABLED]"))
	}

	fmt.Fprintf(os.Stdout, "%s %s\n\n", ui.Bold("Sila External Gateway Master Switch:"), masterBadge)

	fmt.Fprintf(os.Stdout, "  %-10s  %-24s  %-22s  %s\n", "SURFACE", "ADAPTER", "STATUS", "CONFIGURED TARGET")
	fmt.Fprintf(os.Stdout, "  %-10s  %-24s  %-22s  %s\n", "-------", "-------", "------", "-----------------")
	for _, s := range surfaces {
		statusStr := ui.Dim("disabled")
		if s.Enabled {
			statusStr = ui.Green("active")
		} else if s.ConfigOK {
			statusStr = ui.Yellow("configured (inactive)")
		}

		targetStr := s.Target
		if targetStr == "" || targetStr == "chat_id: , token: " {
			targetStr = ui.Dim("(unconfigured)")
		}

		fmt.Fprintf(os.Stdout, "  %-10s  %-24s  %-22s  %s\n", s.Surface, s.Name, statusStr, targetStr)
	}

	fmt.Fprintln(os.Stdout)
	if !cfg.GatewayEnabled {
		fmt.Fprintln(os.Stdout, ui.Dim("Note: Gateway relay is disabled by default. Enable via: sila config set gateway_enabled true"))
	}
}

func renderGatewayStatusTOON(resp GatewayStatusResponse) {
	fmt.Fprintf(os.Stdout, "gateway_status:\n")
	fmt.Fprintf(os.Stdout, "  master_enabled: %t\n", resp.MasterEnabled)
	fmt.Fprintf(os.Stdout, "  surfaces:\n")
	for _, s := range resp.Surfaces {
		fmt.Fprintf(os.Stdout, "    - surface: %s\n", s.Surface)
		fmt.Fprintf(os.Stdout, "      enabled: %t\n", s.Enabled)
		fmt.Fprintf(os.Stdout, "      configured: %t\n", s.ConfigOK)
	}
}

func gatewayTest(ui render.UI, cfg *config.Config, surfaceArg string, jsonOut, toonOut bool) {
	reg := gateway.NewFromConfig(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var results []gateway.TestResult
	if surfaceArg != "" {
		res, err := reg.TestSurface(ctx, surfaceArg)
		if res != nil {
			results = append(results, *res)
		} else if err != nil {
			results = append(results, gateway.TestResult{
				Surface: gateway.Surface(surfaceArg),
				Success: false,
				Message: err.Error(),
			})
		}
	} else {
		results = reg.TestAll(ctx)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "gateway_test_results:\n")
		for _, r := range results {
			fmt.Fprintf(os.Stdout, "  - surface: %s\n", r.Surface)
			fmt.Fprintf(os.Stdout, "    success: %t\n", r.Success)
			fmt.Fprintf(os.Stdout, "    latency_ms: %d\n", r.LatencyMs)
			fmt.Fprintf(os.Stdout, "    message: %s\n", r.Message)
		}
		return
	}

	fmt.Fprintf(os.Stdout, "%s\n\n", ui.Bold("Gateway Connectivity Probes:"))

	fmt.Fprintf(os.Stdout, "  %-10s  %-6s  %-10s  %s\n", "SURFACE", "RESULT", "LATENCY", "DIAGNOSTIC MESSAGE")
	fmt.Fprintf(os.Stdout, "  %-10s  %-6s  %-10s  %s\n", "-------", "------", "-------", "------------------")
	for _, r := range results {
		resBadge := ui.Red("FAIL")
		if r.Success {
			resBadge = ui.Green("PASS")
		}
		fmt.Fprintf(os.Stdout, "  %-10s  %-6s  %-10s  %s\n", string(r.Surface), resBadge, fmt.Sprintf("%dms", r.LatencyMs), r.Message)
	}
}

func gatewaySend(ui render.UI, cfg *config.Config, effProject string, jsonOut, toonOut bool, args []string) {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	title := fs.String("title", "", "Notification title")
	body := fs.String("body", "", "Notification message body")
	surface := fs.String("surface", "", "Target surface (discord, telegram, gemini, mcp; default: all enabled)")
	level := fs.String("level", "info", "Severity level (info, warn, error, success)")
	category := fs.String("category", "alert", "Category (handoff, standup, goal, alert, memo)")
	project := fs.String("project", effProject, "Project identifier")

	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if strings.TrimSpace(*title) == "" {
		fmt.Fprintln(os.Stderr, "error: --title is required")
		os.Exit(2)
	}

	notification := gateway.Notification{
		Title:     *title,
		Body:      *body,
		Level:     *level,
		Category:  *category,
		Project:   *project,
		Timestamp: time.Now(),
	}

	reg := gateway.NewFromConfig(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var results []gateway.DeliveryResult
	if *surface != "" {
		g, ok := reg.Get(gateway.Surface(strings.ToLower(strings.TrimSpace(*surface))))
		if !ok {
			fmt.Fprintf(os.Stderr, "error: unknown surface %q\n", *surface)
			os.Exit(2)
		}
		err := g.SendNotification(ctx, notification)
		res := gateway.DeliveryResult{
			Surface:   g.Surface(),
			Timestamp: time.Now(),
		}
		if err != nil {
			res.Success = false
			res.Message = err.Error()
		} else {
			res.Success = true
			res.Message = "delivered"
		}
		results = append(results, res)
	} else {
		results = reg.SendNotification(ctx, notification)
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
		return
	}

	if toonOut {
		fmt.Fprintf(os.Stdout, "gateway_delivery:\n")
		for _, r := range results {
			fmt.Fprintf(os.Stdout, "  - surface: %s\n", r.Surface)
			fmt.Fprintf(os.Stdout, "    success: %t\n", r.Success)
			fmt.Fprintf(os.Stdout, "    message: %s\n", r.Message)
		}
		return
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stdout, ui.Yellow("No active gateway surfaces enabled. Enable gateways via 'sila config set gateway_enabled true' and configure your surfaces."))
		return
	}

	fmt.Fprintf(os.Stdout, "  %-10s  %-10s  %s\n", "SURFACE", "STATUS", "DELIVERY SUMMARY")
	fmt.Fprintf(os.Stdout, "  %-10s  %-10s  %s\n", "-------", "------", "----------------")
	for _, r := range results {
		resBadge := ui.Red("FAIL")
		if r.Success {
			resBadge = ui.Green("DELIVERED")
		}
		fmt.Fprintf(os.Stdout, "  %-10s  %-10s  %s\n", string(r.Surface), resBadge, r.Message)
	}
}
