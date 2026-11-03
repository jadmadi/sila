package mcp

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// MaxAutoIngestContentSize defines the maximum size for auto-ingested documentation content (512KB).
const MaxAutoIngestContentSize = 512 * 1024

// interceptAutoIngest checks if upstream result is documentation and auto-persists to SQLite.
func (u *UpstreamManager) interceptAutoIngest(serverName, toolName string, args map[string]any, result any, project string) error {
	if u.store == nil {
		return nil
	}

	u.mu.RLock()
	srv, ok := u.servers[serverName]
	u.mu.RUnlock()
	if !ok || !srv.AutoIngest {
		return nil
	}

	// 1. Result must be non-nil and valid map
	if result == nil {
		return nil
	}
	resMap, ok := result.(map[string]any)
	if !ok {
		return nil
	}

	// 2. Reject tool execution errors, error maps, and auth failure status
	if isErr, _ := resMap["isError"].(bool); isErr {
		return nil
	}
	if _, hasErr := resMap["error"]; hasErr {
		return nil
	}
	if status, _ := resMap["status"].(float64); status == 401 || status == 403 {
		return nil
	}
	if status, _ := resMap["statusCode"].(float64); status == 401 || status == 403 {
		return nil
	}

	// 3. Reject non-reference actions
	switch serverName {
	case "drizzle":
		if toolName != "fetch_page" && toolName != "drizzle_fetch_page" &&
			toolName != "search_docs" && toolName != "drizzle_search_docs" {
			return nil // list_topics or other non-reference action
		}
	case "alphaxiv":
		if toolName != "get_paper" && toolName != "get_paper_content" && toolName != "alphaxiv_get_paper" &&
			toolName != "query_pdf" && toolName != "answer_pdf_queries" && toolName != "alphaxiv_query_pdf" {
			return nil // discover / discover_papers or other non-reference action
		}
	case "cloudflare":
		if toolName != "cf_search" {
			return nil // cf_execute is code execution, not reference documentation
		}
	case "astro":
		if toolName != "search_astro_docs" && toolName != "astro_search_astro_docs" {
			return nil
		}
	case "science":
		switch toolName {
		case "search_papers", "get_papers", "find_similar_papers", "read_topic_group":
		default:
			return nil
		}
	default:
		return nil
	}

	if serverName == "science" {
		return u.ingestSciencePapers(resMap, project)
	}

	// 4. Extract content text
	var contentText string
	if contentSlice, ok := resMap["content"].([]any); ok {
		for _, item := range contentSlice {
			if itemMap, ok := item.(map[string]any); ok {
				if t, ok := itemMap["text"].(string); ok {
					contentText += t + "\n"
				}
			}
		}
	} else if txt, ok := resMap["text"].(string); ok {
		contentText = txt
	}

	contentText = strings.TrimSpace(contentText)
	if len(contentText) < 50 {
		return nil
	}

	// 5. Enforce bounded result sizes
	if len(contentText) > MaxAutoIngestContentSize {
		return fmt.Errorf("upstream doc content size (%d bytes) exceeds maximum allowed size (%d bytes)", len(contentText), MaxAutoIngestContentSize)
	}

	// Reject auth error messages in content text
	lowerContent := strings.ToLower(contentText)
	if strings.Contains(lowerContent, "401 unauthorized") ||
		strings.Contains(lowerContent, "403 forbidden") ||
		strings.Contains(lowerContent, "authentication required") ||
		strings.Contains(lowerContent, "invalid api key") {
		return nil
	}

	// 6. Determine title and actual source URL
	var title string
	var sourceURL string

	switch serverName {
	case "drizzle":
		if slug, ok := args["slug"].(string); ok && strings.TrimSpace(slug) != "" {
			cleanSlug := strings.Trim(slug, "/")
			title = fmt.Sprintf("Drizzle ORM: %s", cleanSlug)
			if rawURL, ok := args["url"].(string); ok && rawURL != "" {
				sourceURL = rawURL
			} else {
				sourceURL = fmt.Sprintf("https://orm.drizzle.team/%s", cleanSlug)
			}
		} else if q, ok := args["query"].(string); ok && strings.TrimSpace(q) != "" {
			title = fmt.Sprintf("Drizzle ORM Docs: %s", q)
			if actualURL, ok := args["url"].(string); ok && actualURL != "" {
				sourceURL = actualURL
			} else if resURL, ok := resMap["url"].(string); ok && resURL != "" {
				sourceURL = resURL
			}
			// When no actual page URL is supplied, leave sourceURL empty ("")
			// Generic overview URLs must NOT imply canonical source evidence
		}

	case "cloudflare":
		if q, ok := args["query"].(string); ok && strings.TrimSpace(q) != "" {
			title = fmt.Sprintf("Cloudflare API Docs: %s", q)
			if actualURL, ok := args["url"].(string); ok && actualURL != "" {
				sourceURL = actualURL
			} else if resURL, ok := resMap["url"].(string); ok && resURL != "" {
				sourceURL = resURL
			}
			// Leave sourceURL empty if no actual page URL
		}

	case "astro":
		if q, ok := args["query"].(string); ok && strings.TrimSpace(q) != "" {
			title = fmt.Sprintf("Astro Docs: %s", strings.TrimSpace(q))
			if actualURL, ok := args["url"].(string); ok && actualURL != "" {
				sourceURL = actualURL
			} else if resURL, ok := resMap["url"].(string); ok && resURL != "" {
				sourceURL = resURL
			}
		}

	case "alphaxiv":
		if paperID, ok := args["paper_id"].(string); ok && strings.TrimSpace(paperID) != "" {
			title = fmt.Sprintf("AlphaXiv Paper: %s", paperID)
			sourceURL = fmt.Sprintf("https://alphaxiv.org/abs/%s", paperID)
		} else if rawURL, ok := args["url"].(string); ok && strings.TrimSpace(rawURL) != "" {
			parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
			lastPart := parts[len(parts)-1]
			title = fmt.Sprintf("AlphaXiv Paper: %s", lastPart)
			sourceURL = rawURL
		} else if q, ok := args["question"].(string); ok && strings.TrimSpace(q) != "" {
			title = fmt.Sprintf("AlphaXiv Research: %s", q)
			if actualURL, ok := args["url"].(string); ok && actualURL != "" {
				sourceURL = actualURL
			}
			// Leave sourceURL empty if no actual URL supplied
		}
	}

	if title == "" {
		return nil
	}

	// 7. Enforce project scoping
	if project == "" || project == "." {
		if proj, err := u.store.EnsureProject("."); err == nil && proj.ID != "" {
			project = proj.ID
		} else {
			project = "."
		}
	}

	doc := store.Doc{
		ProjectID: project,
		Title:     RedactEgressString(title),
		URL:       RedactEgressString(sourceURL),
		Content:   RedactEgressString(contentText),
		Format:    "markdown",
	}

	if err := u.store.InsertDoc(doc); err != nil {
		return fmt.Errorf("failed to persist upstream doc for %s/%s: %w", serverName, toolName, err)
	}
	return nil
}

// ingestSciencePapers extracts paper records from a ScienceMadeReadable response
// and persists each paper as an FTS-indexed document with canonical DOI/OpenAlex URLs.
func (u *UpstreamManager) ingestSciencePapers(resMap map[string]any, project string) error {
	papers := extractSciencePaperRecords(resMap)
	if len(papers) == 0 {
		return nil
	}

	if project == "" || project == "." {
		if proj, err := u.store.EnsureProject("."); err == nil && proj.ID != "" {
			project = proj.ID
		} else {
			project = "."
		}
	}

	for _, paper := range papers {
		title := strings.TrimSpace(scienceString(paper["title"]))
		if title == "" {
			continue
		}

		doi := normalizeScienceDOI(scienceString(paper["doi"]))
		openAlexID := firstNonEmpty(
			scienceString(paper["openalex_id"]),
			scienceString(paper["openalex"]),
			scienceString(paper["work_id"]),
			scienceString(paper["id"]),
		)
		sourceURL := ""
		if doi != "" {
			sourceURL = "https://doi.org/" + doi
		} else if openAlexID != "" {
			sourceURL = "https://openalex.org/" + normalizeOpenAlexID(openAlexID)
		} else {
			sourceURL = scienceString(paper["url"])
		}

		content := formatSciencePaperMarkdown(title, paper, sourceURL)
		if len(content) > MaxAutoIngestContentSize {
			continue
		}

		doc := store.Doc{
			ProjectID: project,
			Title:     RedactEgressString("Paper: " + title),
			URL:       RedactEgressString(sourceURL),
			Content:   RedactEgressString(content),
			Format:    "markdown",
		}
		if err := u.store.InsertDoc(doc); err != nil {
			return fmt.Errorf("failed to persist science paper %q: %w", title, err)
		}
	}
	return nil
}

// extractSciencePaperRecords walks common MCP response shapes (papers/results/
// items/data, structuredContent, and JSON-encoded text content) for paper maps.
func extractSciencePaperRecords(resMap map[string]any) []map[string]any {
	var papers []map[string]any
	seen := make(map[string]bool)

	var walk func(node any, depth int)
	walk = func(node any, depth int) {
		if depth > 4 {
			return
		}
		switch typed := node.(type) {
		case []any:
			for _, item := range typed {
				walk(item, depth+1)
			}
		case map[string]any:
			if looksLikeSciencePaper(typed) {
				key := scienceString(typed["id"]) + "|" + scienceString(typed["doi"]) + "|" + scienceString(typed["title"])
				if !seen[key] {
					seen[key] = true
					papers = append(papers, typed)
				}
				return
			}
			for _, key := range []string{"papers", "results", "items", "data", "works", "records", "structuredContent"} {
				if child, ok := typed[key]; ok {
					walk(child, depth+1)
				}
			}
			if text, ok := typed["text"].(string); ok {
				var decoded any
				if json.Unmarshal([]byte(text), &decoded) == nil {
					walk(decoded, depth+1)
				}
			}
		}
	}

	walk(resMap, 0)
	return papers
}

func looksLikeSciencePaper(m map[string]any) bool {
	if strings.TrimSpace(scienceString(m["title"])) == "" {
		return false
	}
	for _, key := range []string{"doi", "id", "openalex_id", "openalex", "work_id", "abstract", "authors"} {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}

func formatSciencePaperMarkdown(title string, paper map[string]any, sourceURL string) string {
	authors := scienceAuthors(paper["authors"])
	venue := scienceString(paper["venue"])
	if venue == "" {
		venue = scienceString(paper["host_venue"])
	}
	year := scienceString(paper["year"])
	if year == "" {
		year = scienceString(paper["publication_year"])
	}
	doi := normalizeScienceDOI(scienceString(paper["doi"]))
	openAlexID := firstNonEmpty(
		scienceString(paper["openalex_id"]),
		scienceString(paper["openalex"]),
		scienceString(paper["work_id"]),
		scienceString(paper["id"]),
	)

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("title: %q\n", title))
	if len(authors) > 0 {
		b.WriteString(fmt.Sprintf("authors: %q\n", strings.Join(authors, ", ")))
	}
	if venue != "" {
		b.WriteString(fmt.Sprintf("venue: %q\n", venue))
	}
	if year != "" {
		b.WriteString(fmt.Sprintf("year: %s\n", year))
	}
	if doi != "" {
		b.WriteString(fmt.Sprintf("doi: %q\n", doi))
	}
	if openAlexID != "" {
		b.WriteString(fmt.Sprintf("openalex: %q\n", openAlexID))
	}
	if sourceURL != "" {
		b.WriteString(fmt.Sprintf("source: %q\n", sourceURL))
	}
	b.WriteString("---\n\n")
	b.WriteString("# " + title + "\n\n")
	if len(authors) > 0 {
		b.WriteString("**Authors:** " + strings.Join(authors, ", ") + "\n\n")
	}
	if venue != "" {
		b.WriteString("**Venue:** " + venue + "\n\n")
	}
	if year != "" {
		b.WriteString("**Year:** " + year + "\n\n")
	}
	if sourceURL != "" {
		b.WriteString("**Source:** " + sourceURL + "\n\n")
	}
	b.WriteString("## Abstract\n\n")
	abstract := strings.TrimSpace(scienceString(paper["abstract"]))
	if abstract == "" {
		abstract = "_No abstract returned by upstream._"
	}
	b.WriteString(abstract + "\n")
	return b.String()
}

func scienceAuthors(v any) []string {
	switch typed := v.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []string:
		return typed
	case []any:
		var out []string
		for _, item := range typed {
			switch author := item.(type) {
			case string:
				if strings.TrimSpace(author) != "" {
					out = append(out, author)
				}
			case map[string]any:
				name := firstNonEmpty(
					scienceString(author["name"]),
					scienceString(author["display_name"]),
					scienceString(author["author"]),
				)
				if name != "" {
					out = append(out, name)
				}
			}
		}
		return out
	}
	return nil
}

func scienceString(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case float64:
		if typed == math.Trunc(typed) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case json.Number:
		return typed.String()
	}
	return ""
}

func normalizeScienceDOI(doi string) string {
	doi = strings.TrimSpace(doi)
	doi = strings.TrimPrefix(doi, "https://doi.org/")
	doi = strings.TrimPrefix(doi, "http://doi.org/")
	return strings.TrimPrefix(doi, "doi:")
}

func normalizeOpenAlexID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "https://openalex.org/")
	id = strings.TrimPrefix(id, "http://openalex.org/")
	return id
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
