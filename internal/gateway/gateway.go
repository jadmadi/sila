package gateway

import (
	"context"
	"time"
)

// Surface represents the platform or communication channel for a gateway.
type Surface string

const (
	// SurfaceDiscord identifies the Discord webhook and bot gateway.
	SurfaceDiscord Surface = "discord"
	// SurfaceTelegram identifies the Telegram Bot API gateway.
	SurfaceTelegram Surface = "telegram"
	// SurfaceGemini identifies the Gemini / Cloudflare D1 task bridge.
	SurfaceGemini Surface = "gemini"
	// SurfaceMCP identifies the custom remote MCP server gateway.
	SurfaceMCP Surface = "mcp"
)

// Notification represents an outbound message dispatched across external surfaces.
type Notification struct {
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Level     string            `json:"level,omitempty"`    // "info", "warn", "error", "success"
	Project   string            `json:"project,omitempty"`  // target project slug or directory
	Category  string            `json:"category,omitempty"` // "handoff", "standup", "goal", "alert", "memo"
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// InboundMessage represents an incoming message received from an external surface.
type InboundMessage struct {
	Surface   Surface           `json:"surface"`
	Sender    string            `json:"sender"`
	Project   string            `json:"project"`
	Content   string            `json:"content"`
	Kind      string            `json:"kind"` // "memo", "task", "query"
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}

// TestResult holds the outcome and latency of a gateway connectivity test.
type TestResult struct {
	Surface   Surface `json:"surface"`
	Success   bool    `json:"success"`
	LatencyMs int64   `json:"latency_ms"`
	Message   string  `json:"message"`
}

// DeliveryResult records delivery status for a specific gateway adapter.
type DeliveryResult struct {
	Surface   Surface   `json:"surface"`
	Success   bool      `json:"success"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Gateway defines the interface for pluggable outbound and inbound relay adapters.
type Gateway interface {
	// Name returns the descriptive name of the gateway adapter.
	Name() string
	// Surface returns the surface identifier.
	Surface() Surface
	// Enabled reports whether the gateway is configured and active.
	Enabled() bool
	// SendNotification dispatches a sanitized outbound notification.
	SendNotification(ctx context.Context, n Notification) error
	// Test checks connectivity to the external endpoint.
	Test(ctx context.Context) (*TestResult, error)
	// HandleInbound processes an incoming message from the surface.
	HandleInbound(ctx context.Context, msg InboundMessage) error
}
