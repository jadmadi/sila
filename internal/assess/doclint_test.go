package assess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAuditDocCoverage_Fixtures(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "doclint-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pkgDir := filepath.Join(tmpDir, "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("failed to create pkg dir: %v", err)
	}

	src := `package pkg

// GoodFunc does something great.
func GoodFunc() {}

func BadFunc() {}

// OtherPrefix comment does not start with symbol name.
func WrongPrefix() {}

// ExportedType is a valid struct.
type ExportedType struct{}

type BadType struct{}

// ExportedConst is a good const.
const ExportedConst = 1

const BadConst = 2

// ExportedVar is a good var.
var ExportedVar = "good"

var BadVar = "bad"

// MethodOne has a valid doc.
func (e ExportedType) MethodOne() {}

func (e ExportedType) BadMethod() {}
`
	if err := os.WriteFile(filepath.Join(pkgDir, "sample.go"), []byte(src), 0644); err != nil {
		t.Fatalf("failed to write sample.go: %v", err)
	}

	opts := DocLintOptions{
		RequireNamePrefix: true,
		ExcludeDirs:       []string{},
		TargetDirs:        []string{"pkg"},
	}

	report, err := AuditDocCoverage(tmpDir, opts)
	if err != nil {
		t.Fatalf("AuditDocCoverage failed: %v", err)
	}

	if report.Clean {
		t.Errorf("expected report not to be clean")
	}

	// GoodFunc, ExportedType, ExportedConst, ExportedVar, MethodOne = 5 documented
	// BadFunc, WrongPrefix, BadType, BadConst, BadVar, BadMethod = 6 missing
	// Total exported = 11
	if report.TotalExported != 11 {
		t.Errorf("expected 11 total exported, got %d", report.TotalExported)
	}
	if report.Documented != 5 {
		t.Errorf("expected 5 documented, got %d", report.Documented)
	}
	if report.MissingCount != 6 {
		t.Errorf("expected 6 missing, got %d", report.MissingCount)
	}

	toon := RenderDocCoverageTOON(report)
	if toon == "" {
		t.Errorf("expected non-empty TOON rendering")
	}
}

func TestAuditDocCoverage_Codebase(t *testing.T) {
	opts := DefaultDocLintOptions()
	report, err := AuditDocCoverage("../..", opts)
	if err != nil {
		t.Fatalf("failed to audit codebase: %v", err)
	}

	t.Logf("Total exported: %d, Documented: %d, Missing: %d, Coverage: %.2f%%",
		report.TotalExported, report.Documented, report.MissingCount, report.CoveragePct)

	for _, m := range report.Missing {
		t.Logf("Missing: %s:%d [%s] %s (%s)", m.File, m.Line, m.Kind, m.Name, m.Detail)
	}
}
