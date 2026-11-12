package dream

// OutcomeWeight scales a transcript turn by its outcome label.
func OutcomeWeight(outcome string) float64 {
	switch outcome {
	case "success":
		return 1.0
	case "corrected":
		return 0.2
	default:
		return 0.6
	}
}
