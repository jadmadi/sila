package sentry

import (
	"testing"
)

func TestSessionHistoryDetector_BurnInCalibration(t *testing.T) {
	detector := DefaultSessionHistoryDetector()

	// 0 sessions
	res0 := detector.Detect([]float64{}, 0.0)
	if res0.Status != "calibrating" || res0.AlertTriggered || res0.BurnInMet {
		t.Errorf("expected calibrating status for 0 sessions, got %+v", res0)
	}

	// 2 sessions (< 3 burn-in)
	res2 := detector.Detect([]float64{15.0, 0.0}, 0.0)
	if res2.Status != "calibrating" || res2.AlertTriggered || res2.BurnInMet {
		t.Errorf("expected calibrating status for 2 sessions, got %+v", res2)
	}
}

func TestSessionHistoryDetector_StableFrequency(t *testing.T) {
	detector := DefaultSessionHistoryDetector()

	// Stable 15 errors per session across 5 sessions
	stable := []float64{15, 16, 14, 15, 15}
	res := detector.Detect(stable, 0.0)
	if res.Status != "stable" {
		t.Errorf("expected status 'stable', got %q", res.Status)
	}
	if res.AlertTriggered {
		t.Errorf("unexpected alert on stable series")
	}
	if !res.BurnInMet {
		t.Errorf("expected burn in met")
	}
}

func TestSessionHistoryDetector_SilenceAnomalyUncoupled(t *testing.T) {
	tracker := PageHinkleyTracker{
		BurnInSessions: 3,
		Delta:          0.10,
		Lambda:         10.0,
	}
	detector := NewSessionHistoryDetector(tracker)

	// Sharp collapse from 20 -> 20 -> 20 -> 0 -> 0 -> 0
	collapsed := []float64{20, 20, 20, 0, 0, 0}
	res := detector.Detect(collapsed, 0.10) // low ACI < 0.40

	if res.Status != "silence_anomaly" {
		t.Fatalf("expected status 'silence_anomaly', got %q", res.Status)
	}
	if !res.AlertTriggered {
		t.Fatalf("expected alert triggered on frequency collapse")
	}
	if res.Statistic < tracker.Lambda {
		t.Errorf("expected stat >= %.2f, got %.2f", tracker.Lambda, res.Statistic)
	}
}

func TestSessionHistoryDetector_VerifiedFixCoupled(t *testing.T) {
	tracker := PageHinkleyTracker{
		BurnInSessions: 3,
		Delta:          0.10,
		Lambda:         10.0,
	}
	detector := NewSessionHistoryDetector(tracker)

	// Frequency collapse with high ACI >= 0.40
	collapsed := []float64{20, 20, 20, 0, 0, 0}
	res := detector.Detect(collapsed, 0.85)

	if res.Status != "verified_fix" {
		t.Fatalf("expected status 'verified_fix', got %q", res.Status)
	}
	if !res.AlertTriggered {
		t.Fatalf("expected alert triggered on frequency collapse")
	}
}

func TestSessionHistoryDetector_EvaluateCluster(t *testing.T) {
	detector := DefaultSessionHistoryDetector()
	c := &DiagnosticCluster{
		Signature:    "sig-test-eval",
		Category:     "compiler_error",
		TargetFile:   "pkg/service.go",
		TargetSymbol: "ServeHTTP",
		Template:     "syntax error in pkg",
	}

	// Insufficient sessions
	aCalib := detector.EvaluateCluster(c, []float64{10}, 0.0)
	if aCalib.Status != "calibrating" {
		t.Errorf("expected calibrating, got %q", aCalib.Status)
	}

	// Real collapse without fix
	detector.Tracker.Lambda = 10.0
	aAnomaly := detector.EvaluateCluster(c, []float64{25, 25, 25, 0, 0}, 0.0)
	if aAnomaly.Status != "silence_anomaly" {
		t.Errorf("expected silence_anomaly, got %q", aAnomaly.Status)
	}

	// Real collapse with verified fix
	aFix := detector.EvaluateCluster(c, []float64{25, 25, 25, 0, 0}, 0.85)
	if aFix.Status != "verified_fix" {
		t.Errorf("expected verified_fix, got %q", aFix.Status)
	}

	// Nil cluster safety
	aNilCluster := detector.EvaluateCluster(nil, []float64{25, 25, 25, 0, 0}, 0.85)
	if aNilCluster.Status != "verified_fix" {
		t.Errorf("expected verified_fix with nil cluster, got %q", aNilCluster.Status)
	}
}

func TestSessionHistoryDetector_EvaluateSilence_FourStates(t *testing.T) {
	c := &DiagnosticCluster{
		Signature:    "sig-eval-silence",
		Category:     "compiler_error",
		TargetFile:   "cmd/app/main.go",
		TargetSymbol: "main",
		Template:     "syntax error",
	}
	tr := NewChangePointTracker()
	tr.Lambda = 10.0

	// a) calibrating when len(sessionFrequencies) < 3
	resCalib := EvaluateSilence(c, []float64{50.0, 10.0}, tr, 0.0)
	if resCalib.Status != "calibrating" {
		t.Fatalf("expected status 'calibrating', got %q", resCalib.Status)
	}

	// b) stable when sessionFrequencies do not drop
	resStable := EvaluateSilence(c, []float64{20, 21, 19, 20, 20}, tr, 0.0)
	if resStable.Status != "stable" {
		t.Fatalf("expected status 'stable', got %q", resStable.Status)
	}

	// c) silence_anomaly when a drop occurs and aci < 0.40
	resAnomaly := EvaluateSilence(c, []float64{25, 25, 25, 0, 0}, tr, 0.20)
	if resAnomaly.Status != "silence_anomaly" {
		t.Fatalf("expected status 'silence_anomaly', got %q", resAnomaly.Status)
	}

	// d) verified_fix when a drop occurs and aci >= 0.40
	resFix := EvaluateSilence(c, []float64{25, 25, 25, 0, 0}, tr, 0.80)
	if resFix.Status != "verified_fix" {
		t.Fatalf("expected status 'verified_fix', got %q", resFix.Status)
	}
}

func TestSessionHistoryDetector_TrackerVariations(t *testing.T) {
	c := &DiagnosticCluster{
		Signature: "sig-var",
	}

	// 1. Nil tracker in NewSessionHistoryDetector defaults to NewChangePointTracker()
	dNil := NewSessionHistoryDetector(nil)
	if dNil.Tracker.BurnInSessions != 3 || dNil.Tracker.Lambda != 25.0 {
		t.Errorf("unexpected tracker defaults for nil: %+v", dNil.Tracker)
	}

	// 2. Pointer tracker in NewSessionHistoryDetector
	custom := PageHinkleyTracker{
		BurnInSessions: 5,
		Delta:          0.20,
		Lambda:         15.0,
	}
	dPtr := NewSessionHistoryDetector(&custom)
	if dPtr.Tracker.BurnInSessions != 5 || dPtr.Tracker.Lambda != 15.0 {
		t.Errorf("unexpected tracker from pointer: %+v", dPtr.Tracker)
	}

	// 3. EvaluateSilence with nil tracker initializes defaults cleanly
	resNilTr := EvaluateSilence(c, []float64{10}, nil, 0.0)
	if resNilTr.Status != "calibrating" {
		t.Errorf("expected calibrating with nil tracker, got %q", resNilTr.Status)
	}

	// 4. EvaluateSilence with pointer tracker
	resPtrTr := EvaluateSilence(c, []float64{30, 30, 30, 0, 0}, &custom, 0.0)
	if resPtrTr.Status != "silence_anomaly" {
		t.Errorf("expected silence_anomaly with custom pointer tracker, got %q", resPtrTr.Status)
	}
}
