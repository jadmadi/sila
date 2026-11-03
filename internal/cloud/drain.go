package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// InboundItem represents one inbound conversation from the edge mailbox.
type InboundItem struct {
	ID        string `json:"id"`
	Project   string `json:"project"`
	Title     string `json:"title"`
	Source    string `json:"source"`
	Summary   string `json:"summary,omitempty"`
	Content   string `json:"content"`
	Tags      any    `json:"tags,omitempty"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	HLC       string `json:"hlc,omitempty"`
}

// DrainResponse holds the API response for GET /api/inbound/drain.
type DrainResponse struct {
	Items        []InboundItem `json:"items"`
	Count        int           `json:"count"`
	SessionToken string        `json:"session_token,omitempty"`
}

// DrainInbound fetches pending inbound items from the edge mailbox.
func (c *Client) DrainInbound(ctx context.Context, project string, limit int) ([]InboundItem, error) {
	items, _, err := c.DrainInboundWithSession(ctx, project, limit)
	return items, err
}

// DrainInboundWithSession fetches pending inbound items and returns the read-your-writes session token.
func (c *Client) DrainInboundWithSession(ctx context.Context, project string, limit int) ([]InboundItem, string, error) {
	if c == nil {
		return nil, "", errors.New("cloud client is nil")
	}

	if limit <= 0 {
		limit = 50
	}

	q := url.Values{}
	q.Set("status", "pending")
	q.Set("limit", strconv.Itoa(limit))
	if project != "" && project != "*" && project != "all" {
		q.Set("project", project)
	}

	reqURL := fmt.Sprintf("%s/api/inbound/drain?%s", c.endpoint, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("drain request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("drain failed (status %d): %s", resp.StatusCode, string(body))
	}

	var drainResp DrainResponse
	if err := json.NewDecoder(resp.Body).Decode(&drainResp); err != nil {
		return nil, "", fmt.Errorf("decoding drain response: %w", err)
	}

	return drainResp.Items, drainResp.SessionToken, nil
}

// AckResponse holds the API response for POST /api/inbound/ack.
type AckResponse struct {
	Success      bool `json:"success"`
	Acknowledged int  `json:"acknowledged"`
}

// AckInbound acknowledges processed items so they are marked as digested.
func (c *Client) AckInbound(ctx context.Context, ids []string) (int, error) {
	return c.AckInboundWithSession(ctx, ids, "")
}

// AckInboundWithSession acknowledges processed items with read-your-writes session token guard.
func (c *Client) AckInboundWithSession(ctx context.Context, ids []string, sessionToken string) (int, error) {
	if c == nil {
		return 0, errors.New("cloud client is nil")
	}
	if len(ids) == 0 {
		return 0, nil
	}

	payload := map[string]any{"ids": ids}
	if sessionToken != "" {
		payload["session_token"] = sessionToken
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("encoding ack payload: %w", err)
	}

	reqURL := c.endpoint + "/api/inbound/ack"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ack request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("ack failed (status %d): %s", resp.StatusCode, string(body))
	}

	var ackResp AckResponse
	if err := json.NewDecoder(resp.Body).Decode(&ackResp); err != nil {
		return 0, fmt.Errorf("decoding ack response: %w", err)
	}

	return ackResp.Acknowledged, nil
}
