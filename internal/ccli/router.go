package ccli

import (
	"context"
	"fmt"
	"strings"
)

// Router coordinates phrase parsing, guardrail verification, canonical rewriting, and dispatch.
type Router struct {
	Inventory *Inventory
	Actions   map[string]*Action
}

// NewRouter constructs a router bound to an inventory of productions.
func NewRouter(inventory *Inventory) *Router {
	return &Router{
		Inventory: inventory,
		Actions:   make(map[string]*Action),
	}
}

// RegisterAction binds an action execution handler to an action identifier.
func (r *Router) RegisterAction(action *Action) {
	if action != nil && action.ID != "" {
		r.Actions[action.ID] = action
	}
}

// Rewrite transforms a parsed AST into a canonical CLI argument slice.
func Rewrite(ast *AST, prod *Production) []string {
	if ast == nil {
		return []string{}
	}

	var canonical []string
	if prod != nil && len(prod.CanonicalBase) > 0 {
		canonical = append(canonical, prod.CanonicalBase...)
	}

	// 1. Append slot-mapped flags in declared order
	if prod != nil && len(prod.ArgsOrder) > 0 {
		for _, slotName := range prod.ArgsOrder {
			val, ok := ast.Operands[slotName]
			if !ok || val == "" {
				continue
			}
			flagPrefix, hasPrefix := prod.FlagMapping[slotName]
			if hasPrefix {
				canonical = append(canonical, flagPrefix+"="+val)
			} else {
				canonical = append(canonical, val)
			}
		}
	} else if prod != nil && len(prod.FlagMapping) > 0 {
		for slotName, flagPrefix := range prod.FlagMapping {
			val, ok := ast.Operands[slotName]
			if !ok || val == "" {
				continue
			}
			canonical = append(canonical, flagPrefix+"="+val)
		}
	}

	// 1b. Append literal static_flags declared on the production (deduplicated)
	if prod != nil && len(prod.StaticFlags) > 0 {
		existing := make(map[string]bool, len(canonical))
		for _, c := range canonical {
			existing[c] = true
		}
		for _, sf := range prod.StaticFlags {
			if sf != "" && !existing[sf] {
				existing[sf] = true
				canonical = append(canonical, sf)
			}
		}
	}

	// 2. Append Pass 1 extracted flags (deduplicated against existing arguments)
	seen := make(map[string]bool)
	for _, arg := range canonical {
		seen[arg] = true
	}

	for i := 0; i < len(ast.RawFlags); i++ {
		rawFlag := ast.RawFlags[i]
		// Skip flags already represented by slot mappings (e.g., --env=prod)
		parts := strings.SplitN(rawFlag, "=", 2)
		baseFlag := parts[0]
		if baseFlag == "--env" && len(parts) == 2 && seen["--env="+parts[1]] {
			continue
		}
		// If this is a space-separated flag followed by its value (e.g. -o "A: SQLite"),
		// preserve the pair in order even when repeated (e.g. -o "A: SQLite" -o "B: PG").
		if !strings.Contains(rawFlag, "=") && strings.HasPrefix(rawFlag, "-") && i+1 < len(ast.RawFlags) && !strings.HasPrefix(ast.RawFlags[i+1], "-") {
			pairKey := rawFlag + "\x00" + ast.RawFlags[i+1]
			if !seen[pairKey] {
				seen[pairKey] = true
				canonical = append(canonical, rawFlag, ast.RawFlags[i+1])
			}
			i++
			continue
		}
		if !seen[rawFlag] {
			seen[rawFlag] = true
			canonical = append(canonical, rawFlag)
		}
	}

	return canonical
}

// Route executes the complete 5-stage pipeline for a raw argument vector.
func (r *Router) Route(ctx context.Context, inputArgs []string, streams *Streams) (retCode ExitCode, retRes *ParseResult) {
	if streams == nil {
		streams = DefaultStreams()
	}

	defer func() {
		if rec := recover(); rec != nil {
			streams.LogError("CCLI internal error: %v", rec)
			retCode = ExitSystem
			if retRes == nil {
				retRes = &ParseResult{}
			}
			retRes.ExitCode = ExitSystem
			retRes.ErrorMessage = fmt.Sprintf("CCLI internal error: %v", rec)
		}
	}()

	// Stage 1 & 2: Parse flags and phrases
	res := Parse(inputArgs, r.Inventory)
	if res.ExitCode != ExitSuccess {
		if res.ErrorMessage != "" {
			streams.LogError("%s", res.ErrorMessage)
		}
		return res.ExitCode, res
	}

	var matchedProd *Production
	if r.Inventory != nil && res.AST != nil {
		for _, p := range r.Inventory.Productions {
			if p.ID == res.AST.ProductionID {
				matchedProd = p
				break
			}
		}
		if matchedProd == nil {
			for _, p := range r.Inventory.Productions {
				if p.ActionID == res.AST.ActionID {
					matchedProd = p
					break
				}
			}
		}
	}

	res.CanonicalArgs = Rewrite(res.AST, matchedProd)

	// Stage 3 & 4: Operational Safety and Guardrail Enforcement
	guardRes := EvaluateGuardrails(res.AST, GuardrailOptions{
		Prod:      matchedProd,
		Inventory: r.Inventory,
		Streams:   streams,
		RawArgs:   inputArgs,
		Canonical: res.CanonicalArgs,
	})
	if guardRes.ExitCode != ExitSuccess {
		streams.LogError("%s", guardRes.Message)
		res.ExitCode = guardRes.ExitCode
		res.ErrorMessage = guardRes.Message
		return guardRes.ExitCode, res
	}

	if guardRes.IsDryRun {
		return ExitSuccess, res
	}

	res.CanonicalArgs = StripControlFlags(res.CanonicalArgs, matchedProd)

	// Stage 5: Action Dispatch if registered
	if res.AST != nil && res.AST.ActionID != "" {
		if action, exists := r.Actions[res.AST.ActionID]; exists && action.Handler != nil {
			execCode := action.Handler(ctx, res.AST, streams)
			mapped := MapHostExitCode(int(execCode), matchedProd)
			res.ExitCode = mapped
			return mapped, res
		}
	}

	return ExitSuccess, res
}

func checkDirectionalInversion(posArgs []PositionalArg, inventory *Inventory, pass1 *Pass1Result) *ParseResult {
	if len(posArgs) < 3 {
		return nil
	}

	for _, p := range inventory.Productions {
		if !p.Directional && p.TargetPolicy != "directional" {
			continue
		}

		allSeqs := append([][]ProductionElement{p.Elements}, p.WhitelistedInversions...)
		for _, elems := range allSeqs {
			var leadKeywords []string
			for _, elem := range elems {
				if elem.Kind == ElementKeyword {
					leadKeywords = append(leadKeywords, elem.Value)
				} else {
					break
				}
			}

			if len(leadKeywords) == 0 || len(posArgs) < len(leadKeywords) {
				continue
			}

			leadMatches := true
			for k, kw := range leadKeywords {
				if !strings.EqualFold(posArgs[k].Raw, kw) {
					leadMatches = false
					break
				}
			}
			if !leadMatches {
				continue
			}

			// Case 1: `from ... to ...` inverted as `to ... from ...`
			toPos := -1
			fromPos := -1
			for k := len(leadKeywords); k < len(posArgs); k++ {
				if strings.EqualFold(posArgs[k].Raw, "to") && toPos == -1 {
					toPos = k
				} else if strings.EqualFold(posArgs[k].Raw, "from") && fromPos == -1 {
					fromPos = k
				}
			}

			if toPos >= 0 && fromPos >= 0 && toPos < fromPos {
				markDirectionalErrorTokens(posArgs, leadKeywords, toPos, fromPos, pass1)
				return &ParseResult{
					Tokens:       pass1.Tokens,
					ExitCode:     ExitUsage,
					ErrorMessage: "Directional commands do not permit inverted ordering",
				}
			}

			// Case 2: `merge projects <keep> into <drop>` inverted as `merge projects into <drop> <keep>` or `merge projects <drop> from <keep>`
			for k := len(leadKeywords); k < len(posArgs); k++ {
				if strings.EqualFold(posArgs[k].Raw, "from") && containsGlue(elems, "into") {
					markDirectionalErrorTokens(posArgs, leadKeywords, k, -1, pass1)
					return &ParseResult{
						Tokens:       pass1.Tokens,
						ExitCode:     ExitUsage,
						ErrorMessage: "Directional commands do not permit inverted ordering",
					}
				}
				if k == len(leadKeywords) && (strings.EqualFold(posArgs[k].Raw, "into") || strings.EqualFold(posArgs[k].Raw, "to")) {
					markDirectionalErrorTokens(posArgs, leadKeywords, k, -1, pass1)
					return &ParseResult{
						Tokens:       pass1.Tokens,
						ExitCode:     ExitUsage,
						ErrorMessage: "Directional commands do not permit inverted ordering",
					}
				}
			}
		}
	}

	return nil
}

func containsGlue(elems []ProductionElement, val string) bool {
	for _, e := range elems {
		if (e.Kind == ElementGlue || e.Kind == ElementKeyword) && strings.EqualFold(e.Value, val) {
			return true
		}
	}
	return false
}

func markDirectionalErrorTokens(posArgs []PositionalArg, leadKeywords []string, glue1, glue2 int, pass1 *Pass1Result) {
	for k := 0; k < len(leadKeywords); k++ {
		pass1.Tokens[posArgs[k].Index] = Token{
			Index:    posArgs[k].Index,
			Raw:      posArgs[k].Raw,
			Class:    TokenKeyword,
			SlotName: nil,
		}
	}
	for k := len(leadKeywords); k < len(posArgs); k++ {
		arg := posArgs[k]
		if k == glue1 || k == glue2 {
			pass1.Tokens[arg.Index] = Token{
				Index:    arg.Index,
				Raw:      arg.Raw,
				Class:    TokenGlue,
				SlotName: nil,
			}
		} else {
			pass1.Tokens[arg.Index] = Token{
				Index:    arg.Index,
				Raw:      arg.Raw,
				Class:    TokenOperand,
				SlotName: nil,
			}
		}
	}
}

// checkSpecificPrefixMissingSlot detects when the user typed the exact fixed keyword prefix
// of a more specific production (such as `remember error`) without its required trailing slot,
// preventing a less-specific greedy production (`remember <directive>`) from swallowing `error`.
func checkSpecificPrefixMissingSlot(posArgs []PositionalArg, inventory *Inventory, pass1 *Pass1Result) *ParseResult {
	for _, p := range inventory.Productions {
		allSeqs := append([][]ProductionElement{p.Elements}, p.WhitelistedInversions...)
		for _, seq := range allSeqs {
			if len(posArgs) >= len(seq) {
				continue
			}
			// Check if all posArgs match fixed keywords/glue in seq
			allFixedPrefix := true
			tokens := make([]Token, 0, len(posArgs))
			for i, arg := range posArgs {
				elem := seq[i]
				if (elem.Kind == ElementKeyword || elem.Kind == ElementGlue) && strings.EqualFold(arg.Raw, elem.Value) {
					tokClass := TokenKeyword
					if elem.Kind == ElementGlue {
						tokClass = TokenGlue
					}
					tokens = append(tokens, Token{
						Index:    arg.Index,
						Raw:      arg.Raw,
						Class:    tokClass,
						SlotName: nil,
					})
				} else {
					allFixedPrefix = false
					break
				}
			}
			if !allFixedPrefix || len(posArgs) < 2 {
				continue
			}
			nextElem := seq[len(posArgs)]
			if (nextElem.Kind == ElementSlot || nextElem.Kind == ElementTarget) && !nextElem.Optional {
				if nextElem.Slot != nil && nextElem.Slot.EffectiveArity() == SlotOptional {
					continue
				}
				for _, tok := range tokens {
					if tok.Index < len(pass1.Tokens) {
						pass1.Tokens[tok.Index] = tok
					}
				}
				slotName := "operand"
				if nextElem.Slot != nil && nextElem.Slot.Name != "" {
					slotName = nextElem.Slot.Name
				}
				msg := fmt.Sprintf("Missing required slot %q", slotName)
				if (nextElem.Kind == ElementTarget || (nextElem.Slot != nil && nextElem.Slot.IsTarget)) &&
					(p.Tier == TierMutation || p.Tier == TierDestructive) {
					msg = fmt.Sprintf("Missing required target (%s)", slotName)
				}
				return &ParseResult{
					Specificity:  ComputeSpecificity(seq),
					Tokens:       pass1.Tokens,
					ExitCode:     ExitUsage,
					ErrorMessage: msg,
				}
			}
		}
	}
	return nil
}

func matchElementsPrefix(posArgs []PositionalArg, elements []ProductionElement) ([]Token, *ProductionElement, bool) {
	if len(posArgs) >= len(elements) {
		return nil, nil, false
	}
	tokens := make([]Token, 0, len(posArgs))
	argIdx := 0
	elemIdx := 0

	for elemIdx < len(elements) && argIdx < len(posArgs) {
		elem := elements[elemIdx]
		arg := posArgs[argIdx]

		switch elem.Kind {
		case ElementKeyword:
			if !strings.EqualFold(arg.Raw, elem.Value) {
				return nil, nil, false
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
				return nil, nil, false
			}

		case ElementTarget, ElementSlot:
			slotName := "operand"
			tokClass := TokenOperand
			if elem.Kind == ElementTarget || (elem.Slot != nil && elem.Slot.IsTarget) {
				slotName = "target"
				tokClass = TokenTarget
			}
			if elem.Slot != nil && elem.Slot.Name != "" {
				slotName = elem.Slot.Name
			}
			arity := SlotSingle
			if elem.Slot != nil {
				arity = elem.Slot.EffectiveArity()
			}
			if arity == SlotUntil && elem.Slot != nil && elem.Slot.Until != "" {
				// Consume until the boundary token or end of posArgs
				stopIdx := -1
				for scan := argIdx; scan < len(posArgs); scan++ {
					if !posArgs[scan].AfterDoubleDash && strings.EqualFold(posArgs[scan].Raw, elem.Slot.Until) {
						stopIdx = scan
						break
					}
				}
				if stopIdx == argIdx {
					return tokens, &elements[elemIdx], true
				}
				if stopIdx > argIdx {
					for scan := argIdx; scan < stopIdx; scan++ {
						tokens = append(tokens, Token{
							Index:    posArgs[scan].Index,
							Raw:      posArgs[scan].Raw,
							Class:    tokClass,
							SlotName: StringPtr(slotName),
						})
					}
					argIdx = stopIdx
					elemIdx++
					continue
				}
			}

			tokens = append(tokens, Token{
				Index:    arg.Index,
				Raw:      arg.Raw,
				Class:    tokClass,
				SlotName: StringPtr(slotName),
			})
			argIdx++
			elemIdx++
		}
	}

	if argIdx != len(posArgs) {
		return nil, nil, false
	}

	for elemIdx < len(elements) && (elements[elemIdx].Optional || (elements[elemIdx].Slot != nil && elements[elemIdx].Slot.EffectiveArity() == SlotOptional)) {
		elemIdx++
	}

	if elemIdx < len(elements) {
		return tokens, &elements[elemIdx], true
	}

	return tokens, nil, true
}

func checkMissingTargetOrOperand(posArgs []PositionalArg, inventory *Inventory, pass1 *Pass1Result) *ParseResult {
	for _, p := range inventory.Productions {
		allSeqs := append([][]ProductionElement{p.Elements}, p.WhitelistedInversions...)
		for _, seq := range allSeqs {
			tokens, nextElem, ok := matchElementsPrefix(posArgs, seq)
			if !ok || nextElem == nil {
				continue
			}

			if (nextElem.Kind == ElementTarget || (nextElem.Slot != nil && nextElem.Slot.IsTarget)) &&
				(p.Tier == TierMutation || p.Tier == TierDestructive || p.TargetPolicy == "required" || p.TargetPolicy == "directional") {
				for _, tok := range tokens {
					if tok.Index < len(pass1.Tokens) {
						pass1.Tokens[tok.Index] = tok
					}
				}
				return &ParseResult{
					Specificity:  ComputeSpecificity(seq),
					Tokens:       pass1.Tokens,
					ExitCode:     ExitUsage,
					ErrorMessage: "Missing required target",
				}
			}

			if nextElem.Kind == ElementSlot || nextElem.Kind == ElementTarget {
				for _, tok := range tokens {
					if tok.Index < len(pass1.Tokens) {
						pass1.Tokens[tok.Index] = tok
					}
				}
				slotName := "operand"
				if nextElem.Slot != nil && nextElem.Slot.Name != "" {
					slotName = nextElem.Slot.Name
				}
				if len(tokens) > 0 && tokens[len(tokens)-1].Class == TokenGlue {
					return &ParseResult{
						Specificity:  ComputeSpecificity(seq),
						Tokens:       pass1.Tokens,
						ExitCode:     ExitUsage,
						ErrorMessage: fmt.Sprintf("Missing operand for parameter clause (%s)", slotName),
					}
				}
				return &ParseResult{
					Specificity:  ComputeSpecificity(seq),
					Tokens:       pass1.Tokens,
					ExitCode:     ExitUsage,
					ErrorMessage: fmt.Sprintf("Missing required slot %q", slotName),
				}
			}
		}
	}

	return nil
}

func deriveCollisionMessage(p1, p2 *Production) string {
	if p1.Commutative || p2.Commutative {
		return "Ambiguous command phrase: commutative slot collision"
	}

	hasOptionalTarget := false
	hasOptionalOther := false
	checkOptional := func(elems []ProductionElement) {
		for _, e := range elems {
			if e.Optional || (e.Slot != nil && e.Slot.EffectiveArity() == SlotOptional) {
				if e.Kind == ElementTarget || (e.Slot != nil && e.Slot.IsTarget) {
					hasOptionalTarget = true
				} else {
					hasOptionalOther = true
				}
			}
		}
	}
	checkOptional(p1.Elements)
	checkOptional(p2.Elements)

	if hasOptionalTarget {
		return "Ambiguous command phrase: optional target collapse collision"
	}
	if hasOptionalOther {
		return "Ambiguous command phrase: optional segment collapse collision"
	}

	minLen := len(p1.Elements)
	if len(p2.Elements) < minLen {
		minLen = len(p2.Elements)
	}
	for k := 0; k < minLen; k++ {
		e1 := p1.Elements[k]
		e2 := p2.Elements[k]
		isFixed1 := (e1.Kind == ElementKeyword || e1.Kind == ElementGlue)
		isFixed2 := (e2.Kind == ElementKeyword || e1.Kind == ElementGlue)
		if (isFixed1 && !isFixed2) || (!isFixed1 && isFixed2) {
			return "Ambiguous command phrase: overlaps with keyword production. Ambiguous command phrase: conflicting keyword and slot interpretation"
		}
	}

	return "Ambiguous command phrase: matches multiple conflicting actions"
}

func classifyUnknownPhrase(pass1 *Pass1Result, posArgs []PositionalArg) {
	for i, arg := range posArgs {
		class := TokenKeyword
		if i == len(posArgs)-1 && len(posArgs) > 2 {
			class = TokenOperand
		}
		pass1.Tokens[arg.Index] = Token{
			Index:    arg.Index,
			Raw:      arg.Raw,
			Class:    class,
			SlotName: nil,
		}
	}
}

func classifyPass1FallbackTokens(pass1 *Pass1Result, inventory *Inventory) {
	if len(pass1.Positional) == 0 {
		return
	}

	for _, p := range inventory.Productions {
		if cand, ok := matchProduction(pass1.Positional, p); ok {
			for _, tok := range cand.tokens {
				if tok.Index < len(pass1.Tokens) {
					pass1.Tokens[tok.Index] = tok
				}
			}
			return
		}
	}

	for _, p := range inventory.Productions {
		if tokens, _, ok := matchElementsPrefix(pass1.Positional, p.Elements); ok {
			for _, tok := range tokens {
				if tok.Index < len(pass1.Tokens) {
					pass1.Tokens[tok.Index] = tok
				}
			}
			return
		}
	}
}
