package importmem

import (
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/jadmadi/sila/internal/loaders"
)

// MaskMode controls how matched secrets are replaced.
type MaskMode string

const (
	MaskFull   MaskMode = "full"
	MaskPrefix MaskMode = "prefix"
)

// RedactorOptions configures the layered redactor.
type RedactorOptions struct {
	Mask     MaskMode
	RulesDir string
}

// Redactor applies layers 2 to 7 plus the existing base scrub.
type Redactor struct {
	mask     MaskMode
	rulesDir string
	rules    []compiledRule
	rulesErr error
}

// NewRedactor validates options and returns a redactor.
func NewRedactor(opts RedactorOptions) (*Redactor, error) {
	mask := opts.Mask
	if mask == "" {
		mask = MaskFull
	}
	if mask != MaskFull && mask != MaskPrefix {
		return nil, &ImportError{Code: CodeRedactionFailed, Message: "redaction mask must be full or prefix"}
	}
	redactor := &Redactor{mask: mask, rulesDir: strings.TrimSpace(opts.RulesDir)}
	if redactor.rulesDir != "" {
		rules, err := loadRedactionRules(redactor.rulesDir)
		if err != nil {
			return nil, &ImportError{Code: CodeRedactionFailed, Message: err.Error()}
		}
		redactor.rules = rules
	}
	return redactor, nil
}

var defaultRedactor, _ = NewRedactor(RedactorOptions{})

// testDetector lets tests inject a failing layer-9 detector.
var testDetector func(string) bool

var (
	dsnPasswordRE  = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://[^/\s:@]+:)([^@\s/]+)(@)`)
	passwordKVRE   = regexp.MustCompile(`(?i)\b(password|passwd|pwd)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`)
	secretPutRE    = regexp.MustCompile(`(?i)(echo\s+)(\$[A-Za-z_][A-Za-z0-9_]*)(\s*\|\s*[^\n|]*\bsecret\s+put\b)`)
	assignmentRE   = regexp.MustCompile(`(?i)\b((?:export\s+)?[A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD)[A-Z0-9_]*\s*=\s*)("[^"]*"|'[^']*'|[^\s;&|]+)`)
	entropyTokenRE = regexp.MustCompile(`[A-Za-z0-9+_\-]{20,200}`)
	jwtRE          = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	pemRE          = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
)

const (
	maskToken       = "[REDACTED_SECRET]"
	entropyMin      = 4.5
	entropyMinBytes = 20
	entropyMaxBytes = 200
)

func (r *Redactor) maskValue(secret string) string {
	if r.mask == MaskPrefix && len(secret) >= 16 {
		return secret[:4] + maskToken
	}
	return maskToken
}

// maskKV uses the base scrub marker so key-value layers match existing output.
func (r *Redactor) maskKV(secret string) string {
	if r.mask == MaskPrefix && len(secret) >= 16 {
		return secret[:4] + "[REDACTED]"
	}
	return "[REDACTED]"
}

func shannonEntropy(text string) float64 {
	if text == "" {
		return 0
	}
	freq := map[rune]int{}
	for _, ch := range text {
		freq[ch]++
	}
	total := float64(len([]rune(text)))
	entropy := 0.0
	for _, count := range freq {
		p := float64(count) / total
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// redactLayers applies layers 2 to 7 and returns the cleaned text.
func (r *Redactor) redactLayers(text string, counts map[string]int) string {
	// Layer 2: scheme://user:password@host
	text = dsnPasswordRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := dsnPasswordRE.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		if secretMarkerRE.MatchString(parts[2]) {
			return match
		}
		counts["secret"]++
		return parts[1] + r.maskKV(parts[2]) + parts[3]
	})
	// Layer 3: password=, pwd=, passwd=, pwd:
	text = passwordKVRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := passwordKVRE.FindStringSubmatch(match)
		if len(parts) != 4 || secretMarkerRE.MatchString(parts[3]) {
			return match
		}
		value := parts[3]
		quote := ""
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			quote = string(value[0])
			value = value[1 : len(value)-1]
		}
		counts["secret"]++
		return parts[1] + parts[2] + quote + r.maskKV(value) + quote
	})
	// Layer 4: echo $VAR | ... secret put
	text = secretPutRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := secretPutRE.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		counts["secret"]++
		return parts[1] + r.maskKV(parts[2]) + parts[3]
	})
	// Layer 5: export NAME=... and inline assignments
	text = assignmentRE.ReplaceAllStringFunc(text, func(match string) string {
		parts := assignmentRE.FindStringSubmatch(match)
		if len(parts) != 3 || secretMarkerRE.MatchString(parts[2]) {
			return match
		}
		value := parts[2]
		quote := ""
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			quote = string(value[0])
			value = value[1 : len(value)-1]
		}
		counts["secret"]++
		return parts[1] + quote + r.maskKV(value) + quote
	})
	// Layer 7: JWTs and PEM private keys.
	text = jwtRE.ReplaceAllStringFunc(text, func(match string) string {
		counts["secret"]++
		return "[REDACTED_JWT]"
	})
	text = pemRE.ReplaceAllStringFunc(text, func(match string) string {
		counts["secret"]++
		return "[REDACTED_PRIVATE_KEY]"
	})
	// Layer 6: high-entropy tokens up to 200 bytes.
	text = replaceEntropyTokens(text, counts, r.maskValue)
	return text
}

func replaceEntropyTokens(text string, counts map[string]int, mask func(string) string) string {
	matches := entropyTokenRE.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, loc := range matches {
		token := text[loc[0]:loc[1]]
		if len(token) < entropyMinBytes || len(token) > entropyMaxBytes || strings.Contains(token, "REDACTED") {
			continue
		}
		if shannonEntropy(token) <= entropyMin {
			continue
		}
		b.WriteString(text[last:loc[0]])
		b.WriteString(mask(token))
		last = loc[1]
		counts["secret"]++
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// Redact applies layers 2 to 7, the base scrub, and a final fail-closed rescan.
func (r *Redactor) Redact(text, repoRoot string) (result RedactionResult, err error) {
	counts := map[string]int{}
	ruleCounts := map[string]int{}
	for _, category := range RedactionCategories {
		counts[category] = 0
	}
	if strings.IndexByte(text, 0) >= 0 {
		return RedactionResult{Counts: counts, Rules: ruleCounts}, &ImportError{Code: CodeRedactionFailed, Message: "NUL byte in memory file"}
	}
	defer func() {
		if recover() != nil {
			result = RedactionResult{Counts: counts, Rules: ruleCounts}
			err = &ImportError{Code: CodeRedactionFailed, Message: "secret scrubber failed"}
		}
	}()

	if r.rulesDir == "" && r.rulesErr == nil && r.rules == nil {
		if repoRoot != "" {
			dir := repoRoot + string(os.PathSeparator) + ".sila" + string(os.PathSeparator) + "redactors"
			if rules, err := loadRedactionRules(dir); err == nil {
				r.rules = rules
			} else {
				r.rulesErr = err
			}
		}
	}
	if r.rulesErr != nil {
		return RedactionResult{Counts: counts, Rules: ruleCounts}, &ImportError{Code: CodeRedactionFailed, Message: r.rulesErr.Error()}
	}
	clean := r.redactLayers(text, counts)
	if len(r.rules) > 0 {
		clean = r.applyRules(clean, counts, ruleCounts)
	}
	counts["home_path"] += len(homePathRE.FindAllString(clean, -1))
	counts["home_path"] += len(homeWinPathRE.FindAllString(clean, -1))
	preMarkers := len(secretMarkerRE.FindAllString(clean, -1))
	clean = loaders.ScrubSecrets(clean)
	if postMarkers := len(secretMarkerRE.FindAllString(clean, -1)); postMarkers > preMarkers {
		counts["secret"] += postMarkers - preMarkers
	}

	clean = emailRE.ReplaceAllStringFunc(clean, func(string) string {
		counts["email"]++
		return "[REDACTED_EMAIL]"
	})
	repo := normalizeRepoRoot(repoRoot)
	clean = replaceAbsolutePaths(clean, repo, counts)
	clean = winAbsRE.ReplaceAllStringFunc(clean, func(match string) string {
		counts["path"]++
		return keepBoundary(match) + "[REDACTED_PATH]"
	})
	clean = uncAbsRE.ReplaceAllStringFunc(clean, func(match string) string {
		counts["path"]++
		return keepBoundary(match) + "[REDACTED_PATH]"
	})
	clean, control := stripControl(clean)
	counts["control"] += control

	if strings.TrimSpace(text) != "" && strings.TrimSpace(clean) == "" {
		return RedactionResult{Counts: counts, Rules: ruleCounts}, &ImportError{Code: CodeRedactionFailed, Message: "redaction produced empty content"}
	}
	for _, re := range residualSecretREs {
		if re.MatchString(clean) {
			return RedactionResult{Counts: counts, Rules: ruleCounts}, &ImportError{Code: CodeRedactionFailed, Message: "known secret pattern survived redaction"}
		}
	}
	if r.residualLayers(clean) > 0 || (testDetector != nil && testDetector(clean)) {
		return RedactionResult{Counts: counts, Rules: ruleCounts}, &ImportError{Code: CodeRedactionFailed, Message: "REDACTION_INCOMPLETE"}
	}
	return RedactionResult{Text: clean, Counts: counts, Rules: ruleCounts}, nil
}

// residualLayers counts layer 2 to 7 matches left after redaction (layer 9).
func (r *Redactor) residualLayers(text string) int {
	remaining := 0
	for _, re := range []*regexp.Regexp{dsnPasswordRE, passwordKVRE, secretPutRE, assignmentRE, jwtRE, pemRE} {
		for _, match := range re.FindAllString(text, -1) {
			if !secretMarkerRE.MatchString(match) {
				remaining++
			}
		}
	}
	return remaining
}
