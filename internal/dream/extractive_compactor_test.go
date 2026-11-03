package dream

import (
	"slices"
	"strings"
	"testing"
)

func TestCompactExtractive_RealWorldTranscript(t *testing.T) {
	rawTranscript := `
# Fix SQLite contention during multi-agent handoff claim

The developer requested an investigation into database contention and lock errors during parallel execution.
We need to ensure multiple concurrent agents do not encounter lock errors when claiming batons.

## User Dialogue & Initial Symptom Report
User: Hey assistant, our CI runner is intermittently throwing errors when running agent swarms in parallel.
Could you check the SQLite store and see why multiple agents fail to claim handoffs concurrently?
It seems like when five or more agents start at once, four of them crash with database lock errors.

Assistant: I'd be happy to investigate this concurrency issue! Let me examine the SQLite transaction logic and analyze the contention.
When multiple agent processes access SQLite concurrently without proper busy timeout handling or using non-atomic select-then-update loops,
the database driver can fail immediately with SQLITE_BUSY. Let me perform a detailed root cause analysis.

## Agent Thoughts & Step-by-Step Analysis
Let me review the codebase to see why we are seeing SQLITE_BUSY errors during parallel task execution.
I will first inspect internal/store/handoffs.go and internal/store/store.go.
When multiple agent swarms run concurrent claims, they invoke ClaimHandoff and PopEligibleHandoff.
We saw exit code 127 in containerized sandboxes because ~/.local/bin was missing from the container PATH.
Also, the worker returned HTTP 502 with ENOSPC when writing temporary files to /tmp instead of the workspace.

Let's check the connection pool settings. SQLite only allows a single concurrent writer in WAL mode unless busy_timeout is set.
If the writer transaction is opened in DEFERRED mode, a read transaction might later attempt to upgrade to a write transaction,
causing immediate deadlock or busy errors if another connection already started writing.
Therefore, we must use IMMEDIATE transactions or an atomic UPDATE ... RETURNING query.

## Execution Logs & Diagnostic Traces
Here are the raw command outputs captured from the terminal run:
Running tests across packages:
=== RUN   TestHandoffClaimContention
    handoffs_test.go:120: database is locked (5) (SQLITE_BUSY)
    handoffs_test.go:125: command exited with exit code 127
--- FAIL: TestHandoffClaimContention (0.45s)
panic: runtime error: index out of range [0] with length 0
goroutine 42 [running]:
main.runHandoff()
    /workspace/sila/cli/cmd/sila/handoff.go:45 +0x12a
    /workspace/sila/cli/cmd/sila/main.go:120 +0x8b

To reproduce this issue, you can run the test suite:
$ go test -race ./internal/store/...

We investigated the issue with commit 78e412a32190 and session b6953258-00a8-4c81-8176-79177ea336f0.
Refer to https://sqlite.org/c3ref/busy_handler.html for upstream SQLite documentation.
Environment variable SILA_ACTIVE_GOAL was not set, causing task_12345 to fail.

## Architectural Recommendation & Next Steps
We recommend the following architectural modifications:
1. Replace two-step SELECT-then-UPDATE with single atomic UPDATE handoffs SET status = 'claimed' WHERE id = ? AND status = 'pending'.
2. Set busy_timeout = 5000ms on all connections to handle transient write locks.
3. Validate all referenced file paths using SafeReadProjectExcerpt before handing off context.
I will now edit the files and run the tests to verify the fix.
Thank you for your patience! Everything is now working smoothly.
`

	result := CompactExtractive(rawTranscript)

	// 1. Verify token reduction > 65%
	if result.SavingsRatio < 0.65 {
		t.Errorf("expected at least 65%% token savings, got %.2f%% (orig: %d, final: %d)",
			result.SavingsRatio*100, result.OriginalTokens, result.FinalTokens)
	}

	// 2. Verify all technical keep-tokens are preserved
	if !slices.Contains(result.Tokens.FilePaths, "internal/store/handoffs.go") {
		t.Errorf("missing path 'internal/store/handoffs.go', got %v", result.Tokens.FilePaths)
	}
	if !slices.Contains(result.Tokens.FilePaths, "internal/store/store.go") {
		t.Errorf("missing path 'internal/store/store.go', got %v", result.Tokens.FilePaths)
	}
	if !slices.Contains(result.Tokens.ErrorCodes, "SQLITE_BUSY") {
		t.Errorf("missing error 'SQLITE_BUSY', got %v", result.Tokens.ErrorCodes)
	}
	if !slices.Contains(result.Tokens.ErrorCodes, "exit code 127") {
		t.Errorf("missing error 'exit code 127', got %v", result.Tokens.ErrorCodes)
	}
	if !slices.Contains(result.Tokens.Symbols, "ClaimHandoff") {
		t.Errorf("missing symbol 'ClaimHandoff', got %v", result.Tokens.Symbols)
	}
	if !slices.Contains(result.Tokens.Symbols, "PopEligibleHandoff") {
		t.Errorf("missing symbol 'PopEligibleHandoff', got %v", result.Tokens.Symbols)
	}
	if !slices.Contains(result.Tokens.Identifiers, "SILA_ACTIVE_GOAL") {
		t.Errorf("missing identifier 'SILA_ACTIVE_GOAL', got %v", result.Tokens.Identifiers)
	}
	if !slices.Contains(result.Tokens.URLs, "https://sqlite.org/c3ref/busy_handler.html") {
		t.Errorf("missing url 'https://sqlite.org/c3ref/busy_handler.html', got %v", result.Tokens.URLs)
	}

	// 3. Verify L0 and L1 structural layers exist
	if !strings.Contains(result.CompactedBody, "L0: Fix SQLite contention during multi-agent handoff claim") {
		t.Errorf("expected L0 title in compacted body, got:\n%s", result.CompactedBody)
	}
	if !strings.Contains(result.CompactedBody, "L1:") {
		t.Errorf("expected L1 intent paragraph in compacted body")
	}
	if !strings.Contains(result.CompactedBody, "L2 (Keep-Tokens):") {
		t.Errorf("expected L2 keep-tokens in compacted body")
	}

	// 4. Verify conversational padding was dropped
	if strings.Contains(result.CompactedBody, "Thinking Process:") {
		t.Errorf("conversational padding 'Thinking Process:' was not stripped")
	}
	if strings.Contains(result.CompactedBody, "Thank you for your patience!") {
		t.Errorf("conversational pleasantry was not stripped")
	}
}

func TestCompactExtractive_Idempotency(t *testing.T) {
	input := `
[Compacted Memory: A2 Zero-LLM Extractive]
L0: Resolve concurrency lock in store
L1: Multiple agents caused lock contention when accessing database.
L2 (Keep-Tokens):
• Paths: internal/store/store.go
• Errors: SQLITE_BUSY
• Symbols: OpenDB
`
	firstPass := CompactExtractive(input)
	secondPass := CompactExtractive(firstPass.CompactedBody)

	if firstPass.CompactedBody != secondPass.CompactedBody {
		t.Fatalf("compaction is not idempotent:\nFirst:\n%s\nSecond:\n%s", firstPass.CompactedBody, secondPass.CompactedBody)
	}

	if secondPass.SavingsRatio != 0.0 {
		t.Errorf("expected 0 savings on already-compacted text, got %f", secondPass.SavingsRatio)
	}
}

func TestCompactExtractive_Empty(t *testing.T) {
	res := CompactExtractive("   ")
	if res.CompactedBody != "" {
		t.Errorf("expected empty output for whitespace input, got %q", res.CompactedBody)
	}
	if res.FinalTokens != 0 {
		t.Errorf("expected 0 tokens, got %d", res.FinalTokens)
	}
}
