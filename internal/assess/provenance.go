package assess

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// ProvenanceViolation captures a license or reachability issue for an external citation.
type ProvenanceViolation struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	URL      string `json:"url"`
	License  string `json:"license,omitempty"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // "error" | "warning"
}

// Citation records an external source citation detected in knowledge or domain rules.
type Citation struct {
	Path      string              `json:"path"`
	Line      int                 `json:"line"`
	URL       string              `json:"url"`
	Title     string              `json:"title,omitempty"`
	License   store.SourceLicense `json:"license,omitempty"`
	Publisher string              `json:"publisher,omitempty"`
	Quotable  bool                `json:"quotable"`
	Verified  bool                `json:"verified"`
}

// ProvenanceReport captures the outcome of a regulatory source provenance scan.
type ProvenanceReport struct {
	ProjectRoot     string                `json:"project_root"`
	Valid           bool                  `json:"valid"`
	TotalCitations  int                   `json:"total_citations"`
	VerifiedSources int                   `json:"verified_sources"`
	Violations      []ProvenanceViolation `json:"violations"`
	Citations       []Citation            `json:"citations"`
	ScannedFiles    int                   `json:"scanned_files"`
}

var (
	mdLinkRE = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\s\)]+)\)`)
	rawURLRE = regexp.MustCompile(`\b(https?://[^\s<>'"\)]+)`)
)

// StandardKnownAuthorities maps well-known standards organization domains to their authoritative license.
var StandardKnownAuthorities = map[string]struct {
	Publisher string
	License   store.SourceLicense
	Quotable  bool
}{
	"w3.org": {
		Publisher: "World Wide Web Consortium (W3C)",
		License:   store.LicenseCCBY,
		Quotable:  true,
	},
	"ietf.org": {
		Publisher: "Internet Engineering Task Force (IETF)",
		License:   store.LicenseFreeToRead,
		Quotable:  false,
	},
	"rfc-editor.org": {
		Publisher: "RFC Editor",
		License:   store.LicenseFreeToRead,
		Quotable:  false,
	},
	"owasp.org": {
		Publisher: "OWASP Foundation",
		License:   store.LicenseCCBY,
		Quotable:  true,
	},
	"nist.gov": {
		Publisher: "National Institute of Standards and Technology (NIST)",
		License:   store.LicensePublicDomain,
		Quotable:  true,
	},
	"iso.org": {
		Publisher: "International Organization for Standardization (ISO)",
		License:   store.LicenseISO,
		Quotable:  false,
	},
	"go.dev": {
		Publisher: "Go Authors",
		License:   store.LicenseCCBY,
		Quotable:  true,
	},
	"golang.org": {
		Publisher: "Go Authors",
		License:   store.LicenseCCBY,
		Quotable:  true,
	},
	"developer.mozilla.org": {
		Publisher: "Mozilla Developer Network (MDN)",
		License:   store.LicenseCCBY,
		Quotable:  true,
	},
	"creativecommons.org": {
		Publisher: "Creative Commons",
		License:   store.LicenseCCBY,
		Quotable:  true,
	},
}

// LookupKnownAuthority checks if a URL domain matches a recognized regulatory standards publisher.
func LookupKnownAuthority(rawURL string) (publisher string, license store.SourceLicense, quotable bool, ok bool) {
	norm := store.NormalizeSourceURL(rawURL)
	u, err := url.Parse(norm)
	if err != nil {
		return "", "", false, false
	}
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")

	for domain, auth := range StandardKnownAuthorities {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return auth.Publisher, auth.License, auth.Quotable, true
		}
	}
	return "", "", false, false
}

// ScanProvenance audits repository rules files and skill definitions for external citation integrity.
func ScanProvenance(rootDir string, st *store.Store) (*ProvenanceReport, error) {
	report := &ProvenanceReport{
		ProjectRoot: rootDir,
		Valid:       true,
	}

	// 1. Collect files to scan (AGENTS.md, GEMINI.md, agents/*.md, skills/*/SKILL.md)
	var targets []string
	for _, f := range []string{"AGENTS.md", "GEMINI.md", "README.md"} {
		p := filepath.Join(rootDir, f)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			targets = append(targets, p)
		}
	}

	// Sub-repositories or agents directories
	_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".sila" || name == "research" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(rootDir, path)
		rel = strings.ReplaceAll(rel, "\\", "/")
		if strings.HasPrefix(rel, "agents/") && strings.HasSuffix(rel, ".md") {
			targets = append(targets, path)
		} else if strings.HasPrefix(rel, "skills/") && strings.HasSuffix(rel, "SKILL.md") {
			targets = append(targets, path)
		}
		return nil
	})

	report.ScannedFiles = len(targets)

	// Pre-load store catalog if available
	catalogMap := make(map[string]store.SourceCatalogEntry)
	if st != nil {
		if list, err := st.ListSources(); err == nil {
			for _, entry := range list {
				catalogMap[store.NormalizeSourceURL(entry.URL)] = entry
			}
		}
	}

	for _, file := range targets {
		relPath, _ := filepath.Rel(rootDir, file)
		relPath = strings.ReplaceAll(relPath, "\\", "/")

		citations, violations := auditFileCitations(file, relPath, catalogMap)
		report.Citations = append(report.Citations, citations...)
		report.Violations = append(report.Violations, violations...)
	}

	report.TotalCitations = len(report.Citations)
	for _, c := range report.Citations {
		if c.Verified {
			report.VerifiedSources++
		}
	}

	for _, v := range report.Violations {
		if v.Severity == "error" {
			report.Valid = false
			break
		}
	}

	return report, nil
}

func auditFileCitations(filePath, relPath string, catalogMap map[string]store.SourceCatalogEntry) ([]Citation, []ProvenanceViolation) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, nil
	}
	defer file.Close()

	var citations []Citation
	var violations []ProvenanceViolation
	scanner := bufio.NewScanner(file)
	lineNum := 0
	seenURLs := make(map[string]bool)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// 1. Extract markdown links
		matches := mdLinkRE.FindAllStringSubmatch(line, -1)
		for _, m := range matches {
			if len(m) >= 3 {
				title := strings.TrimSpace(m[1])
				rawURL := strings.TrimSpace(m[2])
				if shouldAuditURL(rawURL) && !seenURLs[rawURL] {
					seenURLs[rawURL] = true
					c, v := evaluateCitation(relPath, lineNum, rawURL, title, catalogMap)
					citations = append(citations, c)
					if v != nil {
						violations = append(violations, *v)
					}
				}
			}
		}

		// 2. Extract raw URLs
		rawMatches := rawURLRE.FindAllStringSubmatch(line, -1)
		for _, m := range rawMatches {
			if len(m) >= 2 {
				rawURL := strings.TrimSpace(m[1])
				// Clean trailing punctuation
				rawURL = strings.TrimRight(rawURL, ".,;:\"'")
				if shouldAuditURL(rawURL) && !seenURLs[rawURL] {
					seenURLs[rawURL] = true
					c, v := evaluateCitation(relPath, lineNum, rawURL, "", catalogMap)
					citations = append(citations, c)
					if v != nil {
						violations = append(violations, *v)
					}
				}
			}
		}
	}

	return citations, violations
}

func shouldAuditURL(rawURL string) bool {
	norm := strings.ToLower(rawURL)
	if !strings.HasPrefix(norm, "http://") && !strings.HasPrefix(norm, "https://") {
		return false
	}
	// Exclude local or internal URLs
	if strings.Contains(norm, "localhost") || strings.Contains(norm, "127.0.0.1") || strings.Contains(norm, "0.0.0.0") {
		return false
	}
	if strings.Contains(norm, "example.com") {
		return false
	}
	return true
}

func evaluateCitation(path string, line int, rawURL, title string, catalogMap map[string]store.SourceCatalogEntry) (Citation, *ProvenanceViolation) {
	normURL := store.NormalizeSourceURL(rawURL)
	u, err := url.Parse(normURL)
	if err != nil || u.Host == "" {
		return Citation{Path: path, Line: line, URL: rawURL}, &ProvenanceViolation{
			Path:     path,
			Line:     line,
			URL:      rawURL,
			Message:  "malformed external citation URL",
			Severity: "error",
		}
	}

	// 1. Check known open standards authorities
	if pub, lic, quotable, ok := LookupKnownAuthority(normURL); ok {
		return Citation{
			Path:      path,
			Line:      line,
			URL:       normURL,
			Title:     title,
			Publisher: pub,
			License:   lic,
			Quotable:  quotable,
			Verified:  true,
		}, nil
	}

	// 2. Check store catalog entries
	if entry, exists := catalogMap[normURL]; exists {
		if !entry.License.IsValid() {
			return Citation{
				Path:     path,
				Line:     line,
				URL:      normURL,
				Title:    entry.Title,
				License:  entry.License,
				Verified: false,
			}, &ProvenanceViolation{
				Path:     path,
				Line:     line,
				URL:      normURL,
				License:  string(entry.License),
				Message:  fmt.Sprintf("citation cataloged with invalid license %q", entry.License),
				Severity: "error",
			}
		}

		return Citation{
			Path:      path,
			Line:      line,
			URL:       normURL,
			Title:     entry.Title,
			Publisher: entry.Publisher,
			License:   entry.License,
			Quotable:  entry.Quotable,
			Verified:  true,
		}, nil
	}

	// 3. Fallback: uncataloged external citation
	// Common developer platforms (github.com, npmjs.com, pkg.go.dev) default to free-to-read authority
	host := strings.ToLower(u.Host)
	if strings.HasSuffix(host, "github.com") || strings.HasSuffix(host, "gitlab.com") ||
		strings.HasSuffix(host, "npmjs.com") || strings.HasSuffix(host, "pkg.go.dev") ||
		strings.HasSuffix(host, "cloudflare.com") {
		return Citation{
			Path:      path,
			Line:      line,
			URL:       normURL,
			Title:     title,
			Publisher: host,
			License:   store.LicenseFreeToRead,
			Quotable:  false,
			Verified:  true,
		}, nil
	}

	// Unknown external citation: warn if uncataloged
	return Citation{
		Path:     path,
		Line:     line,
		URL:      normURL,
		Title:    title,
		License:  store.LicenseFreeToRead,
		Verified: false,
	}, &ProvenanceViolation{
		Path:     path,
		Line:     line,
		URL:      normURL,
		Message:  fmt.Sprintf("external citation %s has no verified license record in SourceCatalog", normURL),
		Severity: "warning",
	}
}

// ValidateLicenseDeclaration verifies that a license declaration string strictly conforms to closed taxonomy.
func ValidateLicenseDeclaration(lic string) (store.SourceLicense, error) {
	c := store.SourceLicense(strings.ToLower(strings.TrimSpace(lic)))
	if !c.IsValid() {
		return "", fmt.Errorf("invalid license %q (must be one of: public-domain, cc-by, free-to-read, iso, paywalled)", lic)
	}
	return c, nil
}
