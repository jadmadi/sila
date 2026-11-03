package lint

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// UnslopViolation describes a single conversational tell detected in text.
type UnslopViolation struct {
	LineNumber int    `json:"line_number"`
	Rule       string `json:"rule"`
	Original   string `json:"original"`
	Suggestion string `json:"suggestion"`
}

// UnslopReport records all detected conversational tells across scanned text or files.
type UnslopReport struct {
	Clean        bool              `json:"clean"`
	Violations   []UnslopViolation `json:"violations"`
	OriginalText string            `json:"original_text,omitempty"`
	CleanedText  string            `json:"cleaned_text,omitempty"`
	Diff         string            `json:"diff,omitempty"`
}

// Conversational sycophancy patterns
var sycophancyPatterns = []struct {
	re   *regexp.Regexp
	desc string
}{
	{regexp.MustCompile(`(?i)^(\s*)(you(?:'re| are) absolutely right[!.]?)(\s*)`), "sycophantic opener"},
	{regexp.MustCompile(`(?i)^(\s*)(great|excellent) question[!.]?(\s*)`), "conversational filler"},
	{regexp.MustCompile(`(?i)^(\s*)(certainly|sure thing|of course)[!.]?(\s*)`), "conversational opener"},
	{regexp.MustCompile(`(?i)^(\s*)(i(?:'d| would) be happy to help[!.]?)(\s*)`), "service conversational filler"},
	{regexp.MustCompile(`(?i)^(\s*)(as an ai language model[,.]?)(\s*)`), "ai model disclaimer"},
}

// Buzzwords to replace with simple Plain English words
var buzzwordReplacements = []struct {
	re          *regexp.Regexp
	replacement string
	rule        string
}{
	{regexp.MustCompile(`\bleverage\b`), "use", "buzzword: leverage -> use"},
	{regexp.MustCompile(`\bLeverage\b`), "Use", "buzzword: Leverage -> Use"},
	{regexp.MustCompile(`\bleveraged\b`), "used", "buzzword: leveraged -> used"},
	{regexp.MustCompile(`\bleveraging\b`), "using", "buzzword: leveraging -> using"},
	{regexp.MustCompile(`\bstreamline\b`), "simplify", "buzzword: streamline -> simplify"},
	{regexp.MustCompile(`\bStreamline\b`), "Simplify", "buzzword: Streamline -> Simplify"},
	{regexp.MustCompile(`\bstreamlined\b`), "simplified", "buzzword: streamlined -> simplified"},
	{regexp.MustCompile(`\bstreamlining\b`), "simplifying", "buzzword: streamlining -> simplifying"},
	{regexp.MustCompile(`\brobust\b`), "reliable", "buzzword: robust -> reliable"},
	{regexp.MustCompile(`\bRobust\b`), "Reliable", "buzzword: Robust -> Reliable"},
	{regexp.MustCompile(`\bbespoke\b`), "custom", "buzzword: bespoke -> custom"},
	{regexp.MustCompile(`\bBespoke\b`), "Custom", "buzzword: Bespoke -> Custom"},
	{regexp.MustCompile(`\bcommence\b`), "start", "formalism: commence -> start"},
	{regexp.MustCompile(`\bCommence\b`), "Start", "formalism: Commence -> Start"},
	{regexp.MustCompile(`\bcommenced\b`), "started", "formalism: commenced -> started"},
	{regexp.MustCompile(`\bprior to\b`), "before", "formalism: prior to -> before"},
	{regexp.MustCompile(`\bPrior to\b`), "Before", "formalism: Prior to -> Before"},
	{regexp.MustCompile(`\butilize\b`), "use", "formalism: utilize -> use"},
	{regexp.MustCompile(`\bUtilize\b`), "Use", "formalism: Utilize -> Use"},
	{regexp.MustCompile(`\butilized\b`), "used", "formalism: utilized -> used"},
	{regexp.MustCompile(`\butilizing\b`), "using", "formalism: utilizing -> using"},
	{regexp.MustCompile(`\bterminate\b`), "end", "formalism: terminate -> end"},
	{regexp.MustCompile(`\bTerminate\b`), "End", "formalism: Terminate -> End"},
	{regexp.MustCompile(`\bwhilst\b`), "while", "formalism: whilst -> while"},
	{regexp.MustCompile(`\bWhilst\b`), "While", "formalism: Whilst -> While"},
}

var boldPattern = regexp.MustCompile(`\*\*([^*]+)\*\*`)
var emDashPattern = regexp.MustCompile(`—`)

// CleanText processes markdown text to eliminate conversational AI tells while
// preserving code blocks (fenced and inline) and tables verbatim.
func CleanText(text string) string {
	lines := strings.Split(text, "\n")
	cleanedLines := make([]string, len(lines))
	inCodeBlock := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Toggle fenced code blocks
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCodeBlock = !inCodeBlock
			cleanedLines[i] = line
			continue
		}

		if inCodeBlock {
			cleanedLines[i] = line
			continue
		}

		// Preserve markdown table lines
		if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			cleanedLines[i] = line
			continue
		}

		// Preserve markdown headings
		if strings.HasPrefix(trimmed, "#") {
			cleanedLines[i] = line
			continue
		}

		// Process normal text lines outside code blocks
		cleanedLines[i] = cleanLine(line)
	}

	return strings.Join(cleanedLines, "\n")
}

func cleanLine(line string) string {
	if strings.TrimSpace(line) == "" {
		return line
	}

	// 1. Remove sycophantic openers
	for _, syco := range sycophancyPatterns {
		line = syco.re.ReplaceAllString(line, "$1")
	}

	// Shield inline code snippets before transformations
	type placeholder struct {
		token string
		orig  string
	}
	var shielded []placeholder
	inlineCodeRE := regexp.MustCompile("`[^`]+`")
	shieldIdx := 0
	line = inlineCodeRE.ReplaceAllStringFunc(line, func(match string) string {
		token := fmt.Sprintf("__SILA_INLINE_%d__", shieldIdx)
		shieldIdx++
		shielded = append(shielded, placeholder{token: token, orig: match})
		return token
	})

	// 2. Replace em dashes with comma or colon
	line = emDashPattern.ReplaceAllString(line, ", ")

	// 3. Strip excessive bold formatting in prose sentences
	// Note: Avoid stripping bold if line is just a bullet with a label like "- **Key**: Value"
	isLabeledBullet := regexp.MustCompile(`^\s*[-*+]\s+\*\*([^*]+)\*\*:\s*`)
	if !isLabeledBullet.MatchString(line) {
		line = boldPattern.ReplaceAllString(line, "$1")
	}

	// 4. Substitute Plain English buzzwords
	for _, b := range buzzwordReplacements {
		line = b.re.ReplaceAllString(line, b.replacement)
	}

	// Unshield inline code snippets
	for _, s := range shielded {
		line = strings.Replace(line, s.token, s.orig, 1)
	}

	// Clean up any double spaces created by removals
	for strings.Contains(line, "  ") {
		line = strings.ReplaceAll(line, "  ", " ")
	}

	return line
}

// CheckText inspects markdown text and identifies any conversational slop violations.
func CheckText(text string) *UnslopReport {
	lines := strings.Split(text, "\n")
	var violations []UnslopViolation
	inCodeBlock := false

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock || (strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")) {
			continue
		}

		// Check sycophancy
		for _, syco := range sycophancyPatterns {
			if syco.re.MatchString(line) {
				violations = append(violations, UnslopViolation{
					LineNumber: lineNum,
					Rule:       syco.desc,
					Original:   line,
					Suggestion: "remove conversational sycophancy / filler",
				})
			}
		}

		// Check em dash
		if emDashPattern.MatchString(line) {
			violations = append(violations, UnslopViolation{
				LineNumber: lineNum,
				Rule:       "em dash prohibited",
				Original:   line,
				Suggestion: "use comma, colon, or separate sentences",
			})
		}

		// Check bold in body text
		isLabeledBullet := regexp.MustCompile(`^\s*[-*+]\s+\*\*([^*]+)\*\*:\s*`)
		if !strings.HasPrefix(trimmed, "#") && !isLabeledBullet.MatchString(line) && boldPattern.MatchString(line) {
			violations = append(violations, UnslopViolation{
				LineNumber: lineNum,
				Rule:       "excessive bold markers in body text",
				Original:   line,
				Suggestion: "remove markdown bold markers",
			})
		}

		// Check buzzwords
		for _, b := range buzzwordReplacements {
			if b.re.MatchString(line) {
				violations = append(violations, UnslopViolation{
					LineNumber: lineNum,
					Rule:       b.rule,
					Original:   line,
					Suggestion: fmt.Sprintf("replace with simple Plain English equivalent: %s", b.replacement),
				})
			}
		}
	}

	cleaned := CleanText(text)
	return &UnslopReport{
		Clean:        len(violations) == 0,
		Violations:   violations,
		OriginalText: text,
		CleanedText:  cleaned,
	}
}

// CheckFile runs unslop inspection on a target file.
func CheckFile(filePath string) (*UnslopReport, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}
	return CheckText(string(data)), nil
}

// CleanFile inspects and optionally rewrites a target file.
func CleanFile(filePath string, writeBack bool) (*UnslopReport, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", filePath, err)
	}
	content := string(data)
	rep := CheckText(content)
	if writeBack && !rep.Clean {
		if err := os.WriteFile(filePath, []byte(rep.CleanedText), 0644); err != nil {
			return nil, fmt.Errorf("write cleaned file %s: %w", filePath, err)
		}
	}
	return rep, nil
}
