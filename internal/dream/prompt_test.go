package dream

import (
	"strings"
	"testing"
)

func TestBuildDistillPrompt(t *testing.T) {
	req := DistillRequest{
		ClaimID:    "fact_123",
		ProjectID:  "example-app",
		RawMessage: "Done. Fixed issue 2 in auth/tokens.ts by setting refresh token expiry to 7d.",
		RepoFiles:  []string{"auth/tokens.ts"},
		Tools:      []string{"devin", "mimocode"},
		SessionRef: "s1",
	}

	prompt := BuildDistillPrompt(req)
	if !strings.Contains(prompt, "example-app") {
		t.Fatalf("prompt missing project ID: %s", prompt)
	}
	if !strings.Contains(prompt, "auth/tokens.ts") {
		t.Fatalf("prompt missing repo files: %s", prompt)
	}
	if !strings.Contains(prompt, "devin, mimocode") {
		t.Fatalf("prompt missing tools: %s", prompt)
	}
	if !strings.Contains(prompt, "Fixed issue 2 in auth/tokens.ts") {
		t.Fatalf("prompt missing raw message: %s", prompt)
	}
}

func TestParseDistillResponse(t *testing.T) {
	rawJSON := `{"fact": "auth/tokens.ts sets refresh token expiry to 7 days.", "confidence": "high", "reasoning": "Explicit config in auth/tokens.ts"}`
	res, err := ParseDistillResponse(rawJSON)
	if err != nil {
		t.Fatalf("ParseDistillResponse failed: %v", err)
	}
	if res.Status != "distilled" || res.Fact != "auth/tokens.ts sets refresh token expiry to 7 days." {
		t.Fatalf("unexpected parsed result: %+v", res)
	}

	// Code block wrapped JSON
	markdownJSON := "```json\n" + rawJSON + "\n```"
	resMd, err := ParseDistillResponse(markdownJSON)
	if err != nil {
		t.Fatalf("ParseDistillResponse markdown failed: %v", err)
	}
	if resMd.Fact != res.Fact {
		t.Fatalf("expected markdown stripped fact %q, got %q", res.Fact, resMd.Fact)
	}

	// Empty fact -> status skipped
	emptyJSON := `{"fact": "", "confidence": "low", "reasoning": "no factual claim"}`
	resEmpty, err := ParseDistillResponse(emptyJSON)
	if err != nil {
		t.Fatalf("ParseDistillResponse empty failed: %v", err)
	}
	if resEmpty.Status != "skipped" {
		t.Fatalf("expected status=skipped for empty fact, got %s", resEmpty.Status)
	}
}
