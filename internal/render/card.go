package render

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

const (
	// DefaultTerminalWidth is used when terminal size detection is unavailable.
	DefaultTerminalWidth = 100
	// DefaultCardWidth is the standard outer width for terminal cards.
	DefaultCardWidth = 88
	// MinCardWidth is the minimum outer width to preserve readable column spacing.
	MinCardWidth = 72
	// MaxCardWidth is the maximum outer width to prevent line-length blowouts.
	MaxCardWidth = 96
)

// TerminalWidth returns the current terminal width in columns, or
// DefaultTerminalWidth. An explicit override or COLUMNS wins over ioctl.
func TerminalWidth() int {
	if widthOverride > 0 {
		return clampWidth(widthOverride)
	}
	if v := strings.TrimSpace(os.Getenv("COLUMNS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return clampWidth(n)
		}
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return clampWidth(w)
	}
	if w, _, err := term.GetSize(int(os.Stdin.Fd())); err == nil && w > 0 {
		return clampWidth(w)
	}
	return DefaultTerminalWidth
}

// BoundedCardWidth calculates a standardized, responsive card outer width
// that honors terminal boundaries while capping line lengths to standard bounds (72-96 cols).
func BoundedCardWidth(indent int) int {
	w := TerminalWidth()
	avail := w - indent*2
	if avail < MinCardWidth {
		return MinCardWidth
	}
	if avail > MaxCardWidth {
		return MaxCardWidth
	}
	return avail
}

// CardOptions specifies properties for rendering a rounded, themed card container.
type CardOptions struct {
	Title       string
	RightHeader string
	Lines       []string
	Width       int // Target outer width; if 0, fits content automatically.
	MaxWidth    int // Maximum outer width allowed; if 0, no max cap unless Width is set.
	Indent      int // Number of leading spaces to indent the entire card.
	UI          UI
	TitleColor  string // Optional ANSI code for title styling (defaults to UI Primary/Orange).
	BorderColor string // Optional ANSI code for border styling (defaults to UI Dim).
}

// RenderBox wraps content lines in a rounded border with an integrated title
// on the top left and optional header metadata on the top right.
//
// Example:
// ╭─ Title ────────────────────────────────────── Header ─╮
// │ content line                                           │
// ╰────────────────────────────────────────────────────────╯
func RenderBox(opts CardOptions) string {
	ui := opts.UI
	colors := ui.ColorEnabled()

	titlePlain := visibleStrip(opts.Title)
	rightPlain := visibleStrip(opts.RightHeader)
	titleWidth := visibleWidth(titlePlain)
	rightWidth := visibleWidth(rightPlain)

	// Determine inner width (excluding the "│ " and " │" side borders)
	maxContentWidth := 0
	for _, line := range opts.Lines {
		w := visibleWidth(line)
		if w > maxContentWidth {
			maxContentWidth = w
		}
	}

	minHeaderWidth := titleWidth + rightWidth + 4
	if opts.RightHeader != "" {
		minHeaderWidth += 2
	}

	innerWidth := maxContentWidth
	if minHeaderWidth > innerWidth {
		innerWidth = minHeaderWidth
	}
	if opts.Width > 4 {
		innerWidth = opts.Width - 4
		if minHeaderWidth > innerWidth {
			innerWidth = minHeaderWidth
		}
	} else if opts.MaxWidth > 4 && innerWidth > opts.MaxWidth-4 {
		innerWidth = opts.MaxWidth - 4
		if minHeaderWidth > innerWidth {
			innerWidth = minHeaderWidth
		}
	}

	lines := prepareCardLines(opts.Lines, innerWidth)

	indent := ""
	if opts.Indent > 0 {
		indent = strings.Repeat(" ", opts.Indent)
	}

	border := func(s string) string {
		if !colors {
			return s
		}
		if opts.BorderColor != "" {
			return opts.BorderColor + s + Reset
		}
		return ui.Dim(s)
	}

	titleStyled := opts.Title
	if colors {
		if opts.TitleColor != "" {
			titleStyled = opts.TitleColor + Bold + opts.Title + Reset
		} else {
			titleStyled = ui.Orange(ui.Bold(opts.Title))
		}
	}

	rightStyled := opts.RightHeader
	if colors && opts.RightHeader != "" {
		rightStyled = ui.Dim(opts.RightHeader)
	}

	var sb strings.Builder

	// Render Top Border
	if opts.RightHeader != "" {
		ruleSpaces := innerWidth - 4 - titleWidth - rightWidth
		if ruleSpaces < 1 {
			ruleSpaces = 1
		}
		sb.WriteString(indent + border("╭─ ") + titleStyled + " " + border(strings.Repeat("─", ruleSpaces)) + " " + rightStyled + border(" ─╮\n"))
	} else {
		ruleSpaces := innerWidth - 2 - titleWidth
		if ruleSpaces < 1 {
			ruleSpaces = 1
		}
		sb.WriteString(indent + border("╭─ ") + titleStyled + " " + border(strings.Repeat("─", ruleSpaces)) + border("─╮\n"))
	}

	// Render Body Lines
	for _, line := range lines {
		w := visibleWidth(line)
		pad := innerWidth - w
		if pad < 0 {
			pad = 0
		}
		sb.WriteString(indent + border("│ ") + line + strings.Repeat(" ", pad) + border(" │\n"))
	}

	// Render Bottom Border
	sb.WriteString(indent + border("╰"+strings.Repeat("─", innerWidth+2)+"╯\n"))

	return sb.String()
}

func visibleStrip(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// prepareCardLines ensures all lines fit within innerWidth without pushing out borders.
func prepareCardLines(lines []string, innerWidth int) []string {
	var out []string
	for _, line := range lines {
		stripped := visibleStrip(line)
		trimmed := strings.TrimSpace(stripped)

		// 1. Horizontal rule / divider: resize to fit innerWidth exactly
		if len(trimmed) > 0 && (strings.Trim(trimmed, "─") == "" || strings.Trim(trimmed, "-") == "") {
			leadingSpaces := len(stripped) - len(strings.TrimLeft(stripped, " "))
			ruleLen := innerWidth - leadingSpaces
			if ruleLen < 1 {
				ruleLen = 1
			}
			char := "─"
			if strings.Contains(trimmed, "-") {
				char = "-"
			}
			res := strings.Repeat(" ", leadingSpaces) + strings.Repeat(char, ruleLen)
			out = append(out, res)
			continue
		}

		w := visibleWidth(line)
		if w <= innerWidth {
			out = append(out, line)
			continue
		}

		// 2. Bullet point line
		if bulletIdx := strings.Index(line, "• "); bulletIdx != -1 {
			prefix := line[:bulletIdx+len("• ")]
			text := strings.TrimSpace(line[bulletIdx+len("• "):])
			wrapped := WrapBullet(prefix, text, innerWidth)
			out = append(out, wrapped...)
			continue
		}

		// 3. Generic text line: wrap preserving leading indent
		leadingSpaces := len(stripped) - len(strings.TrimLeft(stripped, " "))
		prefix := ""
		if leadingSpaces > 0 && leadingSpaces < len(line) {
			prefix = line[:leadingSpaces]
		}
		text := strings.TrimSpace(line)
		wrapped := WrapBullet(prefix, text, innerWidth)
		out = append(out, wrapped...)
	}
	return out
}

// WrapBullet wraps a bullet item with hanging indentation.
// The first line starts with prefix, and subsequent lines start with
// continuation spaces matching the display width of prefix.
// Optional styleFn is applied to the wrapped text of each line.
func WrapBullet(prefix, text string, limit int, styleFn ...func(string) string) []string {
	prefixWidth := visibleWidth(prefix)
	avail := limit - prefixWidth
	if avail < 12 {
		avail = 12
	}

	continuationIndent := strings.Repeat(" ", prefixWidth)
	words := WrapText(text, avail)
	if len(words) == 0 {
		return []string{prefix}
	}

	fn := func(s string) string { return s }
	if len(styleFn) > 0 && styleFn[0] != nil {
		fn = styleFn[0]
	}

	var result []string
	for i, w := range words {
		if i == 0 {
			result = append(result, prefix+fn(w))
		} else {
			result = append(result, continuationIndent+fn(w))
		}
	}
	return result
}

// WrapText wraps text into lines that do not exceed limit display cells.
// It wraps strictly at whitespace boundaries.
func WrapText(s string, limit int) []string {
	if limit <= 0 {
		return []string{s}
	}
	if visibleWidth(s) <= limit && !strings.Contains(s, "\n") {
		return []string{s}
	}

	var result []string
	paragraphs := strings.Split(s, "\n")
	for _, p := range paragraphs {
		words := strings.Fields(p)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}

		var curLine strings.Builder
		curWidth := 0

		for _, word := range words {
			wWidth := visibleWidth(word)

			if curWidth == 0 {
				curLine.WriteString(word)
				curWidth = wWidth
			} else if curWidth+1+wWidth <= limit {
				curLine.WriteString(" ")
				curLine.WriteString(word)
				curWidth += 1 + wWidth
			} else {
				result = append(result, curLine.String())
				curLine.Reset()
				curLine.WriteString(word)
				curWidth = wWidth
			}
		}
		if curWidth > 0 {
			result = append(result, curLine.String())
		}
	}
	return result
}
