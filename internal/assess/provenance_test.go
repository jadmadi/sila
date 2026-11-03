package assess

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestLookupKnownAuthority(t *testing.T) {
	cases := []struct {
		url          string
		wantPub      string
		wantLicense  store.SourceLicense
		wantQuotable bool
	}{
		{
			url:          "https://www.w3.org/TR/WCAG22/",
			wantPub:      "World Wide Web Consortium (W3C)",
			wantLicense:  store.LicenseCCBY,
			wantQuotable: true,
		},
		{
			url:          "https://www.rfc-editor.org/rfc/rfc9110",
			wantPub:      "RFC Editor",
			wantLicense:  store.LicenseFreeToRead,
			wantQuotable: false,
		},
		{
			url:          "https://owasp.org/www-project-top-ten/",
			wantPub:      "OWASP Foundation",
			wantLicense:  store.LicenseCCBY,
			wantQuotable: true,
		},
		{
			url:          "https://csrc.nist.gov/publications/detail/sp/800-53/rev-5/final",
			wantPub:      "National Institute of Standards and Technology (NIST)",
			wantLicense:  store.LicensePublicDomain,
			wantQuotable: true,
		},
		{
			url:          "https://www.iso.org/standard/74528.html",
			wantPub:      "International Organization for Standardization (ISO)",
			wantLicense:  store.LicenseISO,
			wantQuotable: false,
		},
	}

	for _, tc := range cases {
		pub, lic, quotable, ok := LookupKnownAuthority(tc.url)
		if !ok {
			t.Fatalf("expected authority lookup to succeed for %q", tc.url)
		}
		if pub != tc.wantPub {
			t.Errorf("URL %q: expected publisher %q, got %q", tc.url, tc.wantPub, pub)
		}
		if lic != tc.wantLicense {
			t.Errorf("URL %q: expected license %q, got %q", tc.url, tc.wantLicense, lic)
		}
		if quotable != tc.wantQuotable {
			t.Errorf("URL %q: expected quotable %v, got %v", tc.url, tc.wantQuotable, quotable)
		}
	}
}

func TestValidateLicenseDeclaration(t *testing.T) {
	for _, lic := range []string{"public-domain", "cc-by", "free-to-read", "iso", "paywalled"} {
		parsed, err := ValidateLicenseDeclaration(lic)
		if err != nil {
			t.Errorf("expected %q to be valid, got err: %v", lic, err)
		}
		if string(parsed) != lic {
			t.Errorf("expected %q, got %q", lic, parsed)
		}
	}

	for _, bad := range []string{"mit", "apache-2.0", "gpl-3.0", "proprietary", "custom"} {
		if _, err := ValidateLicenseDeclaration(bad); err == nil {
			t.Errorf("expected error for non-catalog license %q", bad)
		}
	}
}

func TestScanProvenance(t *testing.T) {
	tmp := t.TempDir()

	// Create test rule file with citations
	content := `# Engineering Standards
Citing W3C: [WCAG 2.2](https://www.w3.org/TR/WCAG22/)
Citing RFC: https://www.rfc-editor.org/rfc/rfc9110
Citing custom cataloged: [Standards Hub](https://standards.example.org/guidelines)
Citing uncataloged: https://random-blog.net/article
`
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Create SQLite store with custom catalog entry
	dbFile := filepath.Join(tmp, "test.db")
	s, err := store.Open(dbFile)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.SaveSource(&store.SourceCatalogEntry{
		URL:       "https://standards.example.org/guidelines",
		Title:     "Standards Hub Guidelines",
		Publisher: "Standards Consortium",
		License:   store.LicenseFreeToRead,
	}); err != nil {
		t.Fatal(err)
	}

	report, err := ScanProvenance(tmp, s)
	if err != nil {
		t.Fatalf("ScanProvenance failed: %v", err)
	}

	if report.TotalCitations != 4 {
		t.Errorf("expected 4 total citations, got %d", report.TotalCitations)
	}
	if report.VerifiedSources < 3 { // W3C, RFC, and custom cataloged are verified
		t.Errorf("expected at least 3 verified sources, got %d", report.VerifiedSources)
	}

	// Should have warning for uncataloged URL
	hasWarning := false
	for _, v := range report.Violations {
		if v.Severity == "warning" && v.URL == "https://random-blog.net/article" {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("expected warning for uncataloged source random-blog.net")
	}
}
