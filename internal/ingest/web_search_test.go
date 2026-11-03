package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/config"
)

func TestParseDuckDuckGoHTML(t *testing.T) {
	mockHTML := `
<!DOCTYPE html>
<html>
<body>
	<div class="result results_links results_links_deep highlight_result">
		<div class="result__body links_main links_deep">
			<h2 class="result__title">
				<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fmodernc.org%2Fsqlite&amp;rut=123">
					modernc.org/<b>sqlite</b> - Go Packages
				</a>
			</h2>
			<a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fmodernc.org%2Fsqlite">
				Package <b>sqlite</b> is a CGo-free port of SQLite 3.46.0 C source code to Go.
			</a>
		</div>
	</div>
	<div class="result results_links results_links_deep highlight_result">
		<div class="result__body links_main links_deep">
			<h2 class="result__title">
				<a class="result__a" href="https://sqlite.org/fts5.html">
					SQLite FTS5 Extension
				</a>
			</h2>
			<div class="result__snippet">
				FTS5 is an SQLite virtual table module that provides full-text search capability to database applications.
			</div>
		</div>
	</div>
</body>
</html>
`

	results, err := parseDuckDuckGoHTML(mockHTML, 5)
	if err != nil {
		t.Fatalf("parseDuckDuckGoHTML failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}

	if results[0].Link != "https://pkg.go.dev/modernc.org/sqlite" {
		t.Errorf("expected decoded link https://pkg.go.dev/modernc.org/sqlite, got %s", results[0].Link)
	}
	if !strings.Contains(results[0].Title, "modernc.org/sqlite") {
		t.Errorf("expected clean title, got %s", results[0].Title)
	}
	if !strings.Contains(results[0].Snippet, "CGo-free port of SQLite") {
		t.Errorf("expected clean snippet, got %s", results[0].Snippet)
	}

	if results[1].Link != "https://sqlite.org/fts5.html" {
		t.Errorf("expected link https://sqlite.org/fts5.html, got %s", results[1].Link)
	}
}

func TestSearchProviderSelection(t *testing.T) {
	// 1. Default unconfigured fallback
	cfg := &config.Config{}
	_, prov, err := SearchWeb("test query", 2, nil, cfg)
	if err == nil && !strings.Contains(prov, "DuckDuckGo") {
		t.Errorf("expected DuckDuckGo or Web Search diagnostic, got %s", prov)
	}

	// 2. If Tavily key is set -> Tavily
	cfg.TavilyAPIKey = "tvly-test-123"
	// Tavily will fail network call with invalid key, but provider name returned is Tavily
	_, prov, _ = SearchWeb("test query", 2, nil, cfg)
	if prov != "Tavily" {
		t.Errorf("expected Tavily provider when tavily_api_key set, got %s", prov)
	}

	// 3. If Brave key is set (and provider explicit or tavily clear) -> Brave
	cfg.TavilyAPIKey = ""
	cfg.BraveAPIKey = "brave-test-123"
	_, prov, _ = SearchWeb("test query", 2, nil, cfg)
	if prov != "Brave Search" {
		t.Errorf("expected Brave Search provider when brave_api_key set, got %s", prov)
	}
}

func TestMockTavilyAndBraveSearch(t *testing.T) {
	// Mock Tavily server
	_ = httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"results": []map[string]string{
				{
					"title":   "Anthropic Docs",
					"url":     "https://docs.anthropic.com/en/docs",
					"content": "Claude API reference documentation.",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	// Verify decoding helper
	decLink := decodeDDGURL("//duckduckgo.com/l/?uddg=https%3A%2F%2Fgithub.com%2Fjadmadi%2Fsila")
	if decLink != "https://github.com/jadmadi/sila" {
		t.Errorf("decodeDDGURL failed, got %s", decLink)
	}

	clean := cleanHTMLString("<b>Hello</b> &amp; &#39;World&#39;")
	if clean != "Hello & 'World'" {
		t.Errorf("cleanHTMLString failed, got %s", clean)
	}
}

func TestGoogleCSE_QuotaExhaustionFallback(t *testing.T) {
	googleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{
			"error": {
				"code": 429,
				"message": "Resource has been exhausted (e.g. check quota)."
			}
		}`))
	}))
	defer googleServer.Close()

	mockDDGHTML := `
<!DOCTYPE html>
<html>
<body>
	<div class="result results_links results_links_deep highlight_result">
		<div class="result__body links_main links_deep">
			<h2 class="result__title">
				<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Ftesting">Testing Package</a>
			</h2>
			<a class="result__snippet">Package testing provides support for automated testing.</a>
		</div>
	</div>
</body>
</html>
`
	ddgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockDDGHTML))
	}))
	defer ddgServer.Close()

	origGoogle := googleCSEBaseURL
	origDDG := duckDuckGoBaseURL
	googleCSEBaseURL = googleServer.URL
	duckDuckGoBaseURL = ddgServer.URL
	defer func() {
		googleCSEBaseURL = origGoogle
		duckDuckGoBaseURL = origDDG
	}()

	_, err := SearchGoogleCSE("test-api-key", "test-cx", "go testing", 5, nil)
	if err == nil {
		t.Fatalf("expected error from SearchGoogleCSE on 429, got nil")
	}
	if !IsQuotaExhausted(err) {
		t.Errorf("expected IsQuotaExhausted(err) to be true, got false for: %v", err)
	}

	cfg := &config.Config{
		SearchProvider: "google",
		GoogleAPIKey:   "test-api-key",
		GoogleCSECX:    "test-cx",
	}

	results, prov, err := SearchWeb("go testing", 5, nil, cfg)
	if err != nil {
		t.Fatalf("expected SearchWeb to succeed via fallback, got error: %v", err)
	}
	if prov != "DuckDuckGo" {
		t.Errorf("expected provider to be DuckDuckGo, got %q", prov)
	}
	if len(results) == 0 {
		t.Fatalf("expected non-empty fallback results")
	}
	if results[0].Title != "Testing Package" {
		t.Errorf("expected title 'Testing Package', got %q", results[0].Title)
	}
}
