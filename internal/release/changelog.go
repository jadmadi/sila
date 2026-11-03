package release

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
)

// ChangelogCategory is a Keep a Changelog semantic category.
type ChangelogCategory string

// ChangelogCategory constants represent Keep a Changelog standard section headers.
const (
	CatAdded      ChangelogCategory = "Added"
	CatChanged    ChangelogCategory = "Changed"
	CatDeprecated ChangelogCategory = "Deprecated"
	CatRemoved    ChangelogCategory = "Removed"
	CatFixed      ChangelogCategory = "Fixed"
	CatSecurity   ChangelogCategory = "Security"
)

// CommitEntry is one parsed git commit with semantic classification.
type CommitEntry struct {
	Hash        string            `json:"hash"`
	Subject     string            `json:"subject"`
	Body        string            `json:"body,omitempty"`
	Prefix      string            `json:"prefix,omitempty"`    // feat, fix, perf, refactor, docs, chore, etc.
	Scope       string            `json:"scope,omitempty"`     // e.g. "adr" from "feat(adr): ..."
	Description string            `json:"description"`         // effect-first description after the prefix, goal markers removed
	GoalSlug    string            `json:"goal_slug,omitempty"` // from (goals/<slug>/goal.md), [goal: <slug>] or a Goal: trailer
	Category    ChangelogCategory `json:"category"`            // semantic Keep a Changelog category
	Date        string            `json:"date,omitempty"`      // committer date, RFC 3339
}

// ChangelogReport is the categorized set of commits ready for rendering.
type ChangelogReport struct {
	TagRange    string                              `json:"tag_range"` // e.g. "v0.9.0..HEAD"; empty for the last 100 commits
	Total       int                                 `json:"total"`
	Commits     []CommitEntry                       `json:"commits"` // all parsed commits, newest first within each category
	GroupCounts map[ChangelogCategory]int           `json:"groups"`  // commits per category
	Groups      map[ChangelogCategory][]CommitEntry `json:"-"`       // commits grouped by category
}

// goalPathRe extracts the goal slug from `(goals/<slug>/goal.md)` in commit text.
var goalPathRe = regexp.MustCompile(`\(goals/([a-z0-9-]+)/goal\.md\)`)

// goalTagRe extracts the slug from `[goal: <slug>]` in commit text.
var goalTagRe = regexp.MustCompile(`\[goal:\s*([a-z0-9-]+)\]`)

// goalTrailerRe extracts the slug from a `Goal: <slug>` trailer line in a commit body.
var goalTrailerRe = regexp.MustCompile(`(?m)^Goal:\s*([a-z0-9-]+)\s*$`)

// prefixRe extracts the conventional-commit prefix and optional scope. A scope
// may list several areas, as in `feat(cmd,mcp):`.
var prefixRe = regexp.MustCompile(`^(feat|fix|perf|refactor|docs|chore|style|test|build|ci|revert)(?:\(([a-z0-9_,./ -]+)\))?!?:\s*(.+)$`)

// ScanCommits runs `git log` between tagRange (e.g. "v0.9.0..HEAD" or empty for
// all unreleased commits) and parses each commit into a CommitEntry with
// semantic classification and goal attribution.
func ScanCommits(repoDir, tagRange string) ([]CommitEntry, error) {
	repoDir = ResolveGitRepoDir(repoDir)
	args := []string{"log", "--pretty=format:%h%x1f%s%x1f%b%x1f%cI%x1e"}
	if tagRange != "" {
		args = append(args, tagRange)
	} else {
		args = append(args, "-n", "100")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		errStr := strings.TrimSpace(stderr.String())
		if errStr != "" {
			return nil, fmt.Errorf("git log in %s: %s", repoDir, errStr)
		}
		return nil, fmt.Errorf("git log in %s: %w", repoDir, err)
	}
	return parseGitLog(out), nil
}

// parseGitLog splits raw git log output (delimited by \x1e records, \x1f fields)
// into CommitEntry structs.
func parseGitLog(raw []byte) []CommitEntry {
	entries := bytes.Split(raw, []byte("\x1e"))
	var commits []CommitEntry
	for _, rawEntry := range entries {
		s := strings.TrimSpace(string(rawEntry))
		if s == "" {
			continue
		}
		parts := strings.SplitN(s, "\x1f", 4)
		if len(parts) < 2 {
			continue
		}
		hash := parts[0]
		subject := strings.TrimSpace(parts[1])
		body, date := "", ""
		if len(parts) > 2 {
			body = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			date = strings.TrimSpace(parts[3])
		}
		c := classifyCommit(hash, subject, body)
		c.Date = date
		commits = append(commits, c)
	}
	return commits
}

// classifyCommit parses a commit subject and body into a CommitEntry with
// semantic prefix, scope, description, goal slug, and Keep a Changelog category.
func classifyCommit(hash, subject, body string) CommitEntry {
	c := CommitEntry{Hash: hash, Subject: subject, Body: body}
	fullText := subject + " " + body

	// Extract goal slug from any of the three attribution forms.
	if m := goalPathRe.FindStringSubmatch(fullText); m != nil {
		c.GoalSlug = m[1]
	} else if m := goalTagRe.FindStringSubmatch(fullText); m != nil {
		c.GoalSlug = m[1]
	} else if m := goalTrailerRe.FindStringSubmatch(body); m != nil {
		c.GoalSlug = m[1]
	}

	// Extract prefix/scope/description.
	if m := prefixRe.FindStringSubmatch(subject); m != nil {
		c.Prefix = m[1]
		c.Scope = m[2]
		c.Description = strings.TrimSpace(m[3])
	} else {
		// No conventional prefix — use full subject as description.
		c.Description = subject
	}
	c.Description = stripGoalMarkers(c.Description)

	c.Category = categorize(c.Prefix)
	return c
}

// stripGoalMarkers removes goal attribution from a description. The goal is
// shown once, as a tag, so repeating it in the text is noise.
func stripGoalMarkers(desc string) string {
	desc = goalPathRe.ReplaceAllString(desc, "")
	desc = goalTagRe.ReplaceAllString(desc, "")
	return strings.Join(strings.Fields(desc), " ")
}

// categorize maps a conventional-commit prefix to a Keep a Changelog category.
// See https://keepachangelog.com/ and https://www.conventionalcommits.org/.
func categorize(prefix string) ChangelogCategory {
	switch prefix {
	case "feat":
		return CatAdded
	case "fix":
		return CatFixed
	case "perf":
		return CatChanged
	case "refactor":
		return CatChanged
	case "docs":
		// docs are not user-facing changes unless they describe a new capability.
		// We put them in Changed to avoid cluttering Added.
		return CatChanged
	case "deprecate", "revert":
		return CatDeprecated
	case "remove", "rm":
		return CatRemoved
	case "security":
		return CatSecurity
	default:
		return CatChanged
	}
}

// commitTime parses a commit's RFC 3339 date. An unparseable date sorts last.
func commitTime(c CommitEntry) time.Time {
	t, err := time.Parse(time.RFC3339, c.Date)
	if err != nil {
		return time.Time{}
	}
	return t
}

// BuildReport groups commits by Keep a Changelog category. Within a category,
// entries are newest first, with the hash as a tie-break so output is stable.
func BuildReport(commits []CommitEntry, tagRange string) *ChangelogReport {
	r := &ChangelogReport{
		TagRange:    tagRange,
		Commits:     commits,
		Total:       len(commits),
		Groups:      map[ChangelogCategory][]CommitEntry{},
		GroupCounts: map[ChangelogCategory]int{},
	}
	for _, c := range commits {
		cat := c.Category
		r.Groups[cat] = append(r.Groups[cat], c)
	}
	for cat := range r.Groups {
		group := r.Groups[cat]
		sort.SliceStable(group, func(i, j int) bool {
			ti, tj := commitTime(group[i]), commitTime(group[j])
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
			return group[i].Hash < group[j].Hash
		})
		r.GroupCounts[cat] = len(group)
	}
	// Commits follows the same order the markdown uses.
	sort.SliceStable(r.Commits, func(i, j int) bool {
		ti, tj := commitTime(r.Commits[i]), commitTime(r.Commits[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return r.Commits[i].Hash < r.Commits[j].Hash
	})
	return r
}

// RenderChangelog formats a ChangelogReport into Keep a Changelog markdown.
// Each entry is rendered as a bullet point with the effect-first description
// and optional goal attribution.
func RenderChangelog(r *ChangelogReport) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	order := []ChangelogCategory{CatAdded, CatChanged, CatDeprecated, CatRemoved, CatFixed, CatSecurity}
	for _, cat := range order {
		entries := r.Groups[cat]
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n", cat)
		for _, c := range entries {
			desc := c.Description
			if desc == "" {
				desc = c.Subject
			}
			suffix := ""
			if c.GoalSlug != "" {
				suffix = fmt.Sprintf(" _(goals/%s)_", c.GoalSlug)
			}
			fmt.Fprintf(&b, "- %s (`%s`)%s\n", desc, c.Hash, suffix)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FindUnreleasedSection returns the byte range of the `## [Unreleased]` section
// in CHANGELOG.md content, or nil if not found. The range covers from the
// `## [Unreleased]` header up to (but not including) the next `## [` header.
func FindUnreleasedSection(content string) (start, end int) {
	idx := strings.Index(content, "## [Unreleased]")
	if idx < 0 {
		return -1, -1
	}
	// Find the next `## [` header after Unreleased.
	rest := content[idx+len("## [Unreleased]"):]
	nextIdx := strings.Index(rest, "\n## [")
	if nextIdx < 0 {
		// Unreleased is the last section — covers to end of file.
		return idx, len(content)
	}
	return idx, idx + len("## [Unreleased]") + nextIdx
}

// WriteUnreleased inserts new entries into the `## [Unreleased]` section of
// CHANGELOG.md, merging with existing content. It is idempotent: commits
// already present (matched by hash) are not duplicated.
//
// The newContent should be the rendered markdown (from RenderChangelog) to
// merge in. Returns the updated file content.
func WriteUnreleased(existing, newContent string) string {
	start, end := FindUnreleasedSection(existing)
	if start < 0 {
		// No [Unreleased] section — prepend one.
		return "## [Unreleased]\n\n" + newContent + existing
	}

	section := existing[start:end]
	// Extract existing hashes to avoid duplicates.
	existingHashes := extractCommitHashes(section)

	// Parse newContent into category sections and merge.
	merged := mergeSections(section, newContent, existingHashes)

	return existing[:start] + merged + existing[end:]
}

// extractCommitHashes pulls all `(\`<hash>\`)` patterns from a section.
func extractCommitHashes(section string) map[string]bool {
	hashes := map[string]bool{}
	re := regexp.MustCompile("`([0-9a-f]{7,40})`")
	for _, m := range re.FindAllStringSubmatch(section, -1) {
		if len(m) > 1 {
			hashes[m[1]] = true
		}
	}
	return hashes
}

// mergeSections merges newContent's categorized entries into the existing
// section, skipping commits whose hash already exists. New entries are
// appended at the end of the Unreleased section, grouped by category.
func mergeSections(existing, newContent string, existingHashes map[string]bool) string {
	// Parse newContent into category → lines.
	newLines := strings.Split(newContent, "\n")
	currentCat := ""
	newEntries := map[ChangelogCategory][]string{}
	for _, line := range newLines {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "### "); ok {
			currentCat = after
			continue
		}
		if trimmed == "" || currentCat == "" {
			continue
		}
		// Check if this line's hash is already present.
		hashRe := regexp.MustCompile("`([0-9a-f]{7,40})`")
		if m := hashRe.FindStringSubmatch(line); m != nil && existingHashes[m[1]] {
			continue // skip duplicate
		}
		newEntries[ChangelogCategory(currentCat)] = append(newEntries[ChangelogCategory(currentCat)], line)
	}

	if len(newEntries) == 0 {
		return existing // nothing to add
	}

	// Build the insertion block grouped by category in Keep a Changelog order.
	var block strings.Builder
	order := []ChangelogCategory{CatAdded, CatChanged, CatDeprecated, CatRemoved, CatFixed, CatSecurity}
	for _, cat := range order {
		lines, ok := newEntries[cat]
		if !ok || len(lines) == 0 {
			continue
		}
		// Check if this category already exists in the section.
		catHeader := "### " + string(cat)
		if strings.Contains(existing, catHeader) {
			// Append lines to the existing category section.
			existing = appendLinesToCategory(existing, cat, lines)
		} else {
			// New category — add to the block.
			block.WriteString(catHeader + "\n")
			for _, l := range lines {
				block.WriteString(l + "\n")
			}
			block.WriteString("\n")
		}
	}

	// Append any new category blocks at the end of the Unreleased section.
	insertion := block.String()
	if insertion == "" {
		return existing
	}
	// Find the end of the Unreleased section (next ## [ header or EOF).
	start, end := FindUnreleasedSection(existing)
	if start < 0 {
		return existing
	}
	_ = start
	before := strings.TrimRight(existing[:end], "\n")
	return before + "\n" + insertion + existing[end:]
}

// appendLinesToCategory inserts new lines at the end of an existing category
// subsection within the content.
func appendLinesToCategory(content string, cat ChangelogCategory, lines []string) string {
	catHeader := "### " + string(cat)
	catIdx := strings.Index(content, catHeader)
	if catIdx < 0 {
		return content
	}
	// Find the next "### " or "## [" header after this category.
	afterCat := content[catIdx+len(catHeader):]
	nextSub := strings.Index(afterCat, "\n### ")
	nextMain := strings.Index(afterCat, "\n## [")
	nextPos := -1
	if nextSub >= 0 && (nextMain < 0 || nextSub < nextMain) {
		nextPos = nextSub
	} else if nextMain >= 0 {
		nextPos = nextMain
	}
	var insertPos int
	if nextPos < 0 {
		insertPos = len(content)
	} else {
		insertPos = catIdx + len(catHeader) + nextPos
	}
	// Trim trailing newlines before insert position, then add lines.
	before := strings.TrimRight(content[:insertPos], "\n") + "\n"
	var insertion strings.Builder
	for _, l := range lines {
		insertion.WriteString(l + "\n")
	}
	return before + insertion.String() + content[insertPos:]
}

// GenerateChangelog is the top-level entry point: scans git history, classifies
// commits, and returns rendered Keep a Changelog markdown ready for insertion.
func GenerateChangelog(repoDir, tagRange string) (string, *ChangelogReport, error) {
	commits, err := ScanCommits(repoDir, tagRange)
	if err != nil {
		return "", nil, err
	}
	// Filter out docs(goals)/chore commits that are pure roadmap syncs —
	// they don't represent user-facing changes.
	var filtered []CommitEntry
	for _, c := range commits {
		if c.Prefix == "docs" && c.Scope == "goals" {
			continue
		}
		if c.Prefix == "chore" {
			continue
		}
		filtered = append(filtered, c)
	}
	report := BuildReport(filtered, tagRange)
	return RenderChangelog(report), report, nil
}

// WriteChangelogToFile reads CHANGELOG.md at path, merges unreleased entries,
// and writes the result back. Returns the new content.
func WriteChangelogToFile(path, newEntries string) (string, error) {
	existing, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	updated := WriteUnreleased(string(existing), newEntries)
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return updated, nil
}

// RenderChangelogTOON renders the report as compact TOON: a header object
// followed by one table row per commit, in the same order as the markdown.
func RenderChangelogTOON(r *ChangelogReport) string {
	if r == nil {
		return ""
	}
	head := render.FormatTOONObject("changelog", [][2]string{
		{"tag_range", render.EscapeTOONValue(r.TagRange)},
		{"total", fmt.Sprint(r.Total)},
	})
	rows := make([][]string, 0, len(r.Commits))
	for _, c := range r.Commits {
		rows = append(rows, []string{c.Hash, string(c.Category), c.Scope, c.GoalSlug, c.Description})
	}
	return head + render.FormatTOONTable("commits", []string{"hash", "category", "scope", "goal", "description"}, rows)
}
