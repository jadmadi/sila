package learn

import (
	"strings"
	"testing"
	"time"
)

func TestHarmonizeRule(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"enable WAL and busy_timeout", "Enable WAL and busy_timeout"},
		{"use constructor injection", "Use constructor injection"},
		{"explicit dependencies", "Ensure explicit dependencies"},
		{"Ensure X", "Ensure X"},
		{"ensure X", "Ensure X"},
		{"never do X", "Never do X"},
		{"always X", "Always X"},
		{"avoid X", "Avoid X"},
		{"do not store plaintext secrets", "Do not store plaintext secrets"},
		{"wrap transactions in mutex", "Wrap transactions in mutex"},
		{"Verify migrations before push", "Verify migrations before push"},
		{"Ensure enable WAL mode", "Enable WAL mode"},
		{"Ensure ensure the guard is installed", "Ensure the guard is installed"},
		{"Ensure Ensure X", "Ensure X"},
		{"settings must be validated", "Ensure settings must be validated"},
		{"useful patterns are documented", "Ensure useful patterns are documented"},
		{"", ""},
		{"   ", ""},
	}

	for _, c := range cases {
		got := HarmonizeRule(c.input)
		if got != c.expected {
			t.Errorf("HarmonizeRule(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

// TestHarmonizeRuleNeverDoublePrefixesImperatives enforces the repository-wide
// negative invariant: no input can produce "Ensure <imperative>" or
// "Ensure Ensure".
func TestHarmonizeRuleNeverDoublePrefixesImperatives(t *testing.T) {
	for _, verb := range imperativeVerbs {
		for _, input := range []string{
			verb + " the thing",
			"Ensure " + verb + " the thing",
			"ensure " + verb + " the thing",
		} {
			got := HarmonizeRule(input)
			lower := strings.ToLower(got)
			if lower == "ensure "+verb || strings.HasPrefix(lower, "ensure "+verb+" ") {
				t.Fatalf("HarmonizeRule(%q) produced double imperative %q", input, got)
			}
			if strings.Contains(lower, "ensure ensure") {
				t.Fatalf("HarmonizeRule(%q) produced %q", input, got)
			}
		}
	}
}

func TestDeduplicate_DoesNotMergeAcrossKinds(t *testing.T) {
	now := time.Now()
	lesson := OperationalLearning{
		ID:            "lesson-dependency",
		Kind:          KindLesson,
		Domain:        DomainGeneral,
		Title:         "Dependency injection",
		Symptom:       "Dependency injection",
		RootCause:     "Implicit global wiring",
		PivotalFix:    "Use constructor injection",
		GuardrailRule: "Ensure explicit dependencies",
		CreatedAt:     now,
	}
	c := caseFixture()
	c.Domain = DomainGeneral
	c.Title = "Dependency injection"

	got := Deduplicate([]OperationalLearning{lesson, c})
	if len(got) != 2 {
		t.Fatalf("cross-kind records merged: %d -> %+v", len(got), got)
	}

	var foundCase *OperationalLearning
	for i := range got {
		if got[i].EffectiveKind() == KindCase {
			foundCase = &got[i]
		}
	}
	if foundCase == nil {
		t.Fatal("case disappeared during dedup")
	}
	if foundCase.Intent != c.Intent || foundCase.Approach != c.Approach || foundCase.Invariant != c.Invariant {
		t.Fatalf("case fields lost during dedup: %+v", foundCase)
	}
}

func TestJaccardSimilarity(t *testing.T) {
	s1 := "Cloudflare D1 batching insert queries in loop"
	s2 := "Cloudflare D1 query batching inside loop"

	score := JaccardSimilarity(s1, s2)
	if score < 0.60 {
		t.Fatalf("expected high Jaccard overlap, got %f", score)
	}

	disjoint := JaccardSimilarity("golang goroutine leak", "python django templates")
	if disjoint > 0.10 {
		t.Fatalf("expected near-zero Jaccard overlap, got %f", disjoint)
	}
}

func TestLCSSimilarity(t *testing.T) {
	s1 := "Synthetic Secret Sanitization in Test Fixtures"
	s2 := "Synthetic Secret Sanitization in Test Fixtures"
	if score := LCSSimilarity(s1, s2); score != 1.0 {
		t.Fatalf("expected exact LCS 1.0, got %f", score)
	}

	diff := LCSSimilarity("abcdef", "uvwxyz")
	if diff != 0.0 {
		t.Fatalf("expected 0.0 for disjoint strings, got %f", diff)
	}
}

func TestDeduplicate_MergesAndBoostsAcrossProjects(t *testing.T) {
	now := time.Now()
	l1 := OperationalLearning{
		ID:                "l1",
		ProjectID:         "project-alpha",
		Domain:            DomainD1,
		Title:             "Cloudflare D1 Query Batching in Loops",
		Symptom:           "Hitting Cloudflare Worker CPU limits due to unbatched D1 inserts in loop",
		RootCause:         "Executing individual await db.insert calls inside for loop",
		PivotalFix:        "Use env.DB.batch([...])",
		GuardrailRule:     "Always batch D1 queries",
		Tags:              []string{"d1", "cloudflare"},
		Confidence:        "high",
		SourceOccurrences: 1,
		CreatedAt:         now.Add(-time.Hour),
	}

	l2 := OperationalLearning{
		ID:                "l2",
		ProjectID:         "project-beta",
		Domain:            DomainD1,
		Title:             "Cloudflare D1 Query Batching in Loops",
		Symptom:           "Hitting Cloudflare Worker CPU limits due to unbatched D1 inserts in loop",
		RootCause:         "Executing individual await db.insert calls inside for loop",
		PivotalFix:        "Batch statements with env.DB.batch([...]) to eliminate N+1 roundtrips",
		GuardrailRule:     "Always batch D1 database statements",
		Tags:              []string{"d1", "performance", "edge"},
		Confidence:        "medium",
		SourceOccurrences: 1,
		CreatedAt:         now,
	}

	l3 := OperationalLearning{
		ID:                "l3",
		ProjectID:         "project-gamma",
		Domain:            DomainGo,
		Title:             "Goroutine Leak Detection with Goleak",
		Symptom:           "Tests leak goroutines across packages",
		RootCause:         "Background workers without cancellation",
		PivotalFix:        "Use context.WithCancel and verify with goleak",
		GuardrailRule:     "Always verify goroutines with goleak in tests",
		Tags:              []string{"golang", "goroutines"},
		Confidence:        "high",
		SourceOccurrences: 1,
		CreatedAt:         now,
	}

	input := []OperationalLearning{l1, l2, l3}
	merged := Deduplicate(input)

	if len(merged) != 2 {
		t.Fatalf("expected 2 merged learnings (1 D1 + 1 Go), got %d: %+v", len(merged), merged)
	}

	d1Learning := merged[0]
	if d1Learning.Domain != DomainD1 {
		t.Errorf("expected DomainD1 for first learning, got %s", d1Learning.Domain)
	}
	if d1Learning.Confidence != "verified" {
		t.Errorf("expected confidence upgraded to 'verified' across projects, got %q", d1Learning.Confidence)
	}
	if d1Learning.SourceOccurrences != 2 {
		t.Errorf("expected SourceOccurrences=2, got %d", d1Learning.SourceOccurrences)
	}
	// Verify tags merged
	tagMap := make(map[string]bool)
	for _, tg := range d1Learning.Tags {
		tagMap[tg] = true
	}
	if !tagMap["performance"] || !tagMap["cloudflare"] || !tagMap["d1"] {
		t.Errorf("expected merged tags to contain performance, cloudflare, and d1: %+v", d1Learning.Tags)
	}
}
