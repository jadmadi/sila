package assist

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MockAdapter provides offline, zero-network deterministic simulation of Jev judgments for tests.
type MockAdapter struct {
	SimulatedModel string
	LatenciesMs    int64
	SpendTracker   float64
}

// NewMockAdapter initializes a mock adapter for offline evaluation.
func NewMockAdapter() *MockAdapter {
	return &MockAdapter{
		SimulatedModel: "jev-mock-offline",
		LatenciesMs:    5,
	}
}

// Name returns the provider identifier for MockAdapter.
func (m *MockAdapter) Name() ModelProvider {
	return ProviderMock
}

// Evaluate executes offline simulated evaluation against test questions.
func (m *MockAdapter) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]Question) (*ProviderResponse, error) {
	if model == "" {
		model = m.SimulatedModel
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Duration(m.LatenciesMs) * time.Millisecond):
	}

	out := &ProviderResponse{
		Provider: ProviderMock,
		Model:    model,
		Choices:  make(map[string]ChoiceAnswer),
		Nouls:    make(map[string]NoulAnswer),
		Scores:   make(map[string]ScoreAnswer),
		Usage: TokenUsage{
			InputTokens:  120 * len(questions),
			OutputTokens: 0,
		},
		Cached:     false,
		DurationMs: m.LatenciesMs,
	}

	stateDump := fmt.Sprintf("%v", state)

	for qID, q := range questions {
		instrLower := strings.ToLower(q.Instructions)
		switch q.Type {
		case TypeChoice:
			choice := "unknown"
			// Deterministic heuristic based on criteria presence
			if critMap, ok := q.Criteria.(map[string]string); ok {
				for opt := range critMap {
					if strings.Contains(strings.ToLower(stateDump), strings.ToLower(opt)) {
						choice = opt
						break
					}
				}
				if choice == "unknown" {
					for opt := range critMap {
						choice = opt
						break
					}
				}
			}
			out.Choices[qID] = ChoiceAnswer{
				Choice: choice,
				Probabilities: map[string]float64{
					choice: 0.90,
				},
				Confidence: 0.85,
			}
		case TypeNoul:
			prob := 0.10
			stateLower := strings.ToLower(stateDump)
			if strings.Contains(stateLower, "ignore previous instructions") || strings.Contains(stateLower, "system override") {
				prob = 0.05
			} else if (strings.Contains(instrLower, "durable") || strings.Contains(instrLower, "decision")) &&
				(strings.Contains(stateLower, "decision") || strings.Contains(stateLower, "fix") || strings.Contains(stateLower, "pragma") || strings.Contains(stateLower, "wal") || strings.Contains(stateLower, "refactor") || strings.Contains(stateLower, "invariant") || strings.Contains(stateLower, "standard") || strings.Contains(stateLower, "قرار") || strings.Contains(stateLower, "حل") || strings.Contains(stateLower, "تفعيل") || strings.Contains(stateLower, "هيكلة") || strings.Contains(stateLower, "معيار") || strings.Contains(stateLower, "ثابت")) {
				prob = 0.92
			} else if strings.Contains(instrLower, "blocker") &&
				(strings.Contains(stateLower, "blocker") || strings.Contains(stateLower, "failure") || strings.Contains(stateLower, "deadlock") || strings.Contains(stateLower, "locked error") || strings.Contains(stateLower, "crash") || strings.Contains(stateLower, "outage") || strings.Contains(stateLower, "عطل") || strings.Contains(stateLower, "قفل")) {
				prob = 0.85
			} else if strings.Contains(instrLower, "security") &&
				!strings.Contains(stateLower, "example_token") && !strings.Contains(stateLower, "placeholder") &&
				(strings.Contains(stateLower, "token") || strings.Contains(stateLower, "secret") || strings.Contains(stateLower, "sk_live") || strings.Contains(stateLower, "ghp_") || strings.Contains(stateLower, "private key") || strings.Contains(stateLower, "password") || strings.Contains(stateLower, "مفتاح") || strings.Contains(stateLower, "سري") || strings.Contains(stateLower, "credential")) {
				prob = 0.95
			} else if (strings.Contains(instrLower, "drift") || strings.Contains(instrLower, "violation") || strings.Contains(instrLower, "contradict")) &&
				(strings.Contains(stateLower, "violation") || strings.Contains(stateLower, "cgo") || strings.Contains(stateLower, "mattn") || strings.Contains(stateLower, "violates") || strings.Contains(stateLower, "مخالفة") || strings.Contains(stateLower, "تعارض")) {
				prob = 0.88
			} else if strings.Contains(instrLower, "relevance") && strings.Contains(stateLower, "relevant") {
				prob = 0.89
			} else if strings.Contains(instrLower, "substantiate") || strings.Contains(instrLower, "contradict") {
				targetDump := stateDump
				for k := range 50 {
					fKey := fmt.Sprintf("f%02d", k)
					if strings.Contains(instrLower, fKey) {
						if fm, ok := state["facts"].(map[string]map[string]string); ok {
							if subMap, found := fm[fKey]; found {
								targetDump = fmt.Sprintf("%v", subMap)
								break
							}
						}
					}
				}
				if strings.Contains(instrLower, "substantiate") {
					if strings.Contains(targetDump, "contradict") {
						prob = 0.05
					} else {
						prob = 0.88
					}
				} else {
					if strings.Contains(targetDump, "contradict") {
						prob = 0.91
					} else {
						prob = 0.05
					}
				}
			}
			out.Nouls[qID] = NoulAnswer{Noul: prob}
		case TypeScore:
			score := 1.0
			if strings.Contains(stateDump, "critical") || strings.Contains(stateDump, "invariant") {
				score = 3.0
			}
			out.Scores[qID] = ScoreAnswer{
				Score:      score,
				Confidence: 0.80,
			}
		}
	}

	return out, nil
}
