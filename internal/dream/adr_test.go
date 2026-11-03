package dream

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestParseADRExtractsFields(t *testing.T) {
	body := `# ADR-0007: Adopt pure-Go SQLite

- **Status**: Accepted
- **Date**: 2026-09-07
- **Deciders**: @jadmadi, devin
- **Tags**: storage, sqlite

## Context

We need a single-binary store.

## Decision

Use modernc.org/sqlite with CGO_ENABLED=0.

## Consequences

No CGO build step.

## References

- ` + "`internal/store/store.go`" + `
- ` + "`internal/dream/adr.go`" + `
`
	a, err := ParseADR("docs/adr/ADR-0007-adopt-pure-go-sqlite.md", []byte(body))
	if err != nil {
		t.Fatalf("ParseADR: %v", err)
	}
	if a.Number != 7 {
		t.Errorf("Number = %d, want 7", a.Number)
	}
	if a.Title != "Adopt pure-Go SQLite" {
		t.Errorf("Title = %q", a.Title)
	}
	if a.Status != StatusAccepted {
		t.Errorf("Status = %q, want Accepted", a.Status)
	}
	if a.Date != "2026-09-07" {
		t.Errorf("Date = %q", a.Date)
	}
	if a.Deciders != "@jadmadi, devin" {
		t.Errorf("Deciders = %q", a.Deciders)
	}
	if len(a.Tags) != 2 {
		t.Errorf("Tags = %v", a.Tags)
	}
	if len(a.References) != 2 {
		t.Errorf("References = %v, want 2", a.References)
	}
}

func TestNormalizeStatus(t *testing.T) {
	cases := map[string]ADRStatus{
		"accepted":   StatusAccepted,
		"Accept":     StatusAccepted,
		"proposed":   StatusProposed,
		"superceded": StatusSuperceded,
		"superseded": StatusSuperceded,
		"deprecated": StatusDeprecated,
		"retired":    StatusDeprecated,
		"unknown":    StatusProposed,
	}
	for in, want := range cases {
		if got := normalizeStatus(in); got != want {
			t.Errorf("normalizeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Adopt Pure-Go SQLite": "adopt-pure-go-sqlite",
		"Use  FTS5  Now":       "use-fts5-now",
		"  spaces  ":           "spaces",
		"":                     "decision",
		"!!!symbols!!!":        "symbols",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractFileRefsSkipsURLsAndAbsPaths(t *testing.T) {
	body := "## References\n\n- `https://example.com/x`\n- `$HOME/env`\n- `internal/dream/adr.go`\n- `cmd/sila/main.go`\n"
	refs := ExtractFileRefs(body)
	if len(refs) != 2 {
		t.Fatalf("ExtractFileRefs = %v, want 2 file refs", refs)
	}
	for _, r := range refs {
		if strings.Contains(r, "://") || strings.HasPrefix(r, "$") {
			t.Errorf("should have skipped non-file ref %q", r)
		}
	}
}

func TestCreateAndLoadADRs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs/adr")
	path, body, err := CreateADR(dir, "Adopt pure-Go SQLite", "@jadmadi", []string{"internal/store/store.go"})
	if err != nil {
		t.Fatalf("CreateADR: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(path), "ADR-0001-") {
		t.Errorf("expected ADR-0001 filename, got %s", filepath.Base(path))
	}
	if !strings.Contains(body, "# ADR-0001: Adopt pure-Go SQLite") {
		t.Errorf("body missing header:\n%s", body)
	}
	if !strings.Contains(body, "internal/store/store.go") {
		t.Errorf("body missing seeded reference")
	}

	// Second ADR should be number 2.
	if _, _, err := CreateADR(dir, "Second decision", "", nil); err != nil {
		t.Fatalf("CreateADR #2: %v", err)
	}
	adrs, err := LoadADRs(dir)
	if err != nil {
		t.Fatalf("LoadADRs: %v", err)
	}
	if len(adrs) != 2 {
		t.Fatalf("LoadADRs = %d ADRs, want 2", len(adrs))
	}
	if adrs[0].Number != 1 || adrs[1].Number != 2 {
		t.Errorf("expected sorted by number: got %d, %d", adrs[0].Number, adrs[1].Number)
	}
}

func TestDoctorDetectsDrift(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs/adr")
	// Create a real file on disk.
	realFile := filepath.Join(root, "internal/store/store.go")
	if err := os.MkdirAll(filepath.Dir(realFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realFile, []byte("package store"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ADR references one existing file and one missing file.
	body := "# ADR-0001: Test\n\n- **Status**: Accepted\n\n## References\n\n- `internal/store/store.go`\n- `internal/dream/missing.go`\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ADR-0001-test.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Doctor(dir)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Total != 1 {
		t.Fatalf("Total = %d, want 1", report.Total)
	}
	if report.Healthy != 0 || report.Drifted != 1 {
		t.Errorf("Healthy=%d Drifted=%d, want 0/1", report.Healthy, report.Drifted)
	}
	if report.TotalRefs != 2 || report.MissingRefs != 1 {
		t.Errorf("TotalRefs=%d MissingRefs=%d, want 2/1", report.TotalRefs, report.MissingRefs)
	}
	h := report.Health[0]
	if h.Present != 1 || h.Missing != 1 {
		t.Errorf("Present=%d Missing=%d, want 1/1", h.Present, h.Missing)
	}
	if len(h.MissingRef) != 1 || h.MissingRef[0] != "internal/dream/missing.go" {
		t.Errorf("MissingRef = %v, want [internal/dream/missing.go]", h.MissingRef)
	}
}

func TestDoctorHealthyWhenAllRefsExist(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs/adr")
	realFile := filepath.Join(root, "internal/dream/adr.go")
	if err := os.MkdirAll(filepath.Dir(realFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realFile, []byte("package dream"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "# ADR-0001: Grounded\n\n- **Status**: Accepted\n\n## References\n\n- `internal/dream/adr.go`\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ADR-0001-grounded.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Doctor(dir)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if report.Healthy != 1 || report.Drifted != 0 {
		t.Errorf("Healthy=%d Drifted=%d, want 1/0", report.Healthy, report.Drifted)
	}
}

func TestCompileIndexGroupsByStatus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs/adr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	accepted := "# ADR-0001: First\n\n- **Status**: Accepted\n- **Date**: 2026-01-01\n"
	proposed := "# ADR-0002: Second\n\n- **Status**: Proposed\n- **Date**: 2026-02-02\n"
	if err := os.WriteFile(filepath.Join(dir, "ADR-0001-first.md"), []byte(accepted), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ADR-0002-second.md"), []byte(proposed), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := CompileIndex(dir)
	if err != nil {
		t.Fatalf("CompileIndex: %v", err)
	}
	if !strings.Contains(idx, "## Accepted (1)") {
		t.Errorf("index missing Accepted group:\n%s", idx)
	}
	if !strings.Contains(idx, "## Proposed (1)") {
		t.Errorf("index missing Proposed group:\n%s", idx)
	}
	if !strings.Contains(idx, "ADR-0001: First") {
		t.Errorf("index missing ADR-0001 link")
	}
	if !strings.Contains(idx, "ADR-0002: Second") {
		t.Errorf("index missing ADR-0002 link")
	}
}

func TestFromMemoPromotesFactToADR(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_adr_memo.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	projectID := "testproj"
	cwd := "/tmp/testproj"
	memo, err := st.InsertMemo(projectID, "Always use pure-Go SQLite with modernc.org/sqlite", "devin", cwd, []string{"internal/store/store.go"})
	if err != nil {
		t.Fatalf("InsertMemo: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "docs/adr")
	path, adr, err := FromMemo(st, dir, projectID, memo.ID)
	if err != nil {
		t.Fatalf("FromMemo: %v", err)
	}
	if path == "" || adr == nil {
		t.Fatalf("expected path and adr, got %q / %v", path, adr)
	}
	if adr.Number != 1 {
		t.Errorf("Number = %d, want 1", adr.Number)
	}
	if !strings.Contains(adr.Title, "Always use pure-Go SQLite") {
		t.Errorf("Title = %q", adr.Title)
	}
	if len(adr.References) != 1 || adr.References[0] != "internal/store/store.go" {
		t.Errorf("References = %v, want [internal/store/store.go]", adr.References)
	}
}

func TestFromMemoRejectsNonMemoFact(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_adr_nonmemo.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// Insert a regular fact that is NOT a manual memo by upserting directly.
	f := store.Fact{
		ID:            "fact-nonmemo-001",
		ProjectID:     "testproj",
		Claim:         "some distilled claim",
		DistillStatus: "distilled",
		SessionRef:    "session-xyz",
	}
	if err := st.UpsertFact(f); err != nil {
		t.Fatalf("UpsertFact: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "docs/adr")
	_, _, err = FromMemo(st, dir, "testproj", f.ID)
	if err == nil {
		t.Fatal("FromMemo should reject non-memo facts")
	}
	if !strings.Contains(err.Error(), "not a manual memo") {
		t.Errorf("expected 'not a manual memo' error, got %v", err)
	}
}

func TestFromMemoUnknownIDErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_adr_unknown.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	dir := filepath.Join(t.TempDir(), "docs/adr")
	_, _, err = FromMemo(st, dir, "testproj", "does-not-exist")
	if err == nil {
		t.Fatal("FromMemo should error on unknown ID")
	}
}

func TestNextADRNumberOnEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "docs/adr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if n := NextADRNumber(dir); n != 1 {
		t.Errorf("NextADRNumber on empty dir = %d, want 1", n)
	}
}

func TestNextADRNumberOnMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nonexistent")
	if n := NextADRNumber(dir); n != 1 {
		t.Errorf("NextADRNumber on missing dir = %d, want 1", n)
	}
}

func TestRenderTemplateSections(t *testing.T) {
	body := RenderTemplate(3, "Test decision", "tester", nil)
	required := []string{
		"# ADR-0003: Test decision",
		"- **Status**: Proposed",
		"## Context",
		"## Decision",
		"## Consequences",
		"## References",
	}
	for _, s := range required {
		if !strings.Contains(body, s) {
			t.Errorf("RenderTemplate missing %q in:\n%s", s, body)
		}
	}
}
