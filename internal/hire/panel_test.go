package hire

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestScrubPayload(t *testing.T) {
	// Set test environment variables
	os.Setenv("USER", "testdev")
	os.Setenv("HOME", "/home/testdev")

	input := `
Working directory: /home/testdev/projects/sila
Encountered token: ghp_1234567890abcdef1234567890abcdef1234
Encountered bearer: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.secret
Encountered key: api_key=sk-1234567890abcdef1234567890abcdef
`
	scrubbed := ScrubPayload(input)

	if strings.Contains(scrubbed, "/home/testdev") {
		t.Errorf("ScrubPayload failed to redact home path: %s", scrubbed)
	}
	if !strings.Contains(scrubbed, "/workspace/projects/sila") {
		t.Errorf("ScrubPayload did not substitute /workspace: %s", scrubbed)
	}
	if strings.Contains(scrubbed, "ghp_1234567890abcdef") {
		t.Errorf("ScrubPayload failed to redact GitHub token: %s", scrubbed)
	}
	if strings.Contains(scrubbed, "sk-1234567890") {
		t.Errorf("ScrubPayload failed to redact API key: %s", scrubbed)
	}
	if !strings.Contains(scrubbed, "<REDACTED") {
		t.Errorf("ScrubPayload expected <REDACTED> markers: %s", scrubbed)
	}
}

func TestSynthesizeJuryResults(t *testing.T) {
	cfg := DefaultPanelConfig()

	responses := map[PanelRole]string{
		RolePlanner: "Reviewing architecture.\nISSUE: Unbounded cache growth in memory\nVERDICT: FAIL",
		RoleCoder:   "Reviewing syntax.\nISSUE: Unbounded cache growth in memory\nISSUE: Missing error wrap\nVERDICT: FAIL",
		RoleAuditor: "Reviewing security.\nISSUE: Unbounded cache growth in memory\nVERDICT: FAIL",
	}

	result := SynthesizeJuryResults(responses, cfg)

	if result.Passed {
		t.Errorf("expected jury to fail with consensus defects, got passed")
	}

	if len(result.ConsensusFindings) != 1 {
		t.Fatalf("expected 1 consensus finding, got %d", len(result.ConsensusFindings))
	}

	if !strings.Contains(result.ConsensusFindings[0].Description, "unbounded cache growth") {
		t.Errorf("expected consensus finding on cache growth, got %s", result.ConsensusFindings[0].Description)
	}

	if len(result.LoneFindings) != 1 {
		t.Errorf("expected 1 lone finding (missing error wrap), got %d", len(result.LoneFindings))
	}
}

func TestRunAdvisoryPanel_OfflineFallback(t *testing.T) {
	// Configure unreachable endpoint
	cfg := &PanelConfig{
		Enabled:         true,
		DefaultEndpoint: "http://127.0.0.1:59999/v1", // Unreachable port
		TimeoutSeconds:  1,
		Roles: map[PanelRole]RoleConfig{
			RolePlanner: {
				Model:    "test-model",
				Endpoint: "http://127.0.0.1:59999/v1",
			},
		},
	}

	ctx := context.Background()
	result, err := RunAdvisoryPanel(ctx, cfg, "Contract summary", "Diff payload")
	if err != nil {
		t.Fatalf("expected graceful fallback, got error: %v", err)
	}

	if !result.OfflineFallback {
		t.Errorf("expected OfflineFallback=true, got %v", result.OfflineFallback)
	}
	if !result.Passed {
		t.Errorf("expected Passed=true on graceful offline fallback, got %v", result.Passed)
	}
}

func TestRunAdvisoryPanel_MockServer(t *testing.T) {
	// Stand up mock OpenAI server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req OpenAIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resp := OpenAIResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{
				{
					Message: struct {
						Content string `json:"content"`
					}{
						Content: "Analysis complete.\nVERDICT: PASS",
					},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &PanelConfig{
		Enabled:         true,
		DefaultEndpoint: server.URL,
		TimeoutSeconds:  5,
		Roles: map[PanelRole]RoleConfig{
			RolePlanner: {Model: "planner-mock", Endpoint: server.URL},
			RoleCoder:   {Model: "coder-mock", Endpoint: server.URL},
			RoleAuditor: {Model: "auditor-mock", Endpoint: server.URL},
		},
	}

	ctx := context.Background()
	result, err := RunAdvisoryPanel(ctx, cfg, "Contract summary", "Diff payload")
	if err != nil {
		t.Fatalf("RunAdvisoryPanel error: %v", err)
	}

	if !result.Passed {
		t.Errorf("expected mock panel to pass, got failed")
	}
	if len(result.ConsensusFindings) != 0 {
		t.Errorf("expected 0 consensus findings, got %d", len(result.ConsensusFindings))
	}
}
