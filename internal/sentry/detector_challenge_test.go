package sentry

import (
	"testing"
)

// TestSessionHistoryDetector_BurnInEmpiricalChallenge rigorously challenges
// the burn-in calibration logic across edge cases and arbitrary history sequences.
func TestSessionHistoryDetector_BurnInEmpiricalChallenge(t *testing.T) {
	detector := DefaultSessionHistoryDetector()

	// 1. Extreme collapse from 1000 to 0 on session 2 MUST NOT alert before burn-in (default 3)
	preBurnInDrop := []float64{1000.0, 0.0}
	resPre := detector.Detect(preBurnInDrop, 0.0)
	if resPre.BurnInMet {
		t.Fatalf("burn-in met prematurely for 2 sessions: %+v", resPre)
	}
	if resPre.AlertTriggered {
		t.Fatalf("alert triggered prematurely before burn-in calibration: %+v", resPre)
	}
	if resPre.Status != "calibrating" {
		t.Fatalf("expected status 'calibrating', got %q", resPre.Status)
	}

	// 2. Single session history
	resSingle := detector.Detect([]float64{50.0}, 0.0)
	if resSingle.BurnInMet || resSingle.AlertTriggered || resSingle.Status != "calibrating" {
		t.Fatalf("single session failed burn-in check: %+v", resSingle)
	}

	// 3. Custom BurnInSessions = 5
	customTracker := PageHinkleyTracker{
		BurnInSessions: 5,
		Delta:          0.10,
		Lambda:         10.0,
	}
	customDetector := NewSessionHistoryDetector(customTracker)

	// 4 sessions (< 5) with extreme drop
	resCustom4 := customDetector.Detect([]float64{100, 100, 0, 0}, 0.0)
	if resCustom4.BurnInMet || resCustom4.AlertTriggered || resCustom4.Status != "calibrating" {
		t.Fatalf("custom burn-in of 5 failed to suppress alert at 4 sessions: %+v", resCustom4)
	}

	// 5 sessions (== 5) with extreme drop
	resCustom5 := customDetector.Detect([]float64{100, 100, 100, 0, 0}, 0.0)
	if !resCustom5.BurnInMet {
		t.Fatalf("custom burn-in of 5 was not met at 5 sessions: %+v", resCustom5)
	}
	if !resCustom5.AlertTriggered {
		t.Fatalf("expected alert triggered on frequency collapse at 5 sessions: %+v", resCustom5)
	}
	if resCustom5.Status != "silence_anomaly" {
		t.Fatalf("expected silence_anomaly, got %q", resCustom5.Status)
	}

	// 4. EvaluateCluster helper also respects burn-in
	c := &DiagnosticCluster{
		Signature:    "sig-burn-in-test",
		Category:     "test",
		TargetFile:   "pkg/test.go",
		TargetSymbol: "TestFn",
	}
	anomaly0 := detector.EvaluateCluster(c, []float64{500, 0}, 0.0)
	if anomaly0.Status != "calibrating" {
		t.Fatalf("EvaluateCluster must return calibrating when burn-in not met, got %q", anomaly0.Status)
	}
}
