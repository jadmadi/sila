package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// SourceLicense represents the closed license taxonomy for external reference works.
type SourceLicense string

const (
	LicensePublicDomain SourceLicense = "public-domain" // Quotable, unrestricted public domain
	LicenseCCBY         SourceLicense = "cc-by"         // Quotable with attribution
	LicenseFreeToRead   SourceLicense = "free-to-read"  // Read-only authority, non-quotable
	LicenseISO          SourceLicense = "iso"           // Standards organization authority, read-only
	LicensePaywalled    SourceLicense = "paywalled"     // Gated / subscription authority
)

// ValidSourceLicenses enumerates permitted external source licenses.
var ValidSourceLicenses = map[SourceLicense]bool{
	LicensePublicDomain: true,
	LicenseCCBY:         true,
	LicenseFreeToRead:   true,
	LicenseISO:          true,
	LicensePaywalled:    true,
}

// IsQuotable reports whether the license authorizes verbatim reproduction of excerpts.
func (l SourceLicense) IsQuotable() bool {
	return l == LicensePublicDomain || l == LicenseCCBY
}

// IsValid reports whether the license string matches the closed license taxonomy.
func (l SourceLicense) IsValid() bool {
	return ValidSourceLicenses[l]
}

// SourceCatalogEntry represents an authoritative external reference with provenance metadata.
type SourceCatalogEntry struct {
	ID               string        `json:"id"`
	URL              string        `json:"url"`
	Title            string        `json:"title"`
	Publisher        string        `json:"publisher"`
	License          SourceLicense `json:"license"`
	Jurisdiction     string        `json:"jurisdiction,omitempty"`
	AuthoritativeFor []string      `json:"authoritative_for,omitempty"` // topics or domain tags
	Quotable         bool          `json:"quotable"`
	VerifiedAt       time.Time     `json:"verified_at"`
	CreatedAt        time.Time     `json:"created_at"`
}

// NormalizeSourceURL cleans and normalizes a source URL for canonical deduplication.
func NormalizeSourceURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.ToLower(rawURL)
	}
	u.Host = strings.ToLower(u.Host)
	u.Fragment = "" // Fragment is client-side; canonical sources are page-level
	norm := u.String()
	norm = strings.TrimRight(norm, "/")
	return norm
}

// SourceID generates a deterministic 8-char hex fingerprint for a normalized source URL.
func SourceID(normURL string) string {
	sum := sha256.Sum256([]byte(NormalizeSourceURL(normURL)))
	return "src_" + hex.EncodeToString(sum[:4])
}

// SaveSource inserts or updates a SourceCatalogEntry in the store.
func (s *Store) SaveSource(entry *SourceCatalogEntry) error {
	if entry == nil {
		return fmt.Errorf("nil source entry")
	}
	entry.URL = NormalizeSourceURL(entry.URL)
	if entry.URL == "" {
		return fmt.Errorf("source URL is required")
	}
	if !entry.License.IsValid() {
		return fmt.Errorf("invalid license %q (must be one of: public-domain, cc-by, free-to-read, iso, paywalled)", entry.License)
	}
	if entry.ID == "" {
		entry.ID = SourceID(entry.URL)
	}
	entry.Quotable = entry.License.IsQuotable()

	now := time.Now().UTC()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	if entry.VerifiedAt.IsZero() {
		entry.VerifiedAt = now
	}

	authForJSON, err := json.Marshal(entry.AuthoritativeFor)
	if err != nil {
		authForJSON = []byte("[]")
	}

	query := `INSERT INTO source_catalog (
		id, url, title, publisher, license, jurisdiction, authoritative_for, quotable, verified_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(url) DO UPDATE SET
		title = excluded.title,
		publisher = excluded.publisher,
		license = excluded.license,
		jurisdiction = excluded.jurisdiction,
		authoritative_for = excluded.authoritative_for,
		quotable = excluded.quotable,
		verified_at = excluded.verified_at;`

	_, err = s.DB.Exec(query,
		entry.ID,
		entry.URL,
		entry.Title,
		entry.Publisher,
		string(entry.License),
		entry.Jurisdiction,
		string(authForJSON),
		boolToInt(entry.Quotable),
		entry.VerifiedAt.Format(time.RFC3339),
		entry.CreatedAt.Format(time.RFC3339),
	)
	return err
}

// GetSourceByURL retrieves a catalog entry matching the given normalized URL.
func (s *Store) GetSourceByURL(rawURL string) (*SourceCatalogEntry, error) {
	norm := NormalizeSourceURL(rawURL)
	query := `SELECT id, url, title, publisher, license, jurisdiction, authoritative_for, quotable, verified_at, created_at
		FROM source_catalog WHERE url = ?`
	return s.scanSourceRow(s.DB.QueryRow(query, norm))
}

// GetSourceByID retrieves a catalog entry by primary key ID.
func (s *Store) GetSourceByID(id string) (*SourceCatalogEntry, error) {
	query := `SELECT id, url, title, publisher, license, jurisdiction, authoritative_for, quotable, verified_at, created_at
		FROM source_catalog WHERE id = ?`
	return s.scanSourceRow(s.DB.QueryRow(query, id))
}

// ListSources retrieves all cataloged sources ordered by created_at DESC.
func (s *Store) ListSources() ([]SourceCatalogEntry, error) {
	query := `SELECT id, url, title, publisher, license, jurisdiction, authoritative_for, quotable, verified_at, created_at
		FROM source_catalog ORDER BY created_at DESC`
	rows, err := s.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []SourceCatalogEntry
	for rows.Next() {
		entry, err := s.scanSource(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, *entry)
	}
	return list, rows.Err()
}

// DeleteSource removes a source by ID.
func (s *Store) DeleteSource(id string) error {
	_, err := s.DB.Exec(`DELETE FROM source_catalog WHERE id = ?`, id)
	return err
}

func (s *Store) scanSourceRow(row *sql.Row) (*SourceCatalogEntry, error) {
	var e SourceCatalogEntry
	var licStr, authJSON, verStr, createStr string
	var quotableInt int

	err := row.Scan(
		&e.ID, &e.URL, &e.Title, &e.Publisher, &licStr, &e.Jurisdiction,
		&authJSON, &quotableInt, &verStr, &createStr,
	)
	if err != nil {
		return nil, err
	}
	e.License = SourceLicense(licStr)
	e.Quotable = quotableInt != 0
	_ = json.Unmarshal([]byte(authJSON), &e.AuthoritativeFor)
	e.VerifiedAt, _ = time.Parse(time.RFC3339, verStr)
	e.CreatedAt, _ = time.Parse(time.RFC3339, createStr)
	return &e, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scanSource(sc rowScanner) (*SourceCatalogEntry, error) {
	var e SourceCatalogEntry
	var licStr, authJSON, verStr, createStr string
	var quotableInt int

	err := sc.Scan(
		&e.ID, &e.URL, &e.Title, &e.Publisher, &licStr, &e.Jurisdiction,
		&authJSON, &quotableInt, &verStr, &createStr,
	)
	if err != nil {
		return nil, err
	}
	e.License = SourceLicense(licStr)
	e.Quotable = quotableInt != 0
	_ = json.Unmarshal([]byte(authJSON), &e.AuthoritativeFor)
	e.VerifiedAt, _ = time.Parse(time.RFC3339, verStr)
	e.CreatedAt, _ = time.Parse(time.RFC3339, createStr)
	return &e, nil
}
