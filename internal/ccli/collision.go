package ccli

import (
	"fmt"
	"strings"
	"testing"
)

// CollisionType defines the classification of a grammar collision.
type CollisionType string

const (
	CollisionExactSkeleton      CollisionType = "EXACT_SKELETON"
	CollisionKeywordSlotOverlap CollisionType = "KEYWORD_SLOT_OVERLAP"
	CollisionOptionalCollapse   CollisionType = "OPTIONAL_COLLAPSE"
	CollisionCommutativeOverlap CollisionType = "COMMUTATIVE_OVERLAP"
	CollisionOrderedOverlap     CollisionType = "ORDERED_OVERLAP"
)

// CollisionReport represents the detection of a grammar overlap or collision.
type CollisionReport struct {
	Type               CollisionType    `json:"collision_type"`
	Ambiguous          bool             `json:"ambiguous"`
	ConflictingActions []string         `json:"conflicting_actions"`
	WitnessPhrase      []string         `json:"witness_phrase"`
	ProductionA        string           `json:"production_a"`
	ProductionB        string           `json:"production_b"`
	WinnerProduction   string           `json:"winner_production,omitempty"`
	ScoreA             SpecificityScore `json:"score_a"`
	ScoreB             SpecificityScore `json:"score_b"`
	Explanation        string           `json:"explanation"`
}

// SequenceForm represents one concrete sequence of elements a production can accept.
type SequenceForm struct {
	Production *Production
	Elements   []ProductionElement
	IsCollapse bool
}

// ComputeSpecificity calculates the deterministic specificity score for an element sequence.
func ComputeSpecificity(elems []ProductionElement) SpecificityScore {
	var score SpecificityScore
	for _, e := range elems {
		if e.Kind == ElementKeyword || e.Kind == ElementGlue {
			if !e.Optional {
				score.FixedLiterals++
				score.MinTokens++
			} else {
				score.HasRestOrOpt = true
			}
		} else {
			arity := SlotSingle
			minTok := 1
			if e.Slot != nil {
				arity = e.Slot.EffectiveArity()
				if e.Slot.MinTokens > 0 {
					minTok = e.Slot.MinTokens
				}
			}
			if e.Optional || arity == SlotOptional {
				score.HasRestOrOpt = true
			} else {
				score.MinTokens += minTok
			}
			if arity == SlotRest || arity == SlotUntil {
				score.HasRestOrOpt = true
			}
		}
	}
	exactBonus := 10
	if score.HasRestOrOpt {
		exactBonus = 0
	}
	score.TotalScore = score.FixedLiterals*1000 + score.MinTokens*100 + exactBonus
	return score
}

// CompareSpecificity compares two concrete element sequences according to the 4-tier specificity rules:
//  1. More fixed keyword/glue elements wins.
//  2. At the first differing position, a fixed literal wins over a slot.
//  3. Longer minimum token count wins.
//  4. Exact/declared single-token production wins over a greedy/optional fallback.
//
// Returns >0 if elems1 is strictly more specific, <0 if elems2 is strictly more specific, or 0 if tied.
func CompareSpecificity(elems1, elems2 []ProductionElement) int {
	s1 := ComputeSpecificity(elems1)
	s2 := ComputeSpecificity(elems2)

	// Rule 1: More fixed keyword/glue elements
	if s1.FixedLiterals != s2.FixedLiterals {
		return s1.FixedLiterals - s2.FixedLiterals
	}

	// Rule 2: At the first differing position, fixed literal > slot
	minLen := len(elems1)
	if len(elems2) < minLen {
		minLen = len(elems2)
	}
	for k := 0; k < minLen; k++ {
		f1 := (elems1[k].Kind == ElementKeyword || elems1[k].Kind == ElementGlue) && !elems1[k].Optional
		f2 := (elems2[k].Kind == ElementKeyword || elems2[k].Kind == ElementGlue) && !elems2[k].Optional
		if f1 != f2 {
			if f1 {
				return 1
			}
			return -1
		}
	}

	// Rule 3: Longer minimum token count
	if s1.MinTokens != s2.MinTokens {
		return s1.MinTokens - s2.MinTokens
	}

	// Rule 4: Exact/declared production over greedy/optional fallback
	if s1.HasRestOrOpt != s2.HasRestOrOpt {
		if !s1.HasRestOrOpt {
			return 1
		}
		return -1
	}

	return 0
}

// expandProduction generates all concrete element sequences for a production
// by evaluating optional elements and commutative/inverted variations.
func expandProduction(p *Production) []SequenceForm {
	var forms []SequenceForm

	baseForms := expandOptional(p.Elements)
	for _, bf := range baseForms {
		forms = append(forms, SequenceForm{
			Production: p,
			Elements:   bf,
			IsCollapse: len(bf) < len(p.Elements),
		})
	}

	for _, ce := range p.CommutativeElements {
		cForms := expandOptional(ce)
		for _, cf := range cForms {
			forms = append(forms, SequenceForm{
				Production: p,
				Elements:   cf,
				IsCollapse: len(cf) < len(ce),
			})
		}
	}

	for _, inv := range p.WhitelistedInversions {
		invForms := expandOptional(inv)
		for _, inf := range invForms {
			forms = append(forms, SequenceForm{
				Production: p,
				Elements:   inf,
				IsCollapse: len(inf) < len(inv),
			})
		}
	}

	return forms
}

func expandOptional(elems []ProductionElement) [][]ProductionElement {
	if len(elems) == 0 {
		return [][]ProductionElement{{}}
	}

	first := elems[0]
	isOpt := first.Optional || (first.Slot != nil && first.Slot.EffectiveArity() == SlotOptional)
	rest := expandOptional(elems[1:])

	var result [][]ProductionElement
	for _, r := range rest {
		withFirst := append([]ProductionElement{first}, r...)
		result = append(result, withFirst)

		if isOpt {
			withoutFirst := append([]ProductionElement{}, r...)
			result = append(result, withoutFirst)
		}
	}
	return result
}

// AnalyzeCollisions inspects an inventory and returns both ordered overlaps and ambiguous collisions.
func AnalyzeCollisions(inventory *Inventory) (ordered []CollisionReport, ambiguous []CollisionReport) {
	if inventory == nil || len(inventory.Productions) < 2 {
		return nil, nil
	}

	seen := make(map[string]bool)

	type prodForm struct {
		prod *Production
		form SequenceForm
	}
	var allForms []prodForm
	for _, p := range inventory.Productions {
		expanded := expandProduction(p)
		for _, f := range expanded {
			allForms = append(allForms, prodForm{prod: p, form: f})
		}
	}

	for i := 0; i < len(allForms); i++ {
		for j := i + 1; j < len(allForms); j++ {
			f1 := allForms[i]
			f2 := allForms[j]

			if f1.prod.ActionID == f2.prod.ActionID {
				continue
			}

			rep, collides := checkSequenceCollision(f1.form, f2.form)
			if !collides {
				continue
			}

			pairKey := fmt.Sprintf("%s:%s:%s", f1.prod.ID, f2.prod.ID, rep.Type)
			revKey := fmt.Sprintf("%s:%s:%s", f2.prod.ID, f1.prod.ID, rep.Type)
			if seen[pairKey] || seen[revKey] {
				continue
			}
			seen[pairKey] = true

			if rep.Ambiguous {
				ambiguous = append(ambiguous, rep)
			} else {
				ordered = append(ordered, rep)
			}
		}
	}

	return ordered, ambiguous
}

// DetectCollisions analyzes an inventory of productions to find any ambiguous grammar pairs.
// Ordered overlaps that have an explicit specificity witness are excluded from the error return.
func DetectCollisions(inventory *Inventory) ([]CollisionReport, error) {
	_, ambiguous := AnalyzeCollisions(inventory)
	if len(ambiguous) > 0 {
		return ambiguous, fmt.Errorf("grammar contains %d collision(s)", len(ambiguous))
	}
	return nil, nil
}

func isMultiTokenTail(elems []ProductionElement) bool {
	if len(elems) == 0 {
		return false
	}
	last := elems[len(elems)-1]
	if last.Slot == nil {
		return false
	}
	arity := last.Slot.EffectiveArity()
	return arity == SlotRest || arity == SlotUntil
}

// checkSequenceCollision compares two concrete element sequences for potential collisions or ordered overlaps.
func checkSequenceCollision(s1, s2 SequenceForm) (CollisionReport, bool) {
	elems1 := s1.Elements
	elems2 := s2.Elements

	tail1 := isMultiTokenTail(elems1)
	tail2 := isMultiTokenTail(elems2)

	if len(elems1) != len(elems2) {
		// Different lengths can still overlap if the shorter sequence ends with a rest/until slot
		if !(tail1 && len(elems1) < len(elems2)) && !(tail2 && len(elems2) < len(elems1)) {
			return CollisionReport{}, false
		}
	}

	minLen := len(elems1)
	if len(elems2) < minLen {
		minLen = len(elems2)
	}

	identicalSkeleton := len(elems1) == len(elems2)
	hasKeywordSlotOverlap := len(elems1) != len(elems2)
	var witness []string

	for k := 0; k < minLen; k++ {
		e1 := elems1[k]
		e2 := elems2[k]

		isSlot1 := (e1.Kind == ElementSlot || e1.Kind == ElementTarget)
		isSlot2 := (e2.Kind == ElementSlot || e2.Kind == ElementTarget)

		if !isSlot1 && !isSlot2 {
			if !strings.EqualFold(e1.Value, e2.Value) {
				return CollisionReport{}, false
			}
			witness = append(witness, strings.ToLower(e1.Value))
		} else if isSlot1 && isSlot2 {
			sample := "production"
			if e1.Slot != nil && e1.Slot.Name != "" {
				sample = e1.Slot.Name
			}
			witness = append(witness, sample)
		} else {
			identicalSkeleton = false
			hasKeywordSlotOverlap = true
			if !isSlot1 {
				witness = append(witness, strings.ToLower(e1.Value))
			} else {
				witness = append(witness, strings.ToLower(e2.Value))
			}
		}
	}

	// Append witness tokens for the longer sequence when overlapping a rest slot
	if len(elems1) > minLen {
		for k := minLen; k < len(elems1); k++ {
			if elems1[k].Kind == ElementKeyword || elems1[k].Kind == ElementGlue {
				witness = append(witness, strings.ToLower(elems1[k].Value))
			} else if elems1[k].Slot != nil && elems1[k].Slot.Name != "" {
				witness = append(witness, elems1[k].Slot.Name)
			} else {
				witness = append(witness, "value")
			}
		}
	} else if len(elems2) > minLen {
		for k := minLen; k < len(elems2); k++ {
			if elems2[k].Kind == ElementKeyword || elems2[k].Kind == ElementGlue {
				witness = append(witness, strings.ToLower(elems2[k].Value))
			} else if elems2[k].Slot != nil && elems2[k].Slot.Name != "" {
				witness = append(witness, elems2[k].Slot.Name)
			} else {
				witness = append(witness, "value")
			}
		}
	}

	actions := []string{s1.Production.ActionID, s2.Production.ActionID}
	if actions[0] > actions[1] {
		actions[0], actions[1] = actions[1], actions[0]
	}
	score1 := ComputeSpecificity(elems1)
	score2 := ComputeSpecificity(elems2)

	base := CollisionReport{
		ConflictingActions: actions,
		WitnessPhrase:      witness,
		ProductionA:        s1.Production.ID,
		ProductionB:        s2.Production.ID,
		ScoreA:             score1,
		ScoreB:             score2,
	}

	if s1.IsCollapse || s2.IsCollapse {
		base.Type = CollisionOptionalCollapse
		base.Ambiguous = true
		base.Explanation = "Optional segment collapse collision"
		return base, true
	}

	if s1.Production.Commutative || s2.Production.Commutative {
		base.Type = CollisionCommutativeOverlap
		base.Ambiguous = true
		base.Explanation = "Commutative slot collision"
		return base, true
	}

	if hasKeywordSlotOverlap {
		cmp := CompareSpecificity(elems1, elems2)
		hasWitness := strings.TrimSpace(s1.Production.SpecificityWitness) != "" ||
			strings.TrimSpace(s2.Production.SpecificityWitness) != "" ||
			tail1 != tail2
		if cmp != 0 && hasWitness {
			base.Type = CollisionOrderedOverlap
			base.Ambiguous = false
			if cmp > 0 {
				base.WinnerProduction = s1.Production.ID
			} else {
				base.WinnerProduction = s2.Production.ID
			}
			base.Explanation = fmt.Sprintf("Ordered overlap resolved deterministically in favor of %s (score %d vs %d)",
				base.WinnerProduction, score1.TotalScore, score2.TotalScore)
			return base, true
		}
		base.Type = CollisionKeywordSlotOverlap
		base.Ambiguous = true
		base.Explanation = "Keyword slot overlap collision"
		return base, true
	}

	if identicalSkeleton {
		base.Type = CollisionExactSkeleton
		base.Ambiguous = true
		base.Explanation = "Identical pattern skeleton collision"
		return base, true
	}

	return CollisionReport{}, false
}

// ValidateSchema verifies that an inventory conforms to normative specification rules,
// specifically the security policy prohibiting secret positional slots.
func ValidateSchema(inventory *Inventory) error {
	if inventory == nil {
		return nil
	}

	checkElements := func(prodID string, elems []ProductionElement) error {
		for _, elem := range elems {
			if elem.Slot != nil {
				name := strings.ToLower(elem.Slot.Name)
				if forbiddenSecretSlots[name] {
					return fmt.Errorf("production %s defines forbidden secret positional slot %q (secrets must not be accepted as positional slots)", prodID, elem.Slot.Name)
				}
			}
		}
		return nil
	}

	for _, p := range inventory.Productions {
		if err := checkElements(p.ID, p.Elements); err != nil {
			return err
		}
		for _, ce := range p.CommutativeElements {
			if err := checkElements(p.ID, ce); err != nil {
				return err
			}
		}
		for _, wi := range p.WhitelistedInversions {
			if err := checkElements(p.ID, wi); err != nil {
				return err
			}
		}
	}

	return nil
}

// AssertNoCollisions validates that an inventory contains zero grammar collisions
// and conforms to schema validation rules.
// If any collision or schema error is detected, it fails the test immediately with t.Fatalf.
func AssertNoCollisions(t *testing.T, inventory *Inventory) {
	t.Helper()
	if err := ValidateSchema(inventory); err != nil {
		t.Fatalf("schema validation failed: %v", err)
	}
	reports, err := DetectCollisions(inventory)
	if err != nil {
		for _, r := range reports {
			t.Errorf("COLLISION [%s]: %s conflicts with %s on witness %v. %s",
				r.Type, r.ProductionA, r.ProductionB, r.WitnessPhrase, r.Explanation)
		}
		t.Fatalf("grammar validation failed: %v", err)
	}
}
