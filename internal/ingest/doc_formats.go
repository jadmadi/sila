package ingest

import (
	"bufio"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// ExportFormat specifies the documentation export source format.
type ExportFormat string

// ExportFormat constants define supported documentation source and export formats.
const (
	FormatAuto       ExportFormat = "auto"
	FormatNotion     ExportFormat = "notion"
	FormatConfluence ExportFormat = "confluence"
	FormatGitBook    ExportFormat = "gitbook"
	FormatStandard   ExportFormat = "standard"
)

// ParseExportFormat normalizes a format string.
func ParseExportFormat(s string) ExportFormat {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "notion":
		return FormatNotion
	case "confluence":
		return FormatConfluence
	case "gitbook", "frontmatter":
		return FormatGitBook
	case "standard", "plain", "markdown":
		return FormatStandard
	default:
		return FormatAuto
	}
}

var (
	// Notion 32-character hex ID pattern (e.g. "Architecture Overview 1a2b3c4d5e6f1a2b3c4d5e6f1a2b3c4d")
	notionHashRe    = regexp.MustCompile(`(?i)(?:%20|[ _])([0-9a-f]{32})`)
	notionEndHashRe = regexp.MustCompile(`(?i)(?:%20|[ _])([0-9a-f]{32})(?:\.[a-zA-Z0-9]+)?$`)
	notionLinkRe    = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

	// Confluence macro & metadata patterns
	confMacroCodeRe  = regexp.MustCompile(`(?is)<ac:structured-macro[^>]*ac:name=["']code["'][^>]*>(?:.*?<ac:parameter[^>]*ac:name=["']language["'][^>]*>(.*?)</ac:parameter>)?.*?<ac:plain-text-body><!\[CDATA\[(.*?)\]\]></ac:plain-text-body>.*?</ac:structured-macro>`)
	confMacroAlertRe = regexp.MustCompile(`(?is)<ac:structured-macro[^>]*ac:name=["'](info|warning|note|tip)["'][^>]*>.*?<ac:rich-text-body>(.*?)</ac:rich-text-body>.*?</ac:structured-macro>`)
	confMacroGeneric = regexp.MustCompile(`(?is)<ac:[^>]+>.*?</ac:[^>]+>|<ac:[^>]+/>`)
	confMetaTitleRe  = regexp.MustCompile(`(?is)<meta\s+name=["']ajs-page-title["']\s+content=["'](.*?)["']`)
	confMetaAuthorRe = regexp.MustCompile(`(?is)<meta\s+name=["']author["']\s+content=["'](.*?)["']|<div[^>]*class=["']page-metadata["'][^>]*>.*?Created by\s+<a[^>]*>(.*?)</a>`)
	confMetaLabelsRe = regexp.MustCompile(`(?is)<meta\s+name=["']labels["']\s+content=["'](.*?)["']|<ul[^>]*id=["']labels-section["'][^>]*>(.*?)</ul>`)
	confWikiBodyRe   = regexp.MustCompile(`(?is)<div[^>]*class=["'](?:wiki-content|pageSectionHeader)["'][^>]*>(.*?)</div>\s*<div id=["']footer["']`)
)

// CleanNotionTitle strips the trailing 32-character hexadecimal page hash from a Notion title or filename.
func CleanNotionTitle(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	base = strings.ReplaceAll(base, "%20", " ")
	cleaned := notionEndHashRe.ReplaceAllString(base, "")
	return strings.TrimSpace(cleaned)
}

// CleanNotionMarkdown strips Notion's hexadecimal page hashes from filenames, titles, and internal markdown links.
func CleanNotionMarkdown(content string) string {
	// Normalize markdown cross-reference links: [Title](Page%201a2b3c4d5e6f.md) -> [Title](Page.md)
	return notionLinkRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := notionLinkRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		linkText := sub[1]
		linkURL := sub[2]

		// Do not touch external web links
		if strings.HasPrefix(linkURL, "http://") || strings.HasPrefix(linkURL, "https://") || strings.HasPrefix(linkURL, "mailto:") {
			return m
		}

		cleanedURL := notionHashRe.ReplaceAllString(linkURL, "")
		cleanedURL = strings.ReplaceAll(cleanedURL, "%20", " ")
		return fmt.Sprintf("[%s](%s)", linkText, cleanedURL)
	})
}

// CleanConfluenceHTML parses Confluence export HTML, converting structured macros and extracting metadata.
func CleanConfluenceHTML(raw string) (title string, author string, labels []string, bodyMarkdown string) {
	// 1. Extract title
	if m := confMetaTitleRe.FindStringSubmatch(raw); len(m) > 1 {
		title = strings.TrimSpace(html.UnescapeString(m[1]))
	}

	// 2. Extract author
	if m := confMetaAuthorRe.FindStringSubmatch(raw); len(m) > 1 {
		for i := 1; i < len(m); i++ {
			if m[i] != "" {
				author = strings.TrimSpace(stripTagsRe.ReplaceAllString(m[i], ""))
				break
			}
		}
	}

	// 3. Extract labels
	if m := confMetaLabelsRe.FindStringSubmatch(raw); len(m) > 1 {
		rawLabels := ""
		for i := 1; i < len(m); i++ {
			if m[i] != "" {
				rawLabels = m[i]
				break
			}
		}
		rawLabels = stripTagsRe.ReplaceAllString(rawLabels, " ")
		for _, part := range strings.Split(rawLabels, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				labels = append(labels, part)
			}
		}
	}

	// 4. Transform Code Macros
	s := confMacroCodeRe.ReplaceAllStringFunc(raw, func(match string) string {
		sub := confMacroCodeRe.FindStringSubmatch(match)
		lang := ""
		code := ""
		if len(sub) > 1 {
			lang = strings.TrimSpace(sub[1])
		}
		if len(sub) > 2 {
			code = sub[2]
		}
		return fmt.Sprintf("\n\n```%s\n%s\n```\n\n", lang, strings.TrimRight(code, "\n"))
	})

	// 5. Transform Alert Macros (info, warning, note, tip)
	s = confMacroAlertRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := confMacroAlertRe.FindStringSubmatch(match)
		kind := strings.ToUpper(sub[1])
		inner := stripTagsRe.ReplaceAllString(sub[2], "")
		inner = strings.TrimSpace(html.UnescapeString(inner))
		return fmt.Sprintf("\n\n> [!%s]\n> %s\n\n", kind, inner)
	})

	// 6. Strip remaining Confluence XML tags (<ac:...>, <ri:...>)
	s = confMacroGeneric.ReplaceAllString(s, "")

	// 7. Pass remaining HTML through HTMLToMarkdown
	extractedTitle, md := HTMLToMarkdown(s)
	if title == "" {
		title = extractedTitle
	}

	// Prepend metadata header if author or labels were found
	var metaHeader []string
	if author != "" {
		metaHeader = append(metaHeader, fmt.Sprintf("**Author:** %s", author))
	}
	if len(labels) > 0 {
		metaHeader = append(metaHeader, fmt.Sprintf("**Labels:** %s", strings.Join(labels, ", ")))
	}
	if len(metaHeader) > 0 {
		md = fmt.Sprintf("> %s\n\n%s", strings.Join(metaHeader, " | "), md)
	}

	return title, author, labels, md
}

// FrontmatterData holds parsed metadata from YAML/TOML frontmatter.
type FrontmatterData struct {
	Title       string
	Description string
	Tags        []string
	Author      string
	Body        string
}

// ParseFrontmatter extracts YAML/TOML frontmatter headers and returns structured metadata.
func ParseFrontmatter(raw string) FrontmatterData {
	res := FrontmatterData{Body: raw}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	if !scanner.Scan() {
		return res
	}
	firstLine := strings.TrimSpace(scanner.Text())
	var delimiter string
	if firstLine == "---" {
		delimiter = "---"
	} else if firstLine == "+++" {
		delimiter = "+++"
	} else {
		return res
	}

	var frontmatterLines []string
	var bodyLines []string
	inFrontmatter := true

	for scanner.Scan() {
		line := scanner.Text()
		if inFrontmatter && strings.TrimSpace(line) == delimiter {
			inFrontmatter = false
			continue
		}
		if inFrontmatter {
			frontmatterLines = append(frontmatterLines, line)
		} else {
			bodyLines = append(bodyLines, line)
		}
	}

	if inFrontmatter {
		// Delimiter never closed, treat as raw content
		return res
	}

	for _, line := range frontmatterLines {
		trimmed := strings.TrimSpace(line)
		if idx := strings.Index(trimmed, ":"); idx != -1 {
			key := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
			val := strings.TrimSpace(trimmed[idx+1:])
			val = strings.Trim(val, `"'`)

			switch key {
			case "title":
				res.Title = val
			case "description", "summary":
				res.Description = val
			case "author":
				res.Author = val
			case "tags", "categories", "labels":
				val = strings.Trim(val, "[]")
				for _, t := range strings.Split(val, ",") {
					t = strings.TrimSpace(strings.Trim(t, `"'`))
					if t != "" {
						res.Tags = append(res.Tags, t)
					}
				}
			}
		}
	}

	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	res.Body = body
	return res
}

// SniffExportFormat determines the documentation export format from files in a directory.
func SniffExportFormat(dirPath string) ExportFormat {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return FormatStandard
	}

	for _, e := range entries {
		name := e.Name()
		if notionEndHashRe.MatchString(name) {
			return FormatNotion
		}
		if strings.EqualFold(name, "SUMMARY.md") || strings.EqualFold(name, "book.json") {
			return FormatGitBook
		}
	}

	// Inspect sample file contents
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".md" || ext == ".html" || ext == ".htm" {
			data, err := os.ReadFile(filepath.Join(dirPath, e.Name()))
			if err == nil {
				content := string(data)
				if strings.Contains(content, "<ac:structured-macro") || strings.Contains(content, "confluence") || strings.Contains(content, "wiki-content") {
					return FormatConfluence
				}
				if strings.HasPrefix(content, "---\n") && strings.Contains(content, "title:") {
					return FormatGitBook
				}
			}
			count++
			if count >= 5 {
				break
			}
		}
	}

	return FormatStandard
}

// IngestExportOptions configures directory or export file ingestion.
type IngestExportOptions struct {
	Format      ExportFormat
	ProjectID   string
	CustomTitle string
}

// IngestExportResult summarizes document ingestion results.
type IngestExportResult struct {
	DetectedFormat ExportFormat `json:"detected_format"`
	TotalFiles     int          `json:"total_files"`
	Docs           []*store.Doc `json:"docs"`
}

// IngestExport decodes and ingests an exported wiki directory or single document.
func IngestExport(st *store.Store, target string, opts IngestExportOptions) (*IngestExportResult, error) {
	cleanPath := filepath.Clean(target)
	fi, err := os.Stat(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("stat target: %w", err)
	}

	if opts.ProjectID == "" {
		cwd, _ := os.Getwd()
		opts.ProjectID = store.ProjectID(cwd)
	}

	format := opts.Format
	if format == "" || format == FormatAuto {
		if fi.IsDir() {
			format = SniffExportFormat(cleanPath)
		} else {
			format = FormatStandard
		}
	}

	result := &IngestExportResult{
		DetectedFormat: format,
		Docs:           make([]*store.Doc, 0),
	}

	if !fi.IsDir() {
		doc, err := ingestSingleExportFile(st, cleanPath, opts.ProjectID, opts.CustomTitle, format)
		if err != nil {
			return nil, err
		}
		result.TotalFiles = 1
		result.Docs = append(result.Docs, doc)
		return result, nil
	}

	// Walk directory
	err = filepath.WalkDir(cleanPath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".md" && ext != ".markdown" && ext != ".html" && ext != ".htm" && ext != ".txt" {
			return nil
		}

		doc, err := ingestSingleExportFile(st, path, opts.ProjectID, "", format)
		if err != nil {
			return nil // Skip unreadable files gracefully
		}
		result.TotalFiles++
		result.Docs = append(result.Docs, doc)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	return result, nil
}

func ingestSingleExportFile(st *store.Store, filePath, projectID, customTitle string, format ExportFormat) (*store.Doc, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	raw := string(data)
	ext := strings.ToLower(filepath.Ext(filePath))
	fileName := filepath.Base(filePath)

	var title string
	var content string

	switch format {
	case FormatNotion:
		title = CleanNotionTitle(fileName)
		cleanedMD := CleanNotionMarkdown(raw)
		if ext == ".html" || ext == ".htm" {
			extTitle, md := HTMLToMarkdown(cleanedMD)
			if title == "" || title == fileName {
				title = CleanNotionTitle(extTitle)
			}
			content = md
		} else {
			content = cleanedMD
		}

	case FormatConfluence:
		confTitle, _, _, md := CleanConfluenceHTML(raw)
		if confTitle != "" {
			title = confTitle
		} else {
			title = strings.TrimSuffix(fileName, ext)
		}
		content = md

	case FormatGitBook:
		fm := ParseFrontmatter(raw)
		if fm.Title != "" {
			title = fm.Title
		} else {
			title = strings.TrimSuffix(fileName, ext)
		}
		if fm.Description != "" {
			content = fmt.Sprintf("> %s\n\n%s", fm.Description, fm.Body)
		} else {
			content = fm.Body
		}

	default: // FormatStandard
		if ext == ".html" || ext == ".htm" {
			extractedTitle, md := HTMLToMarkdown(raw)
			title = extractedTitle
			content = md
		} else {
			title = strings.TrimSuffix(fileName, ext)
			content = raw
		}
	}

	if customTitle != "" {
		title = customTitle
	}
	if title == "" {
		title = fileName
	}

	doc := store.Doc{
		ID:        store.DocID(projectID, filePath),
		ProjectID: projectID,
		Title:     title,
		URL:       filePath,
		Content:   content,
		Format:    "markdown",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := st.InsertDoc(doc); err != nil {
		return nil, err
	}
	return &doc, nil
}
