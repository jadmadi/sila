package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

// CodeSearchQuery specifies parameters for open-source code search.
type CodeSearchQuery struct {
	Query     string `json:"query"`
	Language  string `json:"language,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Extension string `json:"extension,omitempty"`
	Path      string `json:"path,omitempty"`
	Top       int    `json:"top,omitempty"`
}

// CodeSearchResult represents a single matching code file from public repositories.
type CodeSearchResult struct {
	Repo     string `json:"repo"`
	Path     string `json:"path"`
	HTMLURL  string `json:"html_url"`
	RawURL   string `json:"raw_url"`
	Snippet  string `json:"snippet,omitempty"`
	Language string `json:"language,omitempty"`
}

// SearchCode searches open-source code across public repositories.
// Follows the priority: Anonymous Search -> gh auth token -> GITHUB_TOKEN -> grep.app fallback.
func SearchCode(q CodeSearchQuery, cfg *config.Config) ([]CodeSearchResult, string, error) {
	if strings.TrimSpace(q.Query) == "" {
		return nil, "", errors.New("search query cannot be empty")
	}
	if q.Top <= 0 {
		q.Top = 5
	}
	if q.Top > 10 {
		q.Top = 10
	}
	if cfg == nil {
		cfg, _ = config.Load("")
	}

	// 1. Try Anonymous GitHub Code Search
	res, err := searchGitHubCode(q, "")
	if err == nil && len(res) > 0 {
		return res, "GitHub Code Search (Anonymous)", nil
	}

	// 2. If anonymous failed or was rate limited, try local gh auth token
	if ghToken := getGHAuthToken(); ghToken != "" {
		res, err = searchGitHubCode(q, ghToken)
		if err == nil && len(res) > 0 {
			return res, "GitHub Code Search (gh CLI auth)", nil
		}
	}

	// 3. Try configured GitHub Token
	token := cfg.GitHubToken
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token != "" {
		res, err = searchGitHubCode(q, token)
		if err == nil && len(res) > 0 {
			return res, "GitHub Code Search (Token auth)", nil
		}
	}

	// 4. Try Sourcegraph API (anonymous or token)
	sgToken := cfg.SourcegraphToken
	if sgToken == "" {
		sgToken = os.Getenv("SRC_ACCESS_TOKEN")
	}
	sgRes, sgErr := searchSourcegraphCode(q, sgToken)
	if sgErr == nil && len(sgRes) > 0 {
		return sgRes, "Sourcegraph", nil
	}

	// 5. Fallback to grep.app if available
	res, grepErr := searchGrepApp(q)
	if grepErr == nil && len(res) > 0 {
		return res, "grep.app", nil
	}

	if err != nil {
		return nil, "GitHub Code Search", err
	}
	return nil, "GitHub Code Search", errors.New("no matching code files found")
}

func searchGitHubCode(q CodeSearchQuery, token string) ([]CodeSearchResult, error) {
	// Construct GitHub Search query
	var parts []string
	parts = append(parts, q.Query)

	if q.Language != "" {
		parts = append(parts, "language:"+q.Language)
	}
	if q.Repo != "" {
		parts = append(parts, "repo:"+q.Repo)
	}
	if q.Extension != "" {
		ext := strings.TrimPrefix(q.Extension, ".")
		parts = append(parts, "extension:"+ext)
	}
	if q.Path != "" {
		parts = append(parts, "path:"+q.Path)
	}

	queryParam := strings.Join(parts, " ")
	endpoint := fmt.Sprintf("https://api.github.com/search/code?q=%s&per_page=%d", url.QueryEscape(queryParam), q.Top)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github code search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github rate limit/auth required (HTTP %d): %s", resp.StatusCode, string(body))
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("github returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var ghResp struct {
		Items []struct {
			Name       string `json:"name"`
			Path       string `json:"path"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName string `json:"full_name"`
				HTMLURL  string `json:"html_url"`
			} `json:"repository"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ghResp); err != nil {
		return nil, fmt.Errorf("failed to decode github search response: %w", err)
	}

	var results []CodeSearchResult
	for _, item := range ghResp.Items {
		rawURL := ""
		if strings.Contains(item.HTMLURL, "/blob/") {
			rawURL = strings.Replace(item.HTMLURL, "github.com", "raw.githubusercontent.com", 1)
			rawURL = strings.Replace(rawURL, "/blob/", "/", 1)
		}
		results = append(results, CodeSearchResult{
			Repo:     item.Repository.FullName,
			Path:     item.Path,
			HTMLURL:  item.HTMLURL,
			RawURL:   rawURL,
			Language: q.Language,
		})
		if q.Top > 0 && len(results) >= q.Top {
			break
		}
	}

	return results, nil
}

func searchGrepApp(q CodeSearchQuery) ([]CodeSearchResult, error) {
	endpoint := fmt.Sprintf("https://grep.app/api/search?q=%s", url.QueryEscape(q.Query))
	if q.Language != "" {
		endpoint += "&lang=" + url.QueryEscape(q.Language)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grep.app returned HTTP %d", resp.StatusCode)
	}

	var grepResp struct {
		Hits struct {
			Hits []struct {
				Repo struct {
					Raw string `json:"raw"`
				} `json:"repo"`
				Path struct {
					Raw string `json:"raw"`
				} `json:"path"`
				Content struct {
					Snippet string `json:"snippet"`
				} `json:"content"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&grepResp); err != nil {
		return nil, err
	}

	var results []CodeSearchResult
	for _, hit := range grepResp.Hits.Hits {
		repo := hit.Repo.Raw
		path := hit.Path.Raw
		if repo == "" || path == "" {
			continue
		}
		htmlURL := fmt.Sprintf("https://github.com/%s/blob/HEAD/%s", repo, path)
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/HEAD/%s", repo, path)
		results = append(results, CodeSearchResult{
			Repo:     repo,
			Path:     path,
			HTMLURL:  htmlURL,
			RawURL:   rawURL,
			Snippet:  cleanHTMLString(hit.Content.Snippet),
			Language: q.Language,
		})
		if q.Top > 0 && len(results) >= q.Top {
			break
		}
	}

	return results, nil
}

func searchSourcegraphCode(q CodeSearchQuery, token string) ([]CodeSearchResult, error) {
	var queryParts []string
	queryParts = append(queryParts, q.Query)
	if q.Language != "" {
		queryParts = append(queryParts, "lang:"+q.Language)
	}
	if q.Repo != "" {
		queryParts = append(queryParts, "repo:"+q.Repo)
	}
	if q.Extension != "" {
		ext := strings.TrimPrefix(q.Extension, ".")
		queryParts = append(queryParts, "file:\\."+ext+"$")
	}

	srcQuery := strings.Join(queryParts, " ")
	graphqlQuery := `query ($q: String!) { search(query: $q, version: V3) { results { results { __typename ... on FileMatch { file { path url } repository { name } lineMatches { lineNumber lineContent } } } } } }`

	payload := map[string]any{
		"query": graphqlQuery,
		"variables": map[string]any{
			"q": srcQuery,
		},
	}
	bodyJSON, _ := json.Marshal(payload)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", "https://sourcegraph.com/.api/graphql", strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sourcegraph returned HTTP %d", resp.StatusCode)
	}

	var sgResp struct {
		Data struct {
			Search struct {
				Results struct {
					Results []struct {
						Typename string `json:"__typename"`
						File     struct {
							Path string `json:"path"`
							URL  string `json:"url"`
						} `json:"file"`
						Repository struct {
							Name string `json:"name"`
						} `json:"repository"`
						LineMatches []struct {
							LineNumber  int    `json:"lineNumber"`
							LineContent string `json:"lineContent"`
						} `json:"lineMatches"`
					} `json:"results"`
				} `json:"results"`
			} `json:"search"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&sgResp); err != nil {
		return nil, err
	}

	var results []CodeSearchResult
	for _, r := range sgResp.Data.Search.Results.Results {
		if r.File.Path == "" || r.Repository.Name == "" {
			continue
		}
		htmlURL := "https://sourcegraph.com" + r.File.URL
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/HEAD/%s", r.Repository.Name, r.File.Path)
		snippet := ""
		if len(r.LineMatches) > 0 {
			snippet = cleanHTMLString(r.LineMatches[0].LineContent)
		}
		results = append(results, CodeSearchResult{
			Repo:     r.Repository.Name,
			Path:     r.File.Path,
			HTMLURL:  htmlURL,
			RawURL:   rawURL,
			Snippet:  snippet,
			Language: q.Language,
		})
		if q.Top > 0 && len(results) >= q.Top {
			break
		}
	}

	return results, nil
}

func getGHAuthToken() string {
	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// IngestCodeSearchResults fetches full source files from code search results and saves them into the project knowledge store.
func IngestCodeSearchResults(st *store.Store, results []CodeSearchResult, projectID string) ([]*store.Doc, error) {
	if st == nil || len(results) == 0 {
		return nil, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var ingested []*store.Doc

	for _, r := range results {
		if r.RawURL == "" {
			continue
		}
		req, err := http.NewRequest("GET", r.RawURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
		resp.Body.Close()
		if err != nil || len(body) == 0 {
			continue
		}

		title := fmt.Sprintf("[%s] %s", r.Repo, filepath.Base(r.Path))
		content := fmt.Sprintf("# %s (%s)\nSource: %s\n\n```%s\n%s\n```\n", filepath.Base(r.Path), r.Repo, r.HTMLURL, r.Language, string(body))

		doc := store.Doc{
			ID:        store.DocID(projectID, r.HTMLURL),
			ProjectID: projectID,
			Title:     title,
			URL:       r.HTMLURL,
			Content:   content,
			Format:    "markdown",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := st.InsertDoc(doc); err == nil {
			ingested = append(ingested, &doc)
		}
	}

	return ingested, nil
}
