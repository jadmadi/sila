package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Doc represents an external reference document, scraped web doc, or RFC attached to a project.
type Doc struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Content   string `json:"content"`
	Format    string `json:"format"` // markdown, html, text
	CreatedAt string `json:"created_at"`
}

// DocID computes a deterministic ID for a document based on project and URL/Title.
func DocID(projectID, urlOrTitle string) string {
	sum := sha256.Sum256([]byte(projectID + "\x00" + urlOrTitle))
	return "doc_" + hex.EncodeToString(sum[:6])
}

// MaxDocContentSize limits individual document content in SQLite (1MB).
const MaxDocContentSize = 1024 * 1024

// InsertDoc upserts an external documentation or source reference.
func (s *Store) InsertDoc(d Doc) error {
	if strings.TrimSpace(d.ProjectID) == "" {
		return errors.New("insert doc: project_id is required")
	}
	if len(d.Content) > MaxDocContentSize {
		return fmt.Errorf("insert doc: content exceeds maximum allowed size (%d bytes): %d bytes", MaxDocContentSize, len(d.Content))
	}
	if d.ID == "" {
		key := d.URL
		if key == "" {
			key = d.Title
		}
		d.ID = DocID(d.ProjectID, key)
	}
	if d.Format == "" {
		d.Format = "markdown"
	}
	if d.CreatedAt == "" {
		d.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	err := WithRetry(context.Background(), func() error {
		_, execErr := s.DB.Exec(`
			INSERT INTO docs(id, project_id, title, url, content, format, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				title=excluded.title,
				url=excluded.url,
				content=excluded.content,
				format=excluded.format,
				created_at=excluded.created_at;
		`, d.ID, d.ProjectID, d.Title, d.URL, d.Content, d.Format, d.CreatedAt)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("insert doc: %w", err)
	}
	return nil
}

// DocsByProject returns all attached docs for a project, newest first.
func (s *Store) DocsByProject(projectSel string, limit int) ([]Doc, error) {
	if limit <= 0 {
		limit = 20
	}
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}
	if len(matching) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
	args := make([]any, len(matching)+1)
	for i, m := range matching {
		args[i] = m
	}
	args[len(matching)] = limit

	query := `
		SELECT id, project_id, title, url, content, format, created_at
		FROM docs
		WHERE project_id IN (` + placeholders + `)
		ORDER BY created_at DESC
		LIMIT ?`

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.URL, &d.Content, &d.Format, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SearchDocsFTS searches the docs_fts index using FTS5 match syntax.
func (s *Store) SearchDocsFTS(query string, projectSel string, limit int) ([]Doc, error) {
	if limit <= 0 {
		limit = 10
	}
	target, cleanQuery := ParseTemporalAnchor(query, time.Now().UTC())
	effectiveQuery := query
	if target != nil && strings.TrimSpace(cleanQuery) != "" {
		effectiveQuery = cleanQuery
	}

	ftsQ := FormatFTS5Query(effectiveQuery)
	if ftsQ == "" {
		if projectSel != "" {
			return s.DocsByProject(projectSel, limit)
		}
		rows, err := s.DB.Query(`SELECT id, project_id, title, url, content, format, created_at FROM docs ORDER BY created_at DESC LIMIT ?`, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []Doc
		for rows.Next() {
			var d Doc
			if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.URL, &d.Content, &d.Format, &d.CreatedAt); err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		return out, rows.Err()
	}

	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return nil, err
	}

	sqlQ := `
		SELECT d.id, d.project_id, d.title, d.url, d.content, d.format, d.created_at
		FROM docs_fts
		JOIN docs d ON d.id = docs_fts.id
		WHERE docs_fts MATCH ?`
	args := []any{ftsQ}

	if len(matching) == 1 {
		sqlQ += ` AND d.project_id = ?`
		args = append(args, matching[0])
	} else if len(matching) > 1 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
		sqlQ += ` AND d.project_id IN (` + placeholders + `)`
		for _, m := range matching {
			args = append(args, m)
		}
	} else if projectSel != "" {
		return nil, nil
	}

	sqlQ += ` ORDER BY bm25(docs_fts) LIMIT ?`
	args = append(args, limit)

	executeSearch := func() ([]Doc, error) {
		ctx, cancel := context.WithTimeout(context.Background(), FTSQueryTimeout)
		defer cancel()

		rows, err := s.DB.QueryContext(ctx, sqlQ, args...)
		if err != nil {
			if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("fts5 query timed out after %v: %w", FTSQueryTimeout, context.DeadlineExceeded)
			}
			return nil, err
		}
		defer rows.Close()

		var out []Doc
		for rows.Next() {
			var d Doc
			if err := rows.Scan(&d.ID, &d.ProjectID, &d.Title, &d.URL, &d.Content, &d.Format, &d.CreatedAt); err != nil {
				return nil, err
			}
			out = append(out, d)
		}
		if err := rows.Err(); err != nil {
			if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("fts5 query timed out after %v: %w", FTSQueryTimeout, context.DeadlineExceeded)
			}
			return nil, err
		}
		return out, nil
	}

	out, err := executeSearch()
	if err != nil && isFTSCorruption(err) {
		if healErr := s.RebuildFTSIndex("docs_fts"); healErr == nil {
			out, err = executeSearch()
		}
	}
	if err != nil {
		return nil, err
	}

	if target != nil && len(out) > 1 {
		sort.SliceStable(out, func(i, j int) bool {
			ti, oki := ParseDocTimestamp(out[i].CreatedAt)
			tj, okj := ParseDocTimestamp(out[j].CreatedAt)
			bi := CalculateTemporalBoost(ti, target)
			bj := CalculateTemporalBoost(tj, target)
			if !oki {
				bi = 0
			}
			if !okj {
				bj = 0
			}
			if math.Abs(bi-bj) > 1e-4 {
				return bi > bj
			}
			return out[i].CreatedAt > out[j].CreatedAt
		})
	} else {
		// Definition-first: within the FTS result set, nudge documents whose title
		// or body reads as a definition above pure prose, keeping bm25 order inside
		// each group (stable sort).
		sort.SliceStable(out, func(i, j int) bool {
			return definitionBoost(out[i]) > definitionBoost(out[j])
		})
	}
	return out, nil
}

// definitionBoost counts definition-shaped markers in a document. It is a
// coarse tiebreak, not a relevance score: the FTS match already decided which
// documents belong in the result.
func definitionBoost(d Doc) int {
	text := d.Title + "\n" + d.Content
	boost := 0
	for _, marker := range []string{"\ntype ", "\nfunc ", "\ndef ", "\nclass ", "\ninterface ", "\nexport function", "\nexport const", "\n# ", "\n## "} {
		if strings.Contains(text, marker) {
			boost++
		}
	}
	if strings.HasPrefix(strings.TrimSpace(d.Content), "#") {
		boost++
	}
	return boost
}

// GetDoc retrieves a single document by ID.
func (s *Store) GetDoc(id string) (*Doc, error) {
	var d Doc
	err := s.DB.QueryRow(`
		SELECT id, project_id, title, url, content, format, created_at
		FROM docs
		WHERE id=?
	`, id).Scan(&d.ID, &d.ProjectID, &d.Title, &d.URL, &d.Content, &d.Format, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &d, nil
}

// DocStats returns the document count and total content size in bytes for a project.
func (s *Store) DocStats(projectSel string) (int, int64, error) {
	matching, err := s.MatchingProjectIDs(projectSel)
	if err != nil {
		return 0, 0, err
	}
	if len(matching) == 0 && projectSel != "" {
		return 0, 0, nil
	}

	var count int
	var totalBytes int64

	if len(matching) == 0 {
		err = s.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(LENGTH(content)), 0) FROM docs`).Scan(&count, &totalBytes)
	} else {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(matching)), ",")
		args := make([]any, len(matching))
		for i, m := range matching {
			args[i] = m
		}
		err = s.DB.QueryRow(`SELECT COUNT(*), COALESCE(SUM(LENGTH(content)), 0) FROM docs WHERE project_id IN (`+placeholders+`)`, args...).Scan(&count, &totalBytes)
	}
	return count, totalBytes, err
}

// DefaultDocSnippetChars bounds a search_docs content snippet.
const DefaultDocSnippetChars = 700

// ExtractDocSnippet selects the Markdown section with the most case-insensitive
// query term hits and bounds it to maxChars. The section heading line is kept
// when the bound allows. SearchDocsFTS still returns full Doc.Content.
func ExtractDocSnippet(content, query string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = DefaultDocSnippetChars
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	terms := strings.Fields(strings.ToLower(query))
	sections := splitDocSections(content)
	best := sections[0]
	bestHits := -1
	for _, section := range sections {
		lower := strings.ToLower(section)
		hits := 0
		for _, term := range terms {
			if term != "" {
				hits += strings.Count(lower, term)
			}
		}
		if hits > bestHits {
			bestHits = hits
			best = section
		}
	}
	return boundDocSnippet(best, maxChars)
}

// splitDocSections splits Markdown at ATX heading lines. The heading line stays
// with its section; text before the first heading is its own section.
func splitDocSections(content string) []string {
	var sections []string
	var current []string
	flush := func() {
		text := strings.TrimSpace(strings.Join(current, "\n"))
		if text != "" {
			sections = append(sections, text)
		}
		current = nil
	}
	for _, line := range strings.Split(content, "\n") {
		if isDocHeading(line) {
			flush()
		}
		current = append(current, line)
	}
	flush()
	if len(sections) == 0 {
		return []string{strings.TrimSpace(content)}
	}
	return sections
}

func isDocHeading(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	if level < 1 || level > 6 {
		return false
	}
	return level == len(trimmed) || trimmed[level] == ' ' || trimmed[level] == '\t'
}

func boundDocSnippet(section string, maxChars int) string {
	if len(section) <= maxChars {
		return section
	}
	firstLineEnd := strings.IndexByte(section, '\n')
	if firstLineEnd < 0 || firstLineEnd+1 >= maxChars {
		return truncateUTF8(section, maxChars)
	}
	heading := section[:firstLineEnd+1]
	return heading + truncateUTF8(section[firstLineEnd+1:], maxChars-len(heading))
}

func truncateUTF8(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	cut := 0
	for i := range text {
		if i > maxBytes {
			break
		}
		cut = i
	}
	return text[:cut]
}
