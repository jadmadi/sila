package assist

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

func TestMockAdapterOffline(t *testing.T) {
	ctx := context.Background()
	adapter := NewMockAdapter()

	state := map[string]any{
		"message": "Encountered critical secret token leak sk_live_test",
	}

	questions := map[string]Question{
		"security": {
			Type:         TypeNoul,
			Instructions: "Does this message report a security leak?",
		},
		"severity": {
			Type:         TypeScore,
			Instructions: "Score the severity from 0 to 3",
		},
	}

	resp, err := adapter.Evaluate(ctx, "", state, questions)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Nouls["security"].Noul < 0.5 {
		t.Errorf("expected security noul >= 0.5, got %f", resp.Nouls["security"].Noul)
	}

	if resp.Scores["severity"].Score < 2.0 {
		t.Errorf("expected severity score >= 2.0, got %f", resp.Scores["severity"].Score)
	}
}

func TestTypeSafeAdapterWithFakeHTTP(t *testing.T) {
	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"answers": {
				"durable": {
					"type": "noul",
					"noul": 0.94
				}
			},
			"usage": {
				"input_tokens": 150,
				"output_tokens": 0
			}
		}`))
	}))
	defer fakeServer.Close()

	ctx := context.Background()
	adapter := NewTypeSafeAdapter("test-key", fakeServer.Client())
	adapter.Endpoint = fakeServer.URL

	resp, err := adapter.Evaluate(ctx, "jev-latest", map[string]any{"data": "test"}, map[string]Question{
		"durable": {
			Type:         TypeNoul,
			Instructions: "Is this durable?",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Nouls["durable"].Noul != 0.94 {
		t.Errorf("expected noul 0.94, got %f", resp.Nouls["durable"].Noul)
	}
	if resp.Usage.InputTokens != 150 {
		t.Errorf("expected 150 input tokens, got %d", resp.Usage.InputTokens)
	}
}

func TestBenchmarkRunnerWithMock(t *testing.T) {
	ctx := context.Background()
	mock := NewMockAdapter()
	runner := NewBenchmarkRunner(mock)

	dir := filepath.Join("testdata")
	if err := runner.LoadFixtures(dir); err != nil {
		t.Fatalf("failed to load fixtures: %v", err)
	}

	if len(runner.Fixtures) == 0 {
		t.Fatalf("expected fixtures to be loaded, got 0")
	}

	metrics, err := runner.RunBenchmark(ctx, "mock-test")
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}

	if metrics.TotalCases == 0 {
		t.Errorf("expected total cases > 0, got 0")
	}
	if metrics.Failures > 0 {
		t.Errorf("expected 0 failures, got %d", metrics.Failures)
	}
}

func TestInterpretActivityFailOpen(t *testing.T) {
	ctx := context.Background()

	// 1. Nil provider returns nil immediately without panic
	insights := InterpretActivity(ctx, nil, []store.StoredMessage{
		{Content: "test message", Role: "assistant"},
	})
	if insights != nil {
		t.Errorf("expected nil insights with nil provider, got %v", insights)
	}

	// 2. Mock provider returns semantic insights
	mock := NewMockAdapter()
	insights = InterpretActivity(ctx, mock, []store.StoredMessage{
		{Content: "Fixed critical blocker in database lock timeout", Role: "assistant"},
	})
	if len(insights) == 0 {
		t.Errorf("expected insights from mock provider, got 0")
	}
}

func TestProposeInstructionUpdates(t *testing.T) {
	ctx := context.Background()

	// 1. Nil provider returns nil
	props := ProposeInstructionUpdates(ctx, nil, "existing rules", "AGENTS.md", []store.StoredMessage{
		{Content: "some message"},
	})
	if props != nil {
		t.Errorf("expected nil proposals with nil provider, got %v", props)
	}

	// 2. Mock provider returns proposals with stable hashes
	mock := NewMockAdapter()
	props = ProposeInstructionUpdates(ctx, mock, "# AGENTS.md\n- Rule 1", "AGENTS.md", []store.StoredMessage{
		{Content: "Failed build because invariant was missing", Role: "assistant"},
	})
	// With mock, choice evaluates to first option or matched criteria; target hash must be non-empty
	if len(props) > 0 && props[0].TargetContentHash == "" {
		t.Errorf("expected non-empty target content hash")
	}
}

func TestLiveHostedTypeSafe(t *testing.T) {
	if os.Getenv("SILA_RUN_HOSTED_TESTS") != "1" {
		t.Skip("skipping live hosted test: SILA_RUN_HOSTED_TESTS=1 not set (tests remain offline by default)")
	}
	spendCapStr := os.Getenv("SILA_HOSTED_SPEND_CAP")
	spendCap, _ := strconv.ParseFloat(spendCapStr, 64)
	if spendCap <= 0 {
		t.Skip("skipping live hosted test: SILA_HOSTED_SPEND_CAP > 0 not set (positive approved spend cap required)")
	}

	cfg, err := config.Load("")
	if err != nil || cfg.TypeSafeAPIKey == "" {
		t.Skip("skipping live TypeSafe test: typesafe_api_key not configured")
	}
	if !cfg.AssistEnabled {
		t.Skip("skipping live hosted test: assist_enabled is false")
	}

	adapter := NewTypeSafeAdapter(cfg.TypeSafeAPIKey, nil)
	adapter.SpendLimit = spendCap

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Test live activity interpretation on real blocker messages
	msgs := []store.StoredMessage{
		{
			ID:      1,
			TS:      "2026-09-18T06:00:00Z",
			Role:    "assistant",
			Content: "Build failed with exit status 1 due to database lock contention on concurrent writes.",
		},
		{
			ID:      2,
			TS:      "2026-09-18T06:01:00Z",
			Role:    "user",
			Content: "This is blocking the deployment pipeline.",
		},
	}

	insights := InterpretActivity(ctx, adapter, msgs)
	t.Logf("Live Activity Insights: %v", insights)

	// 2. Test live instruction proposal review
	proposals := ProposeInstructionUpdates(ctx, adapter, "# Rules\n- Always run tests", "AGENTS.md", msgs)
	t.Logf("Live Instruction Proposals: %d", len(proposals))
	for _, p := range proposals {
		t.Logf("  Action: %s, Summary: %s, Hash: %s", p.Action, p.FailureSummary, p.TargetContentHash)
		if p.TargetContentHash == "" {
			t.Errorf("expected target content hash to be computed")
		}
	}

	// 3. Test live semantic search reranking
	candidates := []RerankCandidate{
		{Key: "hit_01", Excerpt: "Configured PRAGMA busy_timeout = 5000 and WAL mode to eliminate database lock contention."},
		{Key: "hit_02", Excerpt: "Updated frontend color palette to support dark mode themes."},
		{Key: "hit_03", Excerpt: "تم حل أخطاء قفل قاعدة البيانات من خلال ضبط مهلة الانتظار ونمط كتابة السجلات."},
	}
	res, err := Rerank(ctx, adapter, "database write locks on concurrent sync", candidates)
	if err != nil {
		t.Fatalf("live Rerank failed: %v", err)
	}
	t.Logf("Live Rerank Result: duration=%dms, answer_exists=%.2f, scores=%v", res.DurationMs, res.AnswerExists, res.Scores)
	if res.Scores["hit_01"] < 0.5 {
		t.Errorf("expected hit_01 to score high on database lock query, got %.2f", res.Scores["hit_01"])
	}
	// 4. Test live git diff sentry audit
	diffLeak := `diff --git a/config.go b/config.go
@@ -10,1 +10,1 @@
-const apiKey = ""
+const apiKey = "sk_live_994821a00f12"`
	auditRes, err := AuditDiff(ctx, adapter, diffLeak)
	if err != nil {
		t.Fatalf("live AuditDiff failed: %v", err)
	}
	t.Logf("Live Diff Audit Result: leaks=%.2f, skips=%.2f, schema=%.2f, warning=%q", auditRes.LeaksSecret, auditRes.SkipsTests, auditRes.DestructiveSchema, auditRes.Warning)
	if auditRes.LeaksSecret < 0.80 {
		t.Errorf("expected high leaks_secret score on hardcoded key, got %.2f", auditRes.LeaksSecret)
	}

	// 5. Test live fact verification
	claim := "Sila uses modernc.org/sqlite to maintain zero CGO dependencies"
	code := `package store
import _ "modernc.org/sqlite"
// Pure Go SQLite driver ensures CGO_ENABLED=0 builds run cleanly.`
	vRes, err := VerifyFact(ctx, adapter, claim, "internal/store/store.go", code)
	if err != nil {
		t.Fatalf("live VerifyFact failed: %v", err)
	}
	t.Logf("Live Fact Verification: verdict=%s, sub=%.2f, contra=%.2f, duration=%dms",
		vRes.Verdict, vRes.Substantiated, vRes.Contradicted, vRes.DurationMs)
	if vRes.Substantiated < 0.70 {
		t.Errorf("expected high substantiated score, got %.2f", vRes.Substantiated)
	}
}

func TestAuditDiffMock(t *testing.T) {
	ctx := context.Background()

	// 1. Nil provider returns nil
	res, err := AuditDiff(ctx, nil, "diff --git ...")
	if err != nil || res != nil {
		t.Errorf("expected nil on nil provider, got %v, %v", res, err)
	}

	// 2. Mock provider evaluates diff
	mock := NewMockAdapter()
	res, err = AuditDiff(ctx, mock, "diff with sk_live_test key")
	if err != nil {
		t.Fatalf("mock AuditDiff failed: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil result from mock")
	}
}

func TestRerankMock(t *testing.T) {
	ctx := context.Background()

	// 1. Nil provider fails open
	res, err := Rerank(ctx, nil, "query", []RerankCandidate{{Key: "k1", Excerpt: "text"}})
	if err != nil || res != nil {
		t.Errorf("expected nil result on nil provider, got res=%v, err=%v", res, err)
	}

	// 2. Mock provider handles parallel evaluation
	mock := NewMockAdapter()
	candidates := []RerankCandidate{
		{Key: "k1", Excerpt: "Relevant passage with matching keywords"},
		{Key: "k2", Excerpt: "Irrelevant text about cooking"},
	}
	res, err = Rerank(ctx, mock, "matching keywords", candidates)
	if err != nil {
		t.Fatalf("unexpected mock error: %v", err)
	}
	if len(res.Scores) != 2 {
		t.Errorf("expected 2 scores, got %d", len(res.Scores))
	}
}

func TestVerifyFactsMock(t *testing.T) {
	ctx := context.Background()

	// 1. Nil provider returns nil
	res, err := VerifyFacts(ctx, nil, []FactVerificationItem{{Claim: "c", CodeExcerpt: "e"}})
	if err != nil || res != nil {
		t.Errorf("expected nil on nil provider, got %v, %v", res, err)
	}

	// 2. Mock provider handles verification
	mock := NewMockAdapter()
	items := []FactVerificationItem{
		{ID: "f1", Claim: "uses sqlite", CodeExcerpt: "package store\nimport modernc.org/sqlite"},
		{ID: "f2", Claim: "uses postgresql", CodeExcerpt: "package store\nthis contradicts postgresql"},
	}
	res, err = VerifyFacts(ctx, mock, items)
	if err != nil {
		t.Fatalf("unexpected mock error: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].Verdict != "substantiated" {
		t.Errorf("expected substantiated for f1, got %s", res[0].Verdict)
	}
	if res[1].Verdict != "contradicted" {
		t.Errorf("expected contradicted for f2, got %s", res[1].Verdict)
	}
}

func TestTypeSafeAdapter_MalformedAndRubricValidation(t *testing.T) {
	ctx := context.Background()

	// 1. Empty Noul produces InvalidAnswers
	fakeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"q":{}}}`))
	}))
	defer fakeServer.Close()

	adapter := NewTypeSafeAdapter("test-key", fakeServer.Client())
	adapter.Endpoint = fakeServer.URL

	resp, err := adapter.Evaluate(ctx, "alias-model", map[string]any{}, map[string]Question{
		"q": {Type: TypeNoul},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp.Nouls["q"]; ok {
		t.Errorf("expected empty noul to be rejected, got %+v", resp.Nouls["q"])
	}
	if resp.InvalidAnswers["q"] == "" {
		t.Errorf("expected invalid answer explanation for empty noul")
	}
	if adapter.TotalSpent <= 0 {
		t.Errorf("expected spent > 0 when usage is missing (fallback to estimate)")
	}
	if resp.Model != "" {
		t.Errorf("expected empty returned model to NOT be overwritten with requested alias, got %q", resp.Model)
	}
	if resp.RequestedModel != "alias-model" {
		t.Errorf("expected requested model to be preserved as 'alias-model', got %q", resp.RequestedModel)
	}

	// 2. Score exceeding rubric produces InvalidAnswers
	fakeServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"score_q":{"score":4}},"usage":{"input_tokens":10}}`))
	}))
	defer fakeServer2.Close()

	adapter2 := NewTypeSafeAdapter("test-key", fakeServer2.Client())
	adapter2.Endpoint = fakeServer2.URL

	resp2, err := adapter2.Evaluate(ctx, "", map[string]any{}, map[string]Question{
		"score_q": {
			Type:         TypeScore,
			Instructions: "Rate from 0 (lowest) through 3 (highest).",
			Criteria:     []string{"none", "low", "medium", "high"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp2.Scores["score_q"]; ok {
		t.Errorf("expected score 4 to be rejected on [0, 3] rubric, got %+v", resp2.Scores["score_q"])
	}
	if resp2.InvalidAnswers["score_q"] == "" {
		t.Errorf("expected invalid answer explanation for out-of-rubric score")
	}

	// 3. Choice with map criteria validates keys
	fakeServer3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":{"choice_bad":{"choice":"unlisted"},"choice_good":{"choice":"allowed"}},"usage":{"input_tokens":10}}`))
	}))
	defer fakeServer3.Close()

	adapter3 := NewTypeSafeAdapter("test-key", fakeServer3.Client())
	adapter3.Endpoint = fakeServer3.URL

	resp3, err := adapter3.Evaluate(ctx, "", map[string]any{}, map[string]Question{
		"choice_bad": {
			Type:     TypeChoice,
			Criteria: map[string]string{"allowed": "Allowed choice description"},
		},
		"choice_good": {
			Type:     TypeChoice,
			Criteria: map[string]string{"allowed": "Allowed choice description"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := resp3.Choices["choice_bad"]; ok {
		t.Errorf("expected unlisted choice to be rejected against map criteria")
	}
	if resp3.InvalidAnswers["choice_bad"] == "" {
		t.Errorf("expected invalid answer for choice_bad")
	}
	if ans, ok := resp3.Choices["choice_good"]; !ok || ans.Choice != "allowed" {
		t.Errorf("expected choice_good to be accepted with 'allowed', got %+v", ans)
	}
}
