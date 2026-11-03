package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var scoreRubricRE = regexp.MustCompile(`(?i)(?:rate|score).*?from\s+(-?\d+(?:\.\d+)?)\s*(?:\([^)]*\))?\s*(?:to|through)\s+(-?\d+(?:\.\d+)?)`)

const (
	// DefaultTypeSafeEndpoint is the official System One endpoint.
	DefaultTypeSafeEndpoint = "https://api.typesafe.ai/v1/systemone"

	// DefaultTypeSafeModel is the default model alias.
	DefaultTypeSafeModel = "jev-latest"

	// MaxRequestBytes bounds the outbound JSON request payload.
	MaxRequestBytes = 1024 * 1024 // 1 MB

	// MaxResponseBytes bounds the inbound JSON response payload.
	MaxResponseBytes = 2 * 1024 * 1024 // 2 MB

	// MaxQuestionsPerRequest bounds the number of questions in one request.
	MaxQuestionsPerRequest = 50

	// CostPerMillionTokens is the conservative USD cost per 1M tokens ($0.042).
	CostPerMillionTokens = 0.042
)

// TypeSafeAdapter connects to TypeSafe's System One API (e.g. Jev).
type TypeSafeAdapter struct {
	mu         sync.Mutex
	Endpoint   string
	APIKey     string
	HTTPClient *http.Client
	SpendLimit float64 // Hard spending cap in USD (must be > 0)
	TotalSpent float64 // Reconciled cumulative spend in USD
	Reserved   float64 // In-flight reserved spend in USD
}

// NewTypeSafeAdapter initializes a TypeSafe adapter.
func NewTypeSafeAdapter(apiKey string, client *http.Client) *TypeSafeAdapter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &TypeSafeAdapter{
		Endpoint:   DefaultTypeSafeEndpoint,
		APIKey:     strings.TrimSpace(apiKey),
		HTTPClient: client,
		SpendLimit: 5.0, // Default conservative $5 cap
	}
}

// Name returns the provider identifier for TypeSafeAdapter.
func (a *TypeSafeAdapter) Name() ModelProvider {
	return ProviderTypeSafe
}

// estimateRequestCost calculates a conservative upper bound in USD for a request.
func estimateRequestCost(stateBytes, questionsBytes int) float64 {
	// Conservative estimate: 1 token per 3 bytes + 200 token base overhead
	estTokens := float64(stateBytes+questionsBytes)/3.0 + 200.0
	return (estTokens / 1e6) * CostPerMillionTokens
}

// reserveBudget checks and reserves budget for an in-flight request.
func (a *TypeSafeAdapter) reserveBudget(estCost float64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.SpendLimit <= 0 {
		return fmt.Errorf("typesafe: spend limit must be a positive number (got %.6f)", a.SpendLimit)
	}
	if (a.TotalSpent + a.Reserved + estCost) > a.SpendLimit {
		return fmt.Errorf("typesafe: spending limit exceeded (spent $%.4f + reserved $%.4f + est $%.4f > cap $%.4f)",
			a.TotalSpent, a.Reserved, estCost, a.SpendLimit)
	}
	a.Reserved += estCost
	return nil
}

// reconcileBudget reconciles reported token usage and frees the reservation.
func (a *TypeSafeAdapter) reconcileBudget(estCost float64, inputTokens, outputTokens int, hasUsage bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.Reserved -= estCost
	if a.Reserved < 0 {
		a.Reserved = 0
	}

	var actualCost float64
	if hasUsage {
		actualCost = (float64(inputTokens) / 1e6) * CostPerMillionTokens
		if outputTokens > 0 {
			actualCost += (float64(outputTokens) / 1e6) * CostPerMillionTokens
		}
	} else {
		actualCost = estCost
	}
	a.TotalSpent += actualCost

	if a.TotalSpent > a.SpendLimit {
		return fmt.Errorf("typesafe: spend cap overrun detected (total spent $%.6f exceeds cap $%.6f)",
			a.TotalSpent, a.SpendLimit)
	}
	return nil
}

// releaseReservation releases in-flight reservation upon error prior to usage reporting.
func (a *TypeSafeAdapter) releaseReservation(estCost float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Reserved -= estCost
	if a.Reserved < 0 {
		a.Reserved = 0
	}
}

// rawSystemOneRequest matches the JSON wire format for /v1/systemone.
type rawSystemOneRequest struct {
	Model     string              `json:"model"`
	State     map[string]any      `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// rawSystemOneResponse matches the wire format.
type rawSystemOneResponse struct {
	Model   string                     `json:"model,omitempty"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Evaluate submits questions and state context to the TypeSafe API endpoint.
func (a *TypeSafeAdapter) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]Question) (*ProviderResponse, error) {
	if a.APIKey == "" {
		return nil, errors.New("typesafe: API key not configured")
	}
	if len(questions) == 0 {
		return nil, errors.New("typesafe: at least one question is required")
	}
	if len(questions) > MaxQuestionsPerRequest {
		return nil, fmt.Errorf("typesafe: question count %d exceeds maximum %d", len(questions), MaxQuestionsPerRequest)
	}

	requestedModel := model
	if requestedModel == "" {
		requestedModel = DefaultTypeSafeModel
	}

	reqPayload := rawSystemOneRequest{
		Model:     requestedModel,
		State:     state,
		Questions: questions,
	}

	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("typesafe marshal request: %w", err)
	}
	if len(reqBody) > MaxRequestBytes {
		return nil, fmt.Errorf("typesafe: request payload %d bytes exceeds maximum %d bytes", len(reqBody), MaxRequestBytes)
	}

	stateBytes, _ := json.Marshal(state)
	questionsBytes, _ := json.Marshal(questions)
	estCost := estimateRequestCost(len(stateBytes), len(questionsBytes))

	if err := a.reserveBudget(estCost); err != nil {
		return nil, err
	}
	reserved := true
	defer func() {
		if reserved {
			a.releaseReservation(estCost)
		}
	}()

	endpoint := a.Endpoint
	if endpoint == "" {
		endpoint = DefaultTypeSafeEndpoint
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("typesafe new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("User-Agent", "sila/assist-eval")

	start := time.Now()
	resp, err := a.HTTPClient.Do(req)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		return nil, fmt.Errorf("typesafe http call: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, int64(MaxResponseBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("typesafe read body: %w", err)
	}
	if len(bodyBytes) > MaxResponseBytes {
		return nil, fmt.Errorf("typesafe: response size exceeds maximum allowed (%d bytes)", MaxResponseBytes)
	}

	// Redact remote error bodies: do not leak tokens, HTML, or raw bodies
	if resp.StatusCode != http.StatusOK {
		statusText := http.StatusText(resp.StatusCode)
		if statusText == "" {
			statusText = "Unknown Status"
		}
		return nil, fmt.Errorf("typesafe api error HTTP %d (%s)", resp.StatusCode, statusText)
	}

	var rawResp rawSystemOneResponse
	if err := json.Unmarshal(bodyBytes, &rawResp); err != nil {
		return nil, fmt.Errorf("typesafe unmarshal response: %w", err)
	}

	hasUsage := rawResp.Usage != nil
	inTok := 0
	outTok := 0
	if hasUsage {
		inTok = rawResp.Usage.InputTokens
		outTok = rawResp.Usage.OutputTokens
		if inTok < 0 || outTok < 0 {
			return nil, fmt.Errorf("typesafe: negative token usage reported (%d, %d)", inTok, outTok)
		}
	}

	reserved = false
	if err := a.reconcileBudget(estCost, inTok, outTok, hasUsage); err != nil {
		return nil, err
	}

	out := &ProviderResponse{
		Provider:       ProviderTypeSafe,
		Model:          rawResp.Model,
		RequestedModel: requestedModel,
		Choices:        make(map[string]ChoiceAnswer),
		Nouls:          make(map[string]NoulAnswer),
		Scores:         make(map[string]ScoreAnswer),
		InvalidAnswers: make(map[string]string),
		DurationMs:     duration,
	}
	if hasUsage {
		out.Usage = TokenUsage{
			InputTokens:  inTok,
			OutputTokens: outTok,
		}
	} else {
		out.Usage = TokenUsage{
			InputTokens: int((estCost / CostPerMillionTokens) * 1e6),
		}
	}

	for qID, q := range questions {
		rawAns, inRaw := rawResp.Answers[qID]
		if !inRaw {
			out.InvalidAnswers[qID] = "missing answer from provider"
			continue
		}

		var rawMap map[string]json.RawMessage
		if err := json.Unmarshal(rawAns, &rawMap); err != nil {
			out.InvalidAnswers[qID] = "missing or malformed answer object"
			continue
		}

		switch q.Type {
		case TypeChoice:
			choiceRaw, exists := rawMap["choice"]
			if !exists || string(choiceRaw) == "null" {
				out.InvalidAnswers[qID] = "missing choice in answer"
				continue
			}
			var ca ChoiceAnswer
			if err := json.Unmarshal(rawAns, &ca); err != nil || strings.TrimSpace(ca.Choice) == "" {
				out.InvalidAnswers[qID] = "missing or malformed choice answer"
				continue
			}
			if math.IsNaN(ca.Confidence) || math.IsInf(ca.Confidence, 0) || ca.Confidence < 0.0 || ca.Confidence > 1.0 {
				ca.Confidence = 0.0
			}
			var allowed []string
			switch c := q.Criteria.(type) {
			case []string:
				allowed = c
			case []any:
				for _, item := range c {
					if s, ok := item.(string); ok {
						allowed = append(allowed, s)
					}
				}
			case map[string]string:
				for k := range c {
					allowed = append(allowed, k)
				}
			case map[string]any:
				for k := range c {
					allowed = append(allowed, k)
				}
			}
			if len(allowed) > 0 {
				found := false
				for _, opt := range allowed {
					if strings.EqualFold(opt, ca.Choice) {
						found = true
						ca.Choice = opt
						break
					}
				}
				if !found {
					out.InvalidAnswers[qID] = fmt.Sprintf("choice %q not in allowed criteria", ca.Choice)
					continue
				}
			}
			out.Choices[qID] = ca

		case TypeNoul:
			noulRaw, exists := rawMap["noul"]
			if !exists || string(noulRaw) == "null" {
				out.InvalidAnswers[qID] = "missing noul in answer"
				continue
			}
			var na NoulAnswer
			if err := json.Unmarshal(rawAns, &na); err != nil {
				out.InvalidAnswers[qID] = "malformed noul answer"
				continue
			}
			if math.IsNaN(na.Noul) || math.IsInf(na.Noul, 0) || na.Noul < 0.0 || na.Noul > 1.0 {
				out.InvalidAnswers[qID] = fmt.Sprintf("noul %.4f out of finite range [0, 1]", na.Noul)
				continue
			}
			out.Nouls[qID] = na

		case TypeScore:
			scoreRaw, exists := rawMap["score"]
			if !exists || string(scoreRaw) == "null" {
				out.InvalidAnswers[qID] = "missing score in answer"
				continue
			}
			var sa ScoreAnswer
			if err := json.Unmarshal(rawAns, &sa); err != nil {
				out.InvalidAnswers[qID] = "malformed score answer"
				continue
			}
			if math.IsNaN(sa.Score) || math.IsInf(sa.Score, 0) {
				out.InvalidAnswers[qID] = "score is not a finite number"
				continue
			}
			if math.IsNaN(sa.Confidence) || math.IsInf(sa.Confidence, 0) || sa.Confidence < 0.0 || sa.Confidence > 1.0 {
				sa.Confidence = 0.0
			}
			if minVal, maxVal, ok := parseScoreRubric(q); ok {
				if sa.Score < minVal || sa.Score > maxVal {
					out.InvalidAnswers[qID] = fmt.Sprintf("score %.4f out of rubric range [%.1f, %.1f]", sa.Score, minVal, maxVal)
					continue
				}
			}
			out.Scores[qID] = sa
		}
	}

	return out, nil
}

// parseScoreRubric extracts the min and max allowed score from criteria slice or instructions.
func parseScoreRubric(q Question) (min, max float64, ok bool) {
	hasRubric := false
	var minVal, maxVal float64

	switch c := q.Criteria.(type) {
	case []string:
		if len(c) > 0 {
			minVal = 0
			maxVal = float64(len(c) - 1)
			hasRubric = true
		}
	case []any:
		if len(c) > 0 {
			minVal = 0
			maxVal = float64(len(c) - 1)
			hasRubric = true
		}
	}

	if matches := scoreRubricRE.FindStringSubmatch(q.Instructions); len(matches) == 3 {
		pMin, err1 := strconv.ParseFloat(matches[1], 64)
		pMax, err2 := strconv.ParseFloat(matches[2], 64)
		if err1 == nil && err2 == nil && pMin <= pMax {
			minVal = pMin
			maxVal = pMax
			hasRubric = true
		}
	}

	return minVal, maxVal, hasRubric
}
