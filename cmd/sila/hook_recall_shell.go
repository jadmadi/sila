package main

import "strings"

// stripQuotedShellSpans removes single-quoted, double-quoted (backslash
// aware), and heredoc body spans so shell separators and redirections inside
// arguments cannot be mistaken for operators. The command line that opens a
// heredoc is kept, so an unquoted redirect on that line still classifies.
func stripQuotedShellSpans(command string) string {
	command = stripHeredocBodies(command)
	var b strings.Builder
	b.Grow(len(command))
	for i := 0; i < len(command); {
		switch command[i] {
		case '\'':
			j := i + 1
			for j < len(command) && command[j] != '\'' {
				j++
			}
			if j < len(command) {
				j++
			}
			b.WriteByte(' ')
			i = j
		case '"':
			j := i + 1
			for j < len(command) {
				if command[j] == '\\' && j+1 < len(command) {
					j += 2
					continue
				}
				if command[j] == '"' {
					j++
					break
				}
				j++
			}
			b.WriteByte(' ')
			i = j
		default:
			b.WriteByte(command[i])
			i++
		}
	}
	return b.String()
}

// stripHeredocBodies drops the body lines of << and <<- heredocs while keeping
// the opening command line. An unterminated heredoc consumes the rest.
func stripHeredocBodies(command string) string {
	lines := strings.Split(command, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		out = append(out, lines[i])
		marker, ok := heredocMarker(lines[i])
		if !ok {
			continue
		}
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != marker {
			i++
		}
		if i+1 < len(lines) {
			i++
		}
	}
	return strings.Join(out, "\n")
}

func heredocMarker(line string) (string, bool) {
	idx := strings.Index(line, "<<")
	if idx < 0 {
		return "", false
	}
	rest := line[idx+2:]
	if strings.HasPrefix(rest, "-") {
		rest = rest[1:]
	}
	rest = strings.TrimLeft(rest, " \t")
	if rest == "" {
		return "", false
	}
	end := 0
	for end < len(rest) && !strings.ContainsRune(" \t;&|<>", rune(rest[end])) {
		end++
	}
	marker := strings.Trim(rest[:end], `"'`)
	if marker == "" {
		return "", false
	}
	return marker, true
}
