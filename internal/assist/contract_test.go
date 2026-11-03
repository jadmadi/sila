package assist_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/assist"
)

func TestContract_MalformedAndPartialResponses(t *testing.T) {
	ctx := context.Background()

	// 1. Malformed JSON response
	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.WriteString(`{ broken-json-payload`)
			return rec.Result(), nil
		}),
	}
	adapter := assist.NewTypeSafeAdapter("test-key", client)
	q := map[string]assist.Question{"q1": {Type: assist.TypeNoul}}
	_, err := adapter.Evaluate(ctx, "", map[string]any{}, q)
	if err == nil || !strings.Contains(err.Error(), "unmarshal response") {
		t.Fatalf("expected unmarshal error on malformed JSON, got %v", err)
	}

	// 2. Partial response with out-of-range noul, missing question, and allowed choice validation
	clientPartial := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(rec).Encode(map[string]any{
				"model": "jev-resolved-1.0",
				"answers": map[string]any{
					"q_out_of_range": map[string]any{"noul": 3.5},
					"q_valid_noul":   map[string]any{"noul": 0.85},
					"q_invalid_choice": map[string]any{
						"choice":     "not_allowed",
						"confidence": 0.9,
					},
					"q_valid_choice": map[string]any{
						"choice":     "allowed_a",
						"confidence": 0.95,
					},
				},
				"usage": map[string]any{"input_tokens": 100, "output_tokens": 50},
			})
			return rec.Result(), nil
		}),
	}
	adapterPartial := assist.NewTypeSafeAdapter("test-key", clientPartial)
	qMulti := map[string]assist.Question{
		"q_out_of_range":   {Type: assist.TypeNoul},
		"q_valid_noul":     {Type: assist.TypeNoul},
		"q_missing":        {Type: assist.TypeNoul},
		"q_invalid_choice": {Type: assist.TypeChoice, Criteria: []string{"allowed_a", "allowed_b"}},
		"q_valid_choice":   {Type: assist.TypeChoice, Criteria: []string{"allowed_a", "allowed_b"}},
	}
	resp, err := adapterPartial.Evaluate(ctx, "requested-alias", map[string]any{}, qMulti)
	if err != nil {
		t.Fatalf("expected successful evaluate with filtered answers, got: %v", err)
	}

	// Verify model identity
	if resp.Model != "jev-resolved-1.0" {
		t.Errorf("expected resolved model jev-resolved-1.0, got %s", resp.Model)
	}
	if resp.RequestedModel != "requested-alias" {
		t.Errorf("expected requested model requested-alias, got %s", resp.RequestedModel)
	}

	// Out of range noul must not be in Nouls
	if _, ok := resp.Nouls["q_out_of_range"]; ok {
		t.Errorf("expected out-of-range noul to be rejected from Nouls")
	}
	// Missing question must not be in Nouls
	if _, ok := resp.Nouls["q_missing"]; ok {
		t.Errorf("expected missing question to be omitted from Nouls")
	}
	// Invalid choice must not be in Choices
	if _, ok := resp.Choices["q_invalid_choice"]; ok {
		t.Errorf("expected disallowed choice to be rejected")
	}

	// Valid answers must be present
	if noul, ok := resp.Nouls["q_valid_noul"]; !ok || noul.Noul != 0.85 {
		t.Errorf("expected valid noul 0.85, got %v", noul)
	}
	if choice, ok := resp.Choices["q_valid_choice"]; !ok || choice.Choice != "allowed_a" {
		t.Errorf("expected valid choice allowed_a, got %v", choice)
	}

	// InvalidAnswers must track the rejections
	if len(resp.InvalidAnswers) < 3 {
		t.Errorf("expected at least 3 invalid answer tracking entries, got %d", len(resp.InvalidAnswers))
	}
}

func TestContract_LimitsAndErrorRedaction(t *testing.T) {
	ctx := context.Background()

	// 1. Question count ceiling
	excessiveQuestions := make(map[string]assist.Question)
	for i := range 55 {
		excessiveQuestions[fmt.Sprintf("q_%d", i)] = assist.Question{Type: assist.TypeNoul}
	}
	adapter := assist.NewTypeSafeAdapter("test-key", nil)
	_, err := adapter.Evaluate(ctx, "", map[string]any{}, excessiveQuestions)
	if err == nil || !strings.Contains(err.Error(), "question count 55 exceeds maximum") {
		t.Fatalf("expected question count limit error, got %v", err)
	}

	// 2. Remote error body redaction: sensitive tokens in error response must NOT leak in err.Error()
	secretLeakBody := `{"error": "Internal database dump", "token": "sk_live_very_secret_key_12345"}`
	clientErr := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusUnauthorized)
			_, _ = rec.WriteString(secretLeakBody)
			return rec.Result(), nil
		}),
	}
	adapterErr := assist.NewTypeSafeAdapter("test-key", clientErr)
	_, err = adapterErr.Evaluate(ctx, "", map[string]any{}, map[string]assist.Question{"q": {Type: assist.TypeNoul}})
	if err == nil {
		t.Fatalf("expected HTTP error")
	}
	if strings.Contains(err.Error(), "sk_live") || strings.Contains(err.Error(), "Internal database dump") {
		t.Fatalf("remote error body was not redacted; leaked secret in error: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("expected HTTP 401 status to be reported, got: %v", err)
	}

	// 3. Negative / zero spend limit must be rejected
	adapterZeroCap := assist.NewTypeSafeAdapter("test-key", nil)
	adapterZeroCap.SpendLimit = 0
	_, err = adapterZeroCap.Evaluate(ctx, "", map[string]any{}, map[string]assist.Question{"q": {Type: assist.TypeNoul}})
	if err == nil || !strings.Contains(err.Error(), "spend limit must be a positive number") {
		t.Fatalf("expected error on non-positive spend limit, got %v", err)
	}

	// 4. Excessive response size (> 2 MB)
	clientHuge := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			_, _ = rec.Write(make([]byte, 2*1024*1024+100))
			return rec.Result(), nil
		}),
	}
	adapterHuge := assist.NewTypeSafeAdapter("test-key", clientHuge)
	_, err = adapterHuge.Evaluate(ctx, "", map[string]any{}, map[string]assist.Question{"q": {Type: assist.TypeNoul}})
	if err == nil || !strings.Contains(err.Error(), "response size exceeds maximum") {
		t.Fatalf("expected response size limit error, got %v", err)
	}
}

func TestContract_ConcurrentSpendReservationAndOverrun(t *testing.T) {
	ctx := context.Background()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			time.Sleep(10 * time.Millisecond) // simulate RTT
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(rec).Encode(map[string]any{
				"model": "jev-concurrency-test",
				"answers": map[string]any{
					"q": map[string]any{"noul": 0.5},
				},
				"usage": map[string]any{"input_tokens": 1000, "output_tokens": 10},
			})
			return rec.Result(), nil
		}),
	}

	adapter := assist.NewTypeSafeAdapter("test-key", client)
	adapter.SpendLimit = 0.005 // ~$0.005 cap

	var wg sync.WaitGroup
	errCount := 0
	var mu sync.Mutex

	// Fire 20 parallel requests concurrently to verify thread safety and reservation
	for range 20 {
		wg.Go(func() {
			_, err := adapter.Evaluate(ctx, "", map[string]any{"payload": "state data"}, map[string]assist.Question{"q": {Type: assist.TypeNoul}})
			if err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	// Total spend must never exceed the spend limit without surfacing errors
	if adapter.TotalSpent > adapter.SpendLimit && errCount == 0 {
		t.Fatalf("spend cap exceeded without error: spent $%.6f cap $%.6f", adapter.TotalSpent, adapter.SpendLimit)
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
