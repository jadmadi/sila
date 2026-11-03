package sentry

import (
	"fmt"
	"math"
)

// ChangePointTracker is an alias for PageHinkleyTracker for sequential change-point analysis.
type ChangePointTracker = PageHinkleyTracker

// SilenceEvaluation is an alias for SilenceAnomaly.
type SilenceEvaluation = SilenceAnomaly

// NewChangePointTracker returns a new tracker with calibrated default parameters.
func NewChangePointTracker() PageHinkleyTracker {
	return DefaultPageHinkleyTracker()
}

// SessionHistoryDetector performs change-point detection and AST coupling evaluation
// over genuine chronological session frequency histories.
type SessionHistoryDetector struct {
	Tracker PageHinkleyTracker
}

// NewSessionHistoryDetector initializes a detector with the specified tracker.
// Accepts PageHinkleyTracker, *PageHinkleyTracker, or nil (defaults to NewChangePointTracker()).
func NewSessionHistoryDetector(tracker any) *SessionHistoryDetector {
	var tr PageHinkleyTracker
	switch v := tracker.(type) {
	case PageHinkleyTracker:
		tr = v
	case *PageHinkleyTracker:
		if v != nil {
			tr = *v
		} else {
			tr = NewChangePointTracker()
		}
	case nil:
		tr = NewChangePointTracker()
	default:
		tr = NewChangePointTracker()
	}

	if tr.BurnInSessions <= 0 {
		tr.BurnInSessions = 3
	}
	if tr.Lambda <= 0 {
		tr.Lambda = 25.0
	}
	if tr.Delta <= 0 {
		tr.Delta = 0.10
	}

	return &SessionHistoryDetector{
		Tracker: tr,
	}
}

// DefaultSessionHistoryDetector returns a detector with calibrated default parameters.
func DefaultSessionHistoryDetector() *SessionHistoryDetector {
	return NewSessionHistoryDetector(DefaultPageHinkleyTracker())
}

// DetectionResult captures the statistical and epistemic assessment of a cluster's history.
type DetectionResult struct {
	Statistic       float64 `json:"statistic"`
	AlertTriggered  bool    `json:"alert_triggered"`
	BurnInMet       bool    `json:"burn_in_met"`
	SessionsTracked int     `json:"sessions_tracked"`
	Status          string  `json:"status"` // "calibrating", "stable", "silence_anomaly", "verified_fix"
	Explanation     string  `json:"explanation"`
}

// Detect analyzes a chronological series of session frequency counts.
func (d *SessionHistoryDetector) Detect(history []float64, aci float64) DetectionResult {
	n := len(history)
	if n < d.Tracker.BurnInSessions {
		return DetectionResult{
			Statistic:       0.0,
			AlertTriggered:  false,
			BurnInMet:       false,
			SessionsTracked: n,
			Status:          "calibrating",
			Explanation:     fmt.Sprintf("Insufficient session history for change-point calibration (%d/%d sessions recorded).", n, d.Tracker.BurnInSessions),
		}
	}

	ph, alert := d.Tracker.DetectDownwardDrop(history)
	roundedPH := math.Round(ph*100) / 100

	if !alert {
		return DetectionResult{
			Statistic:       roundedPH,
			AlertTriggered:  false,
			BurnInMet:       true,
			SessionsTracked: n,
			Status:          "stable",
			Explanation:     fmt.Sprintf("Diagnostic reporting frequency is stable across %d sessions (PH=%.2f < %.2f).", n, roundedPH, d.Tracker.Lambda),
		}
	}

	if aci < 0.40 {
		return DetectionResult{
			Statistic:       roundedPH,
			AlertTriggered:  true,
			BurnInMet:       true,
			SessionsTracked: n,
			Status:          "silence_anomaly",
			Explanation:     fmt.Sprintf("Flag extinction detected: frequency dropped sharply across %d sessions (PH=%.2f >= %.2f) with zero or weak AST coupling (ACI=%.2f < 0.40).", n, roundedPH, d.Tracker.Lambda, aci),
		}
	}

	return DetectionResult{
		Statistic:       roundedPH,
		AlertTriggered:  true,
		BurnInMet:       true,
		SessionsTracked: n,
		Status:          "verified_fix",
		Explanation:     fmt.Sprintf("Legitimate fix confirmed: diagnostic frequency dropped across %d sessions with verified AST modifications at defect locus (ACI=%.2f).", n, aci),
	}
}

// EvaluateCluster evaluates a cluster against its genuine chronological session frequency history.
func (d *SessionHistoryDetector) EvaluateCluster(c *DiagnosticCluster, history []float64, aci float64) *SilenceAnomaly {
	if d == nil {
		d = DefaultSessionHistoryDetector()
	}
	if d.Tracker.BurnInSessions <= 0 || d.Tracker.Lambda <= 0 {
		d.Tracker = DefaultPageHinkleyTracker()
	}

	res := d.Detect(history, aci)

	sig, cat, tFile, tSym, tpl := "", "", "", "", ""
	if c != nil {
		sig = c.Signature
		cat = c.Category
		tFile = c.TargetFile
		tSym = c.TargetSymbol
		tpl = c.Template
	}

	return &SilenceAnomaly{
		Signature:       sig,
		Category:        cat,
		TargetFile:      tFile,
		TargetSymbol:    tSym,
		Template:        tpl,
		PageHinkleyStat: res.Statistic,
		CouplingIndex:   math.Round(aci*100) / 100,
		Status:          res.Status,
		Explanation:     res.Explanation,
	}
}
