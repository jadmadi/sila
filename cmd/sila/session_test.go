package main

import (
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
)

func TestParseSessionAge(t *testing.T) {
	cases := map[string]time.Duration{
		"":    90 * 24 * time.Hour,
		"90d": 90 * 24 * time.Hour,
		"24h": 24 * time.Hour,
	}
	for raw, want := range cases {
		got, err := parseSessionAge(raw)
		if err != nil || got != want {
			t.Fatalf("parseSessionAge(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := parseSessionAge("nonsense"); err == nil {
		t.Fatal("invalid age should fail")
	}
}

func TestSameSessionClaim(t *testing.T) {
	if !sameSessionClaim(&goals.GoalClaim{SessionID: "s1", Agent: "a"}, "s1", "a") {
		t.Fatal("matching session claim was not recognized")
	}
	if sameSessionClaim(&goals.GoalClaim{SessionID: "s2", Agent: "a"}, "s1", "a") {
		t.Fatal("foreign session claim was accepted")
	}
	if !sameSessionClaim(&goals.GoalClaim{Agent: "a"}, "s1", "a") {
		t.Fatal("legacy agent-only claim was not recognized")
	}
	if sameSessionClaim(&goals.GoalClaim{Agent: "b"}, "s1", "a") {
		t.Fatal("foreign agent claim was accepted")
	}
}
