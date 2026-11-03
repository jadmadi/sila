package ingest

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

// StackOverflowResult represents a technical solution thread from Stack Overflow.
type StackOverflowResult struct {
	QuestionID int64    `json:"question_id"`
	Title      string   `json:"title"`
	Link       string   `json:"link"`
	Score      int      `json:"score"`
	IsAnswered bool     `json:"is_answered"`
	Tags       []string `json:"tags"`
	Snippet    string   `json:"snippet,omitempty"`
	Body       string   `json:"body,omitempty"`
	Answer     string   `json:"answer,omitempty"`
}

// SearchStackOverflow searches developer QA and error resolutions on Stack Overflow.
func SearchStackOverflow(query string, tag string, top int, cfg *config.Config) ([]StackOverflowResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query cannot be empty")
	}
	if top <= 0 {
		top = 5
	}
	if top > 10 {
		top = 10
	}

	endpoint := fmt.Sprintf("https://api.stackexchange.com/2.3/search/advanced?order=desc&sort=relevance&q=%s&site=stackoverflow&filter=withbody&pagesize=%d",
		url.QueryEscape(query), top)
	if tag != "" {
		endpoint += "&tagged=" + url.QueryEscape(tag)
	}
	if cfg != nil && cfg.StackOverflowKey != "" {
		endpoint += "&key=" + url.QueryEscape(cfg.StackOverflowKey)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stack overflow search request failed: %w", err)
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if strings.Contains(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err == nil {
			defer gz.Close()
			reader = gz
		}
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(reader, 1024))
		return nil, fmt.Errorf("stack overflow returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var soResp struct {
		Items []struct {
			QuestionID  int64    `json:"question_id"`
			Title       string   `json:"title"`
			Link        string   `json:"link"`
			Score       int      `json:"score"`
			IsAnswered  bool     `json:"is_answered"`
			Tags        []string `json:"tags"`
			Body        string   `json:"body"`
			AcceptedID  int64    `json:"accepted_answer_id"`
			AnswerCount int      `json:"answer_count"`
		} `json:"items"`
		QuotaRemaining int `json:"quota_remaining"`
	}

	if err := json.NewDecoder(reader).Decode(&soResp); err != nil {
		return nil, fmt.Errorf("failed to decode stack overflow response: %w", err)
	}

	var results []StackOverflowResult
	for _, item := range soResp.Items {
		_, mdBody := HTMLToMarkdown(item.Body)
		snippet := cleanHTMLString(item.Body)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}

		results = append(results, StackOverflowResult{
			QuestionID: item.QuestionID,
			Title:      cleanHTMLString(item.Title),
			Link:       item.Link,
			Score:      item.Score,
			IsAnswered: item.IsAnswered,
			Tags:       item.Tags,
			Snippet:    snippet,
			Body:       mdBody,
		})
		if top > 0 && len(results) >= top {
			break
		}
	}

	return results, nil
}

// IngestStackOverflowResults saves top Stack Overflow troubleshooting threads to the project knowledge store.
func IngestStackOverflowResults(st *store.Store, results []StackOverflowResult, projectID string) ([]*store.Doc, error) {
	if st == nil || len(results) == 0 {
		return nil, nil
	}

	var docs []*store.Doc
	for _, r := range results {
		content := fmt.Sprintf("# [Stack Overflow] %s\n\n**Source**: %s\n**Score**: %d | **Answered**: %v | **Tags**: %s\n\n## Question\n%s\n",
			r.Title, r.Link, r.Score, r.IsAnswered, strings.Join(r.Tags, ", "), r.Body)

		target := fmt.Sprintf("so:%d", r.QuestionID)
		d := store.Doc{
			ID:        store.DocID(projectID, target),
			ProjectID: projectID,
			Title:     fmt.Sprintf("[SO #%d] %s", r.QuestionID, r.Title),
			URL:       r.Link,
			Content:   content,
			Format:    "markdown",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		}

		if err := st.InsertDoc(d); err == nil {
			docs = append(docs, &d)
		}
	}

	return docs, nil
}
