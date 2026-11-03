package ccli

import "context"

// TokenClass represents the grammatical category of a command line token.
type TokenClass string

const (
	TokenKeyword TokenClass = "KEYWORD"
	TokenGlue    TokenClass = "GLUE"
	TokenFlag    TokenClass = "FLAG"
	TokenOperand TokenClass = "OPERAND"
	TokenTarget  TokenClass = "TARGET"
)

// MutationTier represents the operational safety tier of a command.
type MutationTier string

const (
	TierQuery       MutationTier = "QUERY"
	TierMutation    MutationTier = "MUTATION"
	TierDestructive MutationTier = "DESTRUCTIVE"
)

// ExitCode represents POSIX-compliant process exit codes.
type ExitCode int

const (
	ExitSuccess ExitCode = 0 // Success (action executed cleanly)
	ExitUsage   ExitCode = 2 // Usage / syntax error (unknown phrase, missing target, ambiguity)
	ExitPolicy  ExitCode = 3 // Policy refusal (destructive confirmation refused, missing bypass, secret violation)
	ExitRuntime ExitCode = 4 // Runtime execution failure (network timeout, resource not found)
	ExitSystem  ExitCode = 5 // Internal system fault (I/O error, panic, OS fault)
)

// Token represents a single classified token from the command line argument list.
type Token struct {
	Index    int        `json:"index"`
	Raw      string     `json:"raw"`
	Class    TokenClass `json:"class"`
	SlotName *string    `json:"slot_name"`
}

// FlagArity represents the number of argument values a flag consumes.
type FlagArity int

const (
	FlagArityBool  FlagArity = 0
	FlagArityValue FlagArity = 1
)

// FlagSpec defines the configuration of a command line flag.
type FlagSpec struct {
	Arity       int    `json:"arity"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// FlagCatalog maps flag names (such as "--verbose") to their specifications.
type FlagCatalog map[string]FlagSpec

// SlotType represents the data type expected in a grammar slot.
type SlotType string

const (
	SlotTypeString   SlotType = "string"
	SlotTypeInt      SlotType = "int"
	SlotTypePath     SlotType = "path"
	SlotTypeProject  SlotType = "project"
	SlotTypeTool     SlotType = "tool"
	SlotTypeEnum     SlotType = "enum"
	SlotTypeTarget   SlotType = "target"
	SlotTypeOperand  SlotType = "operand"
	SlotTypeSlug     SlotType = "slug"
	SlotTypeDuration SlotType = "duration"
	SlotTypeURL      SlotType = "url"
	SlotTypeDomain   SlotType = "domain"
	SlotTypeGroupID  SlotType = "group-id"
	SlotTypeIntList  SlotType = "int-list"
)

// SlotArity represents the token-consumption cardinality of a grammar slot.
type SlotArity string

const (
	SlotSingle   SlotArity = "single"
	SlotRest     SlotArity = "rest"
	SlotUntil    SlotArity = "until"
	SlotOptional SlotArity = "optional"
)

// Slot defines a named variable parameter slot within a grammar production.
type Slot struct {
	Name        string    `json:"name"`
	Type        SlotType  `json:"type"`
	Arity       SlotArity `json:"arity,omitempty"`
	Until       string    `json:"until,omitempty"`
	MinTokens   int       `json:"min_tokens,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Positive    bool      `json:"positive,omitempty"`
	Description string    `json:"description,omitempty"`
	Required    bool      `json:"required"`
	Default     string    `json:"default,omitempty"`
	HasDefault  bool      `json:"has_default,omitempty"`
	IsTarget    bool      `json:"is_target"`
	Target      bool      `json:"target,omitempty"`
	Sensitive   bool      `json:"sensitive"`
	Greedy      bool      `json:"greedy,omitempty"`
	MultiToken  bool      `json:"multi_token,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
	Validator   string    `json:"validator,omitempty"`
}

// EffectiveArity returns the canonical SlotArity for a slot.
func (s *Slot) EffectiveArity() SlotArity {
	if s == nil {
		return SlotSingle
	}
	if s.Arity != "" {
		return s.Arity
	}
	if s.Greedy || s.MultiToken {
		return SlotRest
	}
	if !s.Required && (s.HasDefault || s.Default != "") {
		return SlotOptional
	}
	return SlotSingle
}

// FlagExtractionMode controls how ExtractFlagsWithMode handles unknown flags.
type FlagExtractionMode string

const (
	FlagModeStrict  FlagExtractionMode = "strict"
	FlagModeLenient FlagExtractionMode = "lenient"
)

// ParseOptions configures optional behavior for ParseWithOptions.
type ParseOptions struct {
	FlagMode FlagExtractionMode
}

// SpecificityScore breaks down a production phrase's deterministic specificity ranking.
type SpecificityScore struct {
	FixedLiterals int  `json:"fixed_literals"`
	MinTokens     int  `json:"min_tokens"`
	HasRestOrOpt  bool `json:"has_rest_or_opt"`
	TotalScore    int  `json:"total_score"`
}

// ProductionElementKind identifies the kind of element in a grammar production.
type ProductionElementKind string

const (
	ElementKeyword ProductionElementKind = "KEYWORD"
	ElementGlue    ProductionElementKind = "GLUE"
	ElementSlot    ProductionElementKind = "SLOT"
	ElementTarget  ProductionElementKind = "TARGET"
)

// ProductionElement represents a single step or slot in a grammar phrase pattern.
type ProductionElement struct {
	Kind     ProductionElementKind `json:"kind"`
	Value    string                `json:"value,omitempty"`
	Optional bool                  `json:"optional,omitempty"`
	Slot     *Slot                 `json:"slot,omitempty"`
}

// ProductionExamples holds paired canonical and conversational CLI examples.
type ProductionExamples struct {
	Canonical      string `json:"canonical,omitempty"`
	Conversational string `json:"conversational,omitempty"`
}

// Production represents a registered phrase grammar production rule.
type Production struct {
	ID                    string                `json:"id"`
	ActionID              string                `json:"action_id"`
	Domain                string                `json:"domain,omitempty"`
	Docs                  string                `json:"docs,omitempty"`
	Tier                  MutationTier          `json:"tier"`
	Directional           bool                  `json:"directional"`
	Elements              []ProductionElement   `json:"elements"`
	CanonicalBase         []string              `json:"canonical_base"`
	FlagMapping           map[string]string     `json:"flag_mapping"`
	ArgsOrder             []string              `json:"args_order"`
	StaticFlags           []string              `json:"static_flags,omitempty"`
	Slots                 []Slot                `json:"slots,omitempty"`
	TargetPolicy          string                `json:"target_policy,omitempty"`
	TargetScope           string                `json:"target_scope,omitempty"`
	PlanRow               int                   `json:"plan_row,omitempty"`
	DryRunPolicy          string                `json:"dry_run_policy,omitempty"`
	DryRunAliases         []string              `json:"dry_run_aliases,omitempty"`
	ConfirmationPolicy    string                `json:"confirmation_policy,omitempty"`
	Precedence            string                `json:"precedence,omitempty"`
	PrecedenceWitness     string                `json:"precedence_witness,omitempty"`
	SpecificityWitness    string                `json:"specificity_witness,omitempty"`
	PreserveExit          int                   `json:"preserve_exit,omitempty"`
	VariantOf             string                `json:"variant_of,omitempty"`
	Source                string                `json:"source,omitempty"`
	SourceCitation        string                `json:"source_citation,omitempty"`
	Correction            string                `json:"correction,omitempty"`
	Examples              ProductionExamples    `json:"examples,omitempty"`
	Aliases               []string              `json:"aliases,omitempty"`
	Deprecated            string                `json:"deprecated,omitempty"`
	Commutative           bool                  `json:"commutative,omitempty"`
	CommutativeElements   [][]ProductionElement `json:"commutative_elements,omitempty"`
	WhitelistedInversions [][]ProductionElement `json:"whitelisted_inversions,omitempty"`
	Description           string                `json:"description,omitempty"`
}

// Inventory contains a collection of registered productions and flag catalogs.
type Inventory struct {
	Productions []*Production `json:"productions"`
	FlagCatalog FlagCatalog   `json:"flag_catalog"`
	Catalog     *Catalog      `json:"-"`
}

// AST represents the abstract syntax tree produced by phrase parsing.
type AST struct {
	ActionID     string            `json:"action_id"`
	ProductionID string            `json:"production_id"`
	Tier         MutationTier      `json:"tier"`
	TargetPolicy string            `json:"target_policy,omitempty"`
	TargetScope  string            `json:"target_scope,omitempty"`
	Specificity  SpecificityScore  `json:"specificity"`
	Tokens       []Token           `json:"tokens"`
	Operands     map[string]string `json:"operands"`
	Flags        map[string]string `json:"flags"`
	RawFlags     []string          `json:"raw_flags"`
	Target       string            `json:"target"`
}

// ParseResult holds the output of command line argument parsing.
type ParseResult struct {
	AST           *AST             `json:"ast"`
	Specificity   SpecificityScore `json:"specificity"`
	Tokens        []Token          `json:"tokens"`
	CanonicalArgs []string         `json:"canonical_args"`
	ExitCode      ExitCode         `json:"exit_code"`
	ErrorMessage  string           `json:"error_message,omitempty"`
}

// HandlerFunc defines the callback signature for executing an action.
type HandlerFunc func(ctx context.Context, ast *AST, streams *Streams) ExitCode

// Action represents an executable action associated with an ActionID.
type Action struct {
	ID          string       `json:"id"`
	Tier        MutationTier `json:"tier"`
	Description string       `json:"description"`
	Handler     HandlerFunc  `json:"-"`
}

// StringPtr is a helper that returns a pointer to a string.
func StringPtr(s string) *string {
	return &s
}
