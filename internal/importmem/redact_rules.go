package importmem

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// compiledRule is one operator-supplied detector.
type compiledRule struct {
	name string
	re   *regexp.Regexp
}

const maxRulePatternBytes = 4096

// loadRedactionRules reads every *.yaml under dir. Each file declares one rule
// with name, pattern, and samples; invalid regexes and non-matching samples are
// rejected with the rule name.
func loadRedactionRules(dir string) ([]compiledRule, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".yaml") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	var rules []compiledRule
	seen := map[string]bool{}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read redactor rule %s: %w", name, err)
		}
		ruleName, pattern, samples, err := parseRuleYAML(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if ruleName == "" || pattern == "" {
			return nil, fmt.Errorf("%s: rule requires name and pattern", name)
		}
		if seen[ruleName] {
			return nil, fmt.Errorf("duplicate redactor rule %q", ruleName)
		}
		if len(pattern) > maxRulePatternBytes {
			return nil, fmt.Errorf("RULE_PATTERN_TOO_LONG: %s", ruleName)
		}
		re, compileErr := regexp.Compile(pattern)
		if compileErr != nil {
			return nil, fmt.Errorf("RULE_INVALID_REGEX: %s: %w", ruleName, compileErr)
		}
		for _, sample := range samples {
			if !re.MatchString(sample) {
				return nil, fmt.Errorf("RULE_SAMPLE_MISMATCH: %s", ruleName)
			}
		}
		seen[ruleName] = true
		rules = append(rules, compiledRule{name: ruleName, re: re})
	}
	return rules, nil
}

// parseRuleYAML handles the minimal rule subset: name, pattern, and samples.
func parseRuleYAML(text string) (name, pattern string, samples []string, err error) {
	inSamples := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "name:"):
			name = cleanYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "name:")))
			inSamples = false
		case strings.HasPrefix(line, "pattern:"):
			pattern = cleanYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "pattern:")))
			inSamples = false
		case strings.HasPrefix(line, "samples:"):
			inSamples = true
		case inSamples && strings.HasPrefix(line, "- "):
			samples = append(samples, cleanYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "- "))))
		default:
			if inSamples && strings.HasPrefix(line, "-") {
				samples = append(samples, cleanYAMLScalar(strings.TrimSpace(strings.TrimPrefix(line, "-"))))
			}
		}
	}
	return name, pattern, samples, nil
}

func cleanYAMLScalar(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// applyRules masks layer 8 matches and records per-rule counts.
func (r *Redactor) applyRules(text string, counts map[string]int, rules map[string]int) string {
	for _, rule := range r.rules {
		text = rule.re.ReplaceAllStringFunc(text, func(match string) string {
			counts["secret"]++
			rules[rule.name]++
			return r.maskKV(match)
		})
	}
	return text
}
