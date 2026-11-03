package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
)

// SearchProvider constants.
const (
	ProviderDuckDuckGo = "duckduckgo"
	ProviderBrave      = "brave"
	ProviderTavily     = "tavily"
	ProviderKagi       = "kagi"
	ProviderGoogle     = "google"
)

var (
	reDDGResult  = regexp.MustCompile(`(?is)<div class="result__body[^"]*">(.*?)</div>\s*</div>`)
	reDDGLink    = regexp.MustCompile(`(?is)<a class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reDDGSnippet = regexp.MustCompile(`(?is)<(?:a|div) class="result__snippet"[^>]*>(.*?)</(?:a|div)>`)
	reStripTags  = regexp.MustCompile(`(?is)<[^>]+>`)
	reWhitespace = regexp.MustCompile(`\s+`)
)

var duckDuckGoBaseURL = "https://html.duckduckgo.com/html/"

// SearchWeb queries the best available web search provider based on configuration.
// Order of priority if SearchProvider is "auto" or empty:
// 1. Tavily (if tavily_api_key configured)
// 2. Brave (if brave_api_key configured)
// 3. Kagi (if kagi_api_key configured)
// 4. Google CSE (if google_api_key & google_cse_cx configured)
// 5. DuckDuckGo (Zero-config out of the box — no keys needed!)
func SearchWeb(query string, top int, docSites []string, cfg *config.Config) ([]SearchResult, string, error) {
	if cfg == nil {
		cfg, _ = config.Load("")
	}
	if top <= 0 {
		top = 5
	}
	if top > 10 {
		top = 10
	}

	provider := strings.ToLower(strings.TrimSpace(cfg.SearchProvider))
	if provider == "" || provider == "auto" {
		if cfg.TavilyAPIKey != "" {
			provider = ProviderTavily
		} else if cfg.BraveAPIKey != "" {
			provider = ProviderBrave
		} else if cfg.KagiAPIKey != "" {
			provider = ProviderKagi
		} else if cfg.GoogleAPIKey != "" && cfg.GoogleCSECX != "" {
			provider = ProviderGoogle
		} else {
			provider = ProviderDuckDuckGo
		}
	}

	switch provider {
	case ProviderTavily:
		res, err := SearchTavily(cfg.TavilyAPIKey, query, top, docSites)
		return res, "Tavily", err
	case ProviderBrave:
		res, err := SearchBrave(cfg.BraveAPIKey, query, top, docSites)
		return res, "Brave Search", err
	case ProviderKagi:
		res, err := SearchKagi(cfg.KagiAPIKey, query, top, docSites)
		return res, "Kagi Search", err
	case ProviderGoogle:
		res, err := SearchGoogleCSE(cfg.GoogleAPIKey, cfg.GoogleCSECX, query, top, docSites)
		if err != nil && IsQuotaExhausted(err) {
			ddgRes, ddgErr := SearchDuckDuckGo(query, top, docSites)
			if ddgErr == nil && len(ddgRes) > 0 {
				return ddgRes, "DuckDuckGo", nil
			}
		}
		return res, "Google Custom Search", err
	default:
		res, err := SearchDuckDuckGo(query, top, docSites)
		if err != nil || len(res) == 0 {
			return nil, "Web Search", errors.New("web search is not configured.\n\n" +
				"Configure any of the following free providers (1 key, ~60 seconds):\n\n" +
				"• Option 1: Brave Search (Recommended — 2,000 free searches / month, 1 key)\n" +
				"  Get key: https://brave.com/search/api/\n" +
				"  Run:     sila config set brave_api_key <YOUR_KEY>\n\n" +
				"• Option 2: Tavily Search (Built for AI Agents — 1,000 free searches / month, 1 key)\n" +
				"  Get key: https://tavily.com\n" +
				"  Run:     sila config set tavily_api_key <YOUR_KEY>\n\n" +
				"• Option 3: Kagi Search API (Clean ad-free search)\n" +
				"  Get key: https://kagi.com/settings?p=api\n" +
				"  Run:     sila config set kagi_api_key <YOUR_KEY>\n\n" +
				"• Option 4: Google Custom Search (100 free searches / day, 2 keys)\n" +
				"  Get key: https://developers.google.com/custom-search/v1/overview#api_key\n" +
				"  Run:     sila config set google_api_key <KEY> && sila config set google_cse_cx <CX>")
		}
		return res, "DuckDuckGo", nil
	}
}

// SearchDuckDuckGo performs a zero-configuration web search using DuckDuckGo HTML.
func SearchDuckDuckGo(query string, top int, allowedSites []string) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query cannot be empty")
	}

	effQuery := query
	if len(allowedSites) > 0 {
		var siteFilters []string
		for _, s := range allowedSites {
			host := cleanHost(s)
			if host != "" {
				siteFilters = append(siteFilters, "site:"+host)
			}
		}
		if len(siteFilters) > 0 {
			effQuery = effQuery + " (" + strings.Join(siteFilters, " OR ") + ")"
		}
	}

	endpoint := duckDuckGoBaseURL + "?q=" + url.QueryEscape(effQuery)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader("q="+url.QueryEscape(effQuery)))
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("duckduckgo search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("duckduckgo returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	return parseDuckDuckGoHTML(string(body), top)
}

func parseDuckDuckGoHTML(html string, top int) ([]SearchResult, error) {
	var results []SearchResult
	seenURLs := make(map[string]bool)

	blocks := reDDGResult.FindAllStringSubmatch(html, -1)
	for _, b := range blocks {
		if len(b) < 2 {
			continue
		}
		inner := b[1]

		linkMatches := reDDGLink.FindStringSubmatch(inner)
		if len(linkMatches) < 3 {
			continue
		}

		rawURL := linkMatches[1]
		actualURL := decodeDDGURL(rawURL)
		if actualURL == "" || seenURLs[actualURL] {
			continue
		}

		title := cleanHTMLString(linkMatches[2])
		snippet := ""
		snippetMatches := reDDGSnippet.FindStringSubmatch(inner)
		if len(snippetMatches) >= 2 {
			snippet = cleanHTMLString(snippetMatches[1])
		}

		if title == "" || actualURL == "" {
			continue
		}

		seenURLs[actualURL] = true
		results = append(results, SearchResult{
			Title:   title,
			Link:    actualURL,
			Snippet: snippet,
		})

		if top > 0 && len(results) >= top {
			break
		}
	}

	return results, nil
}

func decodeDDGURL(rawURL string) string {
	if strings.HasPrefix(rawURL, "//duckduckgo.com/l/?uddg=") || strings.Contains(rawURL, "/l/?uddg=") {
		u, err := url.Parse(rawURL)
		if err == nil {
			uddg := u.Query().Get("uddg")
			if uddg != "" {
				if dec, err := url.QueryUnescape(uddg); err == nil {
					return dec
				}
				return uddg
			}
		}
	}
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		return rawURL
	}
	if strings.HasPrefix(rawURL, "//") {
		return "https:" + rawURL
	}
	return ""
}

// SearchBrave queries Brave Search API (v1).
func SearchBrave(apiKey, query string, top int, allowedSites []string) ([]SearchResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("Brave API key is required (set via: sila config set brave_api_key <key>)")
	}

	effQuery := query
	if len(allowedSites) > 0 {
		var siteFilters []string
		for _, s := range allowedSites {
			host := cleanHost(s)
			if host != "" {
				siteFilters = append(siteFilters, "site:"+host)
			}
		}
		if len(siteFilters) > 0 {
			effQuery = effQuery + " (" + strings.Join(siteFilters, " OR ") + ")"
		}
	}

	endpoint := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d", url.QueryEscape(effQuery), top)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", apiKey)
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("brave search returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("failed to decode brave search response: %w", err)
	}

	var results []SearchResult
	for _, r := range braveResp.Web.Results {
		if r.URL == "" || r.Title == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:   cleanHTMLString(r.Title),
			Link:    r.URL,
			Snippet: cleanHTMLString(r.Description),
		})
		if top > 0 && len(results) >= top {
			break
		}
	}

	return results, nil
}

// SearchTavily queries Tavily Search API.
func SearchTavily(apiKey, query string, top int, allowedSites []string) ([]SearchResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("Tavily API key is required (set via: sila config set tavily_api_key <key>)")
	}

	payload := map[string]any{
		"api_key":             apiKey,
		"query":               query,
		"max_results":         top,
		"search_depth":        "basic",
		"include_raw_content": false,
	}

	if len(allowedSites) > 0 {
		var domains []string
		for _, s := range allowedSites {
			host := cleanHost(s)
			if host != "" {
				domains = append(domains, host)
			}
		}
		if len(domains) > 0 {
			payload["include_domains"] = domains
		}
	}

	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tavily search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tavily search returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tavilyResp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tavilyResp); err != nil {
		return nil, fmt.Errorf("failed to decode tavily search response: %w", err)
	}

	var results []SearchResult
	for _, r := range tavilyResp.Results {
		if r.URL == "" || r.Title == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:   cleanHTMLString(r.Title),
			Link:    r.URL,
			Snippet: cleanHTMLString(r.Content),
		})
		if top > 0 && len(results) >= top {
			break
		}
	}

	return results, nil
}

// SearchKagi queries Kagi Search API (v0).
func SearchKagi(apiKey, query string, top int, allowedSites []string) ([]SearchResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("Kagi API key is required (set via: sila config set kagi_api_key <key>)")
	}

	effQuery := query
	if len(allowedSites) > 0 {
		var siteFilters []string
		for _, s := range allowedSites {
			host := cleanHost(s)
			if host != "" {
				siteFilters = append(siteFilters, "site:"+host)
			}
		}
		if len(siteFilters) > 0 {
			effQuery = effQuery + " (" + strings.Join(siteFilters, " OR ") + ")"
		}
	}

	endpoint := fmt.Sprintf("https://kagi.com/api/v0/search?q=%s&limit=%d", url.QueryEscape(effQuery), top)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+strings.TrimSpace(apiKey))
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kagi search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("kagi search returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var kagiResp struct {
		Data []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Snippet string `json:"snippet"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&kagiResp); err != nil {
		return nil, fmt.Errorf("failed to decode kagi search response: %w", err)
	}

	var results []SearchResult
	for _, r := range kagiResp.Data {
		if r.URL == "" || r.Title == "" {
			continue
		}
		results = append(results, SearchResult{
			Title:   cleanHTMLString(r.Title),
			Link:    r.URL,
			Snippet: cleanHTMLString(r.Snippet),
		})
		if top > 0 && len(results) >= top {
			break
		}
	}

	return results, nil
}

func cleanHTMLString(s string) string {
	s = reStripTags.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func cleanHost(site string) string {
	site = strings.TrimSpace(site)
	site = strings.TrimPrefix(site, "https://")
	site = strings.TrimPrefix(site, "http://")
	site = strings.TrimSuffix(site, "/*")
	site = strings.TrimSuffix(site, "/")
	if idx := strings.Index(site, "/"); idx != -1 {
		site = site[:idx]
	}
	return site
}
