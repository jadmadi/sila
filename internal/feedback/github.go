package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DefaultGitHubRepo is the canonical upstream repository for Sila feedback issues.
const DefaultGitHubRepo = "jadmadi/sila"

// DispatchResult records the outcome of a GitHub issue dispatch.
type DispatchResult struct {
	Method   string `json:"method"`    // "gh_cli" | "github_api" | "web_browser" | "web_url"
	IssueURL string `json:"issue_url"` // Created issue URL or pre-filled web URL
	Detail   string `json:"detail"`
}

var openBrowserCmd = func(targetURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", targetURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	default:
		cmd = exec.Command("xdg-open", targetURL)
	}
	return cmd.Start()
}

var execCommandContext = exec.CommandContext

// DispatchToGitHub executes the 3-tier fallback to submit an issue:
// Tier 1: `gh issue create` if the gh CLI is available and authenticated.
// Tier 2: GitHub REST API if GITHUB_TOKEN is present in the environment.
// Tier 3: Pre-filled Web URL (attempting to launch system browser if openBrowser is true, or returning the URL).
func DispatchToGitHub(ctx context.Context, ownerRepo, title, body string, labels []string, openBrowser bool) (*DispatchResult, error) {
	if ownerRepo == "" {
		ownerRepo = DefaultGitHubRepo
	}

	// 1. Tier 1: Try gh CLI
	if _, err := exec.LookPath("gh"); err == nil {
		args := []string{"issue", "create", "--repo", ownerRepo, "--title", title, "--body", body}
		if len(labels) > 0 {
			args = append(args, "--label", strings.Join(labels, ","))
		}
		cmd := execCommandContext(ctx, "gh", args...)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			issueURL := strings.TrimSpace(outBuf.String())
			if strings.HasPrefix(issueURL, "http") {
				return &DispatchResult{
					Method:   "gh_cli",
					IssueURL: issueURL,
					Detail:   "Created via GitHub CLI (`gh issue create`)",
				}, nil
			}
		}
	}

	// 2. Tier 2: Try GitHub REST API if GITHUB_TOKEN is available
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	if token != "" {
		apiURL := fmt.Sprintf("https://api.github.com/repos/%s/issues", ownerRepo)
		reqPayload := map[string]any{
			"title":  title,
			"body":   body,
			"labels": labels,
		}
		payloadBytes, _ := json.Marshal(reqPayload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payloadBytes))
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("User-Agent", "sila")
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusCreated {
					var ghResp struct {
						HTMLURL string `json:"html_url"`
					}
					if err := json.NewDecoder(resp.Body).Decode(&ghResp); err == nil && ghResp.HTMLURL != "" {
						return &DispatchResult{
							Method:   "github_api",
							IssueURL: ghResp.HTMLURL,
							Detail:   "Created via GitHub REST API",
						}, nil
					}
				}
			}
		}
	}

	// 3. Tier 3: Universal Pre-Filled Web URL Fallback
	webURL := GenerateGitHubIssueURL(ownerRepo, title, body, labels)
	if openBrowser {
		if err := openBrowserCmd(webURL); err == nil {
			return &DispatchResult{
				Method:   "web_browser",
				IssueURL: webURL,
				Detail:   "Pre-filled issue opened in system browser",
			}, nil
		}
	}

	return &DispatchResult{
		Method:   "web_url",
		IssueURL: webURL,
		Detail:   "Pre-filled issue URL generated",
	}, nil
}
