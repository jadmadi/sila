package store

import (
	"testing"
	"uuid"
)

func TestNativeUUID(t *testing.T) {
	// 1. Generation test
	id := NewUUID()
	if id == "" {
		t.Fatal("NewUUID() returned empty string")
	}

	parsed, err := ParseUUID(id)
	if err != nil {
		t.Fatalf("ParseUUID(%q) failed: %v", id, err)
	}
	if parsed == (uuid.UUID{}) {
		t.Fatal("ParseUUID returned zero UUID")
	}

	if !IsValidUUID(id) {
		t.Fatalf("IsValidUUID(%q) = false, want true", id)
	}

	// 2. UUID v7 generation test
	v7 := NewV7UUID()
	if !IsValidUUID(v7) {
		t.Fatalf("IsValidUUID(NewV7UUID: %q) = false, want true", v7)
	}

	// 3. Invalid UUID test
	if IsValidUUID("not-a-uuid") {
		t.Fatal("IsValidUUID('not-a-uuid') = true, want false")
	}

	// 4. Session ID test
	sess := NewSessionID()
	if !IsValidUUID(sess) {
		t.Fatalf("NewSessionID() = %q is not a valid UUID", sess)
	}

	// 5. Uniqueness test
	id2 := NewUUID()
	if id == id2 {
		t.Fatalf("NewUUID generated collisions: %s", id)
	}
}
