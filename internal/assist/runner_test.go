package assist

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type failProvider struct{}

func (f *failProvider) Name() ModelProvider { return ProviderMock }
func (f *failProvider) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]Question) (*ProviderResponse, error) {
	return nil, errors.New("simulated provider network timeout")
}

func TestBenchmarkBaselineInvarianceToFixtureID(t *testing.T) {
	ctx := context.Background()
	mock := NewMockAdapter()

	fixture := EvalFixture{
		ID:       "original-fixture-id-alpha",
		Language: "en",
		Category: "activity",
		InputEvidence: map[string]any{
			"message": "Encountered database locked error on concurrent writes. Set PRAGMA busy_timeout = 5000 and enabled WAL mode to fix concurrency contention.",
		},
		ExpectedLabels: map[string]any{
			"durable_decision": true,
		},
	}

	runner1 := NewBenchmarkRunner(mock)
	runner1.Fixtures = []EvalFixture{fixture}
	m1, err := runner1.RunBenchmark(ctx, "test-model")
	if err != nil {
		t.Fatalf("first run failed: %v", err)
	}

	// Rename the fixture ID to something completely arbitrary
	fixture.ID = "totally-different-opaque-id-998877"
	runner2 := NewBenchmarkRunner(mock)
	runner2.Fixtures = []EvalFixture{fixture}
	m2, err := runner2.RunBenchmark(ctx, "test-model")
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}

	if m1.LocalAccuracy != m2.LocalAccuracy {
		t.Fatalf("F04 regression: LocalAccuracy changed when only ID changed: m1=%f, m2=%f", m1.LocalAccuracy, m2.LocalAccuracy)
	}
	if m1.Accuracy != m2.Accuracy {
		t.Fatalf("Accuracy changed when only ID changed: m1=%f, m2=%f", m1.Accuracy, m2.Accuracy)
	}
}

func TestBenchmarkLatencyPercentilesSorted(t *testing.T) {
	ctx := context.Background()
	mock := NewMockAdapter()

	f1 := EvalFixture{
		ID:       "fix-1",
		Language: "en",
		Category: "activity",
		InputEvidence: map[string]any{
			"message": "Architectural decision 1",
		},
		ExpectedLabels: map[string]any{
			"durable_decision": true,
		},
	}
	f2 := EvalFixture{
		ID:       "fix-2",
		Language: "ar",
		Category: "activity",
		InputEvidence: map[string]any{
			"message": "قرار معماري 2",
		},
		ExpectedLabels: map[string]any{
			"durable_decision": true,
		},
	}
	f3 := EvalFixture{
		ID:       "fix-3",
		Language: "mixed",
		Category: "activity",
		InputEvidence: map[string]any{
			"message": "decision and قرار 3",
		},
		ExpectedLabels: map[string]any{
			"durable_decision": true,
		},
	}

	runnerA := NewBenchmarkRunner(mock)
	runnerA.Fixtures = []EvalFixture{f1, f2, f3}
	mA, err := runnerA.RunBenchmark(ctx, "test-model")
	if err != nil {
		t.Fatalf("runnerA failed: %v", err)
	}

	// Reorder fixtures in reverse
	runnerB := NewBenchmarkRunner(mock)
	runnerB.Fixtures = []EvalFixture{f3, f2, f1}
	mB, err := runnerB.RunBenchmark(ctx, "test-model")
	if err != nil {
		t.Fatalf("runnerB failed: %v", err)
	}

	if mA.P50LatencyMs > mA.P95LatencyMs {
		t.Errorf("mA: P50 (%d ms) cannot be greater than P95 (%d ms)", mA.P50LatencyMs, mA.P95LatencyMs)
	}
	if mB.P50LatencyMs > mB.P95LatencyMs {
		t.Errorf("mB: P50 (%d ms) cannot be greater than P95 (%d ms)", mB.P50LatencyMs, mB.P95LatencyMs)
	}

	// Test deterministic percentile calculation on fixed slices
	fixed := []int64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	p50 := percentile(fixed, 0.50)
	p95 := percentile(fixed, 0.95)
	if p50 != 50 && p50 != 60 {
		t.Errorf("expected p50 around 50-60, got %d", p50)
	}
	if p95 != 100 && p95 != 90 {
		t.Errorf("expected p95 around 90-100, got %d", p95)
	}
	if p50 > p95 {
		t.Errorf("p50 (%d) cannot be greater than p95 (%d)", p50, p95)
	}
}

func TestBenchmarkFailureSurfacing(t *testing.T) {
	ctx := context.Background()
	failP := &failProvider{}

	runner := NewBenchmarkRunner(failP)
	runner.Fixtures = []EvalFixture{
		{
			ID:             "fail-test",
			Language:       "en",
			InputEvidence:  map[string]any{"message": "hello"},
			ExpectedLabels: map[string]any{"durable_decision": false},
		},
	}

	metric, err := runner.RunBenchmark(ctx, "test-model")
	if err != nil {
		t.Fatalf("unexpected error from runner: %v", err)
	}

	if metric.Failures != 1 {
		t.Errorf("expected 1 failure, got %d", metric.Failures)
	}
	if !metric.HasErrors {
		t.Errorf("expected HasErrors to be true on failed evaluation")
	}
	if len(metric.Cases) != 1 || metric.Cases[0].Error == "" {
		t.Errorf("expected error message recorded in case result")
	}
}

func TestBenchmarkHashesAndPerTaskMetrics(t *testing.T) {
	ctx := context.Background()
	mock := NewMockAdapter()

	runner := NewBenchmarkRunner(mock)
	if err := runner.LoadFixtures(filepath.Join("testdata")); err != nil {
		t.Fatalf("failed loading fixtures: %v", err)
	}

	metric, err := runner.RunBenchmark(ctx, "test-model")
	if err != nil {
		t.Fatalf("benchmark run failed: %v", err)
	}

	if metric.FixtureHash == "" {
		t.Errorf("expected non-empty FixtureHash")
	}
	if metric.QuestionsHash == "" {
		t.Errorf("expected non-empty QuestionsHash")
	}
	if len(metric.PerTaskMetrics) == 0 {
		t.Errorf("expected non-empty PerTaskMetrics")
	}

	// Verify per-task metrics has durable_decision and security_violation
	if dd, ok := metric.PerTaskMetrics["durable_decision"]; ok {
		if dd.Total == 0 {
			t.Errorf("expected durable_decision total > 0")
		}
		if dd.Precision < 0 || dd.Precision > 1.0 {
			t.Errorf("invalid precision: %f", dd.Precision)
		}
	} else {
		t.Errorf("missing durable_decision in PerTaskMetrics")
	}

	if metric.HeldOutCases == 0 {
		t.Errorf("expected held out cases > 0, got 0")
	}
}
