package turnoutcome

import (
	"strings"
	"time"
)

// Turn outcome labels for stored assistant messages.
const (
	Neutral   = "neutral"
	Success   = "success"
	Corrected = "corrected"
)

var (
	correctedPatterns = []string{
		"no wait", "that broke", "broke it", "not right", "that's not", "thats not",
		"didn't work", "did not work", "doesn't work", "does not work", "wrong",
		"revert", "undo", "actually no", "still broken", "stop", "fix that",
	}
	successPatterns = []string{
		"perfect", "thanks", "thank you", "great", "ship it", "works", "working",
		"looks good", "nice", "done", "lgtm", "excellent",
	}
)

// Classify labels the assistant turn that the next prompt responds to.
// Corrected wins over success; a gap over 24 hours or an empty prompt is
// neutral.
func Classify(nextPrompt string, gap time.Duration) string {
	if gap > 24*time.Hour {
		return Neutral
	}
	text := strings.ToLower(strings.TrimSpace(nextPrompt))
	if text == "" {
		return Neutral
	}
	runes := []rune(text)
	if len(runes) > 200 {
		text = string(runes[:200])
	}
	for _, pattern := range correctedPatterns {
		if strings.Contains(text, pattern) {
			return Corrected
		}
	}
	for _, pattern := range successPatterns {
		if strings.Contains(text, pattern) {
			return Success
		}
	}
	return Neutral
}
