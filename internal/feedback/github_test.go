package feedback

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestSanitizeForUpstream(t *testing.T) {
	// Set mock HOME
	origHome := os.Getenv("HOME")
	defer os.Setenv("HOME", origHome)

	testToken := "ghp_" + strings.Repeat("a", 36)
	testSK := "sk-" + strings.Repeat("b", 24)
	input := "Crash occurred at /home/developer/src/app/main.go with token " + testToken + " and api_key=secretValue123 and " + testSK
	sanitized := SanitizeForUpstream(input)

	if strings.Contains(sanitized, "developer") {
		t.Errorf("expected username/home to be redacted, got: %s", sanitized)
	}
	if strings.Contains(sanitized, testToken) {
		t.Errorf("expected GitHub token to be redacted, got: %s", sanitized)
	}
	if strings.Contains(sanitized, "secretValue123") {
		t.Errorf("expected api_key parameter to be redacted, got: %s", sanitized)
	}
	if strings.Contains(sanitized, testSK) {
		t.Errorf("expected sk- key to be redacted, got: %s", sanitized)
	}
}

func TestFormatGitHubIssue(t *testing.T) {
	title, body, labels := FormatGitHubIssue("issue", "Error occurred while running sila sync\nDetails here...", "myproject")

	if !strings.HasPrefix(title, "[Issue]") {
		t.Errorf("expected title to start with [Issue], got: %s", title)
	}
	if !strings.Contains(body, "### Feedback Category") {
		t.Errorf("expected body to contain Feedback Category header, got: %s", body)
	}
	if !strings.Contains(body, "Environment Metadata") {
		t.Errorf("expected body to contain Environment Metadata, got: %s", body)
	}
	if len(labels) == 0 || labels[0] != "bug" {
		t.Errorf("expected labels to start with bug, got: %v", labels)
	}
}

func TestGenerateGitHubIssueURL(t *testing.T) {
	url := GenerateGitHubIssueURL("jadmadi/sila", "Title Here", "Body Here", []string{"bug", "feedback"})
	if !strings.HasPrefix(url, "https://github.com/jadmadi/sila/issues/new?") {
		t.Errorf("expected url to start with GitHub issues new, got: %s", url)
	}
	if !strings.Contains(url, "title=Title+Here") {
		t.Errorf("expected encoded title, got: %s", url)
	}
}

func TestDispatchToGitHub_WebFallback(t *testing.T) {
	// Isolate browser open command to avoid launching real window during test
	origBrowser := openBrowserCmd
	defer func() { openBrowserCmd = origBrowser }()

	browserCalled := false
	openBrowserCmd = func(url string) error {
		browserCalled = true
		return nil
	}

	ctx := context.Background()
	res, err := DispatchToGitHub(ctx, "jadmadi/sila", "Test", "Body", []string{"feedback"}, true)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if res.IssueURL == "" {
		t.Fatalf("expected non-empty IssueURL")
	}
	// Even if gh CLI is present or absent, it should produce a valid result
	if !strings.HasPrefix(res.IssueURL, "https://github.com/jadmadi/sila") {
		t.Errorf("expected GitHub url, got: %s", res.IssueURL)
	}
	t.Logf("Dispatch method: %s, URL: %s, Browser called: %v", res.Method, res.IssueURL, browserCalled)
}
