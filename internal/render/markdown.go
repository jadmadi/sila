package render

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	inlineCodeRE = regexp.MustCompile("`([^`]+)`")
	boldRE       = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	italicRE     = regexp.MustCompile(`\*([^*]+)\*`)
	linkRE       = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	strikeRE     = regexp.MustCompile(`~~([^~]+)~~`)
)

// RenderMarkdown renders markdown text to w with clean ANSI terminal formatting using ui.
// If ui has color disabled (non-TTY, piped, or --no-color), ANSI styling is omitted.
func RenderMarkdown(w io.Writer, markdown string, ui UI) error {
	rendered := RenderMarkdownString(markdown, ui)
	_, err := fmt.Fprint(w, rendered)
	return err
}

// RenderMarkdownString parses markdown text line by line and returns styled terminal output.
func RenderMarkdownString(markdown string, ui UI) string {
	if strings.TrimSpace(markdown) == "" {
		return ""
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(markdown))

	inCodeBlock := false
	codeBlockLang := ""
	prevBlank := false

	for scanner.Scan() {
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)

		// Code fence toggle
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if !inCodeBlock {
				inCodeBlock = true
				codeBlockLang = strings.TrimSpace(trimmed[3:])
				if codeBlockLang != "" {
					badge := "─ " + codeBlockLang + " "
					fillLen := 60 - len(badge)
					if fillLen < 4 {
						fillLen = 4
					}
					sb.WriteString("  ┌" + badge + strings.Repeat("─", fillLen) + "\n")
				} else {
					sb.WriteString("  ┌" + strings.Repeat("─", 60) + "\n")
				}
			} else {
				inCodeBlock = false
				codeBlockLang = ""
				sb.WriteString("  └" + strings.Repeat("─", 60) + "\n")
			}
			prevBlank = false
			continue
		}

		if inCodeBlock {
			// Inside code block: preserve exact whitespace and color with syntax tint
			if ui.enabled {
				sb.WriteString("  │ " + ui.Paint(Gold, rawLine) + "\n")
			} else {
				sb.WriteString("  │ " + rawLine + "\n")
			}
			prevBlank = false
			continue
		}

		// Blank line handling (collapse consecutive blank lines)
		if trimmed == "" {
			if !prevBlank {
				sb.WriteString("\n")
				prevBlank = true
			}
			continue
		}
		prevBlank = false

		// Horizontal rule: ---, ***, ___
		if isHorizontalRule(trimmed) {
			hr := strings.Repeat("─", 50)
			if ui.enabled {
				sb.WriteString(ui.Dim(hr) + "\n")
			} else {
				sb.WriteString(hr + "\n")
			}
			continue
		}

		// Blockquote: > text
		if after, ok := strings.CutPrefix(trimmed, ">"); ok {
			quoteContent := strings.TrimSpace(after)
			sb.WriteString(renderBlockquoteLine(quoteContent, ui) + "\n")
			continue
		}

		// Headings
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level < len(trimmed) && trimmed[level] == ' ' {
				headingText := strings.TrimSpace(trimmed[level:])
				sb.WriteString(renderHeading(level, headingText, ui) + "\n")
				continue
			}
		}

		// Unordered list item: - , * , +
		if isUnorderedList(rawLine) {
			indent, text := extractListIndent(rawLine)
			bullet := "•"
			if ui.enabled {
				bullet = ui.Cyan("•")
			}
			styledText := renderInline(text, ui)
			sb.WriteString(indent + bullet + " " + styledText + "\n")
			continue
		}

		// Ordered list item: 1. , 2. , etc.
		if isOrderedList(rawLine) {
			indent, numPrefix, text := extractOrderedListIndent(rawLine)
			styledPrefix := numPrefix
			if ui.enabled {
				styledPrefix = ui.Cyan(numPrefix)
			}
			styledText := renderInline(text, ui)
			sb.WriteString(indent + styledPrefix + " " + styledText + "\n")
			continue
		}

		// Standard paragraph text
		sb.WriteString(renderInline(rawLine, ui) + "\n")
	}

	return sb.String()
}

func isHorizontalRule(s string) bool {
	if len(s) < 3 {
		return false
	}
	r := rune(s[0])
	if r != '-' && r != '*' && r != '_' {
		return false
	}
	for _, ch := range s {
		if ch != r && ch != ' ' {
			return false
		}
	}
	return true
}

func renderHeading(level int, text string, ui UI) string {
	styled := renderInline(text, ui)
	switch level {
	case 1:
		if ui.enabled {
			return "\n" + ui.Cyan(ui.Bold(strings.ToUpper(styled))) + "\n" + ui.Dim(strings.Repeat("━", max(24, len(text)+4)))
		}
		return "\n" + strings.ToUpper(styled) + "\n" + strings.Repeat("=", max(24, len(text)+4))
	case 2:
		if ui.enabled {
			return "\n" + ui.Bold(ui.Cyan("## "+styled))
		}
		return "\n## " + styled
	case 3:
		if ui.enabled {
			return "\n" + ui.Bold("### "+styled)
		}
		return "\n### " + styled
	default:
		prefix := strings.Repeat("#", level)
		if ui.enabled {
			return ui.Bold(prefix + " " + styled)
		}
		return prefix + " " + styled
	}
}

func renderBlockquoteLine(content string, ui UI) string {
	// Alert banners: [!NOTE], [!WARNING], [!IMPORTANT], [!TIP], [!CAUTION]
	upper := strings.ToUpper(content)
	switch {
	case strings.HasPrefix(upper, "[!NOTE]"):
		rest := strings.TrimSpace(content[len("[!NOTE]"):])
		badge := "NOTE:"
		if ui.enabled {
			badge = ui.Cyan(ui.Bold("NOTE:"))
		}
		bar := "│ "
		if ui.enabled {
			bar = ui.Cyan("│ ")
		}
		if rest != "" {
			return bar + badge + " " + renderInline(rest, ui)
		}
		return bar + badge
	case strings.HasPrefix(upper, "[!TIP]"):
		rest := strings.TrimSpace(content[len("[!TIP]"):])
		badge := "TIP:"
		if ui.enabled {
			badge = ui.Green(ui.Bold("TIP:"))
		}
		bar := "│ "
		if ui.enabled {
			bar = ui.Green("│ ")
		}
		if rest != "" {
			return bar + badge + " " + renderInline(rest, ui)
		}
		return bar + badge
	case strings.HasPrefix(upper, "[!IMPORTANT]"):
		rest := strings.TrimSpace(content[len("[!IMPORTANT]"):])
		badge := "IMPORTANT:"
		if ui.enabled {
			badge = ui.Yellow(ui.Bold("IMPORTANT:"))
		}
		bar := "│ "
		if ui.enabled {
			bar = ui.Yellow("│ ")
		}
		if rest != "" {
			return bar + badge + " " + renderInline(rest, ui)
		}
		return bar + badge
	case strings.HasPrefix(upper, "[!WARNING]"):
		rest := strings.TrimSpace(content[len("[!WARNING]"):])
		badge := "WARNING:"
		if ui.enabled {
			badge = ui.Yellow(ui.Bold("WARNING:"))
		}
		bar := "│ "
		if ui.enabled {
			bar = ui.Yellow("│ ")
		}
		if rest != "" {
			return bar + badge + " " + renderInline(rest, ui)
		}
		return bar + badge
	case strings.HasPrefix(upper, "[!CAUTION]"):
		rest := strings.TrimSpace(content[len("[!CAUTION]"):])
		badge := "CAUTION:"
		if ui.enabled {
			badge = ui.Red(ui.Bold("CAUTION:"))
		}
		bar := "│ "
		if ui.enabled {
			bar = ui.Red("│ ")
		}
		if rest != "" {
			return bar + badge + " " + renderInline(rest, ui)
		}
		return bar + badge
	default:
		bar := "│ "
		if ui.enabled {
			bar = ui.Dim("│ ")
		}
		return bar + renderInline(content, ui)
	}
}

func isUnorderedList(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ")
}

func extractListIndent(line string) (string, string) {
	indentLen := len(line) - len(strings.TrimLeft(line, " \t"))
	indent := line[:indentLen]
	trimmed := strings.TrimLeft(line, " \t")
	return indent + "  ", trimmed[2:]
}

func isOrderedList(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	for i, ch := range trimmed {
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '.' && i > 0 && i+1 < len(trimmed) && trimmed[i+1] == ' ' {
			return true
		}
		break
	}
	return false
}

func extractOrderedListIndent(line string) (string, string, string) {
	indentLen := len(line) - len(strings.TrimLeft(line, " \t"))
	indent := line[:indentLen]
	trimmed := strings.TrimLeft(line, " \t")
	dotIdx := strings.Index(trimmed, ". ")
	if dotIdx == -1 {
		return indent + "  ", "", trimmed
	}
	numPrefix := trimmed[:dotIdx+1]
	text := trimmed[dotIdx+2:]
	return indent + "  ", numPrefix, text
}

// renderInline processes inline formatting: `code`, **bold**, *italic*, ~~strikethrough~~, [link](url)
func renderInline(text string, ui UI) string {
	if !ui.enabled {
		// Strip markdown syntax markers for clean plaintext terminal reading
		res := inlineCodeRE.ReplaceAllString(text, "`$1`")
		res = boldRE.ReplaceAllString(res, "$1")
		res = italicRE.ReplaceAllString(res, "$1")
		res = strikeRE.ReplaceAllString(res, "$1")
		res = linkRE.ReplaceAllString(res, "$1 ($2)")
		return res
	}

	// 1. Links: [text](url) -> text (url in Dim)
	res := linkRE.ReplaceAllStringFunc(text, func(m string) string {
		sub := linkRE.FindStringSubmatch(m)
		if len(sub) == 3 {
			if sub[1] == sub[2] {
				return ui.Dim(sub[1])
			}
			return ui.Bold(sub[1]) + " " + ui.Dim("("+sub[2]+")")
		}
		return m
	})

	// 2. Inline Code: `code`
	res = inlineCodeRE.ReplaceAllStringFunc(res, func(m string) string {
		sub := inlineCodeRE.FindStringSubmatch(m)
		if len(sub) == 2 {
			return ui.Paint(Gold, sub[1])
		}
		return m
	})

	// 3. Bold: **text**
	res = boldRE.ReplaceAllStringFunc(res, func(m string) string {
		sub := boldRE.FindStringSubmatch(m)
		if len(sub) == 2 {
			return ui.Bold(sub[1])
		}
		return m
	})

	// 4. Italic: *text*
	res = italicRE.ReplaceAllStringFunc(res, func(m string) string {
		sub := italicRE.FindStringSubmatch(m)
		if len(sub) == 2 {
			return ui.Paint("\x1b[3m", sub[1])
		}
		return m
	})

	// 5. Strikethrough: ~~text~~
	res = strikeRE.ReplaceAllStringFunc(res, func(m string) string {
		sub := strikeRE.FindStringSubmatch(m)
		if len(sub) == 2 {
			return ui.Paint("\x1b[9m", sub[1])
		}
		return m
	})

	return res
}
