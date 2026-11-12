package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MemorySchemaVersion is the only accepted durable memory envelope version.
const MemorySchemaVersion = "sila/memory/v1"

// MemoryMaxBytes bounds one envelope on disk and on the wire.
const MemoryMaxBytes = 1 << 20

// Memory error codes. They are public contract: scripts branch on them.
const (
	MemoryCodeJSONInvalid       = "MEMORY_JSON_INVALID"
	MemoryCodeSchemaUnknown     = "MEMORY_SCHEMA_UNKNOWN"
	MemoryCodeSchemaVersion     = "MEMORY_SCHEMA_VERSION"
	MemoryCodeFieldUnknown      = "MEMORY_FIELD_UNKNOWN"
	MemoryCodeLimit             = "MEMORY_LIMIT"
	MemoryCodeEnum              = "MEMORY_ENUM"
	MemoryCodeBound             = "MEMORY_BOUND"
	MemoryCodeEvidenceRef       = "MEMORY_EVIDENCE_REF"
	MemoryCodeEvidencePath      = "MEMORY_EVIDENCE_PATH"
	MemoryCodeEvidenceDigest    = "MEMORY_EVIDENCE_DIGEST"
	MemoryCodeRelationTarget    = "MEMORY_RELATION_TARGET"
	MemoryCodeRelationCycle     = "MEMORY_RELATION_CYCLE"
	MemoryCodeIdempotency       = "IDEMPOTENCY_CONFLICT"
	MemoryCodeDuplicate         = "DUPLICATE_MEMORY"
	MemoryCodeSimilar           = "SIMILAR_MEMORY"
	MemoryCodeProjectUnresolved = "MEMORY_PROJECT_UNRESOLVED"
)

// Memory kinds.
const (
	MemoryKindArchitectureDecision = "architecture_decision"
	MemoryKindInvariant            = "invariant"
	MemoryKindConvention           = "convention"
	MemoryKindBug                  = "bug"
	MemoryKindRootCause            = "root_cause"
	MemoryKindFix                  = "fix"
	MemoryKindFailedApproach       = "failed_approach"
	MemoryKindPreference           = "preference"
	MemoryKindCodeLandmark         = "code_landmark"
	MemoryKindOwnershipBoundary    = "ownership_boundary"
	MemoryKindProcedure            = "procedure"
)

var memoryKinds = []string{
	MemoryKindArchitectureDecision, MemoryKindInvariant, MemoryKindConvention,
	MemoryKindBug, MemoryKindRootCause, MemoryKindFix, MemoryKindFailedApproach,
	MemoryKindPreference, MemoryKindCodeLandmark, MemoryKindOwnershipBoundary,
	MemoryKindProcedure,
}

var memoryKindSet = func() map[string]bool {
	m := make(map[string]bool, len(memoryKinds))
	for _, k := range memoryKinds {
		m[k] = true
	}
	return m
}()

// MemoryKindAliases maps legacy kind names onto the v1 enum.
var MemoryKindAliases = map[string]string{
	"decision": MemoryKindArchitectureDecision,
}

// Assertion predicates, split by assertion type.
var (
	memoryDescriptivePredicates = map[string]bool{"has_property": true, "causes": true, "supersedes": true}
	memoryNormativePredicates   = map[string]bool{"requires": true, "forbids": true, "recommends": true}
)

// Memory assertion and evidence kinds.
const (
	MemoryAssertionDescriptive = "descriptive"
	MemoryAssertionNormative   = "normative"

	MemoryEvidenceFile            = "file"
	MemoryEvidenceTest            = "test"
	MemoryEvidenceCommit          = "commit"
	MemoryEvidenceAgentInitiated  = "agent_initiated"
	MemoryEvidenceUserAttestation = "user_attestation"
)

var memoryEvidenceKinds = []string{
	MemoryEvidenceFile, MemoryEvidenceTest, MemoryEvidenceCommit,
	MemoryEvidenceAgentInitiated, MemoryEvidenceUserAttestation,
}

var memoryEvidenceKindSet = func() map[string]bool {
	m := make(map[string]bool, len(memoryEvidenceKinds))
	for _, k := range memoryEvidenceKinds {
		m[k] = true
	}
	return m
}()

// Memory relation types.
const (
	MemoryRelationSupersedes  = "supersedes"
	MemoryRelationRefines     = "refines"
	MemoryRelationContradicts = "contradicts"
	MemoryRelationRelatedTo   = "related_to"
)

var memoryRelationTypes = []string{
	MemoryRelationSupersedes, MemoryRelationRefines,
	MemoryRelationContradicts, MemoryRelationRelatedTo,
}

var memoryRelationSet = func() map[string]bool {
	m := make(map[string]bool, len(memoryRelationTypes))
	for _, k := range memoryRelationTypes {
		m[k] = true
	}
	return m
}()

// MemorySubject is the entity an assertion is about.
type MemorySubject struct {
	ContextEntity string `json:"context_entity"`
	Value         string `json:"value"`
}

// MemoryLiteral is a typed object value.
type MemoryLiteral struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}

// MemoryObject is the right-hand side of an assertion.
type MemoryObject struct {
	Literal   *MemoryLiteral `json:"literal,omitempty"`
	Qualifier string         `json:"qualifier,omitempty"`
}

// MemoryAssertion is one typed statement inside an envelope.
type MemoryAssertion struct {
	ID            string        `json:"id"`
	AssertionType string        `json:"assertion_type"`
	Predicate     string        `json:"predicate"`
	Statement     string        `json:"statement"`
	Subject       MemorySubject `json:"subject"`
	Object        MemoryObject  `json:"object"`
	Confidence    float64       `json:"confidence"`
	Importance    int           `json:"importance"`
	EvidenceRefs  []string      `json:"evidence_refs"`
}

// MemoryLocator points at evidence without embedding it.
type MemoryLocator struct {
	Path      string `json:"path,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	Commit    string `json:"commit,omitempty"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
}

// MemoryEvidence is one supporting observation.
type MemoryEvidence struct {
	ID             string        `json:"id"`
	Kind           string        `json:"kind"`
	Locator        MemoryLocator `json:"locator"`
	Summary        string        `json:"summary"`
	SHA256         string        `json:"sha256,omitempty"`
	MissingAtWrite bool          `json:"missing_at_write,omitempty"`
	MissingReason  string        `json:"missing_reason,omitempty"`
}

// MemoryRelation links this envelope to another memory.
type MemoryRelation struct {
	Type           string   `json:"type"`
	TargetMemoryID string   `json:"target_memory_id"`
	EvidenceRefs   []string `json:"evidence_refs,omitempty"`
}

// MemoryScope bounds where a memory applies.
type MemoryScope struct {
	Paths   []string `json:"paths,omitempty"`
	Symbols []string `json:"symbols,omitempty"`
}

// MemoryProvenance attributes an envelope to an agent or person.
type MemoryProvenance struct {
	Agent        string `json:"agent,omitempty"`
	ToolName     string `json:"tool_name,omitempty"`
	ClientKind   string `json:"client_kind,omitempty"`
	OriginEntity string `json:"origin_entity,omitempty"`
	SessionRef   string `json:"session_ref,omitempty"`
}

// MemoryEnvelope is the strict sila/memory/v1 durable record.
type MemoryEnvelope struct {
	SchemaVersion  string            `json:"schema_version"`
	MemoryID       string            `json:"memory_id,omitempty"`
	ProjectID      string            `json:"project_id"`
	Kind           string            `json:"kind"`
	Title          string            `json:"title"`
	Summary        string            `json:"summary"`
	Assertions     []MemoryAssertion `json:"assertions"`
	Evidence       []MemoryEvidence  `json:"evidence"`
	Relations      []MemoryRelation  `json:"relations,omitempty"`
	Scope          *MemoryScope      `json:"scope,omitempty"`
	ValidFrom      string            `json:"valid_from,omitempty"`
	ValidUntil     *string           `json:"valid_until,omitempty"`
	Provenance     *MemoryProvenance `json:"provenance,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	CreatedAt      string            `json:"created_at,omitempty"`
	UpdatedAt      string            `json:"updated_at,omitempty"`
}

// MemoryError is a stable, machine-readable envelope failure.
type MemoryError struct {
	Code    string
	Field   string
	Message string
}

func (e *MemoryError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s: %s", e.Code, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// MemoryCode returns the stable code of any memory error.
func MemoryCode(err error) string {
	var me *MemoryError
	if errors.As(err, &me) {
		return me.Code
	}
	var ve *MemoryValidationError
	if errors.As(err, &ve) && len(ve.Diagnostics) > 0 {
		return ve.Diagnostics[0].Code
	}
	return ""
}

// MemoryDiagnostic is one validation finding.
type MemoryDiagnostic struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

func (d MemoryDiagnostic) String() string {
	if d.Field != "" {
		return fmt.Sprintf("%s: %s: %s", d.Code, d.Field, d.Message)
	}
	return fmt.Sprintf("%s: %s", d.Code, d.Message)
}

// MemoryValidationError reports every diagnostic found in one pass.
type MemoryValidationError struct {
	Diagnostics []MemoryDiagnostic
}

func (e *MemoryValidationError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "memory envelope is invalid"
	}
	parts := make([]string, 0, len(e.Diagnostics))
	for _, d := range e.Diagnostics {
		parts = append(parts, d.String())
	}
	return strings.Join(parts, "; ")
}

func memoryErr(code, field, format string, args ...any) *MemoryError {
	return &MemoryError{Code: code, Field: field, Message: fmt.Sprintf(format, args...)}
}

// DecodeMemoryEnvelope strictly decodes one sila/memory/v1 envelope.
func DecodeMemoryEnvelope(data []byte) (*MemoryEnvelope, error) {
	if len(data) > MemoryMaxBytes {
		return nil, memoryErr(MemoryCodeJSONInvalid, "", "envelope is %d bytes; limit is %d", len(data), MemoryMaxBytes)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return nil, err
	}

	var probe struct {
		SchemaVersion *string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, memoryErr(MemoryCodeJSONInvalid, "", "malformed JSON: %v", err)
	}
	if probe.SchemaVersion == nil {
		return nil, memoryErr(MemoryCodeSchemaUnknown, "schema_version", "schema_version is required")
	}
	if *probe.SchemaVersion != MemorySchemaVersion {
		return nil, memoryErr(MemoryCodeSchemaVersion, "schema_version", "unsupported version %q; expected %q", *probe.SchemaVersion, MemorySchemaVersion)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	var env MemoryEnvelope
	if err := dec.Decode(&env); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			field := unknownFieldName(err.Error())
			return nil, memoryErr(MemoryCodeFieldUnknown, field, "unknown field")
		}
		return nil, memoryErr(MemoryCodeJSONInvalid, "", "decode envelope: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return nil, memoryErr(MemoryCodeJSONInvalid, "", "trailing JSON after envelope")
	}
	normalizeMemoryNumbers(&env)
	return &env, nil
}

func unknownFieldName(msg string) string {
	const marker = `unknown field "`
	i := strings.Index(msg, marker)
	if i < 0 {
		return ""
	}
	rest := msg[i+len(marker):]
	if j := strings.Index(rest, `"`); j >= 0 {
		return rest[:j]
	}
	return ""
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := scanJSONValue(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return memoryErr(MemoryCodeJSONInvalid, "", "trailing JSON after envelope")
	}
	return nil
}

func scanJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return memoryErr(MemoryCodeJSONInvalid, "", "malformed JSON: %v", err)
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return memoryErr(MemoryCodeJSONInvalid, "", "malformed JSON object: %v", err)
			}
			key, _ := kt.(string)
			if seen[key] {
				return memoryErr(MemoryCodeJSONInvalid, key, "duplicate key")
			}
			seen[key] = true
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := scanJSONValue(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}

func normalizeMemoryNumbers(env *MemoryEnvelope) {
	for i := range env.Assertions {
		lit := env.Assertions[i].Object.Literal
		if lit == nil {
			continue
		}
		if n, ok := lit.Value.(json.Number); ok {
			if f, err := n.Float64(); err == nil {
				lit.Value = f
			}
		}
	}
}

// EncodeMemoryEnvelope renders the canonical compact JSON for storage.
func EncodeMemoryEnvelope(env *MemoryEnvelope) ([]byte, error) {
	if env == nil {
		return nil, memoryErr(MemoryCodeJSONInvalid, "", "nil envelope")
	}
	return json.Marshal(env)
}

// MemoryValidateOptions carries filesystem context for evidence checks.
type MemoryValidateOptions struct {
	// ProjectDir enables existence, symlink-escape and digest verification.
	ProjectDir string
	// Now overrides time.Now for deterministic tests.
	Now time.Time
}

// ValidateMemoryEnvelope returns every semantic diagnostic. It never stops at
// the first failure and never mutates the envelope.
func ValidateMemoryEnvelope(env *MemoryEnvelope, opts MemoryValidateOptions) []MemoryDiagnostic {
	var diags []MemoryDiagnostic
	add := func(code, field, format string, args ...any) {
		diags = append(diags, MemoryDiagnostic{Code: code, Field: field, Message: fmt.Sprintf(format, args...)})
	}
	if env == nil {
		return []MemoryDiagnostic{{Code: MemoryCodeJSONInvalid, Message: "nil envelope"}}
	}
	if env.SchemaVersion != MemorySchemaVersion {
		add(MemoryCodeSchemaVersion, "schema_version", "unsupported version %q", env.SchemaVersion)
	}
	if strings.TrimSpace(env.ProjectID) == "" {
		add(MemoryCodeBound, "project_id", "project_id is required")
	} else if len(env.ProjectID) > 128 {
		add(MemoryCodeLimit, "project_id", "project_id exceeds 128 bytes")
	}
	if !memoryKindSet[env.Kind] {
		add(MemoryCodeEnum, "kind", "invalid kind %q", env.Kind)
	}
	checkText := func(field, value string, min, max int) {
		n := utf8.RuneCountInString(value)
		if n < min {
			add(MemoryCodeBound, field, "must be at least %d characters", min)
		}
		if n > max {
			add(MemoryCodeBound, field, "must be at most %d characters", max)
		}
	}
	checkText("title", env.Title, 1, 200)
	checkText("summary", env.Summary, 1, 2000)

	if len(env.Assertions) < 1 || len(env.Assertions) > 8 {
		add(MemoryCodeLimit, "assertions", "must contain 1-8 assertions")
	}
	if len(env.Evidence) < 1 || len(env.Evidence) > 8 {
		add(MemoryCodeLimit, "evidence", "must contain 1-8 evidence items")
	}
	if len(env.Relations) > 8 {
		add(MemoryCodeLimit, "relations", "must contain at most 8 relations")
	}
	if env.Scope != nil {
		if len(env.Scope.Paths) > 32 {
			add(MemoryCodeLimit, "scope.paths", "at most 32 paths")
		}
		if len(env.Scope.Symbols) > 16 {
			add(MemoryCodeLimit, "scope.symbols", "at most 16 symbols")
		}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if env.ValidFrom != "" {
		t, err := time.Parse(time.RFC3339, env.ValidFrom)
		if err != nil {
			add(MemoryCodeBound, "valid_from", "must be RFC3339")
		} else if t.After(now.Add(5 * time.Minute)) {
			add(MemoryCodeBound, "valid_from", "must not be in the future")
		}
	}
	if env.ValidUntil != nil && *env.ValidUntil != "" {
		t, err := time.Parse(time.RFC3339, *env.ValidUntil)
		if err != nil {
			add(MemoryCodeBound, "valid_until", "must be RFC3339 or null")
		} else if env.ValidFrom != "" {
			from, ferr := time.Parse(time.RFC3339, env.ValidFrom)
			if ferr == nil && !t.After(from) {
				add(MemoryCodeBound, "valid_until", "must be after valid_from")
			}
		}
	}
	if env.IdempotencyKey != "" {
		if n := len(env.IdempotencyKey); n < 16 || n > 128 {
			add(MemoryCodeBound, "idempotency_key", "must be 16-128 printable characters")
		} else {
			for _, r := range env.IdempotencyKey {
				if r < 0x21 || r > 0x7e {
					add(MemoryCodeBound, "idempotency_key", "must be printable ASCII without spaces")
					break
				}
			}
		}
	}

	evidenceIDs := map[string]bool{}
	missingAtWrite := false
	userAttested := false
	for i := range env.Evidence {
		ev := &env.Evidence[i]
		field := fmt.Sprintf("evidence[%d]", i)
		if !memoryEvidenceIDPattern.MatchString(ev.ID) {
			add(MemoryCodeBound, field+".id", "must match E-NNN")
		}
		if evidenceIDs[ev.ID] {
			add(MemoryCodeBound, field+".id", "duplicate evidence id %q", ev.ID)
		}
		evidenceIDs[ev.ID] = true
		if !memoryEvidenceKindSet[ev.Kind] {
			add(MemoryCodeEnum, field+".kind", "invalid evidence kind %q", ev.Kind)
		}
		checkEvidenceText := func(f string, v string, max int) {
			if n := utf8.RuneCountInString(v); n < 1 || n > max {
				add(MemoryCodeBound, f, "must be 1-%d characters", max)
			}
		}
		checkEvidenceText(field+".summary", ev.Summary, 500)
		diags = append(diags, validateMemoryEvidence(*ev, field, opts)...)
		if ev.Kind == MemoryEvidenceUserAttestation {
			userAttested = true
		}
		if ev.MissingAtWrite {
			missingAtWrite = true
			if strings.TrimSpace(ev.MissingReason) == "" {
				add(MemoryCodeBound, field+".missing_reason", "required when missing_at_write is true")
			}
		}
	}

	assertionIDs := map[string]bool{}
	for i := range env.Assertions {
		a := &env.Assertions[i]
		field := fmt.Sprintf("assertions[%d]", i)
		if !memoryAssertionIDPattern.MatchString(a.ID) {
			add(MemoryCodeBound, field+".id", "must match A-NNN")
		}
		if assertionIDs[a.ID] {
			add(MemoryCodeBound, field+".id", "duplicate assertion id %q", a.ID)
		}
		assertionIDs[a.ID] = true
		if a.AssertionType != MemoryAssertionDescriptive && a.AssertionType != MemoryAssertionNormative {
			add(MemoryCodeEnum, field+".assertion_type", "invalid assertion_type %q", a.AssertionType)
		} else {
			allowed := memoryDescriptivePredicates
			if a.AssertionType == MemoryAssertionNormative {
				allowed = memoryNormativePredicates
			}
			if !allowed[a.Predicate] {
				add(MemoryCodeEnum, field+".predicate", "predicate %q is not allowed for %s assertions", a.Predicate, a.AssertionType)
			}
		}
		if n := utf8.RuneCountInString(a.Statement); n < 1 || n > 2000 {
			add(MemoryCodeBound, field+".statement", "must be 1-2000 characters")
		}
		if !memorySubjectEntitySet[a.Subject.ContextEntity] {
			add(MemoryCodeEnum, field+".subject.context_entity", "invalid context_entity %q", a.Subject.ContextEntity)
		}
		if n := len(a.Subject.Value); n < 1 || n > 512 {
			add(MemoryCodeBound, field+".subject.value", "must be 1-512 bytes")
		}
		if a.Object.Literal == nil {
			add(MemoryCodeBound, field+".object.literal", "literal is required")
		} else {
			diags = append(diags, validateMemoryLiteral(*a.Object.Literal, field+".object.literal")...)
		}
		if a.Object.Qualifier != "" && !memoryQualifierPattern.MatchString(a.Object.Qualifier) {
			add(MemoryCodeEnum, field+".object.qualifier", "must be snake_case")
		}
		confCap := 0.8
		impCap := 4
		if userAttested {
			confCap = 0.95
			impCap = 5
		}
		if a.Confidence < 0 || a.Confidence > 1 {
			add(MemoryCodeBound, field+".confidence", "must be within [0,1]")
		} else if a.Confidence > confCap {
			add(MemoryCodeBound, field+".confidence", "agent-initiated saves cap confidence at %.2f", confCap)
		}
		if a.Importance < 1 || a.Importance > 5 {
			add(MemoryCodeBound, field+".importance", "must be within [1,5]")
		} else if a.Importance > impCap {
			add(MemoryCodeBound, field+".importance", "agent-initiated saves cap importance at %d", impCap)
		}
		if len(a.EvidenceRefs) < 1 || len(a.EvidenceRefs) > 8 {
			add(MemoryCodeLimit, field+".evidence_refs", "must contain 1-8 evidence references")
		}
		seenRef := map[string]bool{}
		for _, ref := range a.EvidenceRefs {
			if !evidenceIDs[ref] {
				add(MemoryCodeEvidenceRef, field+".evidence_refs", "unknown evidence reference %q", ref)
			}
			if seenRef[ref] {
				add(MemoryCodeBound, field+".evidence_refs", "duplicate evidence reference %q", ref)
			}
			seenRef[ref] = true
		}
	}

	relationTargets := map[string]bool{}
	for i := range env.Relations {
		rel := &env.Relations[i]
		field := fmt.Sprintf("relations[%d]", i)
		if !memoryRelationSet[rel.Type] {
			add(MemoryCodeEnum, field+".type", "invalid relation type %q", rel.Type)
		}
		if strings.TrimSpace(rel.TargetMemoryID) == "" {
			add(MemoryCodeBound, field+".target_memory_id", "target_memory_id is required")
		}
		if env.MemoryID != "" && rel.TargetMemoryID == env.MemoryID {
			add(MemoryCodeRelationCycle, field+".target_memory_id", "a memory cannot relate to itself")
		}
		key := rel.Type + "\x00" + rel.TargetMemoryID
		if relationTargets[key] {
			add(MemoryCodeBound, field, "duplicate relation %s -> %s", rel.Type, rel.TargetMemoryID)
		}
		relationTargets[key] = true
		for _, ref := range rel.EvidenceRefs {
			if !evidenceIDs[ref] {
				add(MemoryCodeEvidenceRef, field+".evidence_refs", "unknown evidence reference %q", ref)
			}
		}
	}
	if missingAtWrite && len(diags) == 0 {
		// missing_at_write is legal; callers report grounded=false from storage.
		_ = missingAtWrite
	}
	return diags
}

var (
	memoryAssertionIDPattern = regexp.MustCompile(`^A-[0-9]{3}$`)
	memoryEvidenceIDPattern  = regexp.MustCompile(`^E-[0-9]{3}$`)
	memoryQualifierPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	memorySHA256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

var memorySubjectEntitySet = map[string]bool{
	"project": true, "file": true, "symbol": true, "memory": true,
}

func validateMemoryLiteral(lit MemoryLiteral, field string) []MemoryDiagnostic {
	var diags []MemoryDiagnostic
	add := func(code, f, format string, args ...any) {
		diags = append(diags, MemoryDiagnostic{Code: code, Field: f, Message: fmt.Sprintf(format, args...)})
	}
	switch lit.Type {
	case "string":
		s, ok := lit.Value.(string)
		if !ok {
			add(MemoryCodeBound, field+".value", "must be a string")
		} else if len(s) > 512 {
			add(MemoryCodeBound, field+".value", "must be at most 512 bytes")
		}
	case "boolean":
		if _, ok := lit.Value.(bool); !ok {
			add(MemoryCodeBound, field+".value", "must be a boolean")
		}
	case "number":
		f, ok := lit.Value.(float64)
		if !ok {
			add(MemoryCodeBound, field+".value", "must be a number")
		} else if f != f || f > 1e308 || f < -1e308 {
			add(MemoryCodeBound, field+".value", "must be a finite number")
		}
	default:
		add(MemoryCodeEnum, field+".type", "invalid literal type %q", lit.Type)
	}
	return diags
}

func validateMemoryEvidence(ev MemoryEvidence, field string, opts MemoryValidateOptions) []MemoryDiagnostic {
	var diags []MemoryDiagnostic
	add := func(code, f, format string, args ...any) {
		diags = append(diags, MemoryDiagnostic{Code: code, Field: f, Message: fmt.Sprintf(format, args...)})
	}
	if ev.SHA256 != "" && !memorySHA256Pattern.MatchString(ev.SHA256) {
		add(MemoryCodeEvidenceDigest, field+".sha256", "must be 64 lowercase hex characters")
	}
	switch ev.Kind {
	case MemoryEvidenceFile:
		if ev.Locator.Path == "" {
			add(MemoryCodeBound, field+".locator.path", "file evidence requires a path")
			return diags
		}
	case MemoryEvidenceTest:
		if ev.Locator.Path == "" && ev.Locator.Symbol == "" {
			add(MemoryCodeBound, field+".locator", "test evidence requires a path or symbol")
		}
	case MemoryEvidenceCommit:
		if strings.TrimSpace(ev.Locator.Commit) == "" {
			add(MemoryCodeBound, field+".locator.commit", "commit evidence requires a commit")
		}
	}
	if ev.Locator.Path != "" {
		diags = append(diags, validateMemoryPath(ev, field, opts)...)
	} else if ev.Locator.LineStart < 0 || ev.Locator.LineEnd < 0 || (ev.Locator.LineEnd > 0 && ev.Locator.LineEnd < ev.Locator.LineStart) {
		add(MemoryCodeBound, field+".locator", "invalid line range")
	}
	return diags
}

var (
	memorySecretBasenames = map[string]bool{
		".env": true, ".npmrc": true, ".netrc": true, ".pgpass": true,
		"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
		"credentials": true, "secrets": true, "secret": true,
	}
	memorySecretExtensions = map[string]bool{
		".pem": true, ".key": true, ".p12": true, ".pfx": true, ".kdbx": true, ".jks": true,
	}
	memoryWindowsAbsPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

func validateMemoryPath(ev MemoryEvidence, field string, opts MemoryValidateOptions) []MemoryDiagnostic {
	var diags []MemoryDiagnostic
	add := func(code, f, format string, args ...any) {
		diags = append(diags, MemoryDiagnostic{Code: code, Field: f, Message: fmt.Sprintf(format, args...)})
	}
	p := ev.Locator.Path
	if len(p) > 512 {
		add(MemoryCodeLimit, field+".locator.path", "path exceeds 512 bytes")
		return diags
	}
	if filepath.IsAbs(p) || memoryWindowsAbsPattern.MatchString(p) || strings.HasPrefix(p, "/") {
		add(MemoryCodeEvidencePath, field+".locator.path", "path must be repository-relative")
		return diags
	}
	clean := filepath.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		add(MemoryCodeEvidencePath, field+".locator.path", "path must not traverse outside the repository")
		return diags
	}
	if strings.HasPrefix(p, "~") {
		add(MemoryCodeEvidencePath, field+".locator.path", "home-relative paths are not allowed")
		return diags
	}
	lower := strings.ToLower(filepath.ToSlash(clean))
	if strings.HasPrefix(lower, "tmp/") || strings.Contains(lower, "/tmp/") || strings.Contains(lower, "/var/tmp/") {
		add(MemoryCodeEvidencePath, field+".locator.path", "temporary paths are not durable evidence")
		return diags
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if strings.HasPrefix(clean, filepath.Clean(home)) {
			add(MemoryCodeEvidencePath, field+".locator.path", "home-directory paths are not allowed")
			return diags
		}
	}
	base := strings.ToLower(filepath.Base(clean))
	if memorySecretBasenames[base] || strings.HasPrefix(base, ".env.") || memorySecretExtensions[strings.ToLower(filepath.Ext(base))] {
		add(MemoryCodeEvidencePath, field+".locator.path", "credential-looking paths are not allowed")
		return diags
	}
	if opts.ProjectDir == "" {
		return diags
	}
	projectDir, err := filepath.Abs(opts.ProjectDir)
	if err != nil {
		add(MemoryCodeEvidencePath, field+".locator.path", "cannot resolve project directory")
		return diags
	}
	projectReal, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		projectReal = projectDir
	}
	full := filepath.Join(projectReal, clean)
	real, statErr := filepath.EvalSymlinks(full)
	if statErr != nil {
		if !ev.MissingAtWrite {
			add(MemoryCodeEvidencePath, field+".locator.path", "file does not exist; set missing_at_write with a reason")
		}
		return diags
	}
	rel, err := filepath.Rel(projectReal, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		add(MemoryCodeEvidencePath, field+".locator.path", "symlink escapes the repository")
		return diags
	}
	fi, err := os.Stat(real)
	if err != nil || fi.IsDir() {
		if !ev.MissingAtWrite {
			add(MemoryCodeEvidencePath, field+".locator.path", "not a readable file")
		}
		return diags
	}
	if ev.SHA256 != "" {
		data, err := os.ReadFile(real)
		if err != nil {
			add(MemoryCodeEvidencePath, field+".locator.path", "cannot read file for digest verification")
			return diags
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != ev.SHA256 {
			add(MemoryCodeEvidenceDigest, field+".sha256", "digest does not match %s", ev.Locator.Path)
		}
	}
	return diags
}

// MemorySubjectContextEnum lists allowed subject entity values for help text.
func MemorySubjectContextEnum() []string {
	return []string{"project", "file", "symbol", "memory"}
}

// MemoryKinds returns the accepted kind values in declaration order.
func MemoryKinds() []string { return append([]string(nil), memoryKinds...) }

// MemoryEvidenceKinds returns the accepted evidence kinds.
func MemoryEvidenceKinds() []string { return append([]string(nil), memoryEvidenceKinds...) }

// MemoryRelationTypes returns the accepted relation types.
func MemoryRelationTypes() []string { return append([]string(nil), memoryRelationTypes...) }

// MemoryContentDigest is the canonical digest without the idempotency key.
// Two records with identical content but different keys must collide here so
// duplicate detection is not defeated by a fresh key.
func MemoryContentDigest(env *MemoryEnvelope) (string, error) {
	if env == nil {
		return "", memoryErr(MemoryCodeJSONInvalid, "", "nil envelope")
	}
	clone := *env
	clone.IdempotencyKey = ""
	return MemoryDigest(&clone)
}

// MemoryDigest is the lowercase SHA-256 of the canonical projection.
func MemoryDigest(env *MemoryEnvelope) (string, error) {
	canonical, err := MemoryCanonicalJSON(env)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// MemoryCanonicalJSON renders the digest projection: sorted IDs, sorted map
// keys, no timestamps, no memory_id, no insignificant whitespace.
func MemoryCanonicalJSON(env *MemoryEnvelope) ([]byte, error) {
	if env == nil {
		return nil, memoryErr(MemoryCodeJSONInvalid, "", "nil envelope")
	}
	assertions := make([]MemoryAssertion, len(env.Assertions))
	copy(assertions, env.Assertions)
	sort.SliceStable(assertions, func(i, j int) bool { return assertions[i].ID < assertions[j].ID })
	evidence := make([]MemoryEvidence, len(env.Evidence))
	copy(evidence, env.Evidence)
	sort.SliceStable(evidence, func(i, j int) bool { return evidence[i].ID < evidence[j].ID })
	relations := make([]MemoryRelation, len(env.Relations))
	copy(relations, env.Relations)
	sort.SliceStable(relations, func(i, j int) bool {
		if relations[i].Type != relations[j].Type {
			return relations[i].Type < relations[j].Type
		}
		return relations[i].TargetMemoryID < relations[j].TargetMemoryID
	})
	prov := MemoryProvenance{}
	if env.Provenance != nil {
		prov = *env.Provenance
	}
	proj := map[string]any{
		"schema_version": env.SchemaVersion,
		"project_id":     env.ProjectID,
		"kind":           env.Kind,
		"title":          env.Title,
		"summary":        env.Summary,
		"assertions":     assertions,
		"evidence":       evidence,
		"relations":      relations,
		"scope":          env.Scope,
		"valid_from":     env.ValidFrom,
		"valid_until":    env.ValidUntil,
		"provenance": map[string]any{
			"agent":         prov.Agent,
			"tool_name":     prov.ToolName,
			"client_kind":   prov.ClientKind,
			"origin_entity": prov.OriginEntity,
		},
		"idempotency_key": env.IdempotencyKey,
	}
	return json.Marshal(proj)
}

// MemoryAuthority labels a saved record.
func MemoryAuthority(env *MemoryEnvelope) string {
	if env == nil {
		return ""
	}
	for _, ev := range env.Evidence {
		if ev.Kind == MemoryEvidenceUserAttestation {
			return "user_attested"
		}
	}
	return "agent_save"
}

// MemoryGrounded reports whether every evidence item is available now.
func MemoryGrounded(env *MemoryEnvelope) bool {
	if env == nil {
		return false
	}
	for _, ev := range env.Evidence {
		if ev.MissingAtWrite {
			return false
		}
	}
	return true
}

// memorySimilarity mirrors dream.Similarity (Dice over token multisets). It
// lives here to keep store free of an import cycle; the threshold and
// behaviour are the same.
func memorySimilarity(a, b string) float64 {
	ta, tb := memoryTokens(a), memoryTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	for w, n := range ta {
		if m, ok := tb[w]; ok {
			if n < m {
				inter += n
			} else {
				inter += m
			}
		}
	}
	return 2 * float64(inter) / float64(len(ta)+len(tb))
}

func memoryTokens(s string) map[string]int {
	out := map[string]int{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,;:!?()[]{}\"'`-")
		if w != "" {
			out[w]++
		}
	}
	return out
}

// normalizeMemoryKind expands a legacy alias and validates membership.
func normalizeMemoryKind(kind string) (string, bool) {
	if alias, ok := MemoryKindAliases[kind]; ok {
		kind = alias
	}
	return kind, memoryKindSet[kind]
}

// memoryPrintable is used by tests and callers validating idempotency keys.
func memoryPrintable(s string) bool {
	if len(s) < 16 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// MemoryEvidenceLocatorURL reports whether a locator names an absolute URL,
// which is never valid durable evidence.
func MemoryEvidenceLocatorURL(ev MemoryEvidence) bool {
	if ev.Locator.Path == "" {
		return false
	}
	u, err := url.Parse(ev.Locator.Path)
	return err == nil && u.IsAbs()
}
