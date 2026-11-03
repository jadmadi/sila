package dream

import (
	"encoding/json"
	"fmt"
	"strings"
)

// DistillRequest is the payload sent to the LLM for claim distillation.
type DistillRequest struct {
	ClaimID    string   `json:"claim_id"`
	ProjectID  string   `json:"project_id"`
	RawMessage string   `json:"raw_message"`
	RepoFiles  []string `json:"repo_files"`
	Tools      []string `json:"tools"`
	SessionRef string   `json:"session_ref"`
}

// DistillResult is the structured response expected from the LLM.
type DistillResult struct {
	ClaimID    string `json:"claim_id,omitempty"`
	Fact       string `json:"fact"`                 // one concise factual sentence (<= 200 chars)
	Confidence string `json:"confidence,omitempty"` // low | mid | high
	Reasoning  string `json:"reasoning,omitempty"`  // brief explanation (<= 140 chars)
	Status     string `json:"status,omitempty"`     // distilled | skipped | failed
}

// BuildDistillPrompt constructs the distillation prompt following strict knowledge-grounding rules.
func BuildDistillPrompt(req DistillRequest) string {
	var b strings.Builder
	b.WriteString("You are a codebase knowledge distiller. Your job is to turn raw AI assistant conversation messages into atomic, permanent codebase facts.\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("1. State ONLY what the message asserts about the listed files. Do NOT invent actions or files.\n")
	b.WriteString("2. If the message is a status announcement (\"Done.\", \"Fixed issue\", \"Tests pass\"), summarize the underlying technical change, NOT the announcement.\n")
	b.WriteString("3. If the message contains no durable architectural/code fact, return fact as empty string \"\" (will be skipped).\n")
	b.WriteString("4. Output exactly ONE single JSON object with this schema: {\"fact\": \"...\", \"confidence\": \"high|mid|low\", \"reasoning\": \"...\"}\n\n")
	b.WriteString(fmt.Sprintf("Project: %s\n", req.ProjectID))
	b.WriteString(fmt.Sprintf("Referenced Files: %s\n", strings.Join(req.RepoFiles, ", ")))
	b.WriteString(fmt.Sprintf("Contributing Tools: %s\n", strings.Join(req.Tools, ", ")))
	b.WriteString(fmt.Sprintf("Raw Message:\n%s\n", req.RawMessage))
	return b.String()
}

// ParseDistillResponse parses the LLM output into a DistillResult.
func ParseDistillResponse(raw string) (DistillResult, error) {
	raw = strings.TrimSpace(raw)
	// If markdown code block wrapped, strip it
	if after, ok := strings.CutPrefix(raw, "```json"); ok {
		raw = after
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	} else if after, ok := strings.CutPrefix(raw, "```"); ok {
		raw = after
		raw = strings.TrimSuffix(raw, "```")
		raw = strings.TrimSpace(raw)
	}
	var res DistillResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return res, fmt.Errorf("invalid json in distill response: %w", err)
	}
	res.Fact = strings.TrimSpace(res.Fact)
	if len(res.Fact) > 200 {
		res.Fact = res.Fact[:200]
	}
	if res.Fact == "" {
		res.Status = "skipped"
	} else {
		res.Status = "distilled"
	}
	return res, nil
}
