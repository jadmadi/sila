package feedback

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
	"github.com/jadmadi/sila/internal/version"
)

// NormalizeCategory standardizes user or agent category input.
func NormalizeCategory(cat string) string {
	cat = strings.ToLower(strings.TrimSpace(cat))
	switch cat {
	case "idea", "suggestion", "feature", "rec", "recommendation":
		return "idea"
	case "issue", "bug", "pain", "painpoint", "problem", "error":
		return "issue"
	case "tip", "best-practice", "advice", "hint":
		return "tip"
	case "challenge", "friction", "blocker", "difficulty":
		return "challenge"
	case "praise", "kudos", "thanks", "thankyou", "love":
		return "praise"
	default:
		if cat == "" {
			return "idea"
		}
		return cat
	}
}

// CategoryBadge renders an emoji and stylized badge for a category.
func CategoryBadge(ui render.UI, cat string) string {
	switch cat {
	case "idea":
		return "💡 " + ui.Cyan(ui.Bold("IDEA / SUGGESTION"))
	case "issue":
		return "⚠️  " + ui.Red(ui.Bold("ISSUE / PAIN POINT"))
	case "tip":
		return "✨ " + ui.Green(ui.Bold("TIP / BEST PRACTICE"))
	case "challenge":
		return "🥊 " + ui.Yellow(ui.Bold("CHALLENGE / FRICTION"))
	case "praise":
		return "💖 " + ui.Magenta(ui.Bold("PRAISE / APPRECIATION"))
	default:
		return "📝 " + ui.Bold(strings.ToUpper(cat))
	}
}

// StatusBadge renders a colored status indicator.
func StatusBadge(ui render.UI, status string) string {
	switch strings.ToLower(status) {
	case "open", "":
		return "💡 " + ui.Green(ui.Bold("[OPEN]"))
	case "in_progress":
		return "🚧 " + ui.Yellow(ui.Bold("[IN PROGRESS]"))
	case "closed":
		return "✅ " + ui.Dim(ui.Bold("[CLOSED]"))
	case "wontfix":
		return "⏹️  " + ui.Dim(ui.Bold("[WONTFIX]"))
	default:
		return "[" + strings.ToUpper(status) + "]"
	}
}

// PrintFeedbackLedger renders the formatted feedback history.
func PrintFeedbackLedger(ui render.UI, items []store.Feedback, stats map[string]int, statusStats map[string]int, filterStatus ...string) {
	titleSuffix := ""
	activeFilter := ""
	if len(filterStatus) > 0 && filterStatus[0] != "" {
		activeFilter = strings.ToLower(filterStatus[0])
		titleSuffix = " (" + strings.ToUpper(activeFilter[:1]) + activeFilter[1:] + " Only)"
	}
	fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · feedback"))+" — Agent Experience & Continuous Improvement Ledger"+titleSuffix)

	openCount := statusStats["open"]
	closedCount := statusStats["closed"]
	if activeFilter != "" {
		fmt.Fprintf(os.Stdout, "  Total Records: %d displayed (%s · %s)\n", len(items), ui.Green(fmt.Sprintf("%d open", openCount)), ui.Dim(fmt.Sprintf("%d closed", closedCount)))
	} else {
		fmt.Fprintf(os.Stdout, "  Total Records: %d (%s · %s)\n", len(items), ui.Green(fmt.Sprintf("%d open", openCount)), ui.Dim(fmt.Sprintf("%d closed", closedCount)))
	}

	var statParts []string
	if count, ok := stats["idea"]; ok && count > 0 {
		statParts = append(statParts, fmt.Sprintf("💡 Ideas: %s", ui.Cyan(fmt.Sprint(count))))
	}
	if count, ok := stats["issue"]; ok && count > 0 {
		statParts = append(statParts, fmt.Sprintf("⚠️ Issues: %s", ui.Red(fmt.Sprint(count))))
	}
	if count, ok := stats["tip"]; ok && count > 0 {
		statParts = append(statParts, fmt.Sprintf("✨ Tips: %s", ui.Green(fmt.Sprint(count))))
	}
	if count, ok := stats["challenge"]; ok && count > 0 {
		statParts = append(statParts, fmt.Sprintf("🥊 Challenges: %s", ui.Yellow(fmt.Sprint(count))))
	}
	if count, ok := stats["praise"]; ok && count > 0 {
		statParts = append(statParts, fmt.Sprintf("💖 Praise: %s", ui.Magenta(fmt.Sprint(count))))
	}

	if len(statParts) > 0 {
		fmt.Fprintf(os.Stdout, "  Breakdown:     %s\n", strings.Join(statParts, " · "))
	}
	fmt.Fprintln(os.Stdout, ui.Dim("================================================================================"))
	fmt.Fprintln(os.Stdout)

	if len(items) == 0 {
		if activeFilter != "" {
			fmt.Fprintf(os.Stdout, "%s\n", ui.Dim(fmt.Sprintf("No %s feedback records found. Agents can submit feedback via `sila feedback <msg>` or MCP `record_feedback`.", activeFilter)))
		} else {
			fmt.Fprintln(os.Stdout, ui.Dim("No feedback records found. Agents can submit feedback via `sila feedback <msg>` or MCP `record_feedback`."))
		}
		return
	}

	for _, item := range items {
		statusBadge := StatusBadge(ui, item.Status)
		catBadge := CategoryBadge(ui, item.Category)
		meta := []string{item.CreatedAt}
		if item.Agent != "" {
			meta = append(meta, "agent: "+ui.Bold(item.Agent))
		}
		if item.ProjectID != "" {
			meta = append(meta, "project: "+ui.Bold(item.ProjectID))
		}

		fmt.Fprintf(os.Stdout, "%s %s  %s  %s\n", statusBadge, catBadge, ui.Dim(item.ID), ui.Dim("("+strings.Join(meta, " · ")+")"))
		if item.Status == "closed" && (item.ResolvedAt != "" || item.ResolvedGoal != "" || item.ResolutionNote != "") {
			resParts := []string{}
			if item.ResolvedAt != "" {
				resParts = append(resParts, item.ResolvedAt)
			}
			if item.ResolvedBy != "" {
				resParts = append(resParts, "by "+ui.Bold(item.ResolvedBy))
			}
			if item.ResolvedGoal != "" {
				resParts = append(resParts, "goal: "+ui.Cyan(item.ResolvedGoal))
			}
			fmt.Fprintf(os.Stdout, "  %s %s\n", ui.Green("✔ Resolved:"), strings.Join(resParts, " · "))
			if item.ResolutionNote != "" {
				fmt.Fprintf(os.Stdout, "    %s\n", ui.Dim(item.ResolutionNote))
			}
		}
		// Indent content
		lines := strings.SplitSeq(item.Content, "\n")
		for line := range lines {
			fmt.Fprintf(os.Stdout, "  %s\n", line)
		}
		fmt.Fprintln(os.Stdout, ui.Dim("--------------------------------------------------------------------------------"))
	}
}

var (
	// Redaction patterns
	homeUnixRE    = regexp.MustCompile(`/(?:home|Users)/[a-zA-Z0-9_\.\-]+`)
	homeWindowsRE = regexp.MustCompile(`[a-zA-Z]:\\(?:[Uu]sers)\\[a-zA-Z0-9_\.\-]+`)
	ghTokenRE     = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[a-zA-Z0-9]{36}\b|\bgithub_pat_[a-zA-Z0-9_]{82}\b`)
	bearerRE      = regexp.MustCompile(`(?i)\b(Bearer\s+)[a-zA-Z0-9_\-\.]{20,}\b`)
	apiKeyRE      = regexp.MustCompile(`\b(?:sk-[a-zA-Z0-9]{20,})\b`)
	pemKeyRE      = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)
	paramSecretRE = regexp.MustCompile(`(?i)(password|token|secret|api_key|client_secret)=([^&\s]+)`)
)

// SanitizeForUpstream strips local usernames, home directory paths, API tokens,
// and private keys from feedback text before outbound transmission.
func SanitizeForUpstream(text string) string {
	if text == "" {
		return ""
	}

	// 1. Scrub actual user home directory if known
	if h, err := os.UserHomeDir(); err == nil && h != "" && h != "/" {
		text = strings.ReplaceAll(text, h, "~")
	}

	// 2. Scrub other potential user home paths
	text = homeUnixRE.ReplaceAllString(text, "~")
	text = homeWindowsRE.ReplaceAllString(text, "~")

	// 3. Scrub secrets & tokens
	text = pemKeyRE.ReplaceAllString(text, "[REDACTED_PRIVATE_KEY]")
	text = ghTokenRE.ReplaceAllString(text, "[REDACTED_GH_TOKEN]")
	text = bearerRE.ReplaceAllString(text, "$1[REDACTED]")
	text = apiKeyRE.ReplaceAllString(text, "[REDACTED_API_KEY]")
	text = paramSecretRE.ReplaceAllString(text, "$1=[REDACTED]")

	return text
}

// FormatGitHubIssue builds a structured title, body, and labels for a GitHub issue.
func FormatGitHubIssue(category, content, project string) (title, body string, labels []string) {
	cat := NormalizeCategory(category)
	catTitle := strings.ToUpper(cat[:1]) + cat[1:]

	cleanContent := SanitizeForUpstream(content)
	lines := strings.Split(strings.TrimSpace(cleanContent), "\n")
	firstLine := ""
	if len(lines) > 0 {
		firstLine = strings.TrimSpace(lines[0])
	}
	if len(firstLine) > 70 {
		firstLine = firstLine[:67] + "..."
	}
	if firstLine == "" {
		firstLine = "Sila Feedback"
	}

	title = fmt.Sprintf("[%s] %s", catTitle, firstLine)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Feedback Category\n`%s`\n\n", cat))
	sb.WriteString("### Description\n")
	sb.WriteString(cleanContent)
	sb.WriteString("\n\n---\n")
	sb.WriteString("**Environment Metadata** (sanitized, zero PII):\n")
	sb.WriteString(fmt.Sprintf("- **Sila Version**: `%s`\n", version.Version))
	sb.WriteString(fmt.Sprintf("- **Platform**: `%s/%s`\n", runtime.GOOS, runtime.GOARCH))
	if project != "" && !strings.Contains(project, "/") && !strings.Contains(project, "\\") {
		sb.WriteString(fmt.Sprintf("- **Project**: `%s`\n", project))
	}
	sb.WriteString("- *Submitted via Sila CLI (`sila feedback --issue`)*\n")

	body = sb.String()

	switch cat {
	case "idea":
		labels = []string{"enhancement", "feedback"}
	case "issue":
		labels = []string{"bug", "feedback"}
	case "tip":
		labels = []string{"documentation", "feedback"}
	case "challenge":
		labels = []string{"friction", "feedback"}
	case "praise":
		labels = []string{"feedback"}
	default:
		labels = []string{"feedback"}
	}

	return title, body, labels
}

// GenerateGitHubIssueURL formats a pre-filled GitHub issue creation URL.
func GenerateGitHubIssueURL(ownerRepo, title, body string, labels []string) string {
	if ownerRepo == "" {
		ownerRepo = "jadmadi/sila"
	}
	q := url.Values{}
	q.Set("title", title)
	q.Set("body", body)
	if len(labels) > 0 {
		q.Set("labels", strings.Join(labels, ","))
	}
	return fmt.Sprintf("https://github.com/%s/issues/new?%s", ownerRepo, q.Encode())
}
