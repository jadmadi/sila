package loaders

import (
	"time"

	"github.com/jadmadi/sila/internal/turnoutcome"
)

// Turn outcome labels for stored assistant messages.
const (
	OutcomeNeutral   = turnoutcome.Neutral
	OutcomeSuccess   = turnoutcome.Success
	OutcomeCorrected = turnoutcome.Corrected
)

// ClassifyTurnOutcome labels the assistant turn that the next prompt responds
// to. Corrected wins over success; a gap over 24 hours or an empty prompt is
// neutral.
func ClassifyTurnOutcome(nextPrompt string, gap time.Duration) string {
	return turnoutcome.Classify(nextPrompt, gap)
}
