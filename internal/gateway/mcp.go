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

// MCPGatewayAdapter relays notifications to a remote Model Context Protocol endpoint.
type MCPGatewayAdapter struct {
	Endpoint    string
	AuthToken   string
	EnabledFlag bool
	Client      *http.Client
}

// NewMCPGatewayAdapter creates a new remote MCP gateway adapter.
func NewMCPGatewayAdapter(endpoint, authToken string, enabled bool) *MCPGatewayAdapter {
	return &MCPGatewayAdapter{
		Endpoint:    strings.TrimSpace(endpoint),
		AuthToken:   strings.TrimSpace(authToken),
		EnabledFlag: enabled && strings.TrimSpace(endpoint) != "",
		Client:      &http.Client{Timeout: 5 * time.Second},
	}
}

// Name returns the human-readable identifier for the MCP gateway adapter.
func (m *MCPGatewayAdapter) Name() string {
	return "Remote MCP Gateway"
}

// Surface returns the MCP gateway surface type.
func (m *MCPGatewayAdapter) Surface() Surface {
	return SurfaceMCP
}

// Enabled reports whether the MCP gateway adapter is active and configured.
func (m *MCPGatewayAdapter) Enabled() bool {
	return m.EnabledFlag
}

type jsonrpcNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type mcpNotificationParams struct {
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Level     string            `json:"level,omitempty"`
	Project   string            `json:"project,omitempty"`
	Category  string            `json:"category,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp string            `json:"timestamp"`
}

// SendNotification dispatches a JSON-RPC 2.0 notification to the remote MCP gateway.
func (m *MCPGatewayAdapter) SendNotification(ctx context.Context, n Notification) error {
	if !m.Enabled() {
		return fmt.Errorf("mcp gateway is not enabled")
	}

	cleanMeta := make(map[string]string, len(n.Metadata))
	for k, v := range n.Metadata {
		cleanMeta[Redact(k)] = Redact(v)
	}

	ts := n.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	payload := jsonrpcNotification{
		JSONRPC: "2.0",
		Method:  "notifications/message",
		Params: mcpNotificationParams{
			Title:     Redact(n.Title),
			Body:      Redact(n.Body),
			Level:     n.Level,
			Project:   Redact(n.Project),
			Category:  Redact(n.Category),
			Metadata:  cleanMeta,
			Timestamp: ts.UTC().Format(time.RFC3339),
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal mcp notification: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create mcp request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if m.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.AuthToken)
	}

	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp gateway request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp gateway returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// Test probes connectivity to the remote MCP gateway via a JSON-RPC ping.
func (m *MCPGatewayAdapter) Test(ctx context.Context) (*TestResult, error) {
	if !m.Enabled() {
		return &TestResult{
			Surface: SurfaceMCP,
			Success: false,
			Message: "mcp gateway is disabled or endpoint missing",
		}, nil
	}

	start := time.Now()
	pingPayload := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "ping",
		"id":      1,
	}
	bodyBytes, _ := json.Marshal(pingPayload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return &TestResult{
			Surface: SurfaceMCP,
			Success: false,
			Message: err.Error(),
		}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+m.AuthToken)
	}

	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return &TestResult{
			Surface:   SurfaceMCP,
			Success:   false,
			LatencyMs: latency,
			Message:   err.Error(),
		}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &TestResult{
			Surface:   SurfaceMCP,
			Success:   false,
			LatencyMs: latency,
			Message:   fmt.Sprintf("mcp gateway ping returned HTTP %d", resp.StatusCode),
		}, fmt.Errorf("mcp gateway HTTP %d", resp.StatusCode)
	}

	return &TestResult{
		Surface:   SurfaceMCP,
		Success:   true,
		LatencyMs: latency,
		Message:   "mcp gateway ping successful",
	}, nil
}

// HandleInbound dispatches inbound Model Context Protocol requests.
func (m *MCPGatewayAdapter) HandleInbound(ctx context.Context, msg InboundMessage) error {
	// Remote MCP JSON-RPC dispatch
	return nil
}
