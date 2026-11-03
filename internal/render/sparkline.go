package render

import (
	"math"
	"strings"
)

// SparklineRunes defines the standard 8-level Unicode block elements (U+2581 to U+2588)
// used for terminal sparkline generation.
var SparklineRunes = []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Sparkline renders a slice of float64 numbers as a single-line micro-histogram
// using Unicode block characters ( ▂▃▄▅▆▇█).
// Empty slices return an empty string. Constant, zero, or NaN/Inf inputs are
// handled gracefully without division-by-zero panics.
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}

	clean := make([]float64, len(values))
	minVal := math.MaxFloat64
	maxVal := -math.MaxFloat64

	for i, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 0
		}
		clean[i] = v
		if v < minVal {
			minVal = v
		}
		if v > maxVal {
			maxVal = v
		}
	}

	if minVal > maxVal {
		return strings.Repeat(string(SparklineRunes[0]), len(values))
	}

	var sb strings.Builder
	sb.Grow(len(values) * 3)

	if minVal == maxVal {
		r := SparklineRunes[0]
		if minVal > 0 {
			r = SparklineRunes[3] // '▄' middle level for non-zero flatline
		}
		for range clean {
			sb.WriteRune(r)
		}
		return sb.String()
	}

	span := maxVal - minVal
	maxIdx := len(SparklineRunes) - 1
	for _, v := range clean {
		ratio := (v - minVal) / span
		if ratio < 0 {
			ratio = 0
		} else if ratio > 1 {
			ratio = 1
		}
		idx := int(ratio * float64(maxIdx))
		if idx < 0 {
			idx = 0
		} else if idx > maxIdx {
			idx = maxIdx
		}
		sb.WriteRune(SparklineRunes[idx])
	}

	return sb.String()
}

// SparklineInts renders a slice of integers as a Unicode sparkline.
func SparklineInts(values []int) string {
	if len(values) == 0 {
		return ""
	}
	floats := make([]float64, len(values))
	for i, v := range values {
		floats[i] = float64(v)
	}
	return Sparkline(floats)
}
