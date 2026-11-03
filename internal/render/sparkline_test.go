package render

import (
	"math"
	"testing"
)

func TestSparklineEmpty(t *testing.T) {
	if got := Sparkline(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}
	if got := Sparkline([]float64{}); got != "" {
		t.Errorf("expected empty string for empty slice, got %q", got)
	}
	if got := SparklineInts(nil); got != "" {
		t.Errorf("expected empty string for nil ints, got %q", got)
	}
	if got := SparklineInts([]int{}); got != "" {
		t.Errorf("expected empty string for empty ints, got %q", got)
	}
}

func TestSparklineConstantValues(t *testing.T) {
	// All zeros
	zeros := []float64{0, 0, 0, 0}
	if got := Sparkline(zeros); got != "    " {
		t.Errorf("expected 4 lowest blocks for zeros, got %q", got)
	}

	// Constant positive
	pos := []float64{10, 10, 10}
	if got := Sparkline(pos); got != "▄▄▄" {
		t.Errorf("expected 3 middle blocks for constant positive, got %q", got)
	}

	// Constant negative
	neg := []float64{-5, -5, -5}
	if got := Sparkline(neg); got != "   " {
		t.Errorf("expected 3 lowest blocks for constant negative, got %q", got)
	}
}

func TestSparklineScaling(t *testing.T) {
	// Exact 8 levels matching SparklineRunes
	values := []float64{0, 1, 2, 3, 4, 5, 6, 7}
	expected := " ▂▃▄▅▆▇█"
	if got := Sparkline(values); got != expected {
		t.Errorf("expected full range %q, got %q", expected, got)
	}

	// Min and Max only
	twoVals := []float64{0, 100}
	if got := Sparkline(twoVals); got != " █" {
		t.Errorf("expected ' █', got %q", got)
	}

	// SparklineInts matches
	intVals := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if got := SparklineInts(intVals); got != expected {
		t.Errorf("SparklineInts: expected %q, got %q", expected, got)
	}
}

func TestSparklineNegativeAndMixed(t *testing.T) {
	vals := []float64{-10, 0, 10}
	// -10 is min ( ), 0 is middle (▄), 10 is max (█)
	expected := " ▄█"
	if got := Sparkline(vals); got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestSparklineNaNAndInfGraceful(t *testing.T) {
	vals := []float64{math.NaN(), 10, math.Inf(1), 0}
	// NaN and Inf convert to 0, so array is [0, 10, 0, 0]
	// min = 0, max = 10.
	// 0 -> ' ', 10 -> '█', 0 -> ' ', 0 -> ' '
	expected := " █  "
	if got := Sparkline(vals); got != expected {
		t.Errorf("expected graceful handling %q, got %q", expected, got)
	}
}
