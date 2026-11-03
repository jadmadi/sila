package learn

import (
	"fmt"
	"os"
	"strings"
)

const (
	// ManagedBlockBegin marks the start of Sila-managed operational rules.
	ManagedBlockBegin = "<!-- sila:begin (operational-rules) -->"
	// ManagedBlockEnd marks the end of Sila-managed operational rules.
	ManagedBlockEnd = "<!-- sila:end -->"
)

// GenerateManagedRulesBlock formats distilled guardrail rules into a structured markdown block.
func GenerateManagedRulesBlock(learnings []OperationalLearning, domains []string) string {
	domainFilter := make(map[string]bool)
	filterActive := false
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "all" || d == "*" {
			filterActive = false
			break
		}
		if d != "" {
			domainFilter[d] = true
			filterActive = true
		}
	}

	// Group by domain in canonical order
	grouped := make(map[string][]OperationalLearning)
	for _, l := range learnings {
		if filterActive && !domainFilter[strings.ToLower(l.Domain)] {
			continue
		}
		if l.GuardrailRule == "" {
			continue
		}
		grouped[l.Domain] = append(grouped[l.Domain], l)
	}

	var sb strings.Builder
	sb.WriteString(ManagedBlockBegin)
	sb.WriteString("\n## Living Operational Rules & Epistemic Guardrails (Auto-Distilled by Sila)\n")
	sb.WriteString("> Battle-tested engineering disciplines, failure postmortems, and architectural invariants distilled across repositories.\n\n")

	for _, d := range AllDomains {
		items := grouped[d]
		if len(items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n", DomainDisplayName(d)))
		for _, item := range items {
			title := item.Title
			if title == "" {
				title = item.Symptom
			}
			rule := HarmonizeRule(item.GuardrailRule)
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", title, rule))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(ManagedBlockEnd)
	return sb.String()
}

// InjectManagedRules inserts or updates the managed block in existing file content non-destructively.
func InjectManagedRules(existingContent, managedBlock string) string {
	beginIdx := strings.Index(existingContent, ManagedBlockBegin)
	endIdx := strings.Index(existingContent, ManagedBlockEnd)

	if beginIdx != -1 && endIdx != -1 && endIdx >= beginIdx {
		// Existing block found: replace content within delimiters
		prefix := existingContent[:beginIdx]
		suffix := existingContent[endIdx+len(ManagedBlockEnd):]
		return prefix + managedBlock + suffix
	}

	// Delimiter not found: append block to end of file
	trimmed := strings.TrimRight(existingContent, "\n")
	if trimmed == "" {
		return managedBlock + "\n"
	}
	return trimmed + "\n\n" + managedBlock + "\n"
}

// ExportRulesToFile writes or injects managed operational rules into a target rule file.
func ExportRulesToFile(targetPath string, learnings []OperationalLearning, domains []string) error {
	block := GenerateManagedRulesBlock(learnings, domains)

	var newContent string
	existingBytes, err := os.ReadFile(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			newContent = block + "\n"
		} else {
			return fmt.Errorf("reading target rule file %s: %w", targetPath, err)
		}
	} else {
		newContent = InjectManagedRules(string(existingBytes), block)
	}

	if err := os.WriteFile(targetPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("writing rules to %s: %w", targetPath, err)
	}

	return nil
}
