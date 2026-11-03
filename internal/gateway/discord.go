package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DiscordAdapter posts structured notifications to a Discord webhook channel.
type DiscordAdapter struct {
	WebhookURL  string
	Username    string
	AvatarURL   string
	EnabledFlag bool
	Client      *http.Client
}

// NewDiscordAdapter creates a new Discord webhook adapter.
func NewDiscordAdapter(webhookURL, username, avatarURL string, enabled bool) *DiscordAdapter {
	if username == "" {
		username = "Sila Substrate"
	}
	return &DiscordAdapter{
		WebhookURL:  strings.TrimSpace(webhookURL),
		Username:    username,
		AvatarURL:   strings.TrimSpace(avatarURL),
		EnabledFlag: enabled && strings.TrimSpace(webhookURL) != "",
		Client:      &http.Client{Timeout: 5 * time.Second},
	}
}

// Name returns the human-readable identifier for the Discord adapter.
func (d *DiscordAdapter) Name() string {
	return "Discord Webhook"
}

// Surface returns the Discord gateway surface type.
func (d *DiscordAdapter) Surface() Surface {
	return SurfaceDiscord
}

// Enabled reports whether the Discord adapter is active and configured.
func (d *DiscordAdapter) Enabled() bool {
	return d.EnabledFlag
}

type discordPayload struct {
	Username  string         `json:"username,omitempty"`
	AvatarURL string         `json:"avatar_url,omitempty"`
	Embeds    []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// colorForLevel maps notification severity levels to standard Discord embed colors.
func colorForLevel(lvl string) int {
	switch strings.ToLower(lvl) {
	case "success":
		return 0x2ecc71 // Emerald Green
	case "warn", "warning":
		return 0xf1c40f // Amber Gold
	case "error":
		return 0xe74c3c // Alizarin Red
	default:
		return 0x3498db // Peter River Blue
	}
}

// SendNotification posts an embed notification to the configured Discord webhook.
func (d *DiscordAdapter) SendNotification(ctx context.Context, n Notification) error {
	if !d.Enabled() {
		return fmt.Errorf("discord gateway is not enabled")
	}

	cleanTitle := Redact(n.Title)
	cleanBody := Redact(n.Body)

	var fields []discordField
	if n.Project != "" {
		fields = append(fields, discordField{
			Name:   "Project",
			Value:  Redact(n.Project),
			Inline: true,
		})
	}
	if n.Category != "" {
		fields = append(fields, discordField{
			Name:   "Category",
			Value:  Redact(n.Category),
			Inline: true,
		})
	}
	for k, v := range n.Metadata {
		fields = append(fields, discordField{
			Name:   Redact(k),
			Value:  Redact(v),
			Inline: true,
		})
	}

	ts := n.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	payload := discordPayload{
		Username:  d.Username,
		AvatarURL: d.AvatarURL,
		Embeds: []discordEmbed{
			{
				Title:       cleanTitle,
				Description: cleanBody,
				Color:       colorForLevel(n.Level),
				Fields:      fields,
				Timestamp:   ts.UTC().Format(time.RFC3339),
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal discord payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.WebhookURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create discord request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("discord webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord webhook returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// Test validates connectivity and credentials against the Discord webhook endpoint.
func (d *DiscordAdapter) Test(ctx context.Context) (*TestResult, error) {
	if !d.Enabled() {
		return &TestResult{
			Surface: SurfaceDiscord,
			Success: false,
			Message: "discord gateway is disabled or webhook URL is missing",
		}, nil
	}

	start := time.Now()
	err := d.SendNotification(ctx, Notification{
		Title:     "Sila Gateway Connectivity Ping",
		Body:      "Dispatched from Sila Substrate to verify Discord webhook relay.",
		Level:     "info",
		Category:  "gateway_test",
		Timestamp: time.Now(),
	})
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return &TestResult{
			Surface:   SurfaceDiscord,
			Success:   false,
			LatencyMs: latency,
			Message:   err.Error(),
		}, err
	}

	return &TestResult{
		Surface:   SurfaceDiscord,
		Success:   true,
		LatencyMs: latency,
		Message:   "discord webhook successfully delivered test embed",
	}, nil
}

// HandleInbound processes inbound messages received from Discord.
func (d *DiscordAdapter) HandleInbound(ctx context.Context, msg InboundMessage) error {
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return nil
	}

	// 1. Direct approval callback format: "appr:<id>:approve" or "appr:<id>:reject"
	if strings.HasPrefix(content, "appr:") {
		parts := strings.Split(content, ":")
		if len(parts) >= 3 {
			apprID := parts[1]
			action := parts[2]
			approved := (action == "approve" || action == "yes")
			sender := msg.Sender
			if sender == "" {
				sender = "discord"
			}
			coord := GetDefaultCoordinator(nil, nil)
			if coord != nil {
				_, _ = coord.Resolve(apprID, approved, sender, "")
			}
		}
		return nil
	}

	// 2. Command format: "/approve <id>" or "/reject <id>"
	if strings.HasPrefix(content, "/approve ") || strings.HasPrefix(content, "/reject ") {
		fields := strings.Fields(content)
		if len(fields) >= 2 {
			action := strings.TrimPrefix(fields[0], "/")
			apprID := fields[1]
			approved := (action == "approve")
			reason := ""
			if len(fields) > 2 {
				reason = strings.Join(fields[2:], " ")
			}
			sender := msg.Sender
			if sender == "" {
				sender = "discord"
			}
			coord := GetDefaultCoordinator(nil, nil)
			if coord != nil {
				_, _ = coord.Resolve(apprID, approved, sender, reason)
			}
		}
		return nil
	}

	return nil
}
