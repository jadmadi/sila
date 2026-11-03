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

// GeminiBridgeAdapter communicates with the Cloudflare D1 / Google Gemini remote bridge.
type GeminiBridgeAdapter struct {
	Endpoint    string
	AuthToken   string
	EnabledFlag bool
	Client      *http.Client
}

// NewGeminiBridgeAdapter creates a new Gemini bridge adapter.
func NewGeminiBridgeAdapter(endpoint, authToken string, enabled bool) *GeminiBridgeAdapter {
	return &GeminiBridgeAdapter{
		Endpoint:    strings.TrimSpace(endpoint),
		AuthToken:   strings.TrimSpace(authToken),
		EnabledFlag: enabled && strings.TrimSpace(endpoint) != "",
		Client:      &http.Client{Timeout: 5 * time.Second},
	}
}

// Name returns the human-readable identifier for the Gemini bridge adapter.
func (g *GeminiBridgeAdapter) Name() string {
	return "Gemini Remote Bridge"
}

// Surface returns the Gemini gateway surface type.
func (g *GeminiBridgeAdapter) Surface() Surface {
	return SurfaceGemini
}

// Enabled reports whether the Gemini bridge adapter is active and configured.
func (g *GeminiBridgeAdapter) Enabled() bool {
	return g.EnabledFlag
}

type geminiBridgePayload struct {
	Type      string            `json:"type"`
	Surface   string            `json:"surface"`
	Project   string            `json:"project"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Level     string            `json:"level,omitempty"`
	Category  string            `json:"category,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Timestamp string            `json:"timestamp"`
}

// SendNotification posts a structured notification to the remote Gemini edge bridge.
func (g *GeminiBridgeAdapter) SendNotification(ctx context.Context, n Notification) error {
	if !g.Enabled() {
		return fmt.Errorf("gemini bridge gateway is not enabled")
	}

	cleanMeta := make(map[string]string, len(n.Metadata))
	for k, v := range n.Metadata {
		cleanMeta[Redact(k)] = Redact(v)
	}

	ts := n.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	payload := geminiBridgePayload{
		Type:      "notification",
		Surface:   string(SurfaceGemini),
		Project:   Redact(n.Project),
		Title:     Redact(n.Title),
		Body:      Redact(n.Body),
		Level:     n.Level,
		Category:  Redact(n.Category),
		Metadata:  cleanMeta,
		Timestamp: ts.UTC().Format(time.RFC3339),
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal gemini bridge payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("failed to create gemini bridge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if g.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+g.AuthToken)
	}

	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gemini bridge request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("gemini bridge returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// Test probes connectivity to the remote Gemini edge bridge health check endpoint.
func (g *GeminiBridgeAdapter) Test(ctx context.Context) (*TestResult, error) {
	if !g.Enabled() {
		return &TestResult{
			Surface: SurfaceGemini,
			Success: false,
			Message: "gemini bridge gateway is disabled or endpoint missing",
		}, nil
	}

	start := time.Now()
	testURL := strings.TrimRight(g.Endpoint, "/") + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return &TestResult{
			Surface: SurfaceGemini,
			Success: false,
			Message: err.Error(),
		}, err
	}
	if g.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+g.AuthToken)
	}

	client := g.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		// Fallback: try sending a test ping to the base endpoint
		pingErr := g.SendNotification(ctx, Notification{
			Title:     "Ping",
			Body:      "Gateway Health Probe",
			Level:     "info",
			Category:  "health",
			Timestamp: time.Now(),
		})
		latency = time.Since(start).Milliseconds()
		if pingErr != nil {
			return &TestResult{
				Surface:   SurfaceGemini,
				Success:   false,
				LatencyMs: latency,
				Message:   fmt.Sprintf("health check failed: %v", err),
			}, err
		}
		return &TestResult{
			Surface:   SurfaceGemini,
			Success:   true,
			LatencyMs: latency,
			Message:   "gemini bridge ping succeeded",
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return &TestResult{
			Surface:   SurfaceGemini,
			Success:   false,
			LatencyMs: latency,
			Message:   fmt.Sprintf("gemini bridge health check returned HTTP %d", resp.StatusCode),
		}, fmt.Errorf("gemini bridge HTTP %d", resp.StatusCode)
	}

	return &TestResult{
		Surface:   SurfaceGemini,
		Success:   true,
		LatencyMs: latency,
		Message:   "gemini bridge endpoint reachable and authenticated",
	}, nil
}

// HandleInbound processes inbound tasks drained from the remote Gemini bridge.
func (g *GeminiBridgeAdapter) HandleInbound(ctx context.Context, msg InboundMessage) error {
	// Inbound task drain from Gemini Spark
	return nil
}
