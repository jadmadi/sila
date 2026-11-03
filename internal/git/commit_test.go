package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCommitTrailers(t *testing.T) {
	msg := `feat(gate): add architectural pre-commit sentry

This sentry prevents monolithic file bloat and anti-patterns.

Signed-off-by: Jad Madi <jad@example.com>
Sila-Skip-Arch: generated legacy fixtures
Reviewed-by: Lead Architect <lead@example.com>
`
	trailers := ParseCommitTrailers(msg)
	if len(trailers) != 3 {
		t.Fatalf("expected 3 trailers, got %d: %+v", len(trailers), trailers)
	}

	if trailers[0].Key != "Signed-off-by" || trailers[0].Value != "Jad Madi <jad@example.com>" {
		t.Errorf("unexpected trailer 0: %+v", trailers[0])
	}
	if trailers[1].Key != "Sila-Skip-Arch" || trailers[1].Value != "generated legacy fixtures" {
		t.Errorf("unexpected trailer 1: %+v", trailers[1])
	}
	if trailers[2].Key != "Reviewed-by" || trailers[2].Value != "Lead Architect <lead@example.com>" {
		t.Errorf("unexpected trailer 2: %+v", trailers[2])
	}

	// Test GetCommitTrailer case-insensitive
	val, ok := GetCommitTrailer(msg, "sila-skip-arch")
	if !ok || val != "generated legacy fixtures" {
		t.Fatalf("GetCommitTrailer lowercase failed: ok=%v, val=%q", ok, val)
	}

	val, ok = HasSkipArchTrailer(msg)
	if !ok || val != "generated legacy fixtures" {
		t.Fatalf("HasSkipArchTrailer failed: ok=%v, val=%q", ok, val)
	}
}

func TestParseCommitTrailers_WithComments(t *testing.T) {
	msg := `docs: update documentation

# Please enter the commit message for your changes. Lines starting
# with '#' will be ignored, and an empty message aborts the commit.
#
Sila-Skip-Arch: intentional documentation exception
# On branch main
`
	val, ok := HasSkipArchTrailer(msg)
	if !ok || val != "intentional documentation exception" {
		t.Fatalf("failed to parse trailer with git comment lines: ok=%v, val=%q", ok, val)
	}
}

func TestParseCommitTrailers_Empty(t *testing.T) {
	if trailers := ParseCommitTrailers(""); len(trailers) != 0 {
		t.Errorf("expected 0 trailers for empty message, got %d", len(trailers))
	}
	if _, ok := HasSkipArchTrailer("ordinary commit without trailers"); ok {
		t.Error("expected false for commit without trailers")
	}
}

func TestScanCommitMessageFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "COMMIT_EDITMSG")
	content := "feat: test message\n\nSila-Skip-Arch: unit-test-bypass\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	trailers, err := ScanCommitMessageFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(trailers) != 1 || trailers[0].Key != "Sila-Skip-Arch" || trailers[0].Value != "unit-test-bypass" {
		t.Fatalf("unexpected trailers: %+v", trailers)
	}
}
