package importmem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRule(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRedactionRulesMaskSamples(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, dir, "acme.yaml", "name: acme-key\npattern: \"ACME_[0-9]{4}\"\nsamples:\n  - ACME_1234\n")
	redactor, err := NewRedactor(RedactorOptions{RulesDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	result, err := redactor.Redact("value ACME_9999 end", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, "ACME_9999") {
		t.Fatalf("rule sample survived: %q", result.Text)
	}
	if result.Rules["acme-key"] != 1 || result.Counts["secret"] == 0 {
		t.Fatalf("counts = %+v rules = %+v", result.Counts, result.Rules)
	}
}

func TestRedactionRulesRejectInvalid(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, dir, "bad.yaml", "name: broken-key\npattern: \"[\"\nsamples:\n  - anything\n")
	if _, err := NewRedactor(RedactorOptions{RulesDir: dir}); err == nil || !strings.Contains(err.Error(), "RULE_INVALID_REGEX: broken-key") {
		t.Fatalf("err = %v", err)
	}

	dir = t.TempDir()
	writeRule(t, dir, "mismatch.yaml", "name: mismatch-key\npattern: \"ABC[0-9]+\"\nsamples:\n  - nope\n")
	if _, err := NewRedactor(RedactorOptions{RulesDir: dir}); err == nil || !strings.Contains(err.Error(), "RULE_SAMPLE_MISMATCH: mismatch-key") {
		t.Fatalf("err = %v", err)
	}

	dir = t.TempDir()
	writeRule(t, dir, "one.yaml", "name: dup\npattern: \"A\"\nsamples:\n  - A\n")
	writeRule(t, dir, "two.yaml", "name: dup\npattern: \"B\"\nsamples:\n  - B\n")
	if _, err := NewRedactor(RedactorOptions{RulesDir: dir}); err == nil || !strings.Contains(err.Error(), "duplicate redactor rule") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedactionRulesLoadFromRepoRoot(t *testing.T) {
	repo := t.TempDir()
	writeRule(t, filepath.Join(repo, ".sila", "redactors"), "repo.yaml", "name: repo-key\npattern: \"REPO_[A-Z]{3}\"\nsamples:\n  - REPO_ABC\n")
	redactor, err := NewRedactor(RedactorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := redactor.Redact("value REPO_XYZ end", repo)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, "REPO_XYZ") || result.Rules["repo-key"] != 1 {
		t.Fatalf("text=%q rules=%+v", result.Text, result.Rules)
	}
}

func TestRedactionRulesEmptyDir(t *testing.T) {
	redactor, err := NewRedactor(RedactorOptions{RulesDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	result, err := redactor.Redact("plain text", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "plain text" || len(result.Rules) != 0 {
		t.Fatalf("result = %+v", result)
	}
}
