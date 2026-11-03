package sentry

import (
	"testing"
)

func TestMaskVolatileTokens(t *testing.T) {
	raw := "fatal error: panic at 0x7fff5fbff8a0 in main.go:42:15 timestamp 2026-10-16T12:00:00Z uuid 123e4567-e89b-12d3-a456-426614174000"
	masked := MaskVolatileTokens(raw)

	if masked == raw {
		t.Fatalf("expected masking, got identical string: %s", masked)
	}
	expectedSubstrings := []string{"<HEX>", ":<POS>", "<TIME>", "<UUID>"}
	for _, sub := range expectedSubstrings {
		if !contains(masked, sub) {
			t.Errorf("masked output missing placeholder %q: %s", sub, masked)
		}
	}
}

func TestDrainParserOnlineClustering(t *testing.T) {
	p := NewDrainParser(0.85)

	m1 := "compiler error: undefined symbol Foo at 0x1000 in file.go:10"
	m2 := "compiler error: undefined symbol Bar at 0x2000 in file.go:20"

	c1 := p.Parse("compiler_error", "file.go", "FooFunc", m1, "s1")
	c2 := p.Parse("compiler_error", "file.go", "FooFunc", m2, "s2")

	if c1.Signature != c2.Signature {
		t.Fatalf("expected both messages to merge into same cluster, got %q vs %q", c1.Signature, c2.Signature)
	}
	if c2.OccurrenceCount != 2 {
		t.Fatalf("expected occurrence count 2, got %d", c2.OccurrenceCount)
	}
}

func TestDownwardPageHinkley(t *testing.T) {
	tracker := PageHinkleyTracker{
		BurnInSessions: 3,
		Delta:          0.10,
		Lambda:         10.0,
	}

	// 1. Stable high error frequency: 15 errors per session
	stable := []float64{15, 16, 14, 15, 15}
	stat, alert := tracker.DetectDownwardDrop(stable)
	if alert {
		t.Fatalf("unexpected alert on stable series: stat=%f", stat)
	}

	// 2. Sudden collapse to 0: flag extinction
	collapsed := []float64{20, 20, 20, 0, 0, 0}
	statCollapsed, alertCollapsed := tracker.DetectDownwardDrop(collapsed)
	if !alertCollapsed {
		t.Fatalf("expected alert on frequency collapse, got stat=%f", statCollapsed)
	}
}

func TestAttributionCouplingIndex(t *testing.T) {
	oldCode := `package example

func TargetHelper() int {
	return 42
}

func Unrelated() string {
	return "hello"
}
`

	// Case 1: Edit only Unrelated(), TargetHelper untouched
	newCodeUnrelated := `package example

func TargetHelper() int {
	return 42
}

func Unrelated() string {
	return "world"
}
`
	aciZero, err := CalculateACIFromSource(oldCode, newCodeUnrelated, "TargetHelper")
	if err != nil {
		t.Fatalf("CalculateACIFromSource failed: %v", err)
	}
	if aciZero != 0.0 {
		t.Fatalf("expected ACI 0.0 for untouched symbol, got %f", aciZero)
	}

	// Case 2: Modify TargetHelper body
	newCodeTarget := `package example

func TargetHelper() int {
	x := 42
	return x * 2
}

func Unrelated() string {
	return "hello"
}
`
	aciModified, err := CalculateACIFromSource(oldCode, newCodeTarget, "TargetHelper")
	if err != nil {
		t.Fatalf("CalculateACIFromSource failed: %v", err)
	}
	if aciModified < 0.40 {
		t.Fatalf("expected ACI >= 0.40 for modified symbol, got %f", aciModified)
	}
}

func TestEvaluateSilence(t *testing.T) {
	tracker := PageHinkleyTracker{
		BurnInSessions: 3,
		Delta:          0.10,
		Lambda:         10.0,
	}
	cluster := &DiagnosticCluster{
		Signature:    "sig_test",
		Category:     "runtime_panic",
		TargetFile:   "store.go",
		TargetSymbol: "Open",
		Template:     "panic: nil pointer <*> at <*>",
	}

	// a) Calibrating when len(sessionFrequencies) < 3
	calib0 := EvaluateSilence(cluster, []float64{}, tracker, 0.0)
	if calib0.Status != "calibrating" {
		t.Fatalf("expected status 'calibrating' for 0 sessions, got %q", calib0.Status)
	}
	calib2 := EvaluateSilence(cluster, []float64{25, 0}, tracker, 0.0)
	if calib2.Status != "calibrating" {
		t.Fatalf("expected status 'calibrating' for 2 sessions, got %q", calib2.Status)
	}

	// b) Stable when sessionFrequencies do not drop
	stable := EvaluateSilence(cluster, []float64{25, 26, 24, 25, 25}, tracker, 0.0)
	if stable.Status != "stable" {
		t.Fatalf("expected status 'stable' for non-dropping series, got %q", stable.Status)
	}

	// c) Silence anomaly when a drop occurs and aci < 0.40
	collapsed := []float64{25, 25, 25, 0, 0}
	anomaly := EvaluateSilence(cluster, collapsed, tracker, 0.0)
	if anomaly.Status != "silence_anomaly" {
		t.Fatalf("expected status 'silence_anomaly', got %q", anomaly.Status)
	}
	if anomaly.PageHinkleyStat < tracker.Lambda {
		t.Fatalf("expected PageHinkleyStat >= %.2f, got %.2f", tracker.Lambda, anomaly.PageHinkleyStat)
	}

	// d) Verified fix when a drop occurs and aci >= 0.40
	fix := EvaluateSilence(cluster, collapsed, tracker, 0.85)
	if fix.Status != "verified_fix" {
		t.Fatalf("expected status 'verified_fix', got %q", fix.Status)
	}

	// Nil tracker and pointer tracker handling
	calibNil := EvaluateSilence(cluster, []float64{10}, nil, 0.0)
	if calibNil.Status != "calibrating" {
		t.Fatalf("expected status 'calibrating' with nil tracker, got %q", calibNil.Status)
	}
	anomalyPtr := EvaluateSilence(cluster, collapsed, &tracker, 0.10)
	if anomalyPtr.Status != "silence_anomaly" {
		t.Fatalf("expected status 'silence_anomaly' with pointer tracker, got %q", anomalyPtr.Status)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
