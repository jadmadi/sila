package feedback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

const (
	// DefaultEdgeFeedbackURL is the public edge ingestion endpoint for Sila user feedback.
	DefaultEdgeFeedbackURL = "https://sila.jadmadi.net/api/feedback"
	// DefaultEdgeDrainURL is the authenticated edge drain endpoint for the Sila maintainer.
	DefaultEdgeDrainURL = "https://sila.jadmadi.net/api/feedback/drain"
	// DefaultEdgeAckURL is the authenticated edge acknowledgment endpoint.
	DefaultEdgeAckURL = "https://sila.jadmadi.net/api/feedback/ack"
)

// EdgeSubmitPayload is the wire format accepted by the Cloudflare edge worker.
type EdgeSubmitPayload struct {
	Category    string `json:"category"`
	Content     string `json:"content"`
	Agent       string `json:"agent,omitempty"`
	ProjectHash string `json:"project_hash,omitempty"`
	SilaVersion string `json:"sila_version,omitempty"`
	OS          string `json:"os,omitempty"`
	Arch        string `json:"arch,omitempty"`
}

// EdgeSubmitResult contains the response from an edge feedback submission.
type EdgeSubmitResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// EdgeDrainResult contains drained feedback items returned by the Cloudflare edge.
type EdgeDrainResult struct {
	Items        []EdgeFeedbackItem `json:"items"`
	Count        int                `json:"count"`
	SessionToken string             `json:"session_token"`
}

// EdgeFeedbackItem matches the schema of rows stored in Cloudflare D1 edge_feedback.
type EdgeFeedbackItem struct {
	ID           string `json:"id"`
	Category     string `json:"category"`
	Content      string `json:"content"`
	Agent        string `json:"agent"`
	ProjectHash  string `json:"project_hash"`
	SilaVersion  string `json:"sila_version"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Status       string `json:"status"`
	SessionToken string `json:"drain_session_token"`
	ClaimedAt    string `json:"claimed_at"`
	CreatedAt    string `json:"created_at"`
}

// ResolveFeedbackURL determines the public feedback submission endpoint.
func ResolveFeedbackURL(customURL string) string {
	if customURL != "" {
		if strings.HasSuffix(customURL, "/api/feedback") || strings.HasSuffix(customURL, "/feedback") {
			return customURL
		}
		return strings.TrimRight(customURL, "/") + "/api/feedback"
	}
	if u := os.Getenv("SILA_FEEDBACK_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	if u := os.Getenv("SILA_EDGE_URL"); u != "" {
		return strings.TrimRight(u, "/") + "/api/feedback"
	}
	return DefaultEdgeFeedbackURL
}

// ResolveDrainURL determines the edge drain endpoint.
func ResolveDrainURL(customURL string) string {
	if customURL != "" {
		if strings.HasSuffix(customURL, "/api/feedback/drain") || strings.HasSuffix(customURL, "/drain") {
			return customURL
		}
		return strings.TrimRight(customURL, "/") + "/api/feedback/drain"
	}
	if u := os.Getenv("SILA_EDGE_URL"); u != "" {
		return strings.TrimRight(u, "/") + "/api/feedback/drain"
	}
	return DefaultEdgeDrainURL
}

// ResolveAckURL determines the edge acknowledgment endpoint.
func ResolveAckURL(customURL string) string {
	if customURL != "" {
		if strings.HasSuffix(customURL, "/api/feedback/ack") || strings.HasSuffix(customURL, "/ack") {
			return customURL
		}
		return strings.TrimRight(customURL, "/") + "/api/feedback/ack"
	}
	if u := os.Getenv("SILA_EDGE_URL"); u != "" {
		return strings.TrimRight(u, "/") + "/api/feedback/ack"
	}
	return DefaultEdgeAckURL
}

// HashProjectID creates an anonymous 16-character SHA-256 fingerprint for a project.
func HashProjectID(projectID string) string {
	if projectID == "" {
		return ""
	}
	h := sha256.Sum256([]byte(projectID))
	return hex.EncodeToString(h[:8])
}

// SubmitToEdge posts sanitized feedback anonymously to the Cloudflare D1 edge.
func SubmitToEdge(ctx context.Context, endpoint string, fb *store.Feedback) (*EdgeSubmitResult, error) {
	if fb == nil {
		return nil, fmt.Errorf("feedback is nil")
	}
	if endpoint == "" {
		endpoint = ResolveFeedbackURL("")
	}

	cleanContent := SanitizeForUpstream(fb.Content)
	if strings.TrimSpace(cleanContent) == "" {
		return nil, fmt.Errorf("feedback content cannot be empty")
	}

	payload := EdgeSubmitPayload{
		Category:    NormalizeCategory(fb.Category),
		Content:     cleanContent,
		Agent:       fb.Agent,
		ProjectHash: HashProjectID(fb.ProjectID),
		SilaVersion: version.Version,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal edge payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create edge request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sila/"+version.Version)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("submit feedback to edge: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read edge response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("edge rejected feedback (status %d): %s", resp.StatusCode, cleanHTTPErrorMessage(resp.StatusCode, respBytes))
	}

	var res EdgeSubmitResult
	if err := json.Unmarshal(respBytes, &res); err != nil {
		return nil, fmt.Errorf("parse edge response: %w", err)
	}

	return &res, nil
}

// DrainFromEdge pulls pending feedback items from Cloudflare D1 edge into local memory.
func DrainFromEdge(ctx context.Context, endpoint, token string, limit int) (*EdgeDrainResult, error) {
	if endpoint == "" {
		endpoint = ResolveDrainURL("")
	}
	if limit <= 0 {
		limit = 50
	}
	if strings.Contains(endpoint, "?") {
		endpoint = fmt.Sprintf("%s&limit=%d", endpoint, limit)
	} else {
		endpoint = fmt.Sprintf("%s?limit=%d", endpoint, limit)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create drain request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "sila/"+version.Version)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("drain feedback from edge: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read drain response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("edge drain failed (status %d): %s", resp.StatusCode, cleanHTTPErrorMessage(resp.StatusCode, respBytes))
	}

	var drainRes EdgeDrainResult
	if err := json.Unmarshal(respBytes, &drainRes); err != nil {
		return nil, fmt.Errorf("parse edge drain response: %w", err)
	}

	return &drainRes, nil
}

// AcknowledgeEdgeFeedback signals to Cloudflare D1 that feedback items were drained and saved.
func AcknowledgeEdgeFeedback(ctx context.Context, endpoint, token string, ids []string, sessionToken string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	if endpoint == "" {
		endpoint = ResolveAckURL("")
	}

	payload := map[string]any{
		"ids": ids,
	}
	if sessionToken != "" {
		payload["session_token"] = sessionToken
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal ack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, fmt.Errorf("create ack request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sila/"+version.Version)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("acknowledge feedback to edge: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read ack response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("edge ack failed (status %d): %s", resp.StatusCode, cleanHTTPErrorMessage(resp.StatusCode, respBytes))
	}

	var ackRes struct {
		Success      bool `json:"success"`
		Acknowledged int  `json:"acknowledged"`
	}
	if err := json.Unmarshal(respBytes, &ackRes); err != nil {
		return 0, fmt.Errorf("parse edge ack response: %w", err)
	}

	return ackRes.Acknowledged, nil
}

// cleanHTTPErrorMessage extracts a concise message from an HTTP error body,
// guarding against verbose HTML error pages dumping raw tags to the terminal.
func cleanHTTPErrorMessage(code int, body []byte) string {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 || strings.Contains(msg, "<html") || strings.Contains(msg, "<!DOCTYPE") || strings.Contains(msg, "<svg") {
		return http.StatusText(code)
	}
	return msg
}
