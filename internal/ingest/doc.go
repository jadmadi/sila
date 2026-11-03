package ingest

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

var (
	stripElements = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`),
		regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`),
		regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`),
		regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`),
		regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`),
		regexp.MustCompile(`(?is)<svg[^>]*>.*?</svg>`),
		regexp.MustCompile(`(?is)<iframe[^>]*>.*?</iframe>`),
		regexp.MustCompile(`(?is)<noscript[^>]*>.*?</noscript>`),
	}
	h1Re          = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	h2Re          = regexp.MustCompile(`(?is)<h2[^>]*>(.*?)</h2>`)
	h3Re          = regexp.MustCompile(`(?is)<h3[^>]*>(.*?)</h3>`)
	h46Re         = regexp.MustCompile(`(?is)<h[4-6][^>]*>(.*?)</h[4-6]>`)
	pRe           = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)
	preCodeRe     = regexp.MustCompile(`(?is)<pre[^>]*><code[^>]*>(.*?)</code></pre>`)
	codeRe        = regexp.MustCompile(`(?is)<code[^>]*>(.*?)</code>`)
	liRe          = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	aRe           = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']*)["'][^>]*>(.*?)</a>`)
	brRe          = regexp.MustCompile(`(?is)<(br|hr)[^>]*>`)
	titleRe       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	stripTagsRe   = regexp.MustCompile(`<[^>]+>`)
	multiSpacesRe = regexp.MustCompile(`[ \t]+`)
	multiNlRe     = regexp.MustCompile(`\n{3,}`)
)

// HTMLToMarkdown converts raw HTML into dense, token-efficient Markdown.
func HTMLToMarkdown(rawHTML string) (title string, markdown string) {
	// Extract title
	if m := titleRe.FindStringSubmatch(rawHTML); len(m) > 1 {
		title = strings.TrimSpace(html.UnescapeString(stripTagsRe.ReplaceAllString(m[1], "")))
	}

	// Strip noise elements
	s := rawHTML
	for _, re := range stripElements {
		s = re.ReplaceAllString(s, "")
	}

	// Convert headers
	s = h1Re.ReplaceAllString(s, "\n\n# $1\n\n")
	s = h2Re.ReplaceAllString(s, "\n\n## $1\n\n")
	s = h3Re.ReplaceAllString(s, "\n\n### $1\n\n")
	s = h46Re.ReplaceAllString(s, "\n\n#### $1\n\n")

	// Convert code blocks and inline code
	s = preCodeRe.ReplaceAllString(s, "\n\n```\n$1\n```\n\n")
	s = codeRe.ReplaceAllString(s, "`$1`")

	// Convert lists and paragraphs
	s = liRe.ReplaceAllString(s, "\n- $1")
	s = pRe.ReplaceAllString(s, "\n\n$1\n\n")
	s = brRe.ReplaceAllString(s, "\n")

	// Convert links: [text](href)
	s = aRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := aRe.FindStringSubmatch(match)
		if len(sub) > 2 {
			href := sub[1]
			text := strings.TrimSpace(stripTagsRe.ReplaceAllString(sub[2], ""))
			if text != "" && !strings.HasPrefix(href, "javascript:") {
				return fmt.Sprintf("[%s](%s)", text, href)
			}
		}
		return match
	})

	// Strip remaining HTML tags
	s = stripTagsRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	// Clean up whitespaces
	lines := strings.Split(s, "\n")
	var cleaned []string
	for _, l := range lines {
		l = strings.TrimSpace(multiSpacesRe.ReplaceAllString(l, " "))
		if l != "" || (len(cleaned) > 0 && cleaned[len(cleaned)-1] != "") {
			cleaned = append(cleaned, l)
		}
	}
	markdown = strings.TrimSpace(multiNlRe.ReplaceAllString(strings.Join(cleaned, "\n"), "\n\n"))

	if title == "" {
		if len(cleaned) > 0 {
			title = strings.TrimPrefix(cleaned[0], "# ")
		} else {
			title = "Document"
		}
	}

	return title, markdown
}

// IngestDoc fetches and stores an external URL or local markdown/HTML file.
func IngestDoc(st *store.Store, target, projectID, customTitle string) (*store.Doc, error) {
	if projectID == "" {
		cwd, _ := os.Getwd()
		projectID = store.ProjectID(cwd)
	}

	var content string
	var title string
	var url string
	isURL := strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")

	if isURL {
		url = target
		parsed, err := neturl.Parse(url)
		if err != nil {
			return nil, fmt.Errorf("parse url: %w", err)
		}
		if err := validatePublicURL(parsed); err != nil {
			return nil, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("User-Agent", "sila/0.4.0 (+https://github.com/jadmadi/sila)")

		resp, err := safeHTTPClient(15*time.Second, false).Do(req)
		if err != nil {
			return nil, fmt.Errorf("fetch url: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("http error: %s", resp.Status)
		}

		bodyBytes, err := readLimited(resp.Body, 5*1024*1024)
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		extractedTitle, md := HTMLToMarkdown(string(bodyBytes))
		title = extractedTitle
		content = md
	} else {
		// Local file
		cleanPath := filepath.Clean(target)
		data, err := os.ReadFile(cleanPath)
		if err != nil {
			return nil, fmt.Errorf("read file %s: %w", cleanPath, err)
		}
		raw := string(data)
		if strings.HasSuffix(cleanPath, ".html") || strings.HasSuffix(cleanPath, ".htm") {
			extractedTitle, md := HTMLToMarkdown(raw)
			title = extractedTitle
			content = md
		} else {
			title = filepath.Base(cleanPath)
			content = raw
		}
		url = cleanPath
	}

	if customTitle != "" {
		title = customTitle
	}
	if title == "" {
		title = target
	}

	doc := store.Doc{
		ID:        store.DocID(projectID, target),
		ProjectID: projectID,
		Title:     title,
		URL:       url,
		Content:   content,
		Format:    "markdown",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := st.InsertDoc(doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

func readLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("response body exceeds %d byte limit", max)
	}
	return b, nil
}
