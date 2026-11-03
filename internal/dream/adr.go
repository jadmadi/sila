package dream

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// ADRDir is the conventional directory for living Architecture Decision Records.
const ADRDir = "docs/adr"

// ADRStatus enumerates the lifecycle states an ADR may hold (MADR-compatible).
type ADRStatus string

// ADRStatus constants define the lifecycle states an ADR may hold (MADR-compatible).
const (
	StatusProposed   ADRStatus = "Proposed"
	StatusAccepted   ADRStatus = "Accepted"
	StatusSuperceded ADRStatus = "Superceded"
	StatusDeprecated ADRStatus = "Deprecated"
)

// ADR is a parsed Architecture Decision Record grounded against disk reality.
type ADR struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	Status     ADRStatus `json:"status"`
	Date       string    `json:"date"`
	Deciders   string    `json:"deciders,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	Path       string    `json:"path"`
	References []string  `json:"references,omitempty"`
	Body       string    `json:"-"`
}

// ADRHealth is the per-ADR grounding verdict produced by `sila adr doctor`.
type ADRHealth struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Path       string   `json:"path"`
	Status     string   `json:"status"`
	TotalRefs  int      `json:"total_refs"`
	Present    int      `json:"present_refs"`
	Missing    int      `json:"missing_refs"`
	MissingRef []string `json:"missing_refs_list,omitempty"`
	Healthy    bool     `json:"healthy"`
}

// DoctorReport aggregates grounding verdicts across an ADR set.
type DoctorReport struct {
	Total       int         `json:"total_adrs"`
	Healthy     int         `json:"healthy_adrs"`
	Drifted     int         `json:"drifted_adrs"`
	TotalRefs   int         `json:"total_refs"`
	MissingRefs int         `json:"missing_refs"`
	Health      []ADRHealth `json:"health"`
}

// statusLineRe extracts a `- **Status**: <Value>` (or `* **Status**:`) header.
var statusLineRe = regexp.MustCompile(`(?im)^\s*[-*]\s*\*{0,2}\s*Status\s*\*{0,2}\s*:\s*(.+?)\s*$`)

// dateLineRe extracts a `- **Date**: <Value>` header.
var dateLineRe = regexp.MustCompile(`(?im)^\s*[-*]\s*\*{0,2}\s*Date\s*\*{0,2}\s*:\s*(.+?)\s*$`)

// decidersLineRe extracts a `- **Deciders**: <Value>` header.
var decidersLineRe = regexp.MustCompile(`(?im)^\s*[-*]\s*\*{0,2}\s*Deciders\s*\*{0,2}\s*:\s*(.+?)\s*$`)

// tagsLineRe extracts a `- **Tags**: <Value>` header.
var tagsLineRe = regexp.MustCompile(`(?im)^\s*[-*]\s*\*{0,2}\s*Tags\s*\*{0,2}\s*:\s*(.+?)\s*$`)

// titleRe extracts the first H1 markdown header: `# ADR-NNNN: <Title>`.
var titleRe = regexp.MustCompile(`(?m)^#\s+ADR-(\d+):\s*(.+?)\s*$`)

// backtickPathRe matches backtick-quoted relative file paths inside the body.
// It deliberately skips URLs and absolute paths starting with `/`.
var backtickPathRe = regexp.MustCompile("`([A-Za-z0-9._~\\-]+/[A-Za-z0-9._~/\\-]+|[A-Za-z0-9_\\-]+\\.(go|md|ts|js|py|rs|sql|sh|yaml|yml|json|toml))`")

// filenameFromPathRe extracts the trailing filename from an ADR file path.
var filenameFromPathRe = regexp.MustCompile(`ADR-(\d+)`)

// ParseADR parses a single ADR markdown document from raw bytes.
func ParseADR(path string, data []byte) (*ADR, error) {
	body := string(data)
	a := &ADR{Path: path, Body: body, Status: StatusProposed}

	if m := titleRe.FindStringSubmatch(body); m != nil {
		n, err := strconv.Atoi(m[1])
		if err == nil {
			a.Number = n
		}
		a.Title = strings.TrimSpace(m[2])
	}
	if a.Title == "" {
		// Fall back to filename-derived title when no H1 header is present.
		base := filepath.Base(path)
		base = strings.TrimSuffix(base, ".md")
		base = strings.TrimPrefix(base, "ADR-")
		// strip leading digits and dash
		base = regexp.MustCompile(`^\d+[-_]?`).ReplaceAllString(base, "")
		base = strings.ReplaceAll(base, "-", " ")
		base = strings.ReplaceAll(base, "_", " ")
		a.Title = strings.TrimSpace(base)
		if m := filenameFromPathRe.FindStringSubmatch(path); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				a.Number = n
			}
		}
	}

	if m := statusLineRe.FindStringSubmatch(body); m != nil {
		a.Status = normalizeStatus(m[1])
	}
	if m := dateLineRe.FindStringSubmatch(body); m != nil {
		a.Date = strings.TrimSpace(m[1])
	}
	if m := decidersLineRe.FindStringSubmatch(body); m != nil {
		a.Deciders = strings.TrimSpace(m[1])
	}
	if m := tagsLineRe.FindStringSubmatch(body); m != nil {
		raw := strings.TrimSpace(m[1])
		for t := range strings.SplitSeq(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				a.Tags = append(a.Tags, t)
			}
		}
	}

	a.References = ExtractFileRefs(body)
	return a, nil
}

// normalizeStatus maps free-form status text to a canonical ADRStatus.
func normalizeStatus(s string) ADRStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "accepted", "accept":
		return StatusAccepted
	case "superceded", "superseded", "supersedes":
		return StatusSuperceded
	case "deprecated", "retired":
		return StatusDeprecated
	default:
		return StatusProposed
	}
}

// ExtractFileRefs pulls backtick-quoted relative file paths from the ADR body,
// preferring the `## References` section but falling back to the whole document.
func ExtractFileRefs(body string) []string {
	section := body
	if i := strings.Index(strings.ToLower(body), "## references"); i >= 0 {
		section = body[i:]
		if j := strings.Index(section[1:], "\n## "); j >= 0 {
			section = section[:j+1]
		}
	}
	seen := map[string]bool{}
	var refs []string
	for _, m := range backtickPathRe.FindAllStringSubmatch(section, -1) {
		p := strings.TrimSpace(m[1])
		if p == "" || seen[p] {
			continue
		}
		// Skip obvious non-file patterns (URLs, env vars).
		if strings.Contains(p, "://") || strings.HasPrefix(p, "$") {
			continue
		}
		seen[p] = true
		refs = append(refs, p)
	}
	return refs
}

// LoadADRs scans dir for `ADR-NNNN-*.md` files and returns them sorted by number.
func LoadADRs(dir string) ([]*ADR, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var adrs []*ADR
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if !strings.HasPrefix(e.Name(), "ADR-") && e.Name() != "README.md" {
			continue
		}
		if e.Name() == "README.md" {
			continue // compiled index, not a decision record
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		a, err := ParseADR(path, data)
		if err != nil {
			return nil, err
		}
		adrs = append(adrs, a)
	}
	sort.Slice(adrs, func(i, j int) bool { return adrs[i].Number < adrs[j].Number })
	return adrs, nil
}

// NextADRNumber returns the next free ADR index (1-based) by scanning dir.
func NextADRNumber(dir string) int {
	adrs, err := LoadADRs(dir)
	if err != nil {
		return 1
	}
	max := 0
	for _, a := range adrs {
		if a.Number > max {
			max = a.Number
		}
	}
	return max + 1
}

// Slugify converts a title into a filesystem-safe kebab-case slug.
func Slugify(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "decision"
	}
	return s
}

// ADRFilename builds the canonical `ADR-NNNN-slug.md` filename.
func ADRFilename(number int, title string) string {
	return fmt.Sprintf("ADR-%04d-%s.md", number, Slugify(title))
}

// RenderTemplate produces the MADR-compatible markdown body for a new ADR.
func RenderTemplate(number int, title, deciders string, refs []string) string {
	if deciders == "" {
		deciders = "unattributed"
	}
	date := time.Now().UTC().Format("2006-01-02")
	var b strings.Builder
	fmt.Fprintf(&b, "# ADR-%04d: %s\n\n", number, title)
	fmt.Fprintf(&b, "- **Status**: %s\n", StatusProposed)
	fmt.Fprintf(&b, "- **Date**: %s\n", date)
	fmt.Fprintf(&b, "- **Deciders**: %s\n", deciders)
	if len(refs) > 0 {
		fmt.Fprintf(&b, "- **Tags**: \n")
	}
	b.WriteString("\n## Context\n\n")
	b.WriteString("<!-- What is the issue we are seeing that motivates this decision? -->\n\n")
	b.WriteString("## Decision\n\n")
	b.WriteString("<!-- What is the change that we are proposing and/or doing? -->\n\n")
	b.WriteString("## Consequences\n\n")
	b.WriteString("<!-- What becomes easier or harder after this decision? -->\n\n")
	b.WriteString("## References\n\n")
	if len(refs) == 0 {
		b.WriteString("<!-- List referenced file paths in backticks, e.g. `internal/dream/adr.go` -->\n")
	} else {
		for _, r := range refs {
			fmt.Fprintf(&b, "- `%s`\n", r)
		}
	}
	return b.String()
}

// CreateADR writes a new ADR file under dir and returns its path and body.
func CreateADR(dir, title, deciders string, refs []string) (string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	number := NextADRNumber(dir)
	body := RenderTemplate(number, title, deciders, refs)
	path := filepath.Join(dir, ADRFilename(number, title))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", "", err
	}
	return path, body, nil
}

// CheckHealth verifies that every referenced file in an ADR exists on disk
// relative to rootDir (the project root). When rootDir is empty, the project
// root is derived from the ADR's own path.
func CheckHealth(a *ADR, rootDir string) ADRHealth {
	if rootDir == "" {
		rootDir = projectRootFor(filepath.Dir(a.Path))
	}
	h := ADRHealth{
		Number:    a.Number,
		Title:     a.Title,
		Path:      a.Path,
		Status:    string(a.Status),
		TotalRefs: len(a.References),
	}
	for _, ref := range a.References {
		full := ref
		if !filepath.IsAbs(ref) {
			full = filepath.Join(rootDir, ref)
		}
		if _, err := os.Stat(full); err == nil {
			h.Present++
		} else {
			h.Missing++
			h.MissingRef = append(h.MissingRef, ref)
		}
	}
	h.Healthy = h.Missing == 0
	return h
}

// Doctor runs grounding checks across every ADR in dir.
// Referenced file paths are resolved relative to the project root (the parent
// of the `docs/` directory that hosts `docs/adr/`), matching how ADR authors
// write references like `internal/dream/adr.go`.
func Doctor(dir string) (*DoctorReport, error) {
	adrs, err := LoadADRs(dir)
	if err != nil {
		return nil, err
	}
	rootDir := projectRootFor(dir)
	report := &DoctorReport{Total: len(adrs)}
	for _, a := range adrs {
		h := CheckHealth(a, rootDir)
		report.Health = append(report.Health, h)
		report.TotalRefs += h.TotalRefs
		report.MissingRefs += h.Missing
		if h.Healthy {
			report.Healthy++
		} else {
			report.Drifted++
		}
	}
	return report, nil
}

// projectRootFor returns the project root directory hosting the ADR directory.
// For `docs/adr` it returns `.`; for `/abs/root/docs/adr` it returns `/abs/root`.
func projectRootFor(adrDir string) string {
	parent := filepath.Dir(adrDir) // .../docs  or  docs
	grand := filepath.Dir(parent)  // .../root  or  .
	if grand == "" || grand == "." {
		return "."
	}
	return grand
}

// CompileIndex generates a unified `docs/adr/README.md` cataloging every ADR
// grouped by status, with grounding health badges.
func CompileIndex(dir string) (string, error) {
	adrs, err := LoadADRs(dir)
	if err != nil {
		return "", err
	}
	report, _ := Doctor(dir)
	healthByNumber := map[int]ADRHealth{}
	if report != nil {
		for _, h := range report.Health {
			healthByNumber[h.Number] = h
		}
	}

	var b strings.Builder
	now := time.Now().UTC().Format("2006-01-02 15:04")
	fmt.Fprintf(&b, "# Architecture Decision Records\n\n")
	fmt.Fprintf(&b, "> Auto-compiled by `sila adr compile` on %s UTC. MADR-compatible.\n\n", now)
	fmt.Fprintf(&b, "Total: %d decisions · %d healthy · %d drifted.\n\n", len(adrs), countHealthy(report), countDrifted(report))
	b.WriteString("---\n\n")

	groups := []struct {
		status ADRStatus
		label  string
	}{
		{StatusAccepted, "Accepted"},
		{StatusProposed, "Proposed"},
		{StatusSuperceded, "Superceded"},
		{StatusDeprecated, "Deprecated"},
	}
	for _, g := range groups {
		var matched []*ADR
		for _, a := range adrs {
			if a.Status == g.status {
				matched = append(matched, a)
			}
		}
		if len(matched) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s (%d)\n\n", g.label, len(matched))
		for _, a := range matched {
			badge := "🟢"
			if h, ok := healthByNumber[a.Number]; ok && !h.Healthy {
				badge = fmt.Sprintf("🔴 (%d missing)", h.Missing)
			}
			link := strings.TrimPrefix(a.Path, dir+string(filepath.Separator))
			fmt.Fprintf(&b, "- %s [ADR-%04d: %s](%s) — _%s_\n", badge, a.Number, a.Title, link, a.Date)
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")
	b.WriteString("### Health Badges\n")
	b.WriteString("- 🟢 = all referenced files exist on disk (grounded)\n")
	b.WriteString("- 🔴 = one or more referenced files are missing (drift detected; run `sila adr doctor`)\n")
	return b.String(), nil
}

func countHealthy(r *DoctorReport) int {
	if r == nil {
		return 0
	}
	return r.Healthy
}

func countDrifted(r *DoctorReport) int {
	if r == nil {
		return 0
	}
	return r.Drifted
}

// FromMemo promotes an explicit `sila memo` fact into a draft ADR file under dir.
// The memo's claim becomes the decision body; referenced repo files become
// explicit `## References` entries that `sila adr doctor` will verify.
func FromMemo(st *store.Store, dir, projectSel, memoID string) (string, *ADR, error) {
	facts, err := st.FactsByProject(projectSel)
	if err != nil {
		return "", nil, err
	}
	var memo *store.Fact
	for i := range facts {
		if facts[i].ID == memoID {
			memo = &facts[i]
			break
		}
	}
	if memo == nil {
		return "", nil, fmt.Errorf("memo %q not found in project %q", memoID, projectSel)
	}
	if memo.SessionRef != "manual_memo" {
		return "", nil, fmt.Errorf("fact %q is not a manual memo (session_ref=%q)", memoID, memo.SessionRef)
	}

	title := memo.Claim
	if len(title) > 70 {
		title = title[:70] + "…"
	}
	deciders := "memo"
	if len(memo.Tools) > 0 {
		deciders = memo.Tools[0]
	}
	path, body, err := CreateADR(dir, title, deciders, memo.RepoFiles)
	if err != nil {
		return "", nil, err
	}
	a, err := ParseADR(path, []byte(body))
	if err != nil {
		return path, nil, err
	}
	return path, a, nil
}
