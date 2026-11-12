package skills

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ParsedFrontmatter holds extracted skill YAML metadata and body.
type ParsedFrontmatter struct {
	Name          string
	Description   string
	License       string
	Tags          []string
	Compatibility []string
	Body          string
}

// ParseSkillContent parses YAML frontmatter and markdown body from SKILL.md.
func ParseSkillContent(slug, content string) ParsedFrontmatter {
	pf := ParsedFrontmatter{
		Name:          slug,
		Tags:          make([]string, 0),
		Compatibility: make([]string, 0),
	}

	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return pf
	}

	// 1. Check for opening delimiter ---
	firstIdx := -1
	for i, l := range lines {
		trimmed := strings.TrimSpace(strings.TrimPrefix(l, "\xef\xbb\xbf")) // Strip BOM
		if trimmed == "" {
			continue
		}
		if trimmed == "---" {
			firstIdx = i
		}
		break
	}

	if firstIdx == -1 {
		pf.Body = content
		return pf
	}

	// 2. Scan frontmatter lines
	inFrontmatter := true
	bodyStart := -1

	i := firstIdx + 1
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" || trimmed == "..." {
			inFrontmatter = false
			bodyStart = i + 1
			break
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}

		key := strings.ToLower(strings.TrimSpace(line[:colonIdx]))
		valPart := strings.TrimSpace(line[colonIdx+1:])

		// Check for block scalar: > or |
		if strings.HasPrefix(valPart, ">") || strings.HasPrefix(valPart, "|") {
			isFolded := strings.HasPrefix(valPart, ">")
			chomp := "clip"
			indicator := valPart[1:]
			if strings.HasPrefix(indicator, "-") {
				chomp = "strip"
			} else if strings.HasPrefix(indicator, "+") {
				chomp = "keep"
			}

			// Gather indented lines
			var blockLines []string
			baseIndent := -1
			j := i + 1
			for j < len(lines) {
				bLine := lines[j]
				bTrim := strings.TrimSpace(bLine)
				if bTrim == "---" || bTrim == "..." {
					break
				}
				if bTrim == "" {
					if baseIndent != -1 {
						blockLines = append(blockLines, "")
					}
					j++
					continue
				}

				indent := len(bLine) - len(strings.TrimLeft(bLine, " "))
				if baseIndent == -1 {
					if indent == 0 {
						break
					}
					baseIndent = indent
				} else if indent < baseIndent {
					break
				}

				if len(bLine) >= baseIndent {
					blockLines = append(blockLines, bLine[baseIndent:])
				} else {
					blockLines = append(blockLines, strings.TrimLeft(bLine, " "))
				}
				j++
			}

			var scalarVal string
			if isFolded {
				scalarVal = foldLines(blockLines, chomp)
			} else {
				scalarVal = literalLines(blockLines, chomp)
			}

			if key == "description" {
				pf.Description = scalarVal
			}
			i = j
			continue
		}

		// Check for flow list [a, b]
		if strings.HasPrefix(valPart, "[") && strings.HasSuffix(valPart, "]") {
			items := parseFlowList(valPart)
			if key == "tags" {
				pf.Tags = append(pf.Tags, items...)
			} else if key == "compatibility" {
				pf.Compatibility = append(pf.Compatibility, items...)
			}
			i++
			continue
		}

		// Check for block list
		if valPart == "" || strings.HasPrefix(valPart, "#") {
			var listItems []string
			j := i + 1
			for j < len(lines) {
				lLine := lines[j]
				lTrim := strings.TrimSpace(lLine)
				if lTrim == "---" || lTrim == "..." {
					break
				}
				if lTrim == "" {
					j++
					continue
				}
				if strings.HasPrefix(lTrim, "- ") || strings.HasPrefix(lTrim, "* ") {
					item := strings.TrimSpace(lTrim[2:])
					item = cleanQuotes(item)
					listItems = append(listItems, item)
					j++
				} else {
					break
				}
			}
			if len(listItems) > 0 {
				if key == "tags" {
					pf.Tags = append(pf.Tags, listItems...)
				} else if key == "compatibility" {
					pf.Compatibility = append(pf.Compatibility, listItems...)
				}
				i = j
				continue
			}
		}

		// Standard single-line scalar
		cleanedVal := cleanScalar(valPart)
		switch key {
		case "name":
			if cleanedVal != "" {
				pf.Name = cleanedVal
			}
		case "description":
			pf.Description = cleanedVal
		case "license":
			pf.License = cleanedVal
		}

		i++
	}

	_ = inFrontmatter
	if bodyStart != -1 && bodyStart < len(lines) {
		pf.Body = strings.Join(lines[bodyStart:], "\n")
	}

	return pf
}

func foldLines(lines []string, chomp string) string {
	var b strings.Builder
	for i, line := range lines {
		if i == 0 {
			b.WriteString(line)
			continue
		}
		if line == "" {
			b.WriteString("\n")
		} else {
			if i > 0 && lines[i-1] == "" {
				b.WriteString(line)
			} else {
				b.WriteString(" " + line)
			}
		}
	}
	res := b.String()
	if chomp == "strip" || chomp == "clip" {
		res = strings.TrimRight(res, "\r\n ")
	}
	return res
}

func literalLines(lines []string, chomp string) string {
	res := strings.Join(lines, "\n")
	if chomp == "strip" || chomp == "clip" {
		res = strings.TrimRight(res, "\r\n ")
	}
	return res
}

func cleanQuotes(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted
		}
		return strings.Trim(s, `"`)
	}
	if strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`) && len(s) >= 2 {
		val := s[1 : len(s)-1]
		return strings.ReplaceAll(val, "''", "'")
	}
	return s
}

func cleanScalar(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if unquoted, ok := unquoteScalarWithComment(s); ok {
		return unquoted
	}
	if idx := strings.Index(s, " #"); idx != -1 {
		s = strings.TrimSpace(s[:idx])
	}
	return cleanQuotes(s)
}

// unquoteScalarWithComment unquotes a leading quoted scalar and ignores any
// comment after the closing quote. It reports false when no closing quote is
// found, so the caller can fall back to unquoted comment stripping.
func unquoteScalarWithComment(s string) (string, bool) {
	if len(s) < 2 {
		return "", false
	}
	switch s[0] {
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) {
				b.WriteByte(s[i+1])
				i++
				continue
			}
			if s[i] == '"' {
				return b.String(), true
			}
			b.WriteByte(s[i])
		}
	case '\'':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				return b.String(), true
			}
			b.WriteByte(s[i])
		}
	}
	return "", false
}

func parseFlowList(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		item := cleanQuotes(strings.TrimSpace(p))
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

// ParseSkillSummaryFile reads only the frontmatter of a SKILL.md file to construct
// a SkillSummary without loading the entire multi-megabyte instruction body into memory.
func ParseSkillSummaryFile(slug, skillPath, source string) (SkillSummary, error) {
	fi, err := os.Stat(skillPath)
	if err != nil {
		return SkillSummary{}, err
	}

	f, err := os.Open(skillPath)
	if err != nil {
		return SkillSummary{}, err
	}
	defer f.Close()

	// Read only the first 8KB which easily contains the entire YAML frontmatter
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	headerText := string(buf[:n])

	pf := ParseSkillContent(slug, headerText)

	// Check if scripts/ exists in the skill bundle directory
	bundleDir := filepath.Dir(skillPath)
	hasScripts := false
	if sfi, err := os.Stat(filepath.Join(bundleDir, "scripts")); err == nil && sfi.IsDir() {
		hasScripts = true
	}

	estTokens := (int(fi.Size()) + 3) / 4

	return SkillSummary{
		Slug:            slug,
		Name:            pf.Name,
		Trigger:         ExtractTrigger(pf.Description),
		EstimatedTokens: estTokens,
		HasScripts:      hasScripts,
		Source:          source,
	}, nil
}
