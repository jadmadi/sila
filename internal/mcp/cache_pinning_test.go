package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestCachePinningSegregationAndInvariance(t *testing.T) {
	facts := []store.Fact{
		{
			ID:         "fact_pin_1",
			ProjectID:  "proj_pin",
			Claim:      "SQLite is compiled with modernc.org/sqlite without CGO",
			Kind:       "invariant",
			Confidence: "grounded",
			RepoFiles:  []string{"internal/store/store.go"},
			Verified:   true,
		},
		{
			ID:         "fact_pin_2",
			ProjectID:  "proj_pin",
			Claim:      "Strict zero-CGO and pure-Go rule applies to all packages",
			Kind:       "fact",
			Confidence: "grounded",
			RepoFiles:  []string{"AGENTS.md"},
			Verified:   true,
		},
	}

	optsA := CachePromptOptions{
		ProjectID:         "proj_pin",
		SystemContract:    DefaultWorkingContract("proj_pin"),
		IncludeTools:      true,
		CustomStaticRules: "Strict Reference & Spec Fidelity: Zero Rushed implementations.",
		RepoRunbook:       "test: go test -race ./...\nbuild: ./build.sh",
		Facts:             facts,
		ActiveGoalSlug:    "goal-alpha",
		ActiveGoalTitle:   "Alpha Implementation",
		ActiveHandoff: &store.Handoff{
			Summary:   "Turn 1 completed",
			OpenItems: []string{"Task 1", "Task 2"},
			Decisions: []string{"Use pure Go"},
		},
		UserPrompt:         "Implement feature X",
		InsertCacheMarkers: true,
	}

	promptA := BuildCachePinnedPrompt(optsA)

	// 1. Verify Phase 1: Static rules segregated from dynamic context
	if !strings.Contains(promptA.StaticPrefix, "Working Contract (7-Step Loop)") {
		t.Fatalf("expected static prefix to contain working contract")
	}
	if !strings.Contains(promptA.StaticPrefix, "Zero Rushed implementations") {
		t.Fatalf("expected static prefix to contain custom static rules")
	}
	if !strings.Contains(promptA.StaticPrefix, "modernc.org/sqlite") {
		t.Fatalf("expected static prefix to contain verified architectural facts")
	}
	if strings.Contains(promptA.StaticPrefix, "Turn 1 completed") {
		t.Fatalf("static prefix leaked dynamic handoff summary!")
	}
	if strings.Contains(promptA.StaticPrefix, "Implement feature X") {
		t.Fatalf("static prefix leaked dynamic user prompt!")
	}

	// 2. Verify Phase 2: System context and tools are pinned to the top
	sysIdx := strings.Index(promptA.StaticPrefix, "Working Contract")
	toolsIdx := strings.Index(promptA.StaticPrefix, "Available MCP Tools")
	rulesIdx := strings.Index(promptA.StaticPrefix, "Workspace Rules & Invariants")
	factsIdx := strings.Index(promptA.StaticPrefix, "Verified Architectural Facts")

	if sysIdx < 0 || toolsIdx < 0 || rulesIdx < 0 || factsIdx < 0 {
		t.Fatalf("missing required sections in static prefix: sys=%d, tools=%d, rules=%d, facts=%d", sysIdx, toolsIdx, rulesIdx, factsIdx)
	}
	if !(sysIdx < toolsIdx && toolsIdx < rulesIdx && rulesIdx < factsIdx) {
		t.Fatalf("incorrect section pinning order! Expected sys < tools < rules < facts, got %d, %d, %d, %d", sysIdx, toolsIdx, rulesIdx, factsIdx)
	}

	// 3. Verify Dynamic variables are placed in DynamicSuffix
	if !strings.Contains(promptA.DynamicSuffix, "Turn 1 completed") {
		t.Fatalf("dynamic suffix missing handoff summary")
	}
	if !strings.Contains(promptA.DynamicSuffix, "Task 1") {
		t.Fatalf("dynamic suffix missing open task")
	}
	if !strings.Contains(promptA.DynamicSuffix, "Implement feature X") {
		t.Fatalf("dynamic suffix missing user prompt")
	}

	// 4. Verify Cache Pinning Invariance (KV Cache Hit Guarantee):
	// Modifying dynamic handoff, tasks, or user prompt MUST NOT change the StaticPrefix hash!
	optsB := optsA
	optsB.ActiveGoalSlug = "goal-beta"
	optsB.ActiveGoalTitle = "Beta Implementation"
	optsB.ActiveHandoff = &store.Handoff{
		Summary:   "Turn 42 completed with entirely different tasks",
		OpenItems: []string{"Task 99", "Task 100"},
		Decisions: []string{"New decision"},
	}
	optsB.UserPrompt = "Completely new user prompt on turn 43"

	promptB := BuildCachePinnedPrompt(optsB)

	hashA := sha256.Sum256([]byte(promptA.StaticPrefix))
	hashB := sha256.Sum256([]byte(promptB.StaticPrefix))

	if hex.EncodeToString(hashA[:]) != hex.EncodeToString(hashB[:]) {
		t.Fatalf("cache pinning invariance failed! Static prefix changed when only dynamic variables varied:\nA: %s\nB: %s", promptA.StaticPrefix, promptB.StaticPrefix)
	}

	// 5. Verify Metrics
	if promptA.StaticTokens <= 0 || promptA.DynamicTokens <= 0 || promptA.TotalTokens <= 0 {
		t.Fatalf("invalid token metrics: static=%d, dynamic=%d, total=%d", promptA.StaticTokens, promptA.DynamicTokens, promptA.TotalTokens)
	}
	if promptA.EstimatedHitPct <= 0.0 || promptA.EstimatedHitPct > 100.0 {
		t.Fatalf("invalid estimated hit percentage: %f", promptA.EstimatedHitPct)
	}
}

func TestMCPPromptsProtocol(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "prompts_test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	if _, err := st.EnsureProject(dir); err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}

	fact := store.Fact{
		ID:            "fact_proto_1",
		ProjectID:     filepath.Base(dir),
		Claim:         "Gopls AST navigation is integrated into Sila MCP",
		Kind:          "invariant",
		Confidence:    "grounded",
		FileRefsExist: true,
	}
	if err := st.UpsertFact(fact); err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"cache_pinned_session","arguments":{"project":"` + filepath.Base(dir) + `","user_task":"Verify prompt pinning"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"cache_pinned_goal","arguments":{"slug":"test-goal","project":"` + filepath.Base(dir) + `"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"prime_context","arguments":{"project":"` + filepath.Base(dir) + `","cache_pinned":true}}}`,
	}

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	srv := NewServer(st, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatalf("Server.Run failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("expected %d responses, got %d:\n%s", len(requests), len(lines), out.String())
	}

	// 1. Verify initialize response includes prompts capability
	var initResp struct {
		Result struct {
			Capabilities struct {
				Prompts map[string]any `json:"prompts"`
				Tools   map[string]any `json:"tools"`
			} `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("unmarshal init response: %v", err)
	}
	if initResp.Result.Capabilities.Prompts == nil {
		t.Fatalf("expected prompts capability in initialize result, got nil")
	}

	// 2. Verify prompts/list returns registered prompts
	var listResp struct {
		Result struct {
			Prompts []PromptDefinition `json:"prompts"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("unmarshal prompts/list response: %v", err)
	}
	if len(listResp.Result.Prompts) < 2 {
		t.Fatalf("expected at least 2 prompt templates, got %d", len(listResp.Result.Prompts))
	}

	// 3. Verify prompts/get cache_pinned_session
	var getResp struct {
		Result struct {
			Description string          `json:"description"`
			Messages    []PromptMessage `json:"messages"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &getResp); err != nil {
		t.Fatalf("unmarshal prompts/get response: %v", err)
	}
	if len(getResp.Result.Messages) == 0 {
		t.Fatalf("expected prompt messages, got 0")
	}
	msgText := getResp.Result.Messages[0].Content.Text
	if !strings.Contains(msgText, "Working Contract") || !strings.Contains(msgText, "Available MCP Tools") {
		t.Fatalf("pinned prompt missing static working contract or tools: %s", msgText)
	}
	if !strings.Contains(msgText, "Verify prompt pinning") {
		t.Fatalf("pinned prompt missing dynamic user task: %s", msgText)
	}

	// 4. Verify prime_context with cache_pinned: true
	var primeResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[4]), &primeResp); err != nil {
		t.Fatalf("unmarshal prime_context response: %v", err)
	}
	if len(primeResp.Result.Content) == 0 {
		t.Fatalf("expected prime_context content, got 0")
	}
	primeText := primeResp.Result.Content[0].Text
	if !strings.Contains(primeText, CacheBoundaryMarker) {
		t.Fatalf("expected prime_context with cache_pinned: true to contain CacheBoundaryMarker: %s", primeText)
	}
}
