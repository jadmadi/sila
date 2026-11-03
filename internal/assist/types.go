package assist

import "context"

// ModelProvider identifies the backend provider.
type ModelProvider string

// ModelProvider constants identify supported backend inference providers.
const (
	ProviderNone     ModelProvider = "none"
	ProviderTypeSafe ModelProvider = "typesafe"
	ProviderMock     ModelProvider = "mock"
)

// QuestionType defines the judgment primitive.
type QuestionType string

// QuestionType constants define the supported judgment primitives.
const (
	TypeChoice QuestionType = "choice"
	TypeNoul   QuestionType = "noul"
	TypeScore  QuestionType = "score"
)

// Question specifies a single typed judgment.
type Question struct {
	Type         QuestionType `json:"type"`
	Instructions string       `json:"instructions"`
	Criteria     any          `json:"criteria,omitempty"`
}

// ChoiceAnswer represents an option selected from a distribution.
type ChoiceAnswer struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// NoulAnswer represents a probability of 'yes'.
type NoulAnswer struct {
	Noul float64 `json:"noul"` // P(yes) in [0, 1]
}

// ScoreAnswer represents expected position on ordered rubrics.
type ScoreAnswer struct {
	Score         float64            `json:"score"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

// TokenUsage records input and output token consumption.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ProviderResponse holds raw or decoded answers from an assistance call.
type ProviderResponse struct {
	Provider       ModelProvider           `json:"provider"`
	Model          string                  `json:"model"`
	RequestedModel string                  `json:"requested_model,omitempty"`
	Choices        map[string]ChoiceAnswer `json:"choices,omitempty"`
	Nouls          map[string]NoulAnswer   `json:"nouls,omitempty"`
	Scores         map[string]ScoreAnswer  `json:"scores,omitempty"`
	InvalidAnswers map[string]string       `json:"invalid_answers,omitempty"`
	Usage          TokenUsage              `json:"usage"`
	Cached         bool                    `json:"cached,omitempty"`
	DurationMs     int64                   `json:"duration_ms"`
}

// Provider defines the clean boundary for external assistance adapters.
type Provider interface {
	Name() ModelProvider
	Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]Question) (*ProviderResponse, error)
}
