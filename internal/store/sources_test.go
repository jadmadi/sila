package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSources_LicenseTaxonomy(t *testing.T) {
	valid := []SourceLicense{
		LicensePublicDomain,
		LicenseCCBY,
		LicenseFreeToRead,
		LicenseISO,
		LicensePaywalled,
	}

	for _, lic := range valid {
		if !lic.IsValid() {
			t.Errorf("expected license %q to be valid", lic)
		}
	}

	// Invalid licenses
	invalid := []SourceLicense{
		"gpl",
		"mit",
		"proprietary",
		"unknown",
		"",
	}

	for _, lic := range invalid {
		if lic.IsValid() {
			t.Errorf("expected license %q to be invalid in closed taxonomy", lic)
		}
	}

	// Quotability assertions
	if !LicensePublicDomain.IsQuotable() {
		t.Errorf("public-domain must be quotable")
	}
	if !LicenseCCBY.IsQuotable() {
		t.Errorf("cc-by must be quotable")
	}
	if LicenseFreeToRead.IsQuotable() {
		t.Errorf("free-to-read must be read-only (non-quotable)")
	}
	if LicenseISO.IsQuotable() {
		t.Errorf("iso standards authority must be non-quotable")
	}
	if LicensePaywalled.IsQuotable() {
		t.Errorf("paywalled authority must be non-quotable")
	}
}

func TestSources_CRUD(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "sources.db")
	s, err := Open(dbFile)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	// 1. Save valid source
	src1 := &SourceCatalogEntry{
		URL:              "https://W3C.org/TR/WCAG22/#visual-contrast",
		Title:            "Web Content Accessibility Guidelines 2.2",
		Publisher:        "W3C",
		License:          LicenseCCBY,
		Jurisdiction:     "International",
		AuthoritativeFor: []string{"a11y", "accessibility"},
		VerifiedAt:       time.Now().UTC(),
	}

	if err := s.SaveSource(src1); err != nil {
		t.Fatalf("SaveSource failed: %v", err)
	}

	// 2. Fetch by URL (canonicalized)
	fetched, err := s.GetSourceByURL("https://w3c.org/TR/WCAG22")
	if err != nil {
		t.Fatalf("GetSourceByURL failed: %v", err)
	}
	if fetched.Publisher != "W3C" {
		t.Errorf("expected publisher W3C, got %q", fetched.Publisher)
	}
	if !fetched.Quotable {
		t.Errorf("expected CC-BY to be quotable")
	}
	if len(fetched.AuthoritativeFor) != 2 {
		t.Errorf("expected 2 authoritative_for tags, got %d", len(fetched.AuthoritativeFor))
	}

	// 3. Reject invalid license
	badSrc := &SourceCatalogEntry{
		URL:       "https://example.com/unlicensed",
		Title:     "Unknown Work",
		Publisher: "Anonymous",
		License:   "unauthorized-copyleft",
	}
	if err := s.SaveSource(badSrc); err == nil {
		t.Errorf("expected error when saving source with invalid license")
	}

	// 4. Update source (UPSERT)
	src1Updated := &SourceCatalogEntry{
		URL:       "https://w3c.org/TR/WCAG22",
		Title:     "Web Content Accessibility Guidelines (WCAG) 2.2 - Official Recommendation",
		Publisher: "W3C",
		License:   LicensePublicDomain,
	}
	if err := s.SaveSource(src1Updated); err != nil {
		t.Fatalf("SaveSource update failed: %v", err)
	}
	updated, err := s.GetSourceByID(fetched.ID)
	if err != nil {
		t.Fatalf("GetSourceByID failed: %v", err)
	}
	if updated.Title != src1Updated.Title {
		t.Errorf("expected updated title %q, got %q", src1Updated.Title, updated.Title)
	}
	if updated.License != LicensePublicDomain {
		t.Errorf("expected updated license %q, got %q", LicensePublicDomain, updated.License)
	}

	// 5. List sources
	list, err := s.ListSources()
	if err != nil {
		t.Fatalf("ListSources failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 source, got %d", len(list))
	}

	// 6. Delete source
	if err := s.DeleteSource(fetched.ID); err != nil {
		t.Fatalf("DeleteSource failed: %v", err)
	}
	afterDelete, err := s.GetSourceByURL("https://w3c.org/TR/WCAG22")
	if err == nil || afterDelete != nil {
		t.Errorf("expected nil after delete, got %v", afterDelete)
	}
}
