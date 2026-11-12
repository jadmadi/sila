package store

import (
	"math"
	"time"
)

// Maturity tiers, lowest to highest.
const (
	MaturityDraft     = "draft"
	MaturityValidated = "validated"
	MaturityCore      = "core"
)

// AdvanceMaturity applies the hysteresis bands. A NaN score leaves the tier
// unchanged; scores outside [0, 100] are clamped.
func AdvanceMaturity(current string, score float64) string {
	if math.IsNaN(score) {
		return current
	}
	score = clampMaturityScore(score)
	switch current {
	case MaturityDraft, "":
		if score >= 65 {
			return MaturityValidated
		}
		return MaturityDraft
	case MaturityValidated:
		if score >= 85 {
			return MaturityCore
		}
		if score < 35 {
			return MaturityDraft
		}
		return MaturityValidated
	case MaturityCore:
		if score < 60 {
			return MaturityValidated
		}
		return MaturityCore
	default:
		return MaturityDraft
	}
}

func clampMaturityScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// DecayMaturityScore applies 0.995^days to a score and clamps the result.
func DecayMaturityScore(score float64, since, now time.Time) float64 {
	if math.IsNaN(score) {
		return 0
	}
	if since.IsZero() || now.Before(since) {
		return clampMaturityScore(score)
	}
	days := now.Sub(since).Hours() / 24
	return clampMaturityScore(score * math.Pow(0.995, days))
}

// MaturityRank orders tiers for display: core first, then validated, then draft.
func MaturityRank(tier string) int {
	switch tier {
	case MaturityCore:
		return 0
	case MaturityValidated:
		return 1
	default:
		return 2
	}
}
