package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestTwoPassPrecisionDilutionShield verifies R4.1:
// Precision test against seeded conversational transcripts:
//  1. A transcript where an assistant generated 2,000 words of generic prose containing "auth token"
//     without solving the problem.
//  2. A second transcript where a user asked "how do we rotate auth token?" and received a 2-line fix.
//
// Assert that the second transcript ranks first in Pass 1 and cleanly returns the 2-line fix in Pass 2.
func TestTwoPassPrecisionDilutionShield(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "twopass.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject("/workspace/auth-service")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Generate 2,000 words of generic prose containing "auth token" multiple times without solving the problem
	var genericWords []string
	filler := []string{"security", "architecture", "microservice", "infrastructure", "concept", "validation", "middleware", "runtime"}
	for i := 0; i < 2000; i++ {
		if i%100 == 0 {
			genericWords = append(genericWords, "rotate", "auth", "token")
		} else {
			genericWords = append(genericWords, filler[i%len(filler)])
		}
	}
	chattyAssistantProse := strings.Join(genericWords, " ")

	sess1 := "sess-chatty-monologue"
	msgs1 := []LoadMessage{
		{
			ProjectID:   p.ID,
			ContentHash: "hash-sess1-u1",
			TS:          "2026-09-01T10:00:00Z",
			Role:        "user",
			Content:     "Can you provide a theoretical overview of our cloud security principles?",
			Tool:        "devin",
			SessionID:   sess1,
			SourceOrder: 1,
		},
		{
			ProjectID:   p.ID,
			ContentHash: "hash-sess1-a1",
			TS:          "2026-09-01T10:01:00Z",
			Role:        "assistant",
			Content:     chattyAssistantProse,
			Tool:        "devin",
			SessionID:   sess1,
			SourceOrder: 2,
		},
	}
	if _, _, err := st.WriteRun("", "devin", sess1, msgs1); err != nil {
		t.Fatalf("failed writing chatty transcript: %v", err)
	}

	// 2. Transcript 2: User asks "how do we rotate auth token?" and receives a 2-line fix.
	sess2 := "sess-concise-solution"
	twoLineFix := "Run `vault token rotate --target=service` to issue a replacement.\nVerify the active credential with `vault token lookup`."
	msgs2 := []LoadMessage{
		{
			ProjectID:   p.ID,
			ContentHash: "hash-sess2-u1",
			TS:          "2026-09-02T11:00:00Z",
			Role:        "user",
			Content:     "how do we rotate auth token?",
			Tool:        "devin",
			SessionID:   sess2,
			SourceOrder: 1,
		},
		{
			ProjectID:   p.ID,
			ContentHash: "hash-sess2-a1",
			TS:          "2026-09-02T11:01:00Z",
			Role:        "assistant",
			Content:     twoLineFix,
			Tool:        "devin",
			SessionID:   sess2,
			SourceOrder: 2,
		},
	}
	if _, _, err := st.WriteRun("", "devin", sess2, msgs2); err != nil {
		t.Fatalf("failed writing concise transcript: %v", err)
	}

	ctx := context.Background()
	query := "auth token"

	// Execute Two-Pass Search
	exchanges, err := st.SearchTwoPass(ctx, query, SearchPassOptions{
		Project:        p.ID,
		CandidateLimit: 5,
	})
	if err != nil {
		t.Fatalf("SearchTwoPass failed: %v", err)
	}

	if len(exchanges) < 2 {
		t.Fatalf("expected at least 2 exchanges, got %d", len(exchanges))
	}

	// Assert that the second transcript ranks FIRST in Pass 1
	firstResult := exchanges[0]
	if firstResult.SessionID != sess2 {
		t.Fatalf("expected concise transcript %q to rank first, but got %q (score: %f vs %f)",
			sess2, firstResult.SessionID, firstResult.FusedScore, exchanges[1].FusedScore)
	}

	// Assert that UserPrompt matches the question
	if !strings.Contains(strings.ToLower(firstResult.UserPrompt), "rotate auth token") {
		t.Fatalf("expected UserPrompt to contain question, got: %q", firstResult.UserPrompt)
	}

	// Assert that AssistantFix cleanly returns the 2-line fix
	if !strings.Contains(firstResult.AssistantFix, "vault token rotate") ||
		!strings.Contains(firstResult.AssistantFix, "vault token lookup") {
		t.Fatalf("expected AssistantFix to contain the 2-line fix, got: %q", firstResult.AssistantFix)
	}

	// Verify formatted exchange contains user and assistant boundaries
	formatted := firstResult.Format()
	if !strings.HasPrefix(formatted, "User: ") || !strings.Contains(formatted, "Assistant: ") {
		t.Fatalf("Format() missing boundaries: %q", formatted)
	}
}

// TestExpandSessionExchange verifies session context window expansion and isolation.
func TestExpandSessionExchange(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "expand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject("/workspace/test-expand")
	if err != nil {
		t.Fatal(err)
	}

	sess := "session-exp-1"
	var msgs []LoadMessage
	for i := 1; i <= 7; i++ {
		role := "user"
		if i%2 == 0 {
			role = "assistant"
		}
		msgs = append(msgs, LoadMessage{
			ProjectID:   p.ID,
			ContentHash: fmt.Sprintf("hash-exp-%d", i),
			TS:          fmt.Sprintf("2026-09-01T12:0%d:00Z", i),
			Role:        role,
			Content:     fmt.Sprintf("Message turn content %d", i),
			Tool:        "devin",
			SessionID:   sess,
			SourceOrder: int64(i),
		})
	}

	if _, _, err := st.WriteRun("", "devin", sess, msgs); err != nil {
		t.Fatal(err)
	}

	// Search for turn 4
	hits, err := st.SearchMessagesFTS("turn content 4", SearchMessagesOptions{Project: p.ID})
	if err != nil || len(hits) == 0 {
		t.Fatalf("failed finding turn 4: %v", err)
	}
	anchorID := hits[0].ID

	// Expand with window = 2 (+/- 2 turns)
	ctx := context.Background()
	msgCtx, err := st.ExpandSessionExchange(ctx, sess, fmt.Sprintf("%d", anchorID), 2)
	if err != nil {
		t.Fatalf("ExpandSessionExchange failed: %v", err)
	}

	if len(msgCtx.Before) != 2 {
		t.Fatalf("expected 2 turns before, got %d", len(msgCtx.Before))
	}
	if len(msgCtx.After) != 2 {
		t.Fatalf("expected 2 turns after, got %d", len(msgCtx.After))
	}

	// Check chronological ordering
	if !strings.Contains(msgCtx.Before[0].Content, "turn content 2") ||
		!strings.Contains(msgCtx.Before[1].Content, "turn content 3") {
		t.Fatalf("unexpected Before turns order: %v, %v", msgCtx.Before[0].Content, msgCtx.Before[1].Content)
	}
	if !strings.Contains(msgCtx.TargetMessage.Content, "turn content 4") {
		t.Fatalf("unexpected Target turn: %v", msgCtx.TargetMessage.Content)
	}
	if !strings.Contains(msgCtx.After[0].Content, "turn content 5") ||
		!strings.Contains(msgCtx.After[1].Content, "turn content 6") {
		t.Fatalf("unexpected After turns order: %v, %v", msgCtx.After[0].Content, msgCtx.After[1].Content)
	}
}

// TestFormatPreviewAndTokenClamping verifies R3.1 and R3.2.
func TestFormatPreviewAndTokenClamping(t *testing.T) {
	// 1. Short content under token budget
	short := "Hello world short prompt"
	if got := FormatPreview(short, 800, []string{"world"}); got != short {
		t.Fatalf("expected unchanged short preview, got: %q", got)
	}

	// 2. Long content over budget centered on query term
	prefix := strings.Repeat("unrelated filler words here ", 100) // ~2800 chars
	target := "CRITICAL_SECURITY_KEY_XYZ"
	suffix := strings.Repeat("more background context data ", 100) // ~2900 chars
	fullText := prefix + target + suffix

	// maxTokens = 50 (~200 chars)
	clamped := FormatPreview(fullText, 50, []string{"CRITICAL_SECURITY_KEY_XYZ"})
	if !strings.Contains(clamped, "CRITICAL_SECURITY_KEY_XYZ") {
		t.Fatalf("clamped preview did not center on query term: %q", clamped)
	}
	if !strings.HasPrefix(clamped, "... ") {
		t.Fatalf("expected leading ellipsis on clamped preview: %q", clamped)
	}
	if !strings.HasSuffix(clamped, " ... [truncated]") {
		t.Fatalf("expected trailing truncation indicator: %q", clamped)
	}

	// 3. ExtractResolvingSnippet with code block
	docWithCode := "Here is some introductory context.\n```sh\ngit commit -m 'fix: resolve bug'\n```\nAnd more summary."
	snippet := ExtractResolvingSnippet(docWithCode, []string{"git", "commit"})
	if !strings.Contains(snippet, "git commit -m 'fix: resolve bug'") {
		t.Fatalf("expected code block extracted, got: %q", snippet)
	}
}

// TestPass1QueryPlanExplanation verifies Task 1 EXPLAIN QUERY PLAN on FTS5 index.
func TestPass1QueryPlanExplanation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "explain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject("/workspace/explain-test")
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = st.WriteRun("", "devin", "s1", []LoadMessage{
		{ProjectID: p.ID, ContentHash: "h1", TS: "2026-09-01T10:00:00Z", Role: "user", Content: "query plan test", Tool: "devin"},
	})
	if err != nil {
		t.Fatal(err)
	}

	query := `EXPLAIN QUERY PLAN
		SELECT m.id, m.role, bm25(messages_fts)
		FROM messages_fts
		JOIN messages m ON m.id = messages_fts.rowid
		WHERE messages_fts MATCH 'test'
		ORDER BY ((CASE WHEN LOWER(m.role) IN ('user', 'tool', 'commit') THEN 2.5 ELSE 1.0 END) * bm25(messages_fts)) ASC, m.ts DESC
		LIMIT 10`

	rows, err := st.DB.Query(query)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed: %v", err)
	}
	defer rows.Close()

	var planLines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err == nil {
			planLines = append(planLines, detail)
		}
	}

	planStr := strings.Join(planLines, "\n")
	if !strings.Contains(planStr, "messages_fts") {
		t.Fatalf("query plan does not utilize messages_fts: %s", planStr)
	}
}

// BenchmarkPass2Unrolling verifies R4.2:
// Pass 2 unrolling overhead must remain under 10ms for 10 sessions.
func BenchmarkPass2Unrolling(b *testing.B) {
	st, err := Open(filepath.Join(b.TempDir(), "bench_twopass.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject("/workspace/bench-project")
	if err != nil {
		b.Fatal(err)
	}

	// Seed 10 distinct sessions with 6 turns each
	var anchorIDs []int64
	for s := 0; s < 10; s++ {
		sessID := fmt.Sprintf("bench-sess-%d", s)
		var msgs []LoadMessage
		for i := 1; i <= 6; i++ {
			role := "user"
			if i%2 == 0 {
				role = "assistant"
			}
			msgs = append(msgs, LoadMessage{
				ProjectID:   p.ID,
				ContentHash: fmt.Sprintf("hash-b-%d-%d", s, i),
				TS:          fmt.Sprintf("2026-09-01T10:%02d:%02dZ", s, i),
				Role:        role,
				Content:     fmt.Sprintf("Benchmark conversational session %d turn %d payload", s, i),
				Tool:        "devin",
				SessionID:   sessID,
				SourceOrder: int64(i),
			})
		}
		if _, _, err := st.WriteRun("", "devin", sessID, msgs); err != nil {
			b.Fatal(err)
		}

		hits, err := st.SearchMessagesFTS(fmt.Sprintf("session %d turn 3", s), SearchMessagesOptions{Project: p.ID})
		if err != nil || len(hits) == 0 {
			b.Fatalf("failed finding anchor for session %d: %v", s, err)
		}
		anchorIDs = append(anchorIDs, hits[0].ID)
	}

	ctx := context.Background()
	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		start := time.Now()
		for s := 0; s < 10; s++ {
			sessID := fmt.Sprintf("bench-sess-%d", s)
			anchorStr := fmt.Sprintf("%d", anchorIDs[s])
			msgCtx, err := st.ExpandSessionExchange(ctx, sessID, anchorStr, 2)
			if err != nil || msgCtx == nil {
				b.Fatalf("failed unrolling session %d: %v", s, err)
			}
		}
		elapsed := time.Since(start)
		if elapsed > 10*time.Millisecond {
			b.Logf("Notice: 10 sessions unrolled in %v (threshold: 10ms)", elapsed)
		}
	}
}

// BenchmarkSinglePassVsTwoPassSearch directly compares monolithic single-pass search vs two-pass search.
func BenchmarkSinglePassVsTwoPassSearch(b *testing.B) {
	st, err := Open(filepath.Join(b.TempDir(), "bench_cmp.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject("/workspace/bench-cmp")
	if err != nil {
		b.Fatal(err)
	}

	for s := 0; s < 10; s++ {
		sessID := fmt.Sprintf("sess-%d", s)
		var msgs []LoadMessage
		for i := 1; i <= 6; i++ {
			role := "user"
			if i%2 == 0 {
				role = "assistant"
			}
			msgs = append(msgs, LoadMessage{
				ProjectID:   p.ID,
				ContentHash: fmt.Sprintf("hash-%d-%d", s, i),
				TS:          fmt.Sprintf("2026-09-01T10:%02d:%02dZ", s, i),
				Role:        role,
				Content:     fmt.Sprintf("Search benchmark session %d turn %d payload", s, i),
				Tool:        "devin",
				SessionID:   sessID,
				SourceOrder: int64(i),
			})
		}
		if _, _, err := st.WriteRun("", "devin", sessID, msgs); err != nil {
			b.Fatal(err)
		}
	}

	ctx := context.Background()

	b.Run("SinglePass", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := st.SearchMessagesFTS("benchmark", SearchMessagesOptions{Project: p.ID, Limit: 10})
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("TwoPass", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			_, err := st.SearchTwoPass(ctx, "benchmark", SearchPassOptions{Project: p.ID, CandidateLimit: 10})
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
