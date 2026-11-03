package ccli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/jadmadi/sila/internal/cli"
)

// RewriteMode controls how RewriteArgs evaluates canonical precedence and opt-out rules.
type RewriteMode string

const (
	RewriteAuto    RewriteMode = "auto"
	RewriteDo      RewriteMode = "do"
	RewriteExplain RewriteMode = "explain"
)

// Decision constants reported by RewriteResult.Decision.
const (
	DecisionRoute      = "ROUTE"
	DecisionClarify    = "CLARIFY"
	DecisionNoMatch    = "NO_MATCH"
	DecisionDisabled   = "DISABLED"
	DecisionDenylisted = "DENYLISTED"
	DecisionCanonical  = "CANONICAL"
)

// DecidedBy constants reported by RewriteResult.DecidedBy.
const (
	DecidedByFlag      = "flag"
	DecidedByEnv       = "env"
	DecidedByDenylist  = "denylist"
	DecidedByCanonical = "canonical"
	DecidedByAuto      = "auto"
	DecidedByDo        = "do"
	DecidedByNoMatch   = "no-match"
	DecidedByForbidden = "forbidden"
)

// Precedence constants for production precedence classification.
const (
	PrecedenceCanonicalFirst  = "canonical-first"
	PrecedencePhraseFirst     = "phrase-first"
	PrecedencePhraseOnlyViaDo = "phrase-only-via-do"
)

// RewriteOptions configures optional overrides for RewriteArgs.
// RewriteArgs is pure and performs no DB, filesystem, network, or process operations.
type RewriteOptions struct {
	FlagMode   FlagExtractionMode
	NoCCLI     bool
	CCLIEnv    string
	Denylist   string
	EnvSet     bool
	Inventory  *Inventory
	WarnWriter io.Writer
}

// RewriteResult holds the structured output of conversational phrase resolution.
type RewriteResult struct {
	Input       []string          `json:"input"`
	Phrase      string            `json:"phrase"`
	Matched     bool              `json:"matched"`
	Production  string            `json:"production,omitempty"`
	ActionID    string            `json:"action,omitempty"`
	Tier        string            `json:"tier,omitempty"`
	TargetReq   string            `json:"target_policy,omitempty"`
	Canonical   []string          `json:"canonical"`
	Precedence  string            `json:"precedence,omitempty"`
	DecidedBy   string            `json:"decided_by"`
	Decision    string            `json:"decision"`
	Safety      string            `json:"safety,omitempty"`
	ExitCode    ExitCode          `json:"exit_code"`
	Tokens      []Token           `json:"tokens,omitempty"`
	AST         *AST              `json:"-"`
	Operands    map[string]string `json:"operands,omitempty"`
	RawFlags    []string          `json:"raw_flags,omitempty"`
	Explanation string            `json:"explanation,omitempty"`
	Suggestions []string          `json:"suggestions,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Forbidden   bool              `json:"forbidden,omitempty"`
	Err         error             `json:"-"`
}

var (
	envWarnMu    sync.Mutex
	envWarnedVal = make(map[string]bool)
)

// ResetEnvWarningsForTest clears the once-per-value invalid SILA_CCLI warning tracker.
func ResetEnvWarningsForTest() {
	envWarnMu.Lock()
	defer envWarnMu.Unlock()
	envWarnedVal = make(map[string]bool)
}

// ParseCCLIEnv interprets SILA_CCLI values.
// Returns (enabled, warningMessage). Unknown values return (true, warning).
func ParseCCLIEnv(raw string) (bool, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true, ""
	}
	switch strings.ToLower(trimmed) {
	case "on", "1", "true", "yes", "enable", "enabled":
		return true, ""
	case "off", "0", "false", "no", "disable", "disabled":
		return false, ""
	default:
		return true, fmt.Sprintf("sila: warning: unknown SILA_CCLI value %q; behaving as \"on\"", trimmed)
	}
}

func extractPreDashWords(args []string, inv *Inventory) []string {
	var catalog FlagCatalog
	if inv != nil {
		catalog = inv.FlagCatalog
	}
	var words []string
	for _, a := range extractPreDashPositionalRaw(args, catalog) {
		words = append(words, strings.Fields(a)...)
	}
	return words
}

// RewriteArgs resolves a command line argument slice according to mode and options.
// It is pure and never mutates state, opens databases, or executes commands.
func RewriteArgs(args []string, mode RewriteMode, opts RewriteOptions) RewriteResult {
	if mode == "" {
		mode = RewriteAuto
	}
	inv := opts.Inventory
	if inv == nil {
		inv = GetSilaRouter().Inventory
	}
	flagMode := opts.FlagMode
	if flagMode == "" {
		flagMode = FlagModeLenient
	}
	if len(args) == 0 {
		return RewriteResult{
			Input: []string{}, Canonical: []string{}, DecidedBy: DecidedByNoMatch,
			Decision: DecisionNoMatch, ExitCode: ExitUsage, Explanation: "empty input",
			Err: errors.New("empty phrase"),
		}
	}

	hasNoCCLIFlag, cleanedArgs := inspectAndStripNoCCLI(args, inv.FlagCatalog)
	noCCLI := opts.NoCCLI || hasNoCCLIFlag

	rawCCLIEnv, rawDenylist := opts.CCLIEnv, opts.Denylist
	if !opts.EnvSet {
		if rawCCLIEnv == "" {
			rawCCLIEnv = os.Getenv("SILA_CCLI")
		}
		if rawDenylist == "" {
			rawDenylist = os.Getenv("SILA_CCLI_DENYLIST")
		}
	}

	envEnabled, envWarn := ParseCCLIEnv(rawCCLIEnv)
	var warnings []string
	if envWarn != "" {
		warnings = append(warnings, envWarn)
		if opts.WarnWriter != nil {
			envWarnMu.Lock()
			if !envWarnedVal[rawCCLIEnv] {
				envWarnedVal[rawCCLIEnv] = true
				fmt.Fprintln(opts.WarnWriter, envWarn)
			}
			envWarnMu.Unlock()
		}
	}

	redactedInput, hasSecret := RedactArgs(cleanedArgs, inv)
	normArgs, extraAfterDash := normalizeInputForParse(cleanedArgs, inv, flagMode)
	pass1 := ExtractFlagsWithMode(normArgs, inv.FlagCatalog, flagMode)
	phraseWords := positionalWords(pass1.Positional)
	phraseStr := buildRedactedPhraseString(pass1.Positional, inv)

	if pass1.ExitCode != ExitSuccess {
		if hasSecret {
			errMsg := redactSecretStringsInText(firstNonEmptyStr(pass1.ErrorMessage,
				"Policy violation: secrets and sensitive configuration keys must not be passed in conversational phrases"), cleanedArgs, inv)
			return RewriteResult{
				Input: redactedInput, Phrase: phraseStr, Precedence: "forbidden",
				DecidedBy: DecidedByForbidden, Decision: DecisionNoMatch, Safety: "refuse",
				ExitCode: ExitPolicy, Tokens: redactTokens(pass1.Tokens, inv), RawFlags: pass1.RawFlags,
				Explanation: errMsg, Warnings: warnings, Forbidden: true, Err: errors.New(errMsg),
			}
		}
		return RewriteResult{
			Input: redactedInput, Phrase: phraseStr, Canonical: cloneSlice(redactedInput),
			DecidedBy: DecidedByNoMatch, Decision: DecisionNoMatch, ExitCode: pass1.ExitCode,
			Tokens: redactTokens(pass1.Tokens, inv), RawFlags: pass1.RawFlags,
			Explanation: redactSecretStringsInText(pass1.ErrorMessage, cleanedArgs, inv), Warnings: warnings,
			Err: errors.New(redactSecretStringsInText(pass1.ErrorMessage, cleanedArgs, inv)),
		}
	}

	denylisted, matchedDenyEntry := MatchesDenylist(phraseWords, rawDenylist)
	parseRes := ParseWithOptions(normArgs, inv, ParseOptions{FlagMode: flagMode})
	matchedProd := findMatchedProduction(parseRes, inv)

	if parseRes.AST != nil && matchedProd != nil {
		for _, s := range matchedProd.Slots {
			if s.Sensitive && parseRes.AST.Operands[s.Name] != "" {
				hasSecret = true
			}
		}
	}
	if parseRes.ExitCode == ExitPolicy {
		hasSecret = true
	}

	if hasSecret {
		errMsg := redactSecretStringsInText(firstNonEmptyStr(parseRes.ErrorMessage,
			"Policy violation: secrets and sensitive configuration keys must not be passed in conversational phrases"), cleanedArgs, inv)
		return RewriteResult{
			Input: redactedInput, Phrase: phraseStr, Precedence: "forbidden",
			DecidedBy: DecidedByForbidden, Decision: DecisionNoMatch, Safety: "refuse",
			ExitCode: ExitPolicy, Tokens: redactTokens(selectTokens(parseRes, pass1), inv),
			Operands: redactMap(selectOperands(parseRes), inv), RawFlags: pass1.RawFlags,
			Explanation: errMsg, Warnings: warnings, Forbidden: true, Err: errors.New(errMsg),
		}
	}

	if noCCLI {
		res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
		res.DecidedBy, res.Decision = DecidedByFlag, DecisionDisabled
		res.Explanation = "conversational CLI disabled by --no-ccli"
		if mode == RewriteAuto {
			res.Matched, res.Canonical, res.ExitCode = false, cloneSlice(args), ExitSuccess
		} else if mode == RewriteDo {
			res.Matched, res.Canonical, res.ExitCode = false, nil, ExitUsage
			res.Err = errors.New("sila do: conversational CLI is disabled by --no-ccli")
		} else {
			res.ExitCode = ExitSuccess
		}
		return res
	}

	if !envEnabled && mode != RewriteDo {
		res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
		res.DecidedBy, res.Decision, res.ExitCode = DecidedByEnv, DecisionDisabled, ExitSuccess
		res.Explanation = "conversational CLI disabled by SILA_CCLI=off"
		if mode == RewriteAuto {
			res.Matched, res.Canonical = false, cloneSlice(args)
		}
		return res
	}

	if denylisted && mode != RewriteDo {
		res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
		res.DecidedBy, res.Decision, res.ExitCode = DecidedByDenylist, DecisionDenylisted, ExitSuccess
		res.Explanation = fmt.Sprintf("phrase matches SILA_CCLI_DENYLIST entry %q", matchedDenyEntry)
		if mode == RewriteAuto {
			res.Matched, res.Canonical = false, cloneSlice(args)
		}
		return res
	}

	firstTok := ""
	if len(pass1.Positional) > 0 {
		firstTok = pass1.Positional[0].Raw
	}
	cmdSpec, isCanonicalFirstToken := cli.Lookup(firstTok)
	if strings.EqualFold(firstTok, "help") || firstTok == "-lp" || firstTok == "--list-projects" {
		isCanonicalFirstToken = true
		if cmdSpec.Name == "" {
			cmdSpec = cli.CommandSpec{Name: firstTok}
		}
	}
	canonicalAccepts := cli.CanonicalAccepts(cleanedArgs) || strings.EqualFold(firstTok, "help") || firstTok == "-lp" || firstTok == "--list-projects"
	isPhraseFirstOverride := !canonicalAccepts && matchedProd != nil && matchedProd.Precedence == PrecedencePhraseFirst && matchedProd.PrecedenceWitness != "" && parseRes.ExitCode == ExitSuccess

	switch mode {
	case RewriteAuto:
		if isCanonicalFirstToken {
			if isPhraseFirstOverride {
				res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
				res.DecidedBy, res.Decision = DecidedByAuto, DecisionRoute
				res.Explanation = fmt.Sprintf("phrase-first override (%s)", matchedProd.PrecedenceWitness)
				return res
			}
			return RewriteResult{
				Input: redactedInput, Phrase: phraseStr, Canonical: cloneSlice(cleanedArgs),
				Precedence: PrecedenceCanonicalFirst, DecidedBy: DecidedByCanonical, Decision: DecisionCanonical,
				Safety: "none", ExitCode: ExitSuccess, Tokens: redactTokens(pass1.Tokens, inv), RawFlags: pass1.RawFlags,
				Explanation: fmt.Sprintf("canonical-first (first token %q resolves to canonical command %q)", firstTok, cmdSpec.Name),
				Warnings:    warnings,
			}
		}
		if parseRes.ExitCode == ExitSuccess && matchedProd != nil {
			res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
			if matchedProd.Precedence == PrecedencePhraseOnlyViaDo {
				res.Matched, res.Canonical = false, cloneSlice(cleanedArgs)
				res.DecidedBy, res.Decision = DecidedByCanonical, DecisionCanonical
				res.Explanation = fmt.Sprintf("production %s requires explicit 'sila do'", matchedProd.ID)
				return res
			}
			res.DecidedBy, res.Decision = DecidedByAuto, DecisionRoute
			res.Explanation = fmt.Sprintf("canonical-first (first token %q is not canonical)", firstTok)
			return res
		}
		return buildNoMatchResult(redactedInput, phraseStr, cloneSlice(cleanedArgs), DecisionNoMatch, parseRes, pass1, phraseWords, inv, warnings)

	case RewriteDo:
		if parseRes.ExitCode == ExitSuccess && matchedProd != nil {
			res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
			res.DecidedBy, res.Decision = DecidedByDo, DecisionRoute
			res.Explanation = fmt.Sprintf("explicit 'sila do' routing via %s", matchedProd.ID)
			return res
		}
		return buildNoMatchResult(redactedInput, phraseStr, nil, classifyFailureDecision(parseRes), parseRes, pass1, phraseWords, inv, warnings)

	case RewriteExplain:
		if isCanonicalFirstToken {
			if isPhraseFirstOverride {
				res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
				res.DecidedBy, res.Decision = DecidedByAuto, DecisionRoute
				res.Explanation = fmt.Sprintf("phrase-first (%s)", matchedProd.PrecedenceWitness)
				return res
			}
			res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
			res.DecidedBy, res.Decision, res.ExitCode, res.Err = DecidedByCanonical, DecisionCanonical, ExitSuccess, nil
			if matchedProd != nil {
				res.Explanation = fmt.Sprintf("canonical-first (first token %q is canonical command %q; use 'sila do %q' to route phrase)", firstTok, cmdSpec.Name, phraseStr)
			} else {
				res.Canonical, res.Precedence, res.Safety = cloneSlice(cleanedArgs), PrecedenceCanonicalFirst, "none"
				res.Explanation = fmt.Sprintf("canonical-first (first token %q is canonical command %q)", firstTok, cmdSpec.Name)
			}
			return res
		}
		if parseRes.ExitCode == ExitSuccess && matchedProd != nil {
			res := buildBaseResult(redactedInput, phraseStr, parseRes, pass1, matchedProd, extraAfterDash, inv, warnings)
			if matchedProd.Precedence == PrecedencePhraseOnlyViaDo {
				res.DecidedBy, res.Decision = DecidedByCanonical, DecisionCanonical
				res.Explanation = fmt.Sprintf("phrase-only-via-do (use 'sila do %q')", phraseStr)
			} else {
				res.DecidedBy, res.Decision = DecidedByAuto, DecisionRoute
				res.Explanation = fmt.Sprintf("canonical-first (first token %q is not canonical)", firstTok)
			}
			return res
		}
		return buildNoMatchResult(redactedInput, phraseStr, nil, classifyFailureDecision(parseRes), parseRes, pass1, phraseWords, inv, warnings)
	}
	return RewriteResult{Input: redactedInput, Canonical: cloneSlice(cleanedArgs), DecidedBy: DecidedByNoMatch, Decision: DecisionNoMatch, ExitCode: ExitUsage}
}

func classifyFailureDecision(parseRes *ParseResult) string {
	if parseRes != nil && strings.Contains(strings.ToLower(parseRes.ErrorMessage), "ambiguous") {
		return DecisionClarify
	}
	return DecisionNoMatch
}

func inspectAndStripNoCCLI(args []string, catalog FlagCatalog) (bool, []string) {
	hasNoCCLI := false
	out := make([]string, 0, len(args))
	afterDash := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !afterDash && a == "--" {
			afterDash = true
			out = append(out, a)
			continue
		}
		if !afterDash {
			if a == "--no-ccli" || strings.EqualFold(a, "--no-ccli=true") || a == "--no-ccli=1" {
				hasNoCCLI = true
				continue
			}
			if strings.EqualFold(a, "--no-ccli=false") || a == "--no-ccli=0" {
				continue
			}
		}
		out = append(out, a)
	}
	return hasNoCCLI, out
}

// MatchesDenylist checks if post-flag-extraction phrase words match any entry in rawDenylist
// using case-insensitive, literal token-boundary prefix or exact matching.
func MatchesDenylist(phraseWords []string, rawDenylist string) (bool, string) {
	if len(phraseWords) == 0 || strings.TrimSpace(rawDenylist) == "" {
		return false, ""
	}
	entries := strings.Split(rawDenylist, ",")
	for _, rawEntry := range entries {
		entry := strings.TrimSpace(rawEntry)
		if entry == "" {
			continue
		}
		entryTokens := strings.Fields(entry)
		if len(entryTokens) == 0 || len(entryTokens) > len(phraseWords) {
			continue
		}
		match := true
		for i, et := range entryTokens {
			if !strings.EqualFold(phraseWords[i], et) {
				match = false
				break
			}
		}
		if match {
			return true, entry
		}
	}
	return false, ""
}

// normalizeInputForParse handles multi-word quoted tokens passed to `sila do` or `explain`,
// while preserving quoted single-argument slot values (such as `sila do "save memo" "use WAL"`)
// and trailing `-- <positionals...>`.
func normalizeInputForParse(args []string, inv *Inventory, flagMode FlagExtractionMode) ([]string, []string) {
	if len(args) == 0 {
		return args, nil
	}

	// Step 1: Try direct parse as-is.
	res1 := ParseWithOptions(args, inv, ParseOptions{FlagMode: flagMode})
	if res1.ExitCode == ExitSuccess && res1.AST != nil {
		return args, nil
	}

	// Step 2: If args contains `--`, check if parsing pre-`--` args succeeds when `--` has extra positionals.
	dashIdx := -1
	for i, a := range args {
		if a == "--" {
			dashIdx = i
			break
		}
	}
	if dashIdx > 0 && dashIdx+1 < len(args) {
		preDash := args[:dashIdx]
		postDash := args[dashIdx+1:]
		resPre := ParseWithOptions(preDash, inv, ParseOptions{FlagMode: flagMode})
		if resPre.ExitCode == ExitSuccess && resPre.AST != nil {
			return preDash, postDash
		}
	}

	// Step 3: Split multi-word non-flag tokens whose words are all known grammar keywords/glue,
	// keeping value arguments (like "use WAL" or "DB engine") intact as single tokens.
	knownKw := collectGrammarWords(inv)
	splitKwArgs := expandKeywordPrefixTokens(args, inv.FlagCatalog, knownKw)
	if !slicesEqual(splitKwArgs, args) {
		res2 := ParseWithOptions(splitKwArgs, inv, ParseOptions{FlagMode: flagMode})
		if res2.ExitCode == ExitSuccess && res2.AST != nil {
			return splitKwArgs, nil
		}
	}

	// Step 4: Full whitespace expansion of non-flag tokens before `--` (e.g. `sila do "release goal legacy-backlog" --yes`).
	fullSplitArgs := expandAllPreDashTokens(args, inv.FlagCatalog)
	if !slicesEqual(fullSplitArgs, args) {
		res3 := ParseWithOptions(fullSplitArgs, inv, ParseOptions{FlagMode: flagMode})
		if res3.ExitCode == ExitSuccess && res3.AST != nil {
			return fullSplitArgs, nil
		}
		// Even on error (e.g., forbidden phrase inside a single quoted string), return fullSplitArgs
		// if there was a single quoted phrase token containing spaces.
		if len(extractPreDashPositionalRaw(args, inv.FlagCatalog)) == 1 {
			return fullSplitArgs, nil
		}
	}

	return args, nil
}

func collectGrammarWords(inv *Inventory) map[string]bool {
	words := make(map[string]bool)
	if inv == nil {
		return words
	}
	for _, p := range inv.Productions {
		for _, el := range p.Elements {
			if el.Kind == ElementKeyword || el.Kind == ElementGlue {
				words[strings.ToLower(el.Value)] = true
			}
		}
	}
	return words
}

func expandKeywordPrefixTokens(args []string, catalog FlagCatalog, knownKw map[string]bool) []string {
	return expandPreDashTokens(args, catalog, func(fields []string) bool {
		if len(fields) <= 1 {
			return false
		}
		for _, f := range fields {
			if !knownKw[strings.ToLower(f)] {
				return false
			}
		}
		return true
	})
}

func expandAllPreDashTokens(args []string, catalog FlagCatalog) []string {
	return expandPreDashTokens(args, catalog, func(fields []string) bool { return len(fields) > 1 })
}

func expandPreDashTokens(args []string, catalog FlagCatalog, shouldSplit func([]string) bool) []string {
	var out []string
	afterDash := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !afterDash && a == "--" {
			afterDash = true
			out = append(out, a)
			continue
		}
		if !afterDash && strings.HasPrefix(a, "-") && len(a) > 1 {
			out = append(out, a)
			if !strings.Contains(a, "=") && catalog != nil {
				if spec, ok := catalog[a]; ok && spec.Arity == 1 && i+1 < len(args) && args[i+1] != "--" {
					i++
					out = append(out, args[i])
				}
			}
			continue
		}
		if !afterDash && strings.Contains(a, " ") {
			if fields := strings.Fields(a); shouldSplit(fields) {
				out = append(out, fields...)
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

func extractPreDashPositionalRaw(args []string, catalog FlagCatalog) []string {
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			if !strings.Contains(a, "=") && catalog != nil {
				if spec, ok := catalog[a]; ok && spec.Arity == 1 && i+1 < len(args) && args[i+1] != "--" {
					i++
				}
			}
			continue
		}
		pos = append(pos, a)
	}
	return pos
}

func buildBaseResult(redactedInput []string, phraseStr string, parseRes *ParseResult, pass1 *Pass1Result, matchedProd *Production, extraAfterDash []string, inv *Inventory, warnings []string) RewriteResult {
	res := RewriteResult{
		Input:    redactedInput,
		Phrase:   phraseStr,
		Tokens:   redactTokens(selectTokens(parseRes, pass1), inv),
		RawFlags: pass1.RawFlags,
		ExitCode: parseRes.ExitCode,
		Warnings: warnings,
	}
	if parseRes.AST != nil {
		res.AST = parseRes.AST
		res.Operands = redactMap(parseRes.AST.Operands, inv)
	}
	if matchedProd != nil && parseRes.ExitCode == ExitSuccess {
		res.Matched = true
		res.Production = matchedProd.ID
		res.ActionID = matchedProd.ActionID
		res.Tier = string(matchedProd.Tier)
		res.TargetReq = matchedProd.TargetPolicy
		res.Precedence = matchedProd.Precedence
		canon := cloneSlice(parseRes.CanonicalArgs)
		if len(extraAfterDash) > 0 {
			canon = append(canon, extraAfterDash...)
		}
		redactedCanon, _ := RedactArgs(canon, inv)
		res.Canonical = redactedCanon
		guardPreview := EvaluateGuardrails(parseRes.AST, GuardrailOptions{
			Prod:        matchedProd,
			Inventory:   inv,
			RawArgs:     redactedInput,
			Canonical:   redactedCanon,
			PreviewOnly: true,
		})
		if guardPreview.SafetyAction != "" {
			res.Safety = guardPreview.SafetyAction
		} else {
			res.Safety = safetyForTier(matchedProd.Tier)
		}
	}
	return res
}

func findMatchedProduction(parseRes *ParseResult, inv *Inventory) *Production {
	if parseRes == nil || parseRes.AST == nil || inv == nil {
		return nil
	}
	for _, p := range inv.Productions {
		if p.ID == parseRes.AST.ProductionID {
			return p
		}
	}
	for _, p := range inv.Productions {
		if p.ActionID == parseRes.AST.ActionID {
			return p
		}
	}
	return nil
}

func safetyForTier(tier MutationTier) string {
	switch tier {
	case TierDestructive:
		return "confirm"
	case TierMutation:
		return "preview"
	default:
		return "none"
	}
}

func positionalWords(pos []PositionalArg) []string {
	var words []string
	for _, p := range pos {
		if p.AfterDoubleDash {
			words = append(words, p.Raw)
		} else {
			words = append(words, strings.Fields(p.Raw)...)
		}
	}
	return words
}

func buildRedactedPhraseString(pos []PositionalArg, inv *Inventory) string {
	raw := make([]string, len(pos))
	for i, p := range pos {
		raw[i] = p.Raw
	}
	redacted, _ := RedactArgs(raw, inv)
	return strings.Join(redacted, " ")
}

func selectTokens(parseRes *ParseResult, pass1 *Pass1Result) []Token {
	if parseRes != nil && len(parseRes.Tokens) > 0 {
		return parseRes.Tokens
	}
	if pass1 != nil {
		return pass1.Tokens
	}
	return nil
}

func selectOperands(parseRes *ParseResult) map[string]string {
	if parseRes != nil && parseRes.AST != nil {
		return parseRes.AST.Operands
	}
	return nil
}

func redactTokens(tokens []Token, inv *Inventory) []Token {
	if len(tokens) == 0 {
		return nil
	}
	raws := make([]string, len(tokens))
	for i, t := range tokens {
		raws[i] = t.Raw
	}
	redactedRaws, _ := RedactArgs(raws, inv)
	out := make([]Token, len(tokens))
	for i, t := range tokens {
		out[i] = t
		out[i].Raw = redactedRaws[i]
		if t.SlotName != nil && IsSensitiveKeyName(*t.SlotName) && !strings.HasPrefix(out[i].Raw, "<redacted:") {
			out[i].Raw = RedactToken(t.Raw)
		}
	}
	return out
}

func redactMap(m map[string]string, inv *Inventory) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if IsSensitiveKeyName(k) || IsSensitiveTokenValue(v) {
			out[k] = RedactToken(v)
		} else {
			out[k] = v
		}
	}
	return out
}

func redactSecretStringsInText(text string, rawArgs []string, inv *Inventory) string {
	redacted, has := RedactArgs(rawArgs, inv)
	if !has {
		return text
	}
	for i, orig := range rawArgs {
		if i < len(redacted) && orig != redacted[i] && orig != "" {
			text = strings.ReplaceAll(text, orig, redacted[i])
		}
	}
	return text
}

// SuggestPhrases returns up to 3 closest conversational phrase examples for an unknown phrase.
func SuggestPhrases(words []string, inv *Inventory) []string {
	if inv == nil || len(inv.Productions) == 0 {
		return []string{"show status", "list goals", "refresh context"}
	}
	query := strings.ToLower(strings.Join(words, " "))
	firstWord := ""
	if len(words) > 0 {
		firstWord = strings.ToLower(words[0])
	}
	type scored struct {
		phrase string
		score  int
	}
	var cands []scored
	seen := make(map[string]bool)
	for _, p := range inv.Productions {
		ex := strings.Trim(strings.TrimPrefix(strings.TrimPrefix(p.Examples.Conversational, "sila do "), "sila "), `"`)
		if ex == "" || seen[ex] {
			continue
		}
		seen[ex] = true
		s := 100
		if firstWord != "" && len(p.Elements) > 0 && strings.EqualFold(p.Elements[0].Value, firstWord) {
			s -= 50
		}
		for _, w := range words {
			for _, el := range p.Elements {
				if el.Value != "" && strings.EqualFold(el.Value, w) {
					s -= 20
				}
			}
		}
		s += levenshteinBounded(query, strings.ToLower(ex), 40)
		cands = append(cands, scored{phrase: ex, score: s})
	}
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && (cands[j].score < cands[j-1].score || (cands[j].score == cands[j-1].score && cands[j].phrase < cands[j-1].phrase)); j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
	var out []string
	for i := 0; i < len(cands) && i < 3; i++ {
		out = append(out, cands[i].phrase)
	}
	return out
}

func levenshteinBounded(a, b string, maxDist int) int {
	if len(a) > 64 {
		a = a[:64]
	}
	if len(b) > 64 {
		b = b[:64]
	}
	la, lb := len(a), len(b)
	prev, curr := make([]int, lb+1), make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j] + 1
			if curr[j-1]+1 < m {
				m = curr[j-1] + 1
			}
			if prev[j-1]+cost < m {
				m = prev[j-1] + cost
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	if prev[lb] > maxDist {
		return maxDist
	}
	return prev[lb]
}

func buildNoMatchResult(redactedInput []string, phraseStr string, canonical []string, decision string, parseRes *ParseResult, pass1 *Pass1Result, phraseWords []string, inv *Inventory, warnings []string) RewriteResult {
	code := ExitUsage
	var errMsg string
	if parseRes != nil {
		if parseRes.ExitCode != ExitSuccess {
			code = parseRes.ExitCode
		}
		errMsg = parseRes.ErrorMessage
	}
	return RewriteResult{
		Input:       redactedInput,
		Phrase:      phraseStr,
		Matched:     false,
		Canonical:   canonical,
		DecidedBy:   DecidedByNoMatch,
		Decision:    decision,
		ExitCode:    code,
		Tokens:      redactTokens(selectTokens(parseRes, pass1), inv),
		RawFlags:    pass1.RawFlags,
		Explanation: errMsg,
		Suggestions: SuggestPhrases(phraseWords, inv),
		Warnings:    warnings,
		Err:         errors.New(firstNonEmptyStr(errMsg, "unknown conversational phrase")),
	}
}

func cloneSlice(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstNonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
