package ccli

import (
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/config"
)

// Provider token prefixes recognized as secret values anywhere in a phrase.
var sensitivePrefixes = []string{
	"ghp_",
	"gho_",
	"ghu_",
	"ghs_",
	"ghr_",
	"github_pat_",
	"sk_live_",
	"sk_test_",
	"sk_edge_",
	"xoxb-",
	"xoxp-",
	"AKIA",
	"AIzaSy",
	"tvly-",
	"sgp_",
}

// Generic secret-introducing positional keywords.
var sensitiveKeywords = map[string]bool{
	"password":   true,
	"secret":     true,
	"token":      true,
	"api_key":    true,
	"apikey":     true,
	"credential": true,
	"auth_token": true,
	"bot_token":  true,
}

// SafeConfigSecretRemediation is the honest remediation message for forbidden config secret phrases.
// It names only channels that actually exist and never invents --token-env/--token-file/--token-stdin.
const SafeConfigSecretRemediation = "Policy violation: secret configuration values must not be passed in positional conversational CLI phrases (Conversational secret assignment is not supported; configure secrets via environment variables or edit config.json directly with mode 0600)"

// PositionalSecretViolationMsg is the standard refusal message when a secret is passed positionally.
const PositionalSecretViolationMsg = "Policy violation: secrets must not be passed as positional arguments"

// RedactToken replaces a token value with <redacted:N chars>.
func RedactToken(s string) string {
	return fmt.Sprintf("<redacted:%d chars>", len(s))
}

// IsSensitiveTokenValue checks if a standalone token value matches a known provider secret prefix.
func IsSensitiveTokenValue(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	for _, prefix := range sensitivePrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// IsSensitiveKeyName reports whether a key or keyword name denotes a secret credential,
// consulting authoritative internal/config metadata (ExplainKey / IsSecret) and generic secret keywords.
func IsSensitiveKeyName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	if sensitiveKeywords[lower] {
		return true
	}
	if doc, ok := config.ExplainKey(lower); ok {
		return doc.IsSecret
	}
	norm := strings.ReplaceAll(strings.ReplaceAll(lower, ".", "_"), "-", "_")
	if doc, ok := config.ExplainKey(norm); ok {
		return doc.IsSecret
	}
	return config.IsSecret(lower) || config.IsSecret(norm)
}

// IsAllowedNonSecretConfigKey reports whether key is a known, non-secret configuration key in internal/config.
func IsAllowedNonSecretConfigKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	if doc, ok := config.ExplainKey(lower); ok {
		return !doc.IsSecret
	}
	norm := strings.ReplaceAll(strings.ReplaceAll(lower, ".", "_"), "-", "_")
	if doc, ok := config.ExplainKey(norm); ok {
		return !doc.IsSecret
	}
	return false
}

// IsCatalogForbiddenKey checks whether key is listed in any ForbiddenPhraseSpec.SensitiveKeys in inv.
func IsCatalogForbiddenKey(key string, inv *Inventory) bool {
	if IsSensitiveKeyName(key) {
		return true
	}
	if inv == nil || inv.Catalog == nil {
		return false
	}
	for _, fp := range inv.Catalog.ForbiddenPhrases {
		for _, k := range fp.SensitiveKeys {
			if strings.EqualFold(k, key) {
				return true
			}
		}
	}
	return false
}

// DetectSecret checks if any AST operand, slot, or raw argument contains a secret credential.
// Used by both the parser and guardrail stages so secret detection has a single source of truth.
func DetectSecret(ast *AST, rawArgs []string) bool {
	ok, _ := DetectSecretDetailed(ast, nil, rawArgs, nil)
	return ok
}

// DetectSecretDetailed checks AST operands, production slot metadata, raw arguments, and forbidden config secret phrases.
// Returns (found, safeReasonMessage).
func DetectSecretDetailed(ast *AST, prod *Production, rawArgs []string, inv *Inventory) (bool, string) {
	if prod != nil {
		for _, s := range prod.Slots {
			if s.Sensitive {
				return true, PositionalSecretViolationMsg
			}
		}
	}

	if ast != nil {
		for name, val := range ast.Operands {
			if val == "" {
				continue
			}
			if IsSensitiveKeyName(name) || IsSensitiveTokenValue(val) {
				return true, PositionalSecretViolationMsg
			}
		}
		if (ast.ActionID == "config.set" || ast.ProductionID == "sila_config_set") && ast.Operands != nil {
			key := firstNonEmptyStr(ast.Operands["key"], ast.Target)
			if key != "" && IsCatalogForbiddenKey(key, inv) {
				return true, SafeConfigSecretRemediation
			}
		}
	}

	// Inspect raw tokens for forbidden config secret phrases, sensitive keywords followed by values, or provider prefixes.
	words := extractPreDashWords(rawArgs, inv)
	if isForbiddenConfigSecretWords(words, inv) {
		return true, SafeConfigSecretRemediation
	}

	afterDash := false
	for i := 0; i < len(rawArgs); i++ {
		arg := rawArgs[i]
		if !afterDash && arg == "--" {
			afterDash = true
			continue
		}
		if !afterDash && strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "=") {
				parts := strings.SplitN(arg, "=", 2)
				fKey := strings.TrimLeft(parts[0], "-")
				fVal := parts[1]
				if IsSensitiveKeyName(fKey) || IsSensitiveTokenValue(fVal) {
					return true, PositionalSecretViolationMsg
				}
			} else {
				fKey := strings.TrimLeft(arg, "-")
				if IsSensitiveKeyName(fKey) && i+1 < len(rawArgs) && rawArgs[i+1] != "--" {
					return true, PositionalSecretViolationMsg
				}
			}
			continue
		}
		if IsSensitiveTokenValue(arg) {
			return true, PositionalSecretViolationMsg
		}
		lower := strings.ToLower(strings.TrimSpace(arg))
		if sensitiveKeywords[lower] && i+1 < len(rawArgs) && !strings.HasPrefix(rawArgs[i+1], "-") {
			return true, PositionalSecretViolationMsg
		}
	}

	return false, ""
}

func isForbiddenConfigSecretWords(words []string, inv *Inventory) bool {
	if len(words) < 3 {
		return false
	}
	if !strings.EqualFold(words[0], "set") || !strings.EqualFold(words[1], "config") {
		return false
	}
	// Find "to" or take words[2]
	toIdx := -1
	for i := 3; i < len(words)-1; i++ {
		if strings.EqualFold(words[i], "to") {
			toIdx = i
			break
		}
	}
	var keyCandidate string
	if toIdx > 2 {
		keyCandidate = strings.ToLower(strings.TrimSpace(strings.Join(words[2:toIdx], " ")))
	} else {
		keyCandidate = strings.ToLower(strings.TrimSpace(words[2]))
	}
	return IsCatalogForbiddenKey(keyCandidate, inv)
}

// checkPositionalSecretPolicy runs during Pass 2 before production matching to reject positional secrets with ExitPolicy (3).
func checkPositionalSecretPolicy(posArgs []PositionalArg, pass1 *Pass1Result) *ParseResult {
	if len(posArgs) == 0 {
		return nil
	}

	// 1. Check standalone secret token prefixes (ghp_, sk_live_, AKIA, etc.) in any positional argument
	for _, arg := range posArgs {
		if IsSensitiveTokenValue(arg.Raw) {
			pass1.Tokens[arg.Index] = Token{
				Index:    arg.Index,
				Raw:      RedactToken(arg.Raw),
				Class:    TokenOperand,
				SlotName: StringPtr("secret"),
			}
			return &ParseResult{
				Tokens:       pass1.Tokens,
				ExitCode:     ExitPolicy,
				ErrorMessage: PositionalSecretViolationMsg,
			}
		}
	}

	// 2. Check sensitive keyword followed by a secret value (e.g. "login user admin password secret" or "token mytok")
	secIdx := -1
	for i, arg := range posArgs {
		lower := strings.ToLower(strings.TrimSpace(arg.Raw))
		if sensitiveKeywords[lower] {
			secIdx = i
			break
		}
	}

	isAuthContext := strings.EqualFold(posArgs[0].Raw, "login") || strings.EqualFold(posArgs[0].Raw, "auth")
	if secIdx < 0 || (!isAuthContext && secIdx+1 >= len(posArgs)) {
		return nil
	}
	// Do not trigger on "set config <key> to <val>" here; checkForbiddenPhrasePolicy handles config secret keys with its specific remediation.
	if len(posArgs) >= 2 && strings.EqualFold(posArgs[0].Raw, "set") && strings.EqualFold(posArgs[1].Raw, "config") {
		return nil
	}

	pass1.Tokens[posArgs[0].Index] = Token{Index: posArgs[0].Index, Raw: posArgs[0].Raw, Class: TokenKeyword}
	for j := 1; j < secIdx; j++ {
		lower := strings.ToLower(posArgs[j].Raw)
		if lower == "user" || lower == "username" {
			pass1.Tokens[posArgs[j].Index] = Token{Index: posArgs[j].Index, Raw: posArgs[j].Raw, Class: TokenKeyword}
			if j+1 < secIdx {
				pass1.Tokens[posArgs[j+1].Index] = Token{
					Index:    posArgs[j+1].Index,
					Raw:      posArgs[j+1].Raw,
					Class:    TokenOperand,
					SlotName: StringPtr("username"),
				}
				j++
			}
		} else if j == secIdx-1 {
			pass1.Tokens[posArgs[j].Index] = Token{
				Index:    posArgs[j].Index,
				Raw:      posArgs[j].Raw,
				Class:    TokenOperand,
				SlotName: StringPtr("username"),
			}
		} else {
			pass1.Tokens[posArgs[j].Index] = Token{Index: posArgs[j].Index, Raw: posArgs[j].Raw, Class: TokenKeyword}
		}
	}

	pass1.Tokens[posArgs[secIdx].Index] = Token{Index: posArgs[secIdx].Index, Raw: posArgs[secIdx].Raw, Class: TokenKeyword}
	if secIdx+1 < len(posArgs) {
		pass1.Tokens[posArgs[secIdx+1].Index] = Token{
			Index:    posArgs[secIdx+1].Index,
			Raw:      RedactToken(posArgs[secIdx+1].Raw),
			Class:    TokenOperand,
			SlotName: StringPtr(strings.ToLower(posArgs[secIdx].Raw)),
		}
	}
	for j := secIdx + 2; j < len(posArgs); j++ {
		pass1.Tokens[posArgs[j].Index] = Token{Index: posArgs[j].Index, Raw: RedactToken(posArgs[j].Raw), Class: TokenOperand}
	}

	return &ParseResult{
		Tokens:       pass1.Tokens,
		ExitCode:     ExitPolicy,
		ErrorMessage: PositionalSecretViolationMsg,
	}
}

// checkForbiddenPhrasePolicy checks forbidden phrase specifications (including set config <secret-key> to <val>).
func checkForbiddenPhrasePolicy(posArgs []PositionalArg, inventory *Inventory, pass1 *Pass1Result) *ParseResult {
	if len(posArgs) == 0 {
		return nil
	}
	if len(posArgs) == 2 &&
		(strings.EqualFold(posArgs[0].Raw, "clean") || strings.EqualFold(posArgs[0].Raw, "prune") || strings.EqualFold(posArgs[0].Raw, "remove")) &&
		(strings.EqualFold(posArgs[1].Raw, "worktrees") || strings.EqualFold(posArgs[1].Raw, "worktree")) {
		for _, pa := range posArgs {
			pass1.Tokens[pa.Index] = Token{Index: pa.Index, Raw: pa.Raw, Class: TokenKeyword}
		}
		return &ParseResult{
			Tokens:       pass1.Tokens,
			ExitCode:     ExitUsage,
			ErrorMessage: "Missing required target: specify a worktree name ('sila remove worktree <name>') or all ('sila prune all worktrees')",
		}
	}

	if len(posArgs) >= 4 && strings.EqualFold(posArgs[0].Raw, "set") && strings.EqualFold(posArgs[1].Raw, "config") {
		toIdx := -1
		for k := 2; k < len(posArgs)-1; k++ {
			if strings.EqualFold(posArgs[k].Raw, "to") {
				toIdx = k
				break
			}
		}
		var keyCandidate string
		valStart := 3
		if toIdx > 2 {
			keyParts := make([]string, 0, toIdx-2)
			for k := 2; k < toIdx; k++ {
				keyParts = append(keyParts, posArgs[k].Raw)
			}
			keyCandidate = strings.ToLower(strings.TrimSpace(strings.Join(keyParts, " ")))
			valStart = toIdx + 1
		} else {
			keyCandidate = strings.ToLower(strings.TrimSpace(posArgs[2].Raw))
			valStart = 3
		}

		if IsCatalogForbiddenKey(keyCandidate, inventory) {
			for k := valStart; k < len(posArgs); k++ {
				pass1.Tokens[posArgs[k].Index] = Token{
					Index:    posArgs[k].Index,
					Raw:      RedactToken(posArgs[k].Raw),
					Class:    TokenOperand,
					SlotName: StringPtr("secret_value"),
				}
			}
			msg := SafeConfigSecretRemediation
			if inventory != nil && inventory.Catalog != nil {
				for _, fp := range inventory.Catalog.ForbiddenPhrases {
					if fp.Reason != "" && fp.Remediation != "" {
						msg = fmt.Sprintf("Policy violation: %s (%s)", fp.Reason, fp.Remediation)
						break
					}
				}
			}
			return &ParseResult{
				Tokens:       pass1.Tokens,
				ExitCode:     ExitPolicy,
				ErrorMessage: msg,
			}
		}
	}
	return nil
}

// RedactArgs returns a copy of args with any secret-bearing or forbidden tokens replaced by <redacted:N chars>,
// and reports whether any secret/forbidden token was found.
func RedactArgs(args []string, inv *Inventory) ([]string, bool) {
	if len(args) == 0 {
		return []string{}, false
	}
	out := make([]string, len(args))
	copy(out, args)
	foundSecret := false

	words := extractPreDashWords(args, inv)
	var forbiddenKey string
	if len(words) >= 3 && strings.EqualFold(words[0], "set") && strings.EqualFold(words[1], "config") {
		candidateKey := strings.ToLower(words[2])
		if IsCatalogForbiddenKey(candidateKey, inv) {
			forbiddenKey = candidateKey
			foundSecret = true
		}
	}

	afterDash := false
	redactAfterForbiddenTo := false
	seenForbiddenKey := false

	for i := 0; i < len(out); i++ {
		tok := out[i]
		if !afterDash && tok == "--" {
			afterDash = true
			continue
		}
		if !afterDash && strings.HasPrefix(tok, "-") && strings.Contains(tok, "=") {
			parts := strings.SplitN(tok, "=", 2)
			fKey := strings.TrimLeft(parts[0], "-")
			fVal := parts[1]
			if IsSensitiveKeyName(fKey) || IsSensitiveTokenValue(fVal) {
				out[i] = parts[0] + "=" + RedactToken(fVal)
				foundSecret = true
			}
			continue
		}
		if !afterDash && strings.HasPrefix(tok, "-") {
			fKey := strings.TrimLeft(tok, "-")
			if IsSensitiveKeyName(fKey) && i+1 < len(out) && out[i+1] != "--" {
				out[i+1] = RedactToken(out[i+1])
				foundSecret = true
				i++
			}
			continue
		}
		if IsSensitiveTokenValue(tok) {
			out[i] = RedactToken(tok)
			foundSecret = true
			continue
		}
		if forbiddenKey != "" && strings.Contains(tok, " ") {
			fields := strings.Fields(tok)
			if len(fields) >= 4 && strings.EqualFold(fields[0], "set") && strings.EqualFold(fields[1], "config") && strings.EqualFold(fields[2], forbiddenKey) {
				startIdx := 3
				if strings.EqualFold(fields[3], "to") && len(fields) >= 5 {
					startIdx = 4
				}
				valStr := strings.Join(fields[startIdx:], " ")
				prefixStr := strings.Join(fields[:startIdx], " ")
				out[i] = prefixStr + " " + RedactToken(valStr)
				foundSecret = true
				continue
			}
		}
		if forbiddenKey != "" {
			if !seenForbiddenKey && strings.EqualFold(tok, forbiddenKey) {
				seenForbiddenKey = true
				continue
			}
			if seenForbiddenKey && !redactAfterForbiddenTo {
				if strings.EqualFold(tok, "to") {
					redactAfterForbiddenTo = true
					continue
				}
				out[i] = RedactToken(tok)
				foundSecret = true
				continue
			}
			if redactAfterForbiddenTo {
				if !afterDash && strings.HasPrefix(tok, "-") {
					continue
				}
				out[i] = RedactToken(tok)
				foundSecret = true
				continue
			}
		}
		lower := strings.ToLower(tok)
		if sensitiveKeywords[lower] && i+1 < len(out) && !strings.HasPrefix(out[i+1], "-") {
			out[i+1] = RedactToken(out[i+1])
			foundSecret = true
			i++
		}
	}

	return out, foundSecret
}
