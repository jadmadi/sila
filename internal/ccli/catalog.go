package ccli

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/jadmadi/sila/internal/cli"
)

//go:embed catalog.json
var embeddedCatalogBytes []byte

// CatalogSchemaVersion is the only schema version accepted by this build.
const CatalogSchemaVersion = "sila/ccli-catalog/v1"

// Validation failure codes required by facts.md §6 and goal R3.2.
const (
	DiagCatalogSchema        = "CATALOG_SCHEMA"
	DiagDuplicateID          = "CATALOG_DUPLICATE_ID"
	DiagDuplicateAction      = "CATALOG_DUPLICATE_ACTION"
	DiagDuplicatePhrase      = "CATALOG_DUPLICATE_PHRASE"
	DiagCollision            = "CATALOG_COLLISION"
	DiagSecretSlot           = "CATALOG_SECRET_SLOT"
	DiagSensitiveSlot        = "CATALOG_SENSITIVE_SLOT"
	DiagUnknownCommand       = "CATALOG_UNKNOWN_COMMAND"
	DiagUnknownSlot          = "CATALOG_UNKNOWN_SLOT"
	DiagUnmappedSlot         = "CATALOG_UNMAPPED_SLOT"
	DiagMissingSlot          = "CATALOG_MISSING_SLOT"
	DiagTargetPolicy         = "CATALOG_TARGET_POLICY"
	DiagImplicitTarget       = "CATALOG_IMPLICIT_TARGET"
	DiagConfirmationPolicy   = "CATALOG_CONFIRMATION_POLICY"
	DiagDryRunAliases        = "CATALOG_DRY_RUN_ALIASES"
	DiagPrecedenceUnproven   = "CATALOG_PRECEDENCE_UNPROVEN"
	DiagUnknownFlag          = "CATALOG_UNKNOWN_FLAG"
	DiagCanonicalFirstShadow = "CATALOG_CANONICAL_FIRST_SHADOW"
	DiagImplicitBypass       = "CATALOG_IMPLICIT_BYPASS"
)

// PlanRowSpec binds one row of the 76-row CCLI plan to its implementation status.
// Row status lives ONLY on PlanRowSpec; productions must not carry row_status or blocked_reason.
type PlanRowSpec struct {
	PlanRow         int      `json:"plan_row"`
	Domain          string   `json:"domain"`
	Command         string   `json:"command"`
	RowStatus       string   `json:"row_status"` // implemented|canonical_only|mixed|forbidden|blocked
	CanonicalPhrase string   `json:"canonical_phrase,omitempty"`
	ProductionIDs   []string `json:"production_ids,omitempty"`
	ForbiddenID     string   `json:"forbidden_id,omitempty"`
	BlockedReason   string   `json:"blocked_reason,omitempty"`
}

// ForbiddenPhraseSpec declares a conversational phrase that must never dispatch
// (for example, secret-bearing phrases) and always fails closed with an exit code and remediation.
type ForbiddenPhraseSpec struct {
	ID            string             `json:"id"`
	Domain        string             `json:"domain,omitempty"`
	PlanRow       int                `json:"plan_row,omitempty"`
	Phrases       [][]CatalogElement `json:"phrases"`
	SensitiveKeys []string           `json:"sensitive_keys,omitempty"`
	Reason        string             `json:"reason"`
	ExitCode      int                `json:"exit_code"`
	Remediation   string             `json:"remediation"`
}

// CatalogElement describes one token step in a catalog phrase form.
type CatalogElement struct {
	Kind     string `json:"kind"` // keyword|glue|slot|target
	Value    string `json:"value,omitempty"`
	Slot     string `json:"slot,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Greedy   bool   `json:"greedy,omitempty"`
}

// CatalogProduction is the JSON schema representation of a CCLI production in catalog.json.
type CatalogProduction struct {
	ID                 string             `json:"id"`
	ActionID           string             `json:"action_id"`
	Domain             string             `json:"domain"`
	Description        string             `json:"description"`
	Docs               string             `json:"docs,omitempty"`
	Tier               MutationTier       `json:"tier"`
	Directional        bool               `json:"directional,omitempty"`
	Commutative        bool               `json:"commutative,omitempty"`
	Precedence         string             `json:"precedence"` // canonical-first|phrase-only-via-do|phrase-first
	PrecedenceWitness  string             `json:"precedence_witness,omitempty"`
	SpecificityWitness string             `json:"specificity_witness,omitempty"`
	TargetPolicy       string             `json:"target_policy,omitempty"` // permitted-default|required|directional|implicit
	TargetScope        string             `json:"target_scope,omitempty"`
	PlanRow            int                `json:"plan_row,omitempty"`
	DryRunPolicy       string             `json:"dry_run_policy,omitempty"` // preview|none
	DryRunAliases      []string           `json:"dry_run_aliases,omitempty"`
	ConfirmationPolicy string             `json:"confirmation_policy,omitempty"` // none|optional|required-interactive|required-or-bypass
	Phrases            [][]CatalogElement `json:"phrases"`
	CanonicalBase      []string           `json:"canonical_base"`
	ArgsOrder          []string           `json:"args_order"`
	FlagMapping        map[string]string  `json:"flag_mapping"`
	StaticFlags        []string           `json:"static_flags,omitempty"`
	Slots              []Slot             `json:"slots"`
	PreserveExit       int                `json:"preserve_exit,omitempty"`
	VariantOf          string             `json:"variant_of,omitempty"`
	Source             string             `json:"source,omitempty"`
	SourceCitation     string             `json:"source_citation,omitempty"`
	Correction         string             `json:"correction,omitempty"`
	Examples           ProductionExamples `json:"examples,omitempty"`
	Aliases            []string           `json:"aliases,omitempty"`
	Deprecated         string             `json:"deprecated,omitempty"`
}

// Catalog is the root versioned CCLI catalog structure.
type Catalog struct {
	SchemaVersion    string                `json:"schema_version"`
	CatalogVersion   string                `json:"catalog_version"`
	MinSilaVersion   string                `json:"min_sila_version"`
	Description      string                `json:"description"`
	PlanRows         []PlanRowSpec         `json:"plan_rows,omitempty"`
	ForbiddenPhrases []ForbiddenPhraseSpec `json:"forbidden_phrases,omitempty"`
	Productions      []CatalogProduction   `json:"productions"`
}

// CatalogDiagnostic describes one structured validation error with production ID, path, and remediation.
type CatalogDiagnostic struct {
	Code         string `json:"code"`
	ProductionID string `json:"production_id,omitempty"`
	Path         string `json:"path"`
	Message      string `json:"message"`
	Remediation  string `json:"remediation"`
}

func (d CatalogDiagnostic) Error() string {
	if d.ProductionID != "" {
		return fmt.Sprintf("[%s] production=%s path=%s: %s (remediation: %s)", d.Code, d.ProductionID, d.Path, d.Message, d.Remediation)
	}
	return fmt.Sprintf("[%s] path=%s: %s (remediation: %s)", d.Code, d.Path, d.Message, d.Remediation)
}

var (
	catalogOnce     sync.Once
	cachedCatalog   *Catalog
	cachedInventory *Inventory
	cachedErr       error
)

// LoadCatalog loads, validates, and returns a defensive copy of the embedded CCLI catalog and Inventory.
func LoadCatalog() (*Inventory, *Catalog, error) {
	catalogOnce.Do(func() {
		inv, cat, _, err := LoadCatalogFromBytes(embeddedCatalogBytes)
		cachedInventory = inv
		cachedCatalog = cat
		cachedErr = err
	})
	if cachedErr != nil {
		return nil, nil, cachedErr
	}
	return cloneInventory(cachedInventory), cloneCatalog(cachedCatalog), nil
}

// LoadCatalogFromFile reads, parses, and validates a catalog JSON file from disk.
func LoadCatalogFromFile(path string) (*Inventory, *Catalog, []CatalogDiagnostic, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	return LoadCatalogFromBytes(raw)
}

// LoadCatalogFromBytes parses JSON with DisallowUnknownFields and runs full catalog validation.
func LoadCatalogFromBytes(raw []byte) (*Inventory, *Catalog, []CatalogDiagnostic, error) {
	var diags []CatalogDiagnostic

	// Pre-scan raw JSON to catch forbidden row-level fields on productions with explicit diagnostics
	var rawProbe struct {
		Productions []map[string]json.RawMessage `json:"productions"`
	}
	if err := json.Unmarshal(raw, &rawProbe); err == nil {
		for idx, pMap := range rawProbe.Productions {
			prodID := ""
			if idRaw, ok := pMap["id"]; ok {
				_ = json.Unmarshal(idRaw, &prodID)
			}
			for _, forbiddenKey := range []string{"row_status", "blocked_reason"} {
				if _, has := pMap[forbiddenKey]; has {
					diags = append(diags, CatalogDiagnostic{
						Code:         DiagCatalogSchema,
						ProductionID: prodID,
						Path:         fmt.Sprintf("productions[%d].%s", idx, forbiddenKey),
						Message:      fmt.Sprintf("production %q must not declare row-level field %q", prodID, forbiddenKey),
						Remediation:  "Move row_status and blocked_reason onto the corresponding PlanRowSpec entry in plan_rows",
					})
				}
			}
		}
	}

	var cat Catalog
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cat); err != nil {
		diags = append(diags, CatalogDiagnostic{
			Code:        DiagCatalogSchema,
			Path:        "root",
			Message:     fmt.Sprintf("JSON schema decode error: %v", err),
			Remediation: "Fix unknown or malformed JSON fields to match Catalog schema (sila/ccli-catalog/v1)",
		})
		return nil, nil, diags, joinDiagnostics(diags)
	}

	valDiags, _ := ValidateCatalog(&cat)
	diags = append(diags, valDiags...)
	if len(diags) > 0 {
		return nil, &cat, diags, joinDiagnostics(diags)
	}

	inv := BuildInventoryFromCatalog(&cat)
	return inv, &cat, nil, nil
}

func joinDiagnostics(diags []CatalogDiagnostic) error {
	if len(diags) == 0 {
		return nil
	}
	errs := make([]error, 0, len(diags))
	for _, d := range diags {
		errs = append(errs, d)
	}
	return errors.Join(errs...)
}

var forbiddenSecretSlots = map[string]bool{
	"password":    true,
	"token":       true,
	"secret":      true,
	"api_key":     true,
	"credential":  true,
	"private_key": true,
	"passwd":      true,
	"auth_token":  true,
	"bearer":      true,
	"jwt":         true,
	"session_key": true,
	"passphrase":  true,
}

// ValidateCatalog performs all normative CCLI catalog validations and returns all diagnostics.
func ValidateCatalog(cat *Catalog) ([]CatalogDiagnostic, error) {
	var diags []CatalogDiagnostic
	if cat == nil {
		diags = append(diags, CatalogDiagnostic{
			Code:        DiagCatalogSchema,
			Path:        "root",
			Message:     "catalog is nil",
			Remediation: "Provide a non-nil Catalog struct",
		})
		return diags, joinDiagnostics(diags)
	}

	if cat.SchemaVersion != CatalogSchemaVersion {
		diags = append(diags, CatalogDiagnostic{
			Code:        DiagCatalogSchema,
			Path:        "schema_version",
			Message:     fmt.Sprintf("unsupported schema_version %q (want %q)", cat.SchemaVersion, CatalogSchemaVersion),
			Remediation: fmt.Sprintf("Set schema_version to %q", CatalogSchemaVersion),
		})
	}
	if strings.TrimSpace(cat.CatalogVersion) == "" {
		diags = append(diags, CatalogDiagnostic{
			Code:        DiagCatalogSchema,
			Path:        "catalog_version",
			Message:     "catalog_version is required",
			Remediation: "Set catalog_version to a semantic version or ISO date",
		})
	}
	if strings.TrimSpace(cat.MinSilaVersion) == "" {
		diags = append(diags, CatalogDiagnostic{
			Code:        DiagCatalogSchema,
			Path:        "min_sila_version",
			Message:     "min_sila_version is required",
			Remediation: "Set min_sila_version to the minimum compatible Sila version",
		})
	}

	prodByID := make(map[string]CatalogProduction, len(cat.Productions))
	for idx, p := range cat.Productions {
		pathPrefix := fmt.Sprintf("productions[%d]", idx)
		if strings.TrimSpace(p.ID) == "" {
			diags = append(diags, CatalogDiagnostic{
				Code:        DiagCatalogSchema,
				Path:        pathPrefix + ".id",
				Message:     "production id is empty",
				Remediation: "Assign a unique snake_case production id",
			})
			continue
		}
		if _, exists := prodByID[p.ID]; exists {
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagDuplicateID,
				ProductionID: p.ID,
				Path:         pathPrefix + ".id",
				Message:      fmt.Sprintf("duplicate production id %q", p.ID),
				Remediation:  "Ensure every production has a unique id",
			})
		} else {
			prodByID[p.ID] = p
		}
	}

	forbiddenByID := make(map[string]ForbiddenPhraseSpec, len(cat.ForbiddenPhrases))
	for idx, fp := range cat.ForbiddenPhrases {
		pPath := fmt.Sprintf("forbidden_phrases[%d]", idx)
		if strings.TrimSpace(fp.ID) == "" {
			diags = append(diags, CatalogDiagnostic{
				Code:        DiagCatalogSchema,
				Path:        pPath + ".id",
				Message:     "forbidden_phrase id is empty",
				Remediation: "Set a unique id on each forbidden_phrases entry",
			})
			continue
		}
		if _, exists := forbiddenByID[fp.ID]; exists {
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagDuplicateID,
				ProductionID: fp.ID,
				Path:         pPath + ".id",
				Message:      fmt.Sprintf("duplicate forbidden_phrase id %q", fp.ID),
				Remediation:  "Ensure every forbidden_phrases entry has a unique id",
			})
		} else {
			forbiddenByID[fp.ID] = fp
		}
		if len(fp.Phrases) == 0 || strings.TrimSpace(fp.Reason) == "" || fp.ExitCode == 0 || strings.TrimSpace(fp.Remediation) == "" {
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagCatalogSchema,
				ProductionID: fp.ID,
				Path:         pPath,
				Message:      fmt.Sprintf("forbidden_phrase %q must declare phrases, reason, non-zero exit_code, and remediation", fp.ID),
				Remediation:  "Populate phrases, reason, exit_code (e.g. 3), and remediation on the forbidden_phrases entry",
			})
		}
	}

	// Validate PlanRowSpec consistency
	seenPlanRows := make(map[int]bool, len(cat.PlanRows))
	for idx, pr := range cat.PlanRows {
		rPath := fmt.Sprintf("plan_rows[%d]", idx)
		if pr.PlanRow <= 0 || seenPlanRows[pr.PlanRow] {
			diags = append(diags, CatalogDiagnostic{
				Code:        DiagCatalogSchema,
				Path:        rPath + ".plan_row",
				Message:     fmt.Sprintf("invalid or duplicate plan_row %d", pr.PlanRow),
				Remediation: "Use a unique positive integer plan_row",
			})
		}
		seenPlanRows[pr.PlanRow] = true

		switch pr.RowStatus {
		case "implemented":
			if len(pr.ProductionIDs) == 0 {
				diags = append(diags, CatalogDiagnostic{
					Code:        DiagCatalogSchema,
					Path:        rPath + ".production_ids",
					Message:     fmt.Sprintf("plan_row %d has status 'implemented' but empty production_ids", pr.PlanRow),
					Remediation: "List the implementing production_ids for status 'implemented'",
				})
			}
		case "canonical_only":
			if strings.TrimSpace(pr.CanonicalPhrase) == "" || len(pr.ProductionIDs) > 0 {
				diags = append(diags, CatalogDiagnostic{
					Code:        DiagCatalogSchema,
					Path:        rPath,
					Message:     fmt.Sprintf("plan_row %d with status 'canonical_only' requires canonical_phrase and zero production_ids", pr.PlanRow),
					Remediation: "Set canonical_phrase and omit production_ids on canonical_only rows",
				})
			}
		case "mixed":
			if strings.TrimSpace(pr.CanonicalPhrase) == "" || len(pr.ProductionIDs) == 0 {
				diags = append(diags, CatalogDiagnostic{
					Code:        DiagCatalogSchema,
					Path:        rPath,
					Message:     fmt.Sprintf("plan_row %d with status 'mixed' requires both canonical_phrase and production_ids", pr.PlanRow),
					Remediation: "Provide both canonical_phrase and production_ids for mixed rows",
				})
			}
		case "forbidden":
			if strings.TrimSpace(pr.ForbiddenID) == "" {
				diags = append(diags, CatalogDiagnostic{
					Code:        DiagCatalogSchema,
					Path:        rPath + ".forbidden_id",
					Message:     fmt.Sprintf("plan_row %d with status 'forbidden' requires forbidden_id", pr.PlanRow),
					Remediation: "Reference a valid forbidden_phrases id in forbidden_id",
				})
			} else if _, ok := forbiddenByID[pr.ForbiddenID]; !ok {
				diags = append(diags, CatalogDiagnostic{
					Code:        DiagCatalogSchema,
					Path:        rPath + ".forbidden_id",
					Message:     fmt.Sprintf("plan_row %d references unknown forbidden_id %q", pr.PlanRow, pr.ForbiddenID),
					Remediation: "Add the corresponding entry to forbidden_phrases",
				})
			}
		case "blocked":
			if strings.TrimSpace(pr.BlockedReason) == "" {
				diags = append(diags, CatalogDiagnostic{
					Code:        DiagCatalogSchema,
					Path:        rPath + ".blocked_reason",
					Message:     fmt.Sprintf("plan_row %d with status 'blocked' requires blocked_reason", pr.PlanRow),
					Remediation: "Provide a non-empty blocked_reason explaining the blocker",
				})
			}
		default:
			diags = append(diags, CatalogDiagnostic{
				Code:        DiagCatalogSchema,
				Path:        rPath + ".row_status",
				Message:     fmt.Sprintf("invalid row_status %q on plan_row %d", pr.RowStatus, pr.PlanRow),
				Remediation: "Use one of: implemented, canonical_only, mixed, forbidden, blocked",
			})
		}
		for _, pid := range pr.ProductionIDs {
			if _, ok := prodByID[pid]; !ok {
				diags = append(diags, CatalogDiagnostic{
					Code:        DiagCatalogSchema,
					Path:        rPath + ".production_ids",
					Message:     fmt.Sprintf("plan_row %d references unknown production_id %q", pr.PlanRow, pid),
					Remediation: "Ensure every production_id in plan_rows exists in productions",
				})
			}
		}
	}

	actionOwner := make(map[string]string)
	phraseOwner := make(map[string]string)

	for idx, p := range cat.Productions {
		pathPrefix := fmt.Sprintf("productions[%d]", idx)

		// Action uniqueness / variant_of check
		if prevProdID, exists := actionOwner[p.ActionID]; exists {
			if p.VariantOf == "" {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagDuplicateAction,
					ProductionID: p.ID,
					Path:         pathPrefix + ".action_id",
					Message:      fmt.Sprintf("duplicate action_id %q shared by %q and %q without variant_of", p.ActionID, prevProdID, p.ID),
					Remediation:  fmt.Sprintf("Set variant_of=%q on %q with a distinct phrase form or use a distinct action_id", prevProdID, p.ID),
				})
			} else if targetProd, ok := prodByID[p.VariantOf]; !ok || targetProd.ActionID != p.ActionID {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagDuplicateAction,
					ProductionID: p.ID,
					Path:         pathPrefix + ".variant_of",
					Message:      fmt.Sprintf("variant_of %q on %q does not reference a valid production with action_id %q", p.VariantOf, p.ID, p.ActionID),
					Remediation:  "Point variant_of at the primary production ID sharing the same action_id",
				})
			}
		} else if p.ActionID != "" {
			actionOwner[p.ActionID] = p.ID
		}

		// Canonical base check against internal/cli
		if len(p.CanonicalBase) == 0 {
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagUnknownCommand,
				ProductionID: p.ID,
				Path:         pathPrefix + ".canonical_base",
				Message:      "canonical_base cannot be empty",
				Remediation:  "Specify a valid command path from internal/cli",
			})
		} else {
			cmdSpec, ok := cli.Lookup(p.CanonicalBase[0])
			if !ok {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagUnknownCommand,
					ProductionID: p.ID,
					Path:         pathPrefix + ".canonical_base[0]",
					Message:      fmt.Sprintf("unknown canonical command %q", p.CanonicalBase[0]),
					Remediation:  "Use a registered command or alias from internal/cli/registry.go",
				})
			} else if len(p.CanonicalBase) > 1 && !strings.HasPrefix(p.CanonicalBase[1], "-") {
				if len(cmdSpec.Subcommands) > 0 && !cmdSpec.AcceptsPositional {
					subOK := false
					for _, sub := range cmdSpec.Subcommands {
						if len(sub.Path) >= 2 && strings.EqualFold(sub.Path[1], p.CanonicalBase[1]) {
							subOK = true
							break
						}
					}
					if !subOK {
						diags = append(diags, CatalogDiagnostic{
							Code:         DiagUnknownCommand,
							ProductionID: p.ID,
							Path:         pathPrefix + ".canonical_base[1]",
							Message:      fmt.Sprintf("unknown subcommand %q for command %q", p.CanonicalBase[1], cmdSpec.Name),
							Remediation:  "Register the subcommand in internal/cli/registry.go or fix canonical_base",
						})
					}
				}
			}
			for _, tok := range p.CanonicalBase[1:] {
				if strings.HasPrefix(tok, "-") {
					if _, ok := cli.LookupFlag(tok); !ok {
						diags = append(diags, CatalogDiagnostic{
							Code:         DiagUnknownFlag,
							ProductionID: p.ID,
							Path:         pathPrefix + ".canonical_base",
							Message:      fmt.Sprintf("canonical_base flag %q is not registered in internal/cli", tok),
							Remediation:  "Register the flag in internal/cli/registry.go or fix canonical_base",
						})
					}
				}
			}
		}

		// Declare slots map
		declaredSlots := make(map[string]Slot, len(p.Slots))
		for sIdx, s := range p.Slots {
			sPath := fmt.Sprintf("%s.slots[%d]", pathPrefix, sIdx)
			if forbiddenSecretSlots[strings.ToLower(s.Name)] {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagSecretSlot,
					ProductionID: p.ID,
					Path:         sPath + ".name",
					Message:      fmt.Sprintf("slot %q is a forbidden secret positional slot per CCLI SPEC", s.Name),
					Remediation:  "Pass secrets via environment variables or stdin, never positional CCLI slots",
				})
			}
			if s.Sensitive {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagSensitiveSlot,
					ProductionID: p.ID,
					Path:         sPath + ".sensitive",
					Message:      fmt.Sprintf("slot %q has sensitive=true, which is forbidden for positional slots", s.Name),
					Remediation:  "Remove sensitive positional slot or move to a forbidden_phrases entry",
				})
			}
			arity := s.EffectiveArity()
			switch arity {
			case SlotSingle, SlotRest, SlotUntil, SlotOptional:
			default:
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagCatalogSchema,
					ProductionID: p.ID,
					Path:         sPath + ".arity",
					Message:      fmt.Sprintf("slot %q has unsupported arity %q", s.Name, s.Arity),
					Remediation:  "Use one of: single, rest, until, optional",
				})
			}
			if arity == SlotUntil && strings.TrimSpace(s.Until) == "" {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagCatalogSchema,
					ProductionID: p.ID,
					Path:         sPath + ".until",
					Message:      fmt.Sprintf("slot %q with arity='until' requires a non-empty until boundary literal", s.Name),
					Remediation:  "Set until to the literal keyword/glue token that terminates the slot",
				})
			}
			if arity == SlotOptional && s.Required {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagCatalogSchema,
					ProductionID: p.ID,
					Path:         sPath + ".required",
					Message:      fmt.Sprintf("slot %q with arity='optional' cannot have required=true", s.Name),
					Remediation:  "Set required=false on optional slots",
				})
			}
			if arity == SlotOptional && !s.HasDefault && s.Default == "" {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagCatalogSchema,
					ProductionID: p.ID,
					Path:         sPath + ".default",
					Message:      fmt.Sprintf("slot %q with arity='optional' requires a declared default or has_default=true", s.Name),
					Remediation:  "Declare default or set has_default=true for explicit empty-string defaults",
				})
			}
			if s.MinTokens < 0 || (s.MaxTokens > 0 && s.MinTokens > s.MaxTokens) {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagCatalogSchema,
					ProductionID: p.ID,
					Path:         sPath + ".min_tokens",
					Message:      fmt.Sprintf("slot %q has invalid min_tokens=%d / max_tokens=%d", s.Name, s.MinTokens, s.MaxTokens),
					Remediation:  "Ensure 0 <= min_tokens <= max_tokens",
				})
			}
			if s.Type == SlotTypeEnum && len(s.Enum) == 0 {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagCatalogSchema,
					ProductionID: p.ID,
					Path:         sPath + ".enum",
					Message:      fmt.Sprintf("slot %q with type='enum' requires a non-empty enum slice", s.Name),
					Remediation:  "Provide allowed enum values in enum[]",
				})
			}
			declaredSlots[s.Name] = s
		}

		emittedSlots := make(map[string]bool)
		for _, slotName := range p.ArgsOrder {
			if _, ok := declaredSlots[slotName]; !ok {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagUnknownSlot,
					ProductionID: p.ID,
					Path:         pathPrefix + ".args_order",
					Message:      fmt.Sprintf("args_order references undeclared slot %q", slotName),
					Remediation:  "Declare the slot in slots[] or remove it from args_order",
				})
			}
			emittedSlots[slotName] = true
		}

		for slotName, flagName := range p.FlagMapping {
			if _, ok := declaredSlots[slotName]; !ok {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagUnknownSlot,
					ProductionID: p.ID,
					Path:         pathPrefix + ".flag_mapping." + slotName,
					Message:      fmt.Sprintf("flag_mapping references undeclared slot %q", slotName),
					Remediation:  "Declare the slot in slots[] or remove it from flag_mapping",
				})
			}
			emittedSlots[slotName] = true
			if _, ok := cli.LookupFlag(flagName); !ok {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagUnknownFlag,
					ProductionID: p.ID,
					Path:         pathPrefix + ".flag_mapping." + slotName,
					Message:      fmt.Sprintf("flag_mapping flag %q is not registered in internal/cli", flagName),
					Remediation:  "Register the flag in internal/cli/registry.go or fix flag_mapping",
				})
			}
		}

		for _, sf := range p.StaticFlags {
			if _, ok := cli.LookupFlag(sf); !ok {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagUnknownFlag,
					ProductionID: p.ID,
					Path:         pathPrefix + ".static_flags",
					Message:      fmt.Sprintf("static_flags flag %q is not registered in internal/cli", sf),
					Remediation:  "Register the flag in internal/cli/registry.go or fix static_flags",
				})
			}
		}

		for _, s := range p.Slots {
			if !emittedSlots[s.Name] {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagUnmappedSlot,
					ProductionID: p.ID,
					Path:         pathPrefix + ".slots." + s.Name,
					Message:      fmt.Sprintf("declared slot %q is not mapped in args_order or flag_mapping", s.Name),
					Remediation:  "Add the slot to args_order or flag_mapping so its value is emitted",
				})
			}
		}

		// Phrase skeleton & slot presence validation
		slotInAnyPhrase := make(map[string]bool)
		if len(p.Phrases) == 0 {
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagCatalogSchema,
				ProductionID: p.ID,
				Path:         pathPrefix + ".phrases",
				Message:      "production must declare at least one phrase",
				Remediation:  "Add at least one token sequence to phrases",
			})
		}

		for pIdx, phrase := range p.Phrases {
			fPath := fmt.Sprintf("%s.phrases[%d]", pathPrefix, pIdx)
			if len(phrase) == 0 {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagCatalogSchema,
					ProductionID: p.ID,
					Path:         fPath,
					Message:      "phrase cannot be empty",
					Remediation:  "Add keyword/slot elements to phrase",
				})
				continue
			}

			skel := phraseSkeleton(phrase)
			if prevOwner, exists := phraseOwner[skel]; exists {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagDuplicatePhrase,
					ProductionID: p.ID,
					Path:         fPath,
					Message:      fmt.Sprintf("duplicate phrase skeleton %q (already declared on %q)", skel, prevOwner),
					Remediation:  "Remove duplicate phrase form or disambiguate keywords",
				})
			} else {
				phraseOwner[skel] = p.ID
			}

			for eIdx, elem := range phrase {
				ePath := fmt.Sprintf("%s[%d]", fPath, eIdx)
				kind := strings.ToLower(strings.TrimSpace(elem.Kind))
				if kind == "slot" || kind == "target" {
					if forbiddenSecretSlots[strings.ToLower(elem.Slot)] {
						diags = append(diags, CatalogDiagnostic{
							Code:         DiagSecretSlot,
							ProductionID: p.ID,
							Path:         ePath + ".slot",
							Message:      fmt.Sprintf("phrase references forbidden secret slot %q", elem.Slot),
							Remediation:  "Do not accept secrets in positional CCLI slots",
						})
					}
					if slotSpec, ok := declaredSlots[elem.Slot]; !ok {
						diags = append(diags, CatalogDiagnostic{
							Code:         DiagUnknownSlot,
							ProductionID: p.ID,
							Path:         ePath + ".slot",
							Message:      fmt.Sprintf("phrase references undeclared slot %q", elem.Slot),
							Remediation:  "Declare the slot in slots[]",
						})
					} else {
						effArity := slotSpec.EffectiveArity()
						if elem.Greedy {
							effArity = SlotRest
						}
						if effArity == SlotRest && eIdx != len(phrase)-1 {
							diags = append(diags, CatalogDiagnostic{
								Code:         DiagCatalogSchema,
								ProductionID: p.ID,
								Path:         ePath + ".slot",
								Message:      fmt.Sprintf("rest/greedy slot %q is only valid on the final phrase element", elem.Slot),
								Remediation:  "Move the rest slot to the end of the phrase or use arity='until' with a literal boundary",
							})
						}
						if effArity == SlotUntil {
							hasBoundary := false
							for nextIdx := eIdx + 1; nextIdx < len(phrase); nextIdx++ {
								nk := strings.ToLower(strings.TrimSpace(phrase[nextIdx].Kind))
								if (nk == "keyword" || nk == "glue") && strings.EqualFold(phrase[nextIdx].Value, slotSpec.Until) {
									hasBoundary = true
									break
								}
							}
							if !hasBoundary {
								diags = append(diags, CatalogDiagnostic{
									Code:         DiagCatalogSchema,
									ProductionID: p.ID,
									Path:         ePath + ".slot",
									Message:      fmt.Sprintf("until slot %q requires literal boundary %q later in the phrase", elem.Slot, slotSpec.Until),
									Remediation:  "Add the matching literal keyword/glue element after the until slot",
								})
							}
						}
					}
					slotInAnyPhrase[elem.Slot] = true
				}
			}

			// Check canonical-first shadow on first fixed token
			firstElem := phrase[0]
			if strings.EqualFold(firstElem.Kind, "keyword") || strings.EqualFold(firstElem.Kind, "glue") {
				if _, isCanonical := cli.Lookup(firstElem.Value); isCanonical {
					if p.Precedence != "phrase-only-via-do" && p.Precedence != "phrase-first" {
						diags = append(diags, CatalogDiagnostic{
							Code:         DiagCanonicalFirstShadow,
							ProductionID: p.ID,
							Path:         fPath,
							Message:      fmt.Sprintf("phrase starts with canonical command/alias %q but precedence is %q", firstElem.Value, p.Precedence),
							Remediation:  "Set precedence to 'phrase-only-via-do' or use a non-canonical leading verb",
						})
					}
				}
			}
		}

		for _, s := range p.Slots {
			if s.Required && s.Default == "" && !slotInAnyPhrase[s.Name] {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagMissingSlot,
					ProductionID: p.ID,
					Path:         pathPrefix + ".slots." + s.Name,
					Message:      fmt.Sprintf("required slot %q is absent from every phrase form", s.Name),
					Remediation:  "Include the required slot in phrases or provide a default value",
				})
			}
		}

		// Precedence validation
		switch p.Precedence {
		case "canonical-first", "phrase-only-via-do":
		case "phrase-first":
			if strings.TrimSpace(p.PrecedenceWitness) == "" {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagPrecedenceUnproven,
					ProductionID: p.ID,
					Path:         pathPrefix + ".precedence_witness",
					Message:      "precedence 'phrase-first' requires a non-empty precedence_witness",
					Remediation:  "Provide a precedence_witness argv that canonical resolution rejects, or use canonical-first / phrase-only-via-do",
				})
			} else {
				witnessTokens := strings.Fields(p.PrecedenceWitness)
				if cli.CanonicalAccepts(witnessTokens) {
					diags = append(diags, CatalogDiagnostic{
						Code:         DiagPrecedenceUnproven,
						ProductionID: p.ID,
						Path:         pathPrefix + ".precedence_witness",
						Message:      fmt.Sprintf("precedence_witness %q is accepted as a valid canonical path", p.PrecedenceWitness),
						Remediation:  "Use 'phrase-only-via-do' when the canonical CLI accepts the phrase",
					})
				}
			}
		default:
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagCatalogSchema,
				ProductionID: p.ID,
				Path:         pathPrefix + ".precedence",
				Message:      fmt.Sprintf("invalid precedence %q", p.Precedence),
				Remediation:  "Set precedence to canonical-first, phrase-only-via-do, or phrase-first",
			})
		}

		// Tier, target_policy, confirmation_policy, dry_run_policy, and implicit bypass checks
		switch p.Tier {
		case TierQuery:
		case TierMutation:
			switch p.TargetPolicy {
			case "required", "directional", "permitted-default":
			case "implicit":
				if strings.TrimSpace(p.TargetScope) == "" {
					diags = append(diags, CatalogDiagnostic{
						Code:         DiagImplicitTarget,
						ProductionID: p.ID,
						Path:         pathPrefix + ".target_scope",
						Message:      "MUTATION production with target_policy='implicit' requires a non-empty target_scope",
						Remediation:  "Specify target_scope (e.g. 'repository-rules' or 'project-transcripts')",
					})
				}
			default:
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagTargetPolicy,
					ProductionID: p.ID,
					Path:         pathPrefix + ".target_policy",
					Message:      fmt.Sprintf("MUTATION production requires valid target_policy (got %q)", p.TargetPolicy),
					Remediation:  "Set target_policy to required, directional, permitted-default, or implicit",
				})
			}
		case TierDestructive:
			switch p.TargetPolicy {
			case "required", "directional":
			case "implicit":
				if strings.TrimSpace(p.TargetScope) == "" {
					diags = append(diags, CatalogDiagnostic{
						Code:         DiagImplicitTarget,
						ProductionID: p.ID,
						Path:         pathPrefix + ".target_scope",
						Message:      "DESTRUCTIVE production with target_policy='implicit' requires a non-empty target_scope",
						Remediation:  "Specify a non-empty target_scope for implicit destructive target",
					})
				}
			default:
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagTargetPolicy,
					ProductionID: p.ID,
					Path:         pathPrefix + ".target_policy",
					Message:      fmt.Sprintf("DESTRUCTIVE production requires explicit or scoped target_policy (got %q)", p.TargetPolicy),
					Remediation:  "Set target_policy to required, directional, or implicit (with target_scope)",
				})
			}
			if p.ConfirmationPolicy != "required-interactive" && p.ConfirmationPolicy != "required-or-bypass" {
				diags = append(diags, CatalogDiagnostic{
					Code:         DiagConfirmationPolicy,
					ProductionID: p.ID,
					Path:         pathPrefix + ".confirmation_policy",
					Message:      fmt.Sprintf("DESTRUCTIVE production requires confirmation_policy 'required-interactive' or 'required-or-bypass' (got %q)", p.ConfirmationPolicy),
					Remediation:  "Set confirmation_policy to required-interactive or required-or-bypass",
				})
			}
		default:
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagCatalogSchema,
				ProductionID: p.ID,
				Path:         pathPrefix + ".tier",
				Message:      fmt.Sprintf("invalid tier %q", p.Tier),
				Remediation:  "Set tier to QUERY, MUTATION, or DESTRUCTIVE",
			})
		}

		if p.DryRunPolicy == "preview" {
			wantAliases := map[string]bool{"--dry-run": false, "--preview": false, "-n": false}
			for _, a := range p.DryRunAliases {
				if _, ok := wantAliases[a]; ok {
					wantAliases[a] = true
				}
			}
			for k, present := range wantAliases {
				if !present {
					diags = append(diags, CatalogDiagnostic{
						Code:         DiagDryRunAliases,
						ProductionID: p.ID,
						Path:         pathPrefix + ".dry_run_aliases",
						Message:      fmt.Sprintf("dry_run_policy='preview' is missing required alias %q", k),
						Remediation:  `Set dry_run_aliases to ["--dry-run", "--preview", "-n"]`,
					})
					break
				}
			}
		}

		// Check implicit bypass (--yes, -y, --force, -f)
		emitsBypass := false
		for _, tok := range append(append([]string{}, p.CanonicalBase...), p.StaticFlags...) {
			if tok == "--yes" || tok == "-y" || tok == "--force" || tok == "-f" {
				emitsBypass = true
				break
			}
		}
		for _, f := range p.FlagMapping {
			if f == "--yes" || f == "-y" || f == "--force" || f == "-f" {
				emitsBypass = true
				break
			}
		}
		if emitsBypass && !(p.Tier == TierDestructive && p.ConfirmationPolicy == "required-interactive") {
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagImplicitBypass,
				ProductionID: p.ID,
				Path:         pathPrefix + ".static_flags",
				Message:      "production emits confirmation/force bypass flag without Tier=DESTRUCTIVE and confirmation_policy=required-interactive",
				Remediation:  "Remove implicit --yes/--force flag or mark the production DESTRUCTIVE with required-interactive confirmation",
			})
		}
	}

	// Build inventory and run grammar collision detector
	inv := BuildInventoryFromCatalog(cat)
	if reports, err := DetectCollisions(inv); err != nil {
		for _, r := range reports {
			prodA := prodByID[r.ProductionA]
			prodB := prodByID[r.ProductionB]
			if prodA.SpecificityWitness != "" || prodB.SpecificityWitness != "" {
				continue
			}
			diags = append(diags, CatalogDiagnostic{
				Code:         DiagCollision,
				ProductionID: r.ProductionA,
				Path:         fmt.Sprintf("productions[%s,%s]", r.ProductionA, r.ProductionB),
				Message:      fmt.Sprintf("grammar collision [%s] between %s and %s on witness %v: %s", r.Type, r.ProductionA, r.ProductionB, r.WitnessPhrase, r.Explanation),
				Remediation:  "Disambiguate phrase keywords/slots or record a specificity_witness for ordered overlap",
			})
		}
	}

	return diags, joinDiagnostics(diags)
}

func phraseSkeleton(elems []CatalogElement) string {
	parts := make([]string, 0, len(elems))
	for _, e := range elems {
		kind := strings.ToLower(strings.TrimSpace(e.Kind))
		opt := ""
		if e.Optional {
			opt = "?"
		}
		switch kind {
		case "keyword", "glue":
			parts = append(parts, kind+":"+strings.ToLower(strings.TrimSpace(e.Value))+opt)
		case "slot", "target":
			parts = append(parts, "slot:"+opt)
		default:
			parts = append(parts, kind+":"+strings.ToLower(strings.TrimSpace(e.Value))+opt)
		}
	}
	return strings.Join(parts, " ")
}

// CatalogDigest computes a deterministic SHA-256 hex digest of a Catalog,
// independent of slice order for productions, plan_rows, forbidden_phrases,
// phrases, slots, static_flags, and dry_run_aliases.
func CatalogDigest(cat *Catalog) string {
	if cat == nil {
		return ""
	}
	norm := cloneCatalog(cat)

	sort.Slice(norm.PlanRows, func(i, j int) bool {
		return norm.PlanRows[i].PlanRow < norm.PlanRows[j].PlanRow
	})
	for i := range norm.PlanRows {
		sort.Strings(norm.PlanRows[i].ProductionIDs)
	}

	sort.Slice(norm.ForbiddenPhrases, func(i, j int) bool {
		return norm.ForbiddenPhrases[i].ID < norm.ForbiddenPhrases[j].ID
	})
	for i := range norm.ForbiddenPhrases {
		sort.Strings(norm.ForbiddenPhrases[i].SensitiveKeys)
	}

	sort.Slice(norm.Productions, func(i, j int) bool {
		return norm.Productions[i].ID < norm.Productions[j].ID
	})
	for i := range norm.Productions {
		p := &norm.Productions[i]
		sort.Strings(p.DryRunAliases)
		sort.Strings(p.StaticFlags)
		sort.Strings(p.Aliases)
		sort.Slice(p.Slots, func(a, b int) bool {
			return p.Slots[a].Name < p.Slots[b].Name
		})
		for sIdx := range p.Slots {
			sort.Strings(p.Slots[sIdx].Enum)
		}
		sort.Slice(p.Phrases, func(a, b int) bool {
			return phraseCanonicalKey(p.Phrases[a]) < phraseCanonicalKey(p.Phrases[b])
		})
	}

	raw, err := json.Marshal(norm)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func phraseCanonicalKey(elems []CatalogElement) string {
	b, _ := json.Marshal(elems)
	return string(b)
}

func cloneCatalog(cat *Catalog) *Catalog {
	if cat == nil {
		return nil
	}
	raw, _ := json.Marshal(cat)
	var out Catalog
	_ = json.Unmarshal(raw, &out)
	return &out
}

func cloneInventory(inv *Inventory) *Inventory {
	if inv == nil {
		return nil
	}
	raw, _ := json.Marshal(inv)
	var out Inventory
	_ = json.Unmarshal(raw, &out)
	out.Catalog = cloneCatalog(inv.Catalog)
	return &out
}
