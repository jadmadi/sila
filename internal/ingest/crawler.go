package ingest

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// CrawlOptions configures the recursive documentation crawler.
type CrawlOptions struct {
	MaxDepth    int
	MaxPages    int
	Concurrency int
	Timeout     time.Duration
	CustomTitle string
	// AllowPrivate is test-only. Production callers must leave it false.
	AllowPrivate bool
}

// sanitizeDoc strips non-content tags like script and style from raw HTML.
func sanitizeDoc(s string) string {
	for _, re := range stripElements {
		s = re.ReplaceAllString(s, "")
	}
	return s
}

// DefaultCrawlOptions returns recommended crawler limits.
func DefaultCrawlOptions() CrawlOptions {
	return CrawlOptions{
		MaxDepth:    2,
		MaxPages:    30,
		Concurrency: 6,
		Timeout:     15 * time.Second,
	}
}

// SitemapXML structures for parsing sitemap.xml
type sitemapURLSet struct {
	XMLName xml.Name `xml:"urlset"`
	URLs    []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

var (
	hrefRe = regexp.MustCompile(`(?i)<a[^>]*href=["']([^"']*)["'][^>]*>`)
)

// CrawlAndIngestContext recursively discovers and ingests documentation from a root URL using caller context.
func CrawlAndIngestContext(ctx context.Context, st *store.Store, targetURL, projectID string, opts CrawlOptions) ([]*store.Doc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 2
	}
	if opts.MaxPages <= 0 {
		opts.MaxPages = 30
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 6
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}

	parsedRoot, err := url.Parse(targetURL)
	if err != nil || parsedRoot.Scheme == "" || parsedRoot.Host == "" {
		// If it's a local file, fall back to single doc ingest
		doc, err := IngestDoc(st, targetURL, projectID, opts.CustomTitle)
		if err != nil {
			return nil, err
		}
		return []*store.Doc{doc}, nil
	}

	if err := validatePublicURL(parsedRoot); err != nil && !opts.AllowPrivate {
		return nil, err
	}
	client := safeHTTPClient(opts.Timeout, opts.AllowPrivate)

	// 1. Try llms.txt / llms-full.txt auto-discovery
	if doc, err := tryLLMsTxt(ctx, st, client, parsedRoot, projectID, opts.CustomTitle); err == nil && doc != nil {
		return []*store.Doc{doc}, nil
	} else if err != nil && ctx.Err() != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 2. Try sitemap.xml auto-discovery
	sitemapURLs, err := trySitemap(ctx, client, parsedRoot)
	if err != nil && ctx.Err() != nil {
		return nil, err
	}
	if err == nil && len(sitemapURLs) > 0 {
		return ingestURLListContext(ctx, st, client, sitemapURLs, projectID, opts)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 3. Fall back to BFS link crawling
	discoveredURLs := crawlBFS(ctx, client, parsedRoot, opts.MaxDepth, opts.MaxPages)
	if err := ctx.Err(); err != nil && len(discoveredURLs) == 0 {
		return nil, err
	}
	return ingestURLListContext(ctx, st, client, discoveredURLs, projectID, opts)
}

// CrawlAndIngest recursively discovers and ingests documentation from a root URL.
func CrawlAndIngest(st *store.Store, targetURL, projectID string, opts CrawlOptions) ([]*store.Doc, error) {
	return CrawlAndIngestContext(context.Background(), st, targetURL, projectID, opts)
}

func tryLLMsTxt(ctx context.Context, st *store.Store, client *http.Client, root *url.URL, projectID, customTitle string) (*store.Doc, error) {
	candidates := []string{
		fmt.Sprintf("%s://%s/llms-full.txt", root.Scheme, root.Host),
		fmt.Sprintf("%s://%s/llms.txt", root.Scheme, root.Host),
		fmt.Sprintf("%s://%s/.well-known/llms.txt", root.Scheme, root.Host),
	}

	for _, cand := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", cand, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if resp.StatusCode == 200 {
			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
			resp.Body.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == nil && len(bodyBytes) > 50 {
				title := customTitle
				if title == "" {
					title = fmt.Sprintf("LLMs Reference (%s)", root.Host)
				}
				doc := &store.Doc{
					ID:        store.DocID(projectID, cand),
					ProjectID: projectID,
					Title:     title,
					URL:       cand,
					Content:   sanitizeDoc(string(bodyBytes)),
					Format:    "markdown",
					CreatedAt: time.Now().UTC().Format(time.RFC3339),
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if err := st.InsertDoc(*doc); err == nil {
					return doc, nil
				}
			}
		} else {
			resp.Body.Close()
		}
	}
	return nil, nil
}

func trySitemap(ctx context.Context, client *http.Client, root *url.URL) ([]string, error) {
	candidates := []string{
		fmt.Sprintf("%s://%s/sitemap.xml", root.Scheme, root.Host),
		fmt.Sprintf("%s://%s/sitemap_index.xml", root.Scheme, root.Host),
	}

	var found []string
	prefix := root.String()
	prefix = strings.TrimSuffix(prefix, "/")

	for _, cand := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, "GET", cand, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "sila/1.0")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		if resp.StatusCode == 200 {
			body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
			resp.Body.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == nil {
				var urlSet sitemapURLSet
				if err := xml.Unmarshal(body, &urlSet); err == nil && len(urlSet.URLs) > 0 {
					for _, u := range urlSet.URLs {
						loc := strings.TrimSpace(u.Loc)
						if strings.HasPrefix(loc, prefix) || strings.HasPrefix(loc, root.Scheme+"://"+root.Host+root.Path) {
							found = append(found, loc)
						}
					}
					if len(found) > 0 {
						return found, nil
					}
				}
			}
		} else {
			resp.Body.Close()
		}
	}
	return nil, nil
}

func crawlBFS(ctx context.Context, client *http.Client, root *url.URL, maxDepth, maxPages int) []string {
	visited := make(map[string]bool)
	var queue []struct {
		u     string
		depth int
	}

	rootNorm := normalizeURL(root.String())
	queue = append(queue, struct {
		u     string
		depth int
	}{rootNorm, 0})
	visited[rootNorm] = true

	var results []string

	for len(queue) > 0 && len(results) < maxPages {
		if ctx.Err() != nil {
			return results
		}
		curr := queue[0]
		queue = queue[1:]

		results = append(results, curr.u)
		if curr.depth >= maxDepth || len(results) >= maxPages {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "GET", curr.u, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "sila/1.0")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return results
			}
			continue
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			continue
		}
		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
		resp.Body.Close()
		if err != nil || ctx.Err() != nil {
			continue
		}

		matches := hrefRe.FindAllStringSubmatch(string(bodyBytes), -1)
		for _, m := range matches {
			if len(m) > 1 {
				href := strings.TrimSpace(m[1])
				resolved := resolveHref(curr.u, href)
				if resolved == "" {
					continue
				}
				parsed, err := url.Parse(resolved)
				if err != nil {
					continue
				}
				// Same host and within root path prefix
				if parsed.Host == root.Host && strings.HasPrefix(parsed.Path, root.Path) {
					norm := normalizeURL(resolved)
					if !visited[norm] {
						visited[norm] = true
						queue = append(queue, struct {
							u     string
							depth int
						}{norm, curr.depth + 1})
					}
				}
			}
		}
	}

	return results
}

func ingestURLList(st *store.Store, client *http.Client, urls []string, projectID string, opts CrawlOptions) ([]*store.Doc, error) {
	return ingestURLListContext(context.Background(), st, client, urls, projectID, opts)
}

func ingestURLListContext(ctx context.Context, st *store.Store, client *http.Client, urls []string, projectID string, opts CrawlOptions) ([]*store.Doc, error) {
	if len(urls) > opts.MaxPages {
		urls = urls[:opts.MaxPages]
	}

	var mu sync.Mutex
	var docs []*store.Doc

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup

	for _, rawURL := range urls {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		select {
		case <-ctx.Done():
			wg.Done()
			break
		case sem <- struct{}{}:
		}

		if ctx.Err() != nil {
			<-sem
			wg.Done()
			break
		}

		go func(u string) {
			defer wg.Done()
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}

			req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")
			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != 200 {
				return
			}
			defer resp.Body.Close()
			bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
			if err != nil || len(bodyBytes) == 0 || ctx.Err() != nil {
				return
			}

			title, md := HTMLToMarkdown(string(bodyBytes))
			if strings.TrimSpace(md) == "" || ctx.Err() != nil {
				return
			}
			if title == "" {
				title = u
			}

			doc := &store.Doc{
				ID:        store.DocID(projectID, u),
				ProjectID: projectID,
				Title:     title,
				URL:       u,
				Content:   md,
				Format:    "markdown",
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}

			if ctx.Err() != nil {
				return
			}

			if err := st.InsertDoc(*doc); err == nil {
				mu.Lock()
				docs = append(docs, doc)
				mu.Unlock()
			}
		}(rawURL)
	}

	wg.Wait()
	if ctx.Err() != nil && len(docs) == 0 {
		return nil, ctx.Err()
	}
	return docs, nil
}

func normalizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	// Remove common query tracking parameters
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") || k == "ref" || k == "source" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return strings.TrimSuffix(u.String(), "/")
}

func resolveHref(baseURL, href string) string {
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") {
		return ""
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	rel, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return base.ResolveReference(rel).String()
}
