package dream

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// FailureNudge is the bounded recall suggestion emitted after a failed tool call.
type FailureNudge struct {
	Schema  string   `json:"schema"`
	Query   string   `json:"query"`
	Matches []string `json:"matches"`
	Hint    string   `json:"hint"`
	Bytes   int      `json:"bytes"`
}

// BuildFailureNudge returns nil when the payload is not a failure. It never
// blocks or retries the failed command.
func BuildFailureNudge(st *store.Store, projectID string, payload map[string]any, max int) *FailureNudge {
	if st == nil || payload == nil || !payloadFailed(payload) {
		return nil
	}
	if max <= 0 || max > 3 {
		max = 3
	}
	command := stringValue(payload["command"])
	output := stringValue(payload["output"])
	errorLine := firstErrorLine(output)
	query := strings.TrimSpace(strings.Join([]string{command, errorLine}, " "))
	if len(query) > 200 {
		query = query[:200]
	}
	if query == "" {
		return nil
	}

	nudge := &FailureNudge{
		Schema:  "sila/failure-nudge/v1",
		Query:   query,
		Matches: []string{},
		Hint:    fmt.Sprintf("sila recall %q", query),
	}
	res, err := RecallSolutions(st, query, RecallOptions{Project: projectID, Limit: max})
	if err == nil && res != nil {
		for _, hit := range res.Hits {
			if len(nudge.Matches) >= max {
				break
			}
			line := strings.TrimSpace(hit.Kind + ": " + hit.Resolution)
			if len(line) > 120 {
				line = line[:120]
			}
			nudge.Matches = append(nudge.Matches, line)
		}
	}
	// Keep the human rendering under the 512-byte budget.
	for len(nudge.Matches) > 0 && nudgeSize(nudge) > 512 {
		nudge.Matches = nudge.Matches[:len(nudge.Matches)-1]
	}
	nudge.Bytes = nudgeSize(nudge)
	return nudge
}

func nudgeSize(n *FailureNudge) int {
	b, err := json.Marshal(n)
	if err != nil {
		return 0
	}
	return len(b)
}

func payloadFailed(payload map[string]any) bool {
	switch v := payload["exit_code"].(type) {
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case json.Number:
		return v.String() != "0"
	case string:
		return strings.TrimSpace(v) != "" && strings.TrimSpace(v) != "0"
	default:
		return false
	}
}

func stringValue(v any) string {
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

func firstErrorLine(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		for _, marker := range []string{"error", "failed", "failure", "cannot", "undefined", "not found", "denied", "panic"} {
			if strings.Contains(lower, marker) {
				return line
			}
		}
	}
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
