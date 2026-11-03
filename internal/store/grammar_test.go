package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeriveFeedbackSlug(t *testing.T) {
	tests := []struct {
		name     string
		category string
		content  string
		expected string
	}{
		{
			name:     "Harmonize workspace sovereignty",
			category: "idea",
			content:  "We should harmonize workspace sovereignty across subrepos",
			expected: "harmonize-sovereignty-workspace", // or contains both nouns
		},
		{
			name:     "Sentry toolchain integrity",
			category: "issue",
			content:  "Need to sentry toolchain integrity in pre-push hook",
			expected: "sentry-toolchain-integrity",
		},
		{
			name:     "Unify knowledge toolchains",
			category: "tip",
			content:  "Unify knowledge toolchains for all subagents",
			expected: "unify-toolchain-knowledge",
		},
		{
			name:     "Reconcile workspace sovereignty",
			category: "challenge",
			content:  "Reconciling workspace sovereignty during monorepo migrations",
			expected: "reconcile-sovereignty-workspace",
		},
		{
			name:     "Default category fallback for issue",
			category: "issue",
			content:  "Unexpected network timeout during crawling",
			expected: "diagnose-timeout-crawling",
		},
		{
			name:     "Default category fallback for praise",
			category: "praise",
			content:  "Amazing performance boost with context compression",
			expected: "elevate-context-compression",
		},
		{
			name:     "Default category fallback for tip",
			category: "tip",
			content:  "Keep git commits atomic and well documented",
			expected: "illuminate-commits-atomic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slug := DeriveFeedbackSlug(tc.category, tc.content)
			if slug == "" {
				t.Fatalf("expected non-empty slug")
			}
			parts := strings.Split(slug, "-")
			if len(parts) < 2 {
				t.Fatalf("expected at least 2 parts (verb-noun), got %q", slug)
			}
		})
	}
}

func TestSanitizeFeedbackID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"dialectic-subshell-paths", "fb_dialectic-subshell-paths"},
		{"fb_dialectic-subshell-paths", "fb_dialectic-subshell-paths"},
		{"Dialectic Subshell Paths!", "fb_dialectic-subshell-paths"},
		{"--custom__slug--here--", "fb_custom-slug-here"},
		{"", "fb_observe-verity"},
		{"fb_", "fb_observe-verity"},
		{"fb_12345", "fb_12345"},
		{"fb_cce53225b12e45fc", "fb_cce53225b12e45fc"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			actual := SanitizeFeedbackID(tc.input)
			if actual != tc.expected {
				t.Errorf("SanitizeFeedbackID(%q) = %q, expected %q", tc.input, actual, tc.expected)
			}
		})
	}
}

func TestFeedbackCollisionResolution(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sila.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// 1. Save first feedback item
	fb1 := &Feedback{
		ProjectID: "sila",
		Agent:     "antigravity",
		Category:  "issue",
		Content:   "Sentry toolchain integrity during builds",
	}
	if err := st.SaveFeedback(fb1); err != nil {
		t.Fatalf("SaveFeedback fb1: %v", err)
	}
	if !strings.HasPrefix(fb1.ID, "fb_sentry-") {
		t.Errorf("expected fb1.ID to have prefix fb_sentry-, got %s", fb1.ID)
	}

	// 2. Save second feedback item with exact same content -> must resolve collision with suffix
	time.Sleep(2 * time.Millisecond)
	fb2 := &Feedback{
		ProjectID: "sila",
		Agent:     "claude-code",
		Category:  "issue",
		Content:   "Sentry toolchain integrity during builds",
	}
	if err := st.SaveFeedback(fb2); err != nil {
		t.Fatalf("SaveFeedback fb2: %v", err)
	}

	if fb1.ID == fb2.ID {
		t.Fatalf("expected different IDs for collision, got both %s", fb1.ID)
	}
	if !strings.HasPrefix(fb2.ID, fb1.ID+"-") {
		t.Errorf("expected fb2.ID to extend fb1.ID with entropy suffix, got fb1=%s fb2=%s", fb1.ID, fb2.ID)
	}

	// 3. Both must be retrievable
	ret1, err := st.GetFeedback(fb1.ID)
	if err != nil {
		t.Fatalf("GetFeedback fb1: %v", err)
	}
	if ret1.Agent != "antigravity" {
		t.Errorf("expected fb1 agent antigravity, got %s", ret1.Agent)
	}

	ret2, err := st.GetFeedback(fb2.ID)
	if err != nil {
		t.Fatalf("GetFeedback fb2: %v", err)
	}
	if ret2.Agent != "claude-code" {
		t.Errorf("expected fb2 agent claude-code, got %s", ret2.Agent)
	}

	// 4. Test explicit ID override
	fbCustom := &Feedback{
		ID:        "dialectic-subshell-paths",
		ProjectID: "sila",
		Agent:     "antigravity",
		Category:  "idea",
		Content:   "Dialectic subshell paths guidance",
	}
	if err := st.SaveFeedback(fbCustom); err != nil {
		t.Fatalf("SaveFeedback fbCustom: %v", err)
	}
	if fbCustom.ID != "fb_dialectic-subshell-paths" {
		t.Errorf("expected fbCustom.ID == fb_dialectic-subshell-paths, got %s", fbCustom.ID)
	}

	// 5. Test backward compatibility with arbitrary legacy hex ID
	fbLegacy := &Feedback{
		ID:        "fb_cce53225b12e45fc",
		ProjectID: "sila",
		Agent:     "legacy-agent",
		Category:  "tip",
		Content:   "Legacy hex ID ticket",
	}
	if err := st.SaveFeedback(fbLegacy); err != nil {
		t.Fatalf("SaveFeedback fbLegacy: %v", err)
	}
	if fbLegacy.ID != "fb_cce53225b12e45fc" {
		t.Errorf("expected fbLegacy.ID == fb_cce53225b12e45fc, got %s", fbLegacy.ID)
	}

	legacyGot, err := st.GetFeedback("fb_cce53225b12e45fc")
	if err != nil {
		t.Fatalf("GetFeedback legacy: %v", err)
	}
	if legacyGot.Content != "Legacy hex ID ticket" {
		t.Errorf("unexpected content: %s", legacyGot.Content)
	}
}
