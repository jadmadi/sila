package goals

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	isnadSectionRE = regexp.MustCompile(`(?is)(##+\s*(?:Chain\s+of\s+Custody\s*(?:\(Isnad\))?|Isnad)\s*\r?\n)(.*?)(?:\r?\n##+|\z)`)
	isnadLineRE    = regexp.MustCompile(`(?im)^[ \t]*[-*]\s*\*\*([^:*]+)\*\*\s*:\s*([^\r\n]+)$`)
)

// ParseIsnadHeader parses the 4-stage Chain of Custody (Isnad) from goal.md content.
// Returns nil if no Isnad section is present.
func ParseIsnadHeader(content string) *IsnadHeader {
	m := isnadSectionRE.FindStringSubmatch(content)
	if len(m) < 3 {
		return nil
	}
	body := m[2]

	isnad := &IsnadHeader{}
	hasAny := false

	lines := strings.Split(body, "\n")
	for _, line := range lines {
		sub := isnadLineRE.FindStringSubmatch(line)
		if len(sub) < 3 {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(sub[1]))
		value := strings.TrimSpace(sub[2])

		parts := strings.Split(value, "|")
		primary := strings.TrimSpace(parts[0])

		switch {
		case strings.Contains(label, "register"):
			isnad.RegisteredBy = primary
			hasAny = true
			for _, p := range parts[1:] {
				kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
				if len(kv) == 2 {
					k := strings.ToLower(strings.TrimSpace(kv[0]))
					v := strings.TrimSpace(kv[1])
					switch k {
					case "session", "session_id":
						isnad.RegisteredSession = strings.Trim(v, "`'\"")
					case "date", "registered_at", "at":
						isnad.RegisteredDate = strings.Trim(v, "`'\"")
					}
				}
			}

		case strings.Contains(label, "spec audit"):
			isnad.SpecAuditedBy = primary
			hasAny = true
			for _, p := range parts[1:] {
				kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
				if len(kv) == 2 {
					k := strings.ToLower(strings.TrimSpace(kv[0]))
					v := strings.TrimSpace(kv[1])
					switch k {
					case "session", "session_id":
						isnad.SpecAuditedSession = strings.Trim(v, "`'\"")
					case "date", "audited_at", "at":
						isnad.SpecAuditedDate = strings.Trim(v, "`'\"")
					}
				}
			}

		case strings.Contains(label, "implement"):
			isnad.ImplementedBy = primary
			hasAny = true
			for _, p := range parts[1:] {
				kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
				if len(kv) == 2 {
					k := strings.ToLower(strings.TrimSpace(kv[0]))
					v := strings.TrimSpace(kv[1])
					switch k {
					case "session", "session_id":
						isnad.ImplementedSession = strings.Trim(v, "`'\"")
					case "commit", "commit_sha", "sha":
						isnad.ImplementedCommit = strings.Trim(v, "`'\"")
					}
				}
			}

		case strings.Contains(label, "mystery audit"):
			isnad.MysteryAuditedBy = primary
			hasAny = true
			for _, p := range parts[1:] {
				kv := strings.SplitN(strings.TrimSpace(p), ":", 2)
				if len(kv) == 2 {
					k := strings.ToLower(strings.TrimSpace(kv[0]))
					v := strings.TrimSpace(kv[1])
					switch k {
					case "session", "session_id":
						isnad.MysteryAuditedSession = strings.Trim(v, "`'\"")
					case "report", "report_path", "dossier":
						isnad.MysteryAuditedReport = strings.Trim(v, "`'\"")
					}
				}
			}
		}
	}

	if !hasAny {
		return nil
	}
	return isnad
}

// FormatIsnadHeader serializes an IsnadHeader struct into standard Markdown.
func FormatIsnadHeader(isnad *IsnadHeader) string {
	if isnad == nil {
		isnad = &IsnadHeader{}
	}

	var b strings.Builder
	b.WriteString("## Chain of Custody (Isnad)\n")

	// 1. Registered By
	reg := isnad.RegisteredBy
	if reg == "" {
		reg = "Pending Registration"
	}
	b.WriteString(fmt.Sprintf("- **Registered By**: %s", reg))
	if isnad.RegisteredSession != "" {
		b.WriteString(fmt.Sprintf(" | Session: `%s`", isnad.RegisteredSession))
	}
	if isnad.RegisteredDate != "" {
		b.WriteString(fmt.Sprintf(" | Date: `%s`", isnad.RegisteredDate))
	}
	b.WriteString("\n")

	// 2. Spec Audited By
	spec := isnad.SpecAuditedBy
	if spec == "" {
		spec = "Pending Spec Audit"
	}
	b.WriteString(fmt.Sprintf("- **Spec Audited By**: %s", spec))
	if isnad.SpecAuditedSession != "" {
		b.WriteString(fmt.Sprintf(" | Session: `%s`", isnad.SpecAuditedSession))
	}
	if isnad.SpecAuditedDate != "" {
		b.WriteString(fmt.Sprintf(" | Date: `%s`", isnad.SpecAuditedDate))
	}
	b.WriteString("\n")

	// 3. Implemented By
	imp := isnad.ImplementedBy
	if imp == "" {
		imp = "Pending Implementation"
	}
	b.WriteString(fmt.Sprintf("- **Implemented By**: %s", imp))
	if isnad.ImplementedSession != "" {
		b.WriteString(fmt.Sprintf(" | Session: `%s`", isnad.ImplementedSession))
	}
	if isnad.ImplementedCommit != "" {
		b.WriteString(fmt.Sprintf(" | Commit: `%s`", isnad.ImplementedCommit))
	}
	b.WriteString("\n")

	// 4. Mystery Audited By
	aud := isnad.MysteryAuditedBy
	if aud == "" {
		aud = "Pending Mystery Audit"
	}
	b.WriteString(fmt.Sprintf("- **Mystery Audited By**: %s", aud))
	if isnad.MysteryAuditedSession != "" {
		b.WriteString(fmt.Sprintf(" | Session: `%s`", isnad.MysteryAuditedSession))
	}
	if isnad.MysteryAuditedReport != "" {
		b.WriteString(fmt.Sprintf(" | Report: `%s`", isnad.MysteryAuditedReport))
	}
	b.WriteString("\n")

	return b.String()
}

// UpdateGoalMdIsnad updates or inserts the Chain of Custody (Isnad) section in goal.md.
func UpdateGoalMdIsnad(goalMdPath string, isnad *IsnadHeader) error {
	data, err := os.ReadFile(goalMdPath)
	if err != nil {
		return fmt.Errorf("read goal.md: %w", err)
	}
	content := string(data)
	formatted := FormatIsnadHeader(isnad)

	loc := isnadSectionRE.FindStringSubmatchIndex(content)
	if loc != nil {
		// Replace the entire isnad section match, preserving any following section header
		// loc[0] is start of match, loc[1] is end of match.
		// If match ends with "\n##", preserve that header.
		matchedStr := content[loc[0]:loc[1]]
		replacement := formatted
		if trailingIdx := strings.LastIndex(matchedStr, "\n##"); trailingIdx != -1 {
			replacement = formatted + matchedStr[trailingIdx+1:]
		}
		newContent := content[:loc[0]] + replacement + content[loc[1]:]
		return AtomicWriteFile(goalMdPath, []byte(newContent), 0644)
	}

	// Insert after main title header if present
	titleMatch := regexp.MustCompile(`(?m)^#+[ \t]*[^\r\n]+\r?\n`).FindStringIndex(content)
	if titleMatch != nil {
		insertPos := titleMatch[1]
		newContent := content[:insertPos] + "\n" + formatted + "\n" + content[insertPos:]
		return AtomicWriteFile(goalMdPath, []byte(newContent), 0644)
	}

	// Prepend to top
	newContent := formatted + "\n\n" + content
	return AtomicWriteFile(goalMdPath, []byte(newContent), 0644)
}
