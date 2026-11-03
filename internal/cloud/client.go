package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
)

// Client interacts with the Cloudflare D1 Sila Edge Bridge.
type Client struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

// NewClient creates a new cloud client with a custom HTTP client or default.
func NewClient(endpoint, token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	endpoint = strings.TrimRight(endpoint, "/")
	return &Client{
		endpoint:   endpoint,
		token:      token,
		httpClient: httpClient,
	}
}

// Endpoint returns the configured cloud endpoint URL.
func (c *Client) Endpoint() string {
	if c == nil {
		return ""
	}
	return c.endpoint
}

// ActiveCloudFor returns an active Client if cloud sync is explicitly enabled,
// the feature scope is consented, the project is within the allowed scope,
// and the endpoint and token are present. Storing credentials alone causes zero network calls.
func ActiveCloudFor(scope, project string) *Client {
	cfg, err := config.Load("")
	if err != nil || cfg == nil {
		return nil
	}
	return ActiveCloudWithConfigFor(cfg, scope, project)
}

// ActiveCloudWithConfigFor checks consent and returns a Client using the provided configuration.
func ActiveCloudWithConfigFor(cfg *config.Config, scope, project string) *Client {
	if cfg == nil || !cfg.CloudEnabled {
		return nil
	}
	if !cfg.HasCloudScope(scope) {
		return nil
	}
	if !cfg.AllowsCloudProject(project) {
		return nil
	}
	endpoint := strings.TrimSpace(cfg.CloudEndpoint)
	token := strings.TrimSpace(cfg.CloudToken)
	if endpoint == "" || token == "" {
		return nil
	}
	return NewClient(endpoint, token, nil)
}

// StatusResponse is the parsed health and status response.
type StatusResponse struct {
	Status       string `json:"status"`
	Service      string `json:"service"`
	PendingCount int    `json:"pending_count"`
	Endpoint     string `json:"endpoint"`
}

// Status checks edge connectivity and counts pending inbound items.
func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	if c == nil {
		return StatusResponse{}, errors.New("cloud client is nil")
	}

	// 1. Health check
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/health", nil)
	if err != nil {
		return StatusResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return StatusResponse{}, fmt.Errorf("connecting to %s: %w", c.endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return StatusResponse{}, fmt.Errorf("health check failed (status %d): %s", resp.StatusCode, string(body))
	}

	var health struct {
		Status  string `json:"status"`
		Service string `json:"service"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return StatusResponse{}, fmt.Errorf("parsing health response: %w", err)
	}

	// 2. Query pending drain count
	drainReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/api/inbound/drain?status=pending&limit=1", nil)
	if err != nil {
		return StatusResponse{}, err
	}
	drainReq.Header.Set("Authorization", "Bearer "+c.token)

	drainResp, err := c.httpClient.Do(drainReq)
	if err != nil {
		return StatusResponse{
			Status:   health.Status,
			Service:  health.Service,
			Endpoint: c.endpoint,
		}, nil
	}
	defer drainResp.Body.Close()

	pending := 0
	if drainResp.StatusCode == http.StatusOK {
		var drainData struct {
			Count int `json:"count"`
		}
		if err := json.NewDecoder(drainResp.Body).Decode(&drainData); err == nil {
			pending = drainData.Count
		}
	}

	return StatusResponse{
		Status:       health.Status,
		Service:      health.Service,
		PendingCount: pending,
		Endpoint:     c.endpoint,
	}, nil
}
