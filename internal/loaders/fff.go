package loaders

import (
	"path/filepath"
	"strings"
	"unicode"
)

// FFFToolNames are the workspace-search tool names the fff ecosystem exposes.
// Sila recognizes them so transcripts that use fff are attributed and their
// file hints can ground distilled claims.
var FFFToolNames = map[string]bool{
	"fffind":         true,
	"ffgrep":         true,
	"fff-multi-grep": true,
	"fff-search":     true,
	"fff-grep":       true,
	"fff":            true,
	"fff-mcp":        true,
}

// IsFFFTool reports whether a tool name belongs to the fff search family.
func IsFFFTool(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "_", "-")
	if FFFToolNames[n] {
		return true
	}
	return strings.HasPrefix(n, "fff-")
}

// NormalizeFFFTool maps every fff variant to the canonical "fff" attribution.
func NormalizeFFFTool(name string) string {
	if IsFFFTool(name) {
		return "fff"
	}
	return name
}

// ExtractFFFInvocation scans text for fff tool invocations and returns the
// first query (a quoted argument or the leading free text) plus every
// path-like argument. It is deliberately conservative: a bare word is never
// treated as a path.
func ExtractFFFInvocation(content string) (query string, paths []string) {
	seen := map[string]bool{}
	for line := range strings.SplitSeq(content, "\n") {
		tool, rest, ok := splitFFFCall(line)
		if !ok || tool == "" {
			continue
		}
		tokens := tokenizeInvocation(rest)
		for i, tok := range tokens {
			if isPathLike(tok) {
				clean := strings.Trim(tok, `"'`+"`")
				clean = strings.TrimRight(clean, ".,;:!?")
				if clean != "" && !seen[clean] {
					seen[clean] = true
					paths = append(paths, clean)
				}
				continue
			}
			if query == "" && i == 0 {
				query = strings.Trim(tok, `"'`+"`")
			}
		}
	}
	return query, paths
}

// ExtractFFFToolPaths returns only the path-like arguments of fff invocations.
func ExtractFFFToolPaths(content string) []string {
	_, paths := ExtractFFFInvocation(content)
	return paths
}

// splitFFFCall recognizes a line that starts with an fff tool name, allowing
// common transcript prefixes such as "- ", "* ", "> ", or "tool: ".
func splitFFFCall(line string) (tool, rest string, ok bool) {
	s := strings.TrimSpace(line)
	s = strings.TrimLeft(s, "-*>")
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, prefix := range []string{"tool:", "call:", "invoke:"} {
		if strings.HasPrefix(lower, prefix) {
			s = strings.TrimSpace(s[len(prefix):])
			lower = strings.ToLower(s)
			break
		}
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", "", false
	}
	name := strings.TrimSuffix(fields[0], ":")
	if !IsFFFTool(name) {
		return "", "", false
	}
	rest = strings.TrimSpace(strings.TrimPrefix(s, fields[0]))
	return name, rest, true
}

// tokenizeInvocation splits an argument string on whitespace while keeping
// quoted segments together.
func tokenizeInvocation(s string) []string {
	var tokens []string
	var current strings.Builder
	var quote rune
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				flush()
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'' || r == '`':
			flush()
			quote = r
		case unicode.IsSpace(r):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// isPathLike reports whether a token looks like a file or directory path.
func isPathLike(tok string) bool {
	tok = strings.Trim(tok, `"'`+"`")
	if tok == "" || strings.HasPrefix(tok, "-") {
		return false
	}
	if strings.ContainsAny(tok, "/\\") {
		return true
	}
	switch strings.ToLower(filepath.Ext(tok)) {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".md", ".json", ".yaml", ".yml", ".toml", ".sql", ".py", ".rs":
		return true
	}
	return false
}
