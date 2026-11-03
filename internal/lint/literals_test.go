package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormatAndSQLPredicates(t *testing.T) {
	if !isFormatString("failed with code %d: %s") {
		t.Fatal("expected format string detection")
	}
	if isFormatString("plain unformatted string") {
		t.Fatal("expected plain string not to be format string")
	}

	if !isSQL("SELECT * FROM projects WHERE id = ?") {
		t.Fatal("expected SQL detection for SELECT")
	}
	if !isSQL("CREATE TABLE IF NOT EXISTS facts (id TEXT)") {
		t.Fatal("expected SQL detection for CREATE TABLE")
	}
	if isSQL("normal configuration text") {
		t.Fatal("expected regular text not to be SQL")
	}
}

func TestSuggestConstName(t *testing.T) {
	errName := suggestConstName("record not found in store", true)
	if !strings.HasPrefix(errName, "ErrRecordNotFound") {
		t.Fatalf("expected ErrRecordNotFound prefix, got %s", errName)
	}

	constName := suggestConstName("candidate_evaluated", false)
	if constName != "CandidateEvaluated" {
		t.Fatalf("expected CandidateEvaluated, got %s", constName)
	}
}

func TestScanSourceAST(t *testing.T) {
	tmpDir := t.TempDir()

	srcFile := filepath.Join(tmpDir, "example.go")
	code := `package sample

import "errors"

const DeclaredConst = "declared_constant_value"

func DoWork() error {
	msg := "magic_domain_token"
	msg2 := "magic_domain_token"
	msg3 := "magic_domain_token"
	msg4 := "magic_domain_token"
	_ = msg + msg2 + msg3 + msg4

	if false {
		return errors.New("sentinel failure occurred")
	}
	if false {
		return errors.New("sentinel failure occurred")
	}
	return nil
}
`
	if err := os.WriteFile(srcFile, []byte(code), 0o600); err != nil {
		t.Fatalf("write sample source: %v", err)
	}

	cfg := DefaultConfig()
	cfg.MinOccurrences = 3

	rep, err := ScanFiles([]string{srcFile}, cfg, time.Now())
	if err != nil {
		t.Fatalf("scan files: %v", err)
	}

	if rep.Clean {
		t.Fatal("expected violations in sample source")
	}

	foundMagic := false
	foundErr := false
	for _, v := range rep.Violations {
		if v.Literal == "magic_domain_token" {
			foundMagic = true
			if v.Count != 4 {
				t.Fatalf("expected 4 occurrences, got %d", v.Count)
			}
			if v.Category != "magic_string" {
				t.Fatalf("expected magic_string, got %s", v.Category)
			}
		}
		if v.Literal == "sentinel failure occurred" {
			foundErr = true
			if v.Category != "sentinel_error" {
				t.Fatalf("expected sentinel_error, got %s", v.Category)
			}
		}
	}

	if !foundMagic {
		t.Fatal("expected magic_domain_token to be flagged")
	}
	if !foundErr {
		t.Fatal("expected sentinel failure occurred to be flagged")
	}
}

func TestScanDirPerformance(t *testing.T) {
	// Scan the internal/andon package as a real directory test
	andonDir := filepath.Join("..", "andon")
	if _, err := os.Stat(andonDir); err != nil {
		t.Skip("andon dir not found")
	}

	cfg := DefaultConfig()
	start := time.Now()
	rep, err := ScanDir(andonDir, cfg)
	dur := time.Since(start)

	if err != nil {
		t.Fatalf("scan dir error: %v", err)
	}
	if dur > 500*time.Millisecond {
		t.Fatalf("scan took too long: %v (expected <500ms)", dur)
	}
	if rep.FilesScanned == 0 {
		t.Fatal("expected at least 1 file scanned in andon package")
	}
}

func TestScanCurrentRepo(t *testing.T) {
	repoDir := filepath.Join("..", "..")
	cfg := DefaultConfig()
	// Set threshold reasonable for full repo
	cfg.MinOccurrences = 6
	start := time.Now()
	rep, err := ScanDir(repoDir, cfg)
	dur := time.Since(start)

	if err != nil {
		t.Fatalf("scan repo error: %v", err)
	}
	t.Logf("Scanned %d files (%d literals) in %v: found %d violations", rep.FilesScanned, rep.TotalLiterals, dur, len(rep.Violations))
	for _, v := range rep.Violations {
		t.Logf("Violation: %q (count: %d, category: %s, suggestion: %s)", v.Literal, v.Count, v.Category, v.Suggestion)
	}
}
