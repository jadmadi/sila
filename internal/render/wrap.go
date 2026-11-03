package render

import "strings"

var widthOverride int

// SetWidthOverride sets an explicit terminal width (0 means auto).
func SetWidthOverride(width int) { widthOverride = width }

// clampWidth bounds a resolved width to a readable range.
func clampWidth(width int) int {
	if width < 40 {
		return 40
	}
	if width > 200 {
		return 200
	}
	return width
}

// VisibleWidth counts printable columns, ignoring ANSI sequences and treating
// wide CJK/emoji runes as two columns.
func VisibleWidth(s string) int {
	s = ansiRE.ReplaceAllString(s, "")
	width := 0
	for _, r := range s {
		width += runeWidth(r)
	}
	return width
}

func runeWidth(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1FAFF:
		return 2
	}
	return 1
}

// Wrap hard-wraps each line at width columns, preserving leading indentation
// and never splitting a single long token (commands and URLs stay intact).
func WrapAt(text string, width int) string {
	if width <= 0 {
		width = TerminalWidth()
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = wrapLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func wrapLine(line string, width int) string {
	if VisibleWidth(line) <= width {
		return line
	}
	prefixLen := 0
	for prefixLen < len(line) && (line[prefixLen] == ' ' || line[prefixLen] == '\t') {
		prefixLen++
	}
	prefix := line[:prefixLen]
	words := strings.Fields(line[prefixLen:])
	if len(words) == 0 {
		return line
	}

	var out []string
	cur := prefix
	curHasWord := false
	for _, word := range words {
		candidate := prefix + word
		if curHasWord {
			candidate = cur + " " + word
		}
		if curHasWord && VisibleWidth(candidate) > width {
			out = append(out, cur)
			cur = prefix + word
			continue
		}
		cur = candidate
		curHasWord = true
	}
	if curHasWord {
		out = append(out, cur)
	}
	return strings.Join(out, "\n")
}
