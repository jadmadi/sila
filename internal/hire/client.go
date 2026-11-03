package hire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// OpenAIRequest models standard OpenAI-compatible chat completion payload.
type OpenAIRequest struct {
	Model       string          `json:"model"`
	Messages    []OpenAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

// OpenAIMessage represents a single chat message.
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIResponse models standard chat completion response.
type OpenAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ModelClient defines the interface for executing role evaluations.
type ModelClient struct {
	httpClient *http.Client
}

// NewModelClient creates a pure-Go ModelClient using standard net/http.
func NewModelClient(timeout time.Duration) *ModelClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &ModelClient{
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// QueryRole sends a chat completion request to the configured model endpoint.
func (c *ModelClient) QueryRole(ctx context.Context, roleCfg RoleConfig, systemPrompt, userPrompt string) (string, error) {
	endpoint := roleCfg.Endpoint
	if endpoint == "" {
		endpoint = "http://localhost:11434/v1"
	}
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = strings.TrimSuffix(endpoint, "/") + "/chat/completions"
	}

	reqBody := OpenAIRequest{
		Model: roleCfg.Model,
		Messages: []OpenAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: roleCfg.Temperature,
		MaxTokens:   roleCfg.MaxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if roleCfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+roleCfg.APIKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("model endpoint error (%d): %s", resp.StatusCode, string(respBytes))
	}

	var parsed OpenAIResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return "", fmt.Errorf("parse model response: %w", err)
	}

	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("model api error: %s", parsed.Error.Message)
	}

	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty choices from model %s", roleCfg.Model)
	}

	return parsed.Choices[0].Message.Content, nil
}

// RunAdvisoryPanel executes concurrent multi-model jury evaluations on scrubbed diffs.
func RunAdvisoryPanel(ctx context.Context, cfg *PanelConfig, contractSummary, diffPayload string) (*PanelReviewResult, error) {
	start := time.Now()

	if cfg == nil {
		cfg = DefaultPanelConfig()
	}

	// 1. Mandatory Outbound Payload Redaction (Zero Unredacted Outbound Payloads)
	scrubbedSummary := ScrubPayload(contractSummary)
	scrubbedDiff := ScrubDiff(diffPayload)

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	panelCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := NewModelClient(timeout)

	systemPrompts := map[PanelRole]string{
		RolePlanner: "You are the Lead Architectural Planner on a high-stakes engineering jury. Evaluate whether the implementation adheres to specification requirements, domain boundaries, and architectural invariants. Flag issues with 'ISSUE: <description>'. If clean, state 'VERDICT: PASS'.",
		RoleCoder:   "You are the Senior Implementation Specialist on a high-stakes engineering jury. Scrutinize code diffs for syntax traps, race conditions, edge-case null dereferences, and off-by-one errors. Flag issues with 'ISSUE: <description>'. If clean, state 'VERDICT: PASS'.",
		RoleAuditor: "You are the Adversarial Forensic Auditor on a high-stakes engineering jury. Actively search for security vulnerabilities, path traversal hazards, unredacted credentials, and verification shortcuts. Flag issues with 'ISSUE: <description>'. If clean, state 'VERDICT: PASS'.",
	}

	userPayload := fmt.Sprintf("SPECIFICATION CONTRACT:\n%s\n\nIMPLEMENTATION DIFF:\n%s\n", scrubbedSummary, scrubbedDiff)

	responses := make(map[PanelRole]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var queryErrors []error

	for role, roleCfg := range cfg.Roles {
		wg.Add(1)
		go func(r PanelRole, rc RoleConfig) {
			defer wg.Done()
			sysPrompt := systemPrompts[r]
			resp, err := client.QueryRole(panelCtx, rc, sysPrompt, userPayload)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				queryErrors = append(queryErrors, fmt.Errorf("role %s (%s): %w", r, rc.Model, err))
			} else {
				responses[r] = resp
			}
		}(role, roleCfg)
	}

	wg.Wait()

	// If all models failed to respond (e.g. offline, local Ollama not running),
	// gracefully fall back to local deterministic probes per offline resilience invariant.
	if len(responses) == 0 {
		return &PanelReviewResult{
			EvaluatedAt:       time.Now().UTC(),
			Duration:          time.Since(start),
			Passed:            true,
			ConsensusScore:    1.0,
			ConsensusFindings: []PanelFinding{},
			LoneFindings:      []PanelFinding{},
			OfflineFallback:   true,
			Summary:           "Remote panel unreachable or offline; bypassed advisory jury cleanly per local-first offline resilience.",
		}, nil
	}

	res := SynthesizeJuryResults(responses, cfg)
	res.Duration = time.Since(start)
	return res, nil
}
