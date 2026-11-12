package store

import (
	"testing"
	"time"
)

func TestInValidityWindowBoundaries(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	from := now.Format(time.RFC3339)
	until := now.Add(time.Hour).Format(time.RFC3339)
	if !InValidityWindow(Fact{ValidFrom: from}, now) {
		t.Fatal("from must be inclusive")
	}
	if InValidityWindow(Fact{ValidUntil: from}, now) {
		t.Fatal("until must be exclusive")
	}
	if !InValidityWindow(Fact{}, now) {
		t.Fatal("empty window must be open")
	}
	if InValidityWindow(Fact{ValidFrom: now.Add(time.Hour).Format(time.RFC3339)}, now) {
		t.Fatal("future from must hide")
	}
	if !InValidityWindow(Fact{ValidFrom: from, ValidUntil: until}, now) {
		t.Fatal("inside window must show")
	}
	// Offsets are respected.
	offsetFrom := now.Add(2 * time.Hour).Format(time.RFC3339)
	if InValidityWindow(Fact{ValidFrom: offsetFrom}, now) {
		t.Fatal("future offset window must hide")
	}
}

func TestValidateValidityWindow(t *testing.T) {
	now := time.Now().UTC()
	later := now.Add(time.Hour).Format(time.RFC3339)
	earlier := now.Format(time.RFC3339)
	if err := ValidateValidityWindow("", ""); err != nil {
		t.Fatal(err)
	}
	if err := ValidateValidityWindow(earlier, later); err != nil {
		t.Fatal(err)
	}
	if err := ValidateValidityWindow(later, earlier); err == nil {
		t.Fatal("from >= until accepted")
	}
	if err := ValidateValidityWindow("not-a-time", ""); err == nil {
		t.Fatal("invalid RFC3339 accepted")
	}
}
