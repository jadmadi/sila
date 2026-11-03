package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// SearchResult represents a single hit from Google Custom Search.
type SearchResult struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

type googleCSEResponse struct {
	Items []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"items"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ErrQuotaExceeded is returned when a search provider's rate limit or quota is exhausted (HTTP 429).
var ErrQuotaExceeded = errors.New("search provider quota exceeded (HTTP 429)")

// IsQuotaExhausted reports whether an error indicates rate-limit or quota exhaustion.
func IsQuotaExhausted(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrQuotaExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "quota") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "resource has been exhausted")
}

var googleCSEBaseURL = "https://www.googleapis.com/customsearch/v1"

// SearchGoogleCSE queries Google Programmable Search (Custom Search JSON API).
func SearchGoogleCSE(apiKey, cx, query string, top int, allowedSites []string) ([]SearchResult, error) {
	if apiKey == "" {
		return nil, errors.New("Google API key is required (set via sila config set google_api_key <key> or GOOGLE_API_KEY env)")
	}
	if cx == "" {
		return nil, errors.New("Google Search Engine ID (CX) is required (set via sila config set google_cse_cx <cx> or GOOGLE_CSE_CX env)")
	}
	if top <= 0 {
		top = 5
	}
	if top > 10 {
		top = 10
	}

	q := strings.TrimSpace(query)
	if len(allowedSites) > 0 {
		var siteTerms []string
		for _, s := range allowedSites {
			s = strings.TrimSpace(s)
			s = strings.TrimPrefix(s, "https://")
			s = strings.TrimPrefix(s, "http://")
			s = strings.TrimSuffix(s, "/*")
			s = strings.TrimSuffix(s, "*")
			s = strings.TrimSuffix(s, "/")
			if s != "" {
				siteTerms = append(siteTerms, "site:"+s)
			}
		}
		if len(siteTerms) > 0 {
			q = fmt.Sprintf("%s (%s)", q, strings.Join(siteTerms, " OR "))
		}
	}

	endpoint := fmt.Sprintf("%s?key=%s&cx=%s&q=%s&num=%d",
		googleCSEBaseURL,
		url.QueryEscape(apiKey),
		url.QueryEscape(cx),
		url.QueryEscape(q),
		top,
	)

	req, err := http.NewRequestWithContext(context.Background(), "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "sila/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: HTTP 429 Too Many Requests", ErrQuotaExceeded)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	var cseResp googleCSEResponse
	if err := json.Unmarshal(bodyBytes, &cseResp); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("google search http error (%d)", resp.StatusCode)
		}
		return nil, fmt.Errorf("failed to parse google search response: %w", err)
	}

	if cseResp.Error != nil {
		if cseResp.Error.Code == 429 || strings.Contains(strings.ToLower(cseResp.Error.Message), "quota") || strings.Contains(strings.ToLower(cseResp.Error.Message), "rate limit") {
			return nil, fmt.Errorf("%w: google search api error (%d): %s", ErrQuotaExceeded, cseResp.Error.Code, cseResp.Error.Message)
		}
		return nil, fmt.Errorf("google search api error (%d): %s", cseResp.Error.Code, cseResp.Error.Message)
	}

	var results []SearchResult
	for _, it := range cseResp.Items {
		results = append(results, SearchResult{
			Title:   it.Title,
			Link:    it.Link,
			Snippet: it.Snippet,
		})
	}

	return results, nil
}

// IngestSearchResults takes search results, fetches their full HTML, converts to Markdown, and saves into store.
func IngestSearchResults(st *store.Store, results []SearchResult, projectID string) ([]*store.Doc, error) {
	var urls []string
	for _, r := range results {
		if r.Link != "" {
			urls = append(urls, r.Link)
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	opts := DefaultCrawlOptions()
	opts.MaxPages = len(urls)

	return ingestURLList(st, client, urls, projectID, opts)
}
