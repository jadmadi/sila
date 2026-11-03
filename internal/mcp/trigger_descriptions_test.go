package mcp

import (
	"strings"
	"testing"
)

func TestMemoryToolDescriptionsContainTriggerPhrases(t *testing.T) {
	required := map[string]string{
		"prime_context": "Call this ONCE at the start of a session",
		"save_memo":     "Call immediately when",
		"save_lesson":   "Call this the moment a fix is proven",
		"save_handoff":  "Call before ending a session",
		"recall":        "Use it when a command fails",
	}
	descriptions := map[string]string{}
	for _, tool := range ToolDefinitions() {
		descriptions[tool.Name] = tool.Description
	}
	for name, phrase := range required {
		if !strings.Contains(descriptions[name], phrase) {
			t.Fatalf("%s description is missing %q: %s", name, phrase, descriptions[name])
		}
	}
}
