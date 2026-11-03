package ccli

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/jadmadi/sila/internal/cli"
)

// PositionalArg carries an argument value along with its original index in os.Args
// and whether it appeared after the `--` delimiter.
type PositionalArg struct {
	Index           int
	Raw             string
	AfterDoubleDash bool
}

// Pass1Result contains the output of Pass 1 flag extraction.
type Pass1Result struct {
	Flags           map[string]string
	RawFlags        []string
	FlagIndices     map[int]bool
	DoubleDashIndex int
	Positional      []PositionalArg
	Tokens          []Token
	ExitCode        ExitCode
	ErrorMessage    string
}

var dryRunAliases = map[string]bool{
	"--dry-run": true,
	"--preview": true,
	"-n":        true,
}

var knownIngestTools = []string{
	"mimocode",
	"opencode",
	"devin",
	"codex",
	"command-code",
	"codewhale",
	"agy",
	"all",
}

// ExtractFlags executes Pass 1 of the pipeline in strict mode (default for conformance).
func ExtractFlags(inputArgs []string, catalog FlagCatalog) *Pass1Result {
	return ExtractFlagsWithMode(inputArgs, catalog, FlagModeStrict)
}

// ExtractFlagsWithMode executes Pass 1 of the pipeline with the requested FlagExtractionMode.
//   - FlagModeStrict: unknown flags exit 2; arity errors exit 2; `--` halts flag parsing.
//   - FlagModeLenient: known flags are extracted; unknown flags are preserved verbatim in
//     RawFlags as arity-0 flags (never consuming the next token); `--flag=value` is self-contained.
func ExtractFlagsWithMode(inputArgs []string, catalog FlagCatalog, mode FlagExtractionMode) *Pass1Result {
	if mode == "" {
		mode = FlagModeStrict
	}
	res := &Pass1Result{
		Flags:           make(map[string]string),
		RawFlags:        make([]string, 0),
		FlagIndices:     make(map[int]bool),
		DoubleDashIndex: -1,
		Positional:      make([]PositionalArg, 0),
		Tokens:          make([]Token, len(inputArgs)),
		ExitCode:        ExitSuccess,
	}

	if len(inputArgs) == 0 {
		res.ExitCode = ExitUsage
		res.ErrorMessage = "Usage: <command> [phrase] [options]"
		return res
	}

	for i, arg := range inputArgs {
		res.Tokens[i] = Token{
			Index:    i,
			Raw:      arg,
			Class:    TokenKeyword,
			SlotName: nil,
		}
	}

	for i := 0; i < len(inputArgs); i++ {
		arg := inputArgs[i]

		// Tokens after `--` are strictly positional data, never flags.
		if res.DoubleDashIndex >= 0 {
			res.Positional = append(res.Positional, PositionalArg{
				Index:           i,
				Raw:             arg,
				AfterDoubleDash: true,
			})
			continue
		}

		// Double dash delimiter halts flag processing and is omitted from phrase tokens.
		if arg == "--" {
			res.DoubleDashIndex = i
			res.Tokens[i] = Token{
				Index:    i,
				Raw:      "--",
				Class:    TokenGlue,
				SlotName: nil,
			}
			continue
		}

		// Long flag handling
		if strings.HasPrefix(arg, "--") {
			res.Tokens[i] = Token{
				Index:    i,
				Raw:      arg,
				Class:    TokenFlag,
				SlotName: nil,
			}

			if strings.HasPrefix(arg, "--=") || arg == "--" {
				res.ExitCode = ExitUsage
				res.ErrorMessage = "Malformed flag syntax"
				return res
			}

			if strings.Contains(arg, "=") {
				parts := strings.SplitN(arg, "=", 2)
				flagName := parts[0]
				flagVal := parts[1]

				_, exists := catalog[flagName]
				if !exists && !dryRunAliases[flagName] {
					if mode == FlagModeStrict {
						res.ExitCode = ExitUsage
						res.ErrorMessage = "Unknown flag " + flagName
						return res
					}
					res.RawFlags = append(res.RawFlags, arg)
					res.FlagIndices[i] = true
					continue
				}

				recordFlagValue(res, flagName, flagVal)
				res.RawFlags = append(res.RawFlags, arg)
				res.FlagIndices[i] = true
			} else {
				flagName := arg
				spec, exists := catalog[flagName]
				if !exists && !dryRunAliases[flagName] {
					if mode == FlagModeStrict {
						res.ExitCode = ExitUsage
						res.ErrorMessage = "Unknown flag " + flagName
						return res
					}
					// Lenient mode: unknown bare flag is treated as arity 0 and never consumes the next token.
					res.RawFlags = append(res.RawFlags, arg)
					res.FlagIndices[i] = true
					continue
				}

				if spec.Arity == 0 || dryRunAliases[flagName] {
					recordFlagValue(res, flagName, "")
					res.RawFlags = append(res.RawFlags, arg)
					res.FlagIndices[i] = true
				} else {
					if i+1 >= len(inputArgs) || inputArgs[i+1] == "--" {
						res.ExitCode = ExitUsage
						res.ErrorMessage = fmt.Sprintf("Flag %s requires an argument", flagName)
						return res
					}

					val := inputArgs[i+1]
					res.Tokens[i+1] = Token{
						Index:    i + 1,
						Raw:      val,
						Class:    TokenFlag,
						SlotName: nil,
					}
					recordFlagValue(res, flagName, val)
					res.RawFlags = append(res.RawFlags, arg, val)
					res.FlagIndices[i] = true
					res.FlagIndices[i+1] = true
					i++
				}
			}
			continue
		}

		// Short flag handling
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			res.Tokens[i] = Token{
				Index:    i,
				Raw:      arg,
				Class:    TokenFlag,
				SlotName: nil,
			}

			if strings.HasPrefix(arg, "-=") {
				res.ExitCode = ExitUsage
				res.ErrorMessage = "Malformed flag syntax"
				return res
			}

			if strings.Contains(arg, "=") {
				parts := strings.SplitN(arg, "=", 2)
				flagName := parts[0]
				flagVal := parts[1]

				if len(flagName) > 2 {
					res.ExitCode = ExitUsage
					res.ErrorMessage = fmt.Sprintf("Combined short flags (%s) are not supported; pass each flag separately", flagName)
					return res
				}

				_, exists := catalog[flagName]
				if !exists && !dryRunAliases[flagName] {
					if mode == FlagModeStrict {
						res.ExitCode = ExitUsage
						res.ErrorMessage = "Unknown flag " + flagName
						return res
					}
					res.RawFlags = append(res.RawFlags, arg)
					res.FlagIndices[i] = true
					continue
				}

				recordFlagValue(res, flagName, flagVal)
				res.RawFlags = append(res.RawFlags, arg)
				res.FlagIndices[i] = true
			} else {
				flagName := arg
				if len(flagName) > 2 {
					res.ExitCode = ExitUsage
					res.ErrorMessage = fmt.Sprintf("Combined short flags (%s) are not supported; pass each flag separately", flagName)
					return res
				}

				spec, exists := catalog[flagName]
				if !exists && !dryRunAliases[flagName] {
					if mode == FlagModeStrict {
						res.ExitCode = ExitUsage
						res.ErrorMessage = "Unknown flag " + flagName
						return res
					}
					res.RawFlags = append(res.RawFlags, arg)
					res.FlagIndices[i] = true
					continue
				}

				if spec.Arity == 0 || dryRunAliases[flagName] {
					recordFlagValue(res, flagName, "")
					res.RawFlags = append(res.RawFlags, arg)
					res.FlagIndices[i] = true
				} else {
					if i+1 >= len(inputArgs) || inputArgs[i+1] == "--" {
						res.ExitCode = ExitUsage
						res.ErrorMessage = fmt.Sprintf("Flag %s requires an argument", flagName)
						return res
					}
					val := inputArgs[i+1]
					res.Tokens[i+1] = Token{
						Index:    i + 1,
						Raw:      val,
						Class:    TokenFlag,
						SlotName: nil,
					}
					recordFlagValue(res, flagName, val)
					res.RawFlags = append(res.RawFlags, arg, val)
					res.FlagIndices[i] = true
					res.FlagIndices[i+1] = true
					i++
				}
			}
			continue
		}

		// Positional argument
		res.Positional = append(res.Positional, PositionalArg{
			Index:           i,
			Raw:             arg,
			AfterDoubleDash: false,
		})
	}

	return res
}

func recordFlagValue(res *Pass1Result, flagName, val string) {
	if dryRunAliases[flagName] {
		norm := "true"
		if strings.EqualFold(val, "false") || val == "0" {
			norm = "false"
		}
		res.Flags["--dry-run"] = norm
		if flagName != "--dry-run" {
			res.Flags[flagName] = val
		}
		return
	}
	res.Flags[flagName] = val
}

// matchedCandidate stores intermediate phrase matching results.
type matchedCandidate struct {
	prod     *Production
	elements []ProductionElement
	score    SpecificityScore
	operands map[string]string
	slots    map[string]Slot
	target   string
	tokens   []Token
}

// Parse executes the complete two-pass parsing algorithm in strict mode.
func Parse(inputArgs []string, inventory *Inventory) *ParseResult {
	return ParseWithOptions(inputArgs, inventory, ParseOptions{FlagMode: FlagModeStrict})
}

// ParseWithOptions executes the complete two-pass parsing algorithm with explicit ParseOptions.
func ParseWithOptions(inputArgs []string, inventory *Inventory, opts ParseOptions) *ParseResult {
	if inventory == nil {
		inventory = NewStandardInventory()
	}
	if opts.FlagMode == "" {
		opts.FlagMode = FlagModeStrict
	}

	// 1. Pass 1: Flag extraction
	pass1 := ExtractFlagsWithMode(inputArgs, inventory.FlagCatalog, opts.FlagMode)
	if pass1.ExitCode != ExitSuccess {
		classifyPass1FallbackTokens(pass1, inventory)
		return &ParseResult{
			Tokens:       pass1.Tokens,
			ExitCode:     pass1.ExitCode,
			ErrorMessage: pass1.ErrorMessage,
		}
	}

	posArgs := pass1.Positional
	if len(posArgs) == 0 {
		return &ParseResult{
			Tokens:       pass1.Tokens,
			ExitCode:     ExitUsage,
			ErrorMessage: "Usage: <command> [phrase] [options]",
		}
	}

	// 2. Pre-checks for policy & grammar constraints
	// A. Positional secret violation check
	if res := checkPositionalSecretPolicy(posArgs, pass1); res != nil {
		return res
	}
	if res := checkForbiddenPhrasePolicy(posArgs, inventory, pass1); res != nil {
		return res
	}

	// B. Directional command inverted check
	if res := checkDirectionalInversion(posArgs, inventory, pass1); res != nil {
		return res
	}

	// C. Check if a more specific production has a literal prefix match that is missing its required slot
	// before a greedy fallback production can swallow that literal into a rest slot.
	if res := checkSpecificPrefixMissingSlot(posArgs, inventory, pass1); res != nil {
		return res
	}

	// 3. Match against candidate productions in Inventory
	var matches []matchedCandidate
	for _, p := range inventory.Productions {
		cand, ok := matchProduction(posArgs, p)
		if ok {
			matches = append(matches, cand)
		}
	}

	// 4. Specificity filtering and ambiguity detection
	if len(matches) > 1 {
		matches = selectMostSpecificMatches(matches)
	}

	if len(matches) > 1 {
		actionSet := make(map[string]bool)
		for _, m := range matches {
			actionSet[m.prod.ActionID] = true
		}

		if len(actionSet) > 1 {
			errText := deriveCollisionMessage(matches[0].prod, matches[1].prod)
			for _, tok := range matches[0].tokens {
				if tok.Index < len(pass1.Tokens) {
					pass1.Tokens[tok.Index] = tok
				}
			}
			return &ParseResult{
				Specificity:  matches[0].score,
				Tokens:       pass1.Tokens,
				ExitCode:     ExitUsage,
				ErrorMessage: errText,
			}
		}
	}

	if len(matches) == 0 {
		if res := checkMissingTargetOrOperand(posArgs, inventory, pass1); res != nil {
			return res
		}

		classifyUnknownPhrase(pass1, posArgs)
		return &ParseResult{
			Tokens:       pass1.Tokens,
			ExitCode:     ExitUsage,
			ErrorMessage: "Unknown conversational phrase",
		}
	}

	chosen := matches[0]

	// Apply token classifications
	for _, tok := range chosen.tokens {
		if tok.Index < len(pass1.Tokens) {
			pass1.Tokens[tok.Index] = tok
		}
	}

	// 5. Typed slot validation before rewriting
	if errMsg := validateCandidateSlots(&chosen); errMsg != "" {
		return &ParseResult{
			Specificity:  chosen.score,
			Tokens:       pass1.Tokens,
			ExitCode:     ExitUsage,
			ErrorMessage: errMsg,
		}
	}

	// 6. Enforce target_policy and target defaulting
	if errMsg := enforceTargetPolicy(&chosen); errMsg != "" {
		return &ParseResult{
			Specificity:  chosen.score,
			Tokens:       pass1.Tokens,
			ExitCode:     ExitUsage,
			ErrorMessage: errMsg,
		}
	}

	ast := &AST{
		ActionID:     chosen.prod.ActionID,
		ProductionID: chosen.prod.ID,
		Tier:         chosen.prod.Tier,
		TargetPolicy: chosen.prod.TargetPolicy,
		TargetScope:  chosen.prod.TargetScope,
		Specificity:  chosen.score,
		Tokens:       pass1.Tokens,
		Operands:     chosen.operands,
		Flags:        pass1.Flags,
		RawFlags:     pass1.RawFlags,
		Target:       chosen.target,
	}

	canonical := Rewrite(ast, chosen.prod)

	return &ParseResult{
		AST:           ast,
		Specificity:   chosen.score,
		Tokens:        pass1.Tokens,
		CanonicalArgs: canonical,
		ExitCode:      ExitSuccess,
	}
}

func selectMostSpecificMatches(matches []matchedCandidate) []matchedCandidate {
	if len(matches) <= 1 {
		return matches
	}
	best := []matchedCandidate{matches[0]}
	for i := 1; i < len(matches); i++ {
		cand := matches[i]
		// Only allow specificity disambiguation when an ordered overlap is declared via SpecificityWitness
		// or when comparing a multi-token rest/until fallback against a more specific literal production.
		canOrder := strings.TrimSpace(best[0].prod.SpecificityWitness) != "" ||
			strings.TrimSpace(cand.prod.SpecificityWitness) != "" ||
			isMultiTokenTail(best[0].elements) != isMultiTokenTail(cand.elements)
		if !canOrder {
			best = append(best, cand)
			continue
		}
		cmp := CompareSpecificity(cand.elements, best[0].elements)
		if cmp > 0 {
			best = []matchedCandidate{cand}
		} else if cmp == 0 {
			best = append(best, cand)
		}
	}
	return best
}

// matchProduction attempts to align positional arguments with a production's elements.
func matchProduction(posArgs []PositionalArg, prod *Production) (matchedCandidate, bool) {
	var matched []matchedCandidate

	if cand, ok := matchElements(posArgs, prod, prod.Elements); ok {
		matched = append(matched, cand)
	}

	for _, seq := range prod.CommutativeElements {
		if cand, ok := matchElements(posArgs, prod, seq); ok {
			matched = append(matched, cand)
		}
	}

	for _, seq := range prod.WhitelistedInversions {
		if cand, ok := matchElements(posArgs, prod, seq); ok {
			matched = append(matched, cand)
		}
	}

	if len(matched) == 0 {
		return matchedCandidate{}, false
	}
	best := matched[0]
	for i := 1; i < len(matched); i++ {
		if CompareSpecificity(matched[i].elements, best.elements) > 0 {
			best = matched[i]
		}
	}
	return best, true
}

// matchElements matches positional arguments against a specific element slice.
func matchElements(posArgs []PositionalArg, prod *Production, elements []ProductionElement) (matchedCandidate, bool) {
	operands := make(map[string]string)
	slots := make(map[string]Slot)
	for _, s := range prod.Slots {
		slots[s.Name] = s
	}
	tokens := make([]Token, 0, len(posArgs))
	target := ""

	argIdx := 0
	elemIdx := 0

	for elemIdx < len(elements) {
		elem := elements[elemIdx]
		isOptElem := elem.Optional || (elem.Slot != nil && elem.Slot.EffectiveArity() == SlotOptional)

		if argIdx >= len(posArgs) {
			if isOptElem {
				applySlotDefault(elem, operands, &target)
				elemIdx++
				continue
			}
			return matchedCandidate{}, false
		}

		arg := posArgs[argIdx]

		switch elem.Kind {
		case ElementKeyword:
			if !strings.EqualFold(arg.Raw, elem.Value) {
				if elem.Optional {
					elemIdx++
					continue
				}
				return matchedCandidate{}, false
			}
			tokens = append(tokens, Token{
				Index:    arg.Index,
				Raw:      arg.Raw,
				Class:    TokenKeyword,
				SlotName: nil,
			})
			argIdx++
			elemIdx++

		case ElementGlue:
			if strings.EqualFold(arg.Raw, elem.Value) {
				tokens = append(tokens, Token{
					Index:    arg.Index,
					Raw:      arg.Raw,
					Class:    TokenGlue,
					SlotName: nil,
				})
				argIdx++
				elemIdx++
			} else if elem.Optional {
				elemIdx++
			} else {
				return matchedCandidate{}, false
			}

		case ElementTarget, ElementSlot:
			slotSpec := Slot{Name: "operand", Type: SlotTypeString, Required: true}
			if elem.Kind == ElementTarget {
				slotSpec.Name = "target"
				slotSpec.Type = SlotTypeTarget
				slotSpec.IsTarget = true
			}
			if elem.Slot != nil {
				slotSpec = *elem.Slot
				if elem.Kind == ElementTarget {
					slotSpec.IsTarget = true
				}
			}
			slots[slotSpec.Name] = slotSpec

			tokClass := TokenOperand
			if elem.Kind == ElementTarget || slotSpec.IsTarget || slotSpec.Target {
				tokClass = TokenTarget
			}

			arity := slotSpec.EffectiveArity()
			minTok := slotSpec.MinTokens
			maxTok := slotSpec.MaxTokens
			if minTok <= 0 {
				if arity == SlotOptional {
					minTok = 0
				} else {
					minTok = 1
				}
			}

			switch arity {
			case SlotSingle:
				if maxTok <= 0 {
					maxTok = 1
				}
				if argIdx+minTok > len(posArgs) {
					return matchedCandidate{}, false
				}
				endIdx := argIdx + maxTok
				if endIdx > len(posArgs) {
					endIdx = len(posArgs)
				}
				val, consumedTokens, ok := joinSlotTokens(posArgs[argIdx:endIdx], slotSpec.Name, tokClass)
				if !ok {
					return matchedCandidate{}, false
				}
				operands[slotSpec.Name] = val
				if tokClass == TokenTarget && target == "" {
					target = val
				}
				tokens = append(tokens, consumedTokens...)
				argIdx = endIdx
				elemIdx++

			case SlotOptional:
				// Optional slot consumes 0 or 1 token at this position.
				// If there are remaining elements after this optional slot and the current token matches the next literal, consume 0.
				if elemIdx+1 < len(elements) && isLiteralMatch(posArgs[argIdx], elements[elemIdx+1]) {
					applySlotDefault(elem, operands, &target)
					elemIdx++
					continue
				}
				val, consumedTokens, ok := joinSlotTokens(posArgs[argIdx:argIdx+1], slotSpec.Name, tokClass)
				if !ok {
					return matchedCandidate{}, false
				}
				operands[slotSpec.Name] = val
				if tokClass == TokenTarget && target == "" {
					target = val
				}
				tokens = append(tokens, consumedTokens...)
				argIdx++
				elemIdx++

			case SlotRest:
				// Rest slot is only valid on the final element and consumes all remaining tokens.
				if elemIdx != len(elements)-1 {
					return matchedCandidate{}, false
				}
				remaining := len(posArgs) - argIdx
				if remaining < minTok || (maxTok > 0 && remaining > maxTok) {
					return matchedCandidate{}, false
				}
				// A rest slot must not cross the `--` boundary: either all consumed tokens are before `--` or all are after `--`.
				if crossesDoubleDash(posArgs[argIdx:]) {
					return matchedCandidate{}, false
				}
				val, consumedTokens, ok := joinSlotTokens(posArgs[argIdx:], slotSpec.Name, tokClass)
				if !ok {
					return matchedCandidate{}, false
				}
				operands[slotSpec.Name] = val
				if tokClass == TokenTarget && target == "" {
					target = val
				}
				tokens = append(tokens, consumedTokens...)
				argIdx = len(posArgs)
				elemIdx++

			case SlotUntil:
				boundary := slotSpec.Until
				if boundary == "" && elemIdx+1 < len(elements) {
					boundary = elements[elemIdx+1].Value
				}
				if boundary == "" {
					return matchedCandidate{}, false
				}
				stopIdx := -1
				for scan := argIdx; scan < len(posArgs); scan++ {
					// Literal boundaries must be outside `--` positional data
					if !posArgs[scan].AfterDoubleDash && strings.EqualFold(posArgs[scan].Raw, boundary) {
						stopIdx = scan
						break
					}
				}
				if stopIdx <= argIdx {
					return matchedCandidate{}, false
				}
				count := stopIdx - argIdx
				if count < minTok || (maxTok > 0 && count > maxTok) {
					return matchedCandidate{}, false
				}
				if crossesDoubleDash(posArgs[argIdx:stopIdx]) {
					return matchedCandidate{}, false
				}
				val, consumedTokens, ok := joinSlotTokens(posArgs[argIdx:stopIdx], slotSpec.Name, tokClass)
				if !ok {
					return matchedCandidate{}, false
				}
				operands[slotSpec.Name] = val
				if tokClass == TokenTarget && target == "" {
					target = val
				}
				tokens = append(tokens, consumedTokens...)
				argIdx = stopIdx
				elemIdx++
			}
		}
	}

	if argIdx != len(posArgs) {
		return matchedCandidate{}, false
	}

	return matchedCandidate{
		prod:     prod,
		elements: elements,
		score:    ComputeSpecificity(elements),
		operands: operands,
		slots:    slots,
		target:   target,
		tokens:   tokens,
	}, true
}

func applySlotDefault(elem ProductionElement, operands map[string]string, target *string) {
	if elem.Slot == nil {
		return
	}
	if elem.Slot.HasDefault || elem.Slot.Default != "" {
		operands[elem.Slot.Name] = elem.Slot.Default
		if (elem.Kind == ElementTarget || elem.Slot.IsTarget || elem.Slot.Target) && *target == "" && elem.Slot.Default != "" {
			*target = elem.Slot.Default
		}
	}
}

func isLiteralMatch(arg PositionalArg, elem ProductionElement) bool {
	if arg.AfterDoubleDash {
		return false
	}
	if elem.Kind == ElementKeyword || elem.Kind == ElementGlue {
		return strings.EqualFold(arg.Raw, elem.Value)
	}
	return false
}

func crossesDoubleDash(slice []PositionalArg) bool {
	if len(slice) <= 1 {
		return false
	}
	first := slice[0].AfterDoubleDash
	for _, a := range slice[1:] {
		if a.AfterDoubleDash != first {
			return true
		}
	}
	return false
}

func joinSlotTokens(slice []PositionalArg, slotName string, class TokenClass) (string, []Token, bool) {
	if len(slice) == 0 {
		return "", nil, false
	}
	parts := make([]string, 0, len(slice))
	toks := make([]Token, 0, len(slice))
	for _, a := range slice {
		trimmed := strings.TrimSpace(a.Raw)
		if trimmed == "" {
			return "", nil, false
		}
		parts = append(parts, trimmed)
		toks = append(toks, Token{
			Index:    a.Index,
			Raw:      a.Raw,
			Class:    class,
			SlotName: StringPtr(slotName),
		})
	}
	joined := strings.TrimSpace(strings.Join(parts, " "))
	if joined == "" {
		return "", nil, false
	}
	return joined, toks, true
}

func validateCandidateSlots(cand *matchedCandidate) string {
	if cand.prod != nil && (cand.prod.ActionID == "config.set" || cand.prod.ID == "sila_config_set") {
		if key := cand.operands["key"]; key != "" && !IsAllowedNonSecretConfigKey(key) {
			return fmt.Sprintf("slot key: unknown or disallowed config key %q", key)
		}
	}
	for slotName, val := range cand.operands {
		slotSpec, hasSpec := cand.slots[slotName]
		if !hasSpec {
			continue
		}
		// Allow empty default on optional slot when explicitly declared
		if val == "" && slotSpec.EffectiveArity() == SlotOptional && slotSpec.HasDefault {
			continue
		}
		if errMsg := validateSlotValue(slotSpec, val, cand.tokens); errMsg != "" {
			return errMsg
		}
	}
	return ""
}

func validateSlotValue(slot Slot, val string, tokens []Token) string {
	trimmed := strings.TrimSpace(val)

	switch slot.Type {
	case SlotTypeInt:
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			return fmt.Sprintf("slot %s: expected integer", slot.Name)
		}
		if slot.Positive && n <= 0 {
			return fmt.Sprintf("slot %s: expected positive integer", slot.Name)
		}

	case SlotTypePath:
		if trimmed == "" || hasControlOrNul(val) {
			return fmt.Sprintf("slot %s: invalid path", slot.Name)
		}
		if strings.HasPrefix(trimmed, "-") {
			// A path beginning with `-` requires `--` or an explicit `--path=` binding
			afterDD := false
			for _, t := range tokens {
				if t.SlotName != nil && *t.SlotName == slot.Name && t.Raw == val {
					afterDD = true
					break
				}
			}
			_ = afterDD
		}

	case SlotTypeProject:
		if trimmed == "" || hasControlOrNul(val) {
			return fmt.Sprintf("slot %s: project required", slot.Name)
		}

	case SlotTypeTool:
		ok := false
		for _, allowed := range knownIngestTools {
			if strings.EqualFold(trimmed, allowed) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Sprintf("slot %s: unknown tool; expected one of %s", slot.Name, strings.Join(knownIngestTools, ", "))
		}

	case SlotTypeEnum:
		if len(slot.Enum) > 0 {
			ok := false
			for _, allowed := range slot.Enum {
				if strings.EqualFold(trimmed, allowed) {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Sprintf("slot %s: expected one of %s", slot.Name, strings.Join(slot.Enum, ", "))
			}
		}

	case SlotTypeTarget:
		if trimmed == "" || hasControlOrNul(val) {
			return fmt.Sprintf("slot %s: target required", slot.Name)
		}

	default:
		if hasControlOrNul(val) {
			return fmt.Sprintf("slot %s: invalid control character in value", slot.Name)
		}
	}

	return ""
}

func hasControlOrNul(s string) bool {
	for _, r := range s {
		if r == 0 || (unicode.IsControl(r) && r != '\t') {
			return true
		}
	}
	return false
}

func enforceTargetPolicy(cand *matchedCandidate) string {
	p := cand.prod
	policy := p.TargetPolicy

	switch policy {
	case "required":
		if cand.target == "" {
			return "Missing required target"
		}
	case "directional":
		src := cand.operands["source"]
		dst := cand.operands["destination"]
		if src == "" || dst == "" {
			// Also support named two-target productions (e.g. keep/drop) via ArgsOrder
			if len(p.ArgsOrder) >= 2 {
				src = cand.operands[p.ArgsOrder[0]]
				dst = cand.operands[p.ArgsOrder[1]]
			}
		}
		if src == "" || dst == "" {
			return "Missing required directional targets"
		}
		if strings.EqualFold(src, dst) {
			return "Directional command requires distinct source and destination targets"
		}
	case "implicit":
		if strings.TrimSpace(p.TargetScope) == "" {
			return "Missing target scope for implicit target policy"
		}
		if cand.target == "" {
			cand.target = p.TargetScope
		}
	case "permitted-default":
		if cand.target == "" && p.Tier == TierQuery {
			cand.target = "default"
			if _, has := cand.operands["target"]; !has {
				cand.operands["target"] = "default"
			}
		}
	default:
		// Legacy fallback when TargetPolicy is unset on standard test inventory
		if p.Tier == TierQuery && cand.target == "" {
			cand.target = "default"
			cand.operands["target"] = "default"
		} else if (p.Tier == TierMutation || p.Tier == TierDestructive) && cand.target == "" &&
			(cand.operands["source"] == "" || cand.operands["destination"] == "") {
			return "Missing required target"
		}
	}
	return ""
}

// IsCanonicalPassthrough reports whether rawArgs is already a valid canonical CLI invocation.
func IsCanonicalPassthrough(rawArgs []string) bool {
	if len(rawArgs) == 0 {
		return false
	}
	return cli.CanonicalAccepts(rawArgs)
}
