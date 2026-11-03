package store

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxTemporalBoost is the maximum score multiplier boost (40%) applied to temporally proximate hits.
const DefaultMaxTemporalBoost = 0.40

// TemporalTarget encapsulates a parsed relative time anchor, target reference date, and decay window.
type TemporalTarget struct {
	TargetDate time.Time // question_date - offset
	WindowDays float64   // Half-width of target window in days
	MaxBoost   float64   // Maximum boost multiplier (default 0.40)
	RawAnchor  string    // The extracted text anchor (e.g. "3 days ago")
}

var wordToNumber = map[string]int{
	"a":     1,
	"an":    1,
	"one":   1,
	"two":   2,
	"three": 3,
	"four":  4,
	"five":  5,
	"six":   6,
	"seven": 7,
	"eight": 8,
	"nine":  9,
	"ten":   10,
}

var (
	// Ordered patterns: more specific multi-word anchors first.
	reEarlierToday     = regexp.MustCompile(`(?i)\bearlier\s+today\b`)
	reCoupleDaysAgo    = regexp.MustCompile(`(?i)\ba\s+couple\s+(?:of\s+)?days\s+ago\b`)
	reNDaysAgo         = regexp.MustCompile(`(?i)\b(\d+|a|an|one|two|three|four|five|six|seven|eight|nine|ten)\s+days?\s+ago\b`)
	reNWeeksAgo        = regexp.MustCompile(`(?i)\b(\d+|a|an|one|two|three|four|five|six|seven|eight|nine|ten)\s+weeks?\s+ago\b`)
	reNMonthsAgo       = regexp.MustCompile(`(?i)\b(\d+|a|an|one|two|three|four|five|six|seven|eight|nine|ten)\s+months?\s+ago\b`)
	reYesterday        = regexp.MustCompile(`(?i)\byesterday\b`)
	reToday            = regexp.MustCompile(`(?i)\btoday\b`)
	reThisWeek         = regexp.MustCompile(`(?i)\bthis\s+week\b`)
	reLastWeek         = regexp.MustCompile(`(?i)\blast\s+week\b`)
	reThisMonth        = regexp.MustCompile(`(?i)\bthis\s+month\b`)
	reLastMonth        = regexp.MustCompile(`(?i)\blast\s+month\b`)
	reRecently         = regexp.MustCompile(`(?i)\brecently\b`)
	reMultipleSpaces   = regexp.MustCompile(`\s{2,}`)
	reSpaceBeforePunct = regexp.MustCompile(`\s+([?.!,;:])/`)
)

func parseNumberOrWord(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, ok := wordToNumber[s]; ok {
		return n
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return 1
}

// ParseTemporalAnchor extracts relative time expressions from a query string and returns
// the corresponding TemporalTarget (anchored against now.UTC()) and the cleaned query with
// the temporal phrase excised. If no temporal anchor is detected, it returns nil and the unmodified query.
func ParseTemporalAnchor(query string, now time.Time) (*TemporalTarget, string) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, query
	}
	nowUTC := now.UTC()

	type matchCandidate struct {
		target *TemporalTarget
		loc    []int
	}

	var match *matchCandidate

	checkMatch := func(re *regexp.Regexp, fn func(raw string, submatches []string) *TemporalTarget) bool {
		locs := re.FindStringSubmatchIndex(trimmed)
		if len(locs) >= 2 {
			raw := trimmed[locs[0]:locs[1]]
			var subs []string
			for i := 2; i < len(locs); i += 2 {
				if locs[i] >= 0 && locs[i+1] >= 0 {
					subs = append(subs, trimmed[locs[i]:locs[i+1]])
				}
			}
			target := fn(raw, subs)
			if target != nil {
				match = &matchCandidate{
					target: target,
					loc:    locs[:2],
				}
				return true
			}
		}
		return false
	}

	// 1. Qualitative / specific phrases
	if checkMatch(reEarlierToday, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC,
			WindowDays: 0.5,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reCoupleDaysAgo, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-2 * 24 * time.Hour),
			WindowDays: 2.0,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reNDaysAgo, func(raw string, subs []string) *TemporalTarget {
		n := 1
		if len(subs) > 0 {
			n = parseNumberOrWord(subs[0])
		}
		offsetDays := float64(n)
		window := math.Max(2.0, offsetDays*0.3)
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-time.Duration(offsetDays * 24 * float64(time.Hour))),
			WindowDays: window,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reNWeeksAgo, func(raw string, subs []string) *TemporalTarget {
		n := 1
		if len(subs) > 0 {
			n = parseNumberOrWord(subs[0])
		}
		offsetDays := float64(n * 7)
		window := float64(n * 3)
		if window < 3.0 {
			window = 3.0
		}
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-time.Duration(offsetDays * 24 * float64(time.Hour))),
			WindowDays: window,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reNMonthsAgo, func(raw string, subs []string) *TemporalTarget {
		n := 1
		if len(subs) > 0 {
			n = parseNumberOrWord(subs[0])
		}
		offsetDays := float64(n * 30)
		window := float64(n * 10)
		if window < 10.0 {
			window = 10.0
		}
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-time.Duration(offsetDays * 24 * float64(time.Hour))),
			WindowDays: window,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reYesterday, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-24 * time.Hour),
			WindowDays: 1.5,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reToday, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC,
			WindowDays: 1.0,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reLastWeek, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-7 * 24 * time.Hour),
			WindowDays: 5.0,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reThisWeek, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC,
			WindowDays: 4.0,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reLastMonth, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-30 * 24 * time.Hour),
			WindowDays: 15.0,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reThisMonth, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC,
			WindowDays: 15.0,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	} else if checkMatch(reRecently, func(raw string, _ []string) *TemporalTarget {
		return &TemporalTarget{
			TargetDate: nowUTC.Add(-2 * 24 * time.Hour),
			WindowDays: 7.0,
			MaxBoost:   DefaultMaxTemporalBoost,
			RawAnchor:  raw,
		}
	}) {
		// matched
	}

	if match == nil {
		return nil, query
	}

	// Excise the anchor cleanly from the query string
	start, end := match.loc[0], match.loc[1]
	cleaned := trimmed[:start] + " " + trimmed[end:]
	cleaned = reMultipleSpaces.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)

	// Clean up any awkward spaces before punctuation: "foo ?" -> "foo?"
	cleaned = strings.ReplaceAll(cleaned, " ?", "?")
	cleaned = strings.ReplaceAll(cleaned, " !", "!")
	cleaned = strings.ReplaceAll(cleaned, " ,", ",")
	cleaned = strings.ReplaceAll(cleaned, " .", ".")

	return match.target, cleaned
}

// CalculateTemporalBoost returns the relative multiplier in [0.0, target.MaxBoost]
// based on the linear date proximity between docDate and target.TargetDate.
//
//	Δ_days = |T_doc - T_target| / 24h
//	ratio  = max(0.0, 1.0 - Δ_days / W_days)
//	boost  = MaxBoost * ratio
func CalculateTemporalBoost(docDate time.Time, target *TemporalTarget) float64 {
	if target == nil || docDate.IsZero() || target.WindowDays <= 0 {
		return 0.0
	}
	deltaHours := math.Abs(docDate.UTC().Sub(target.TargetDate.UTC()).Hours())
	deltaDays := deltaHours / 24.0

	ratio := 1.0 - (deltaDays / target.WindowDays)
	if ratio < 0.0 {
		ratio = 0.0
	}

	maxBoost := target.MaxBoost
	if maxBoost <= 0.0 {
		maxBoost = DefaultMaxTemporalBoost
	}
	return maxBoost * ratio
}

// ApplyTemporalBoostHigherBetter scales a relevance score (where higher is better) by (1.0 + boost).
func ApplyTemporalBoostHigherBetter(score float64, boost float64) float64 {
	if boost <= 0.0 {
		return score
	}
	return score * (1.0 + boost)
}

// ApplyTemporalBoostLowerBetter scales a rank or distance score (where lower is better) by (1.0 - boost).
func ApplyTemporalBoostLowerBetter(distance float64, boost float64) float64 {
	if boost <= 0.0 {
		return distance
	}
	return distance * (1.0 - boost)
}

// ParseDocTimestamp parses a variety of standard timestamp representations found across Sila records.
func ParseDocTimestamp(ts string) (time.Time, bool) {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}, false
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, ts); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
