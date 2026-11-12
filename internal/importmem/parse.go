package importmem

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/store"
)

// MaxCandidateChars is the documented per-candidate character bound.
const MaxCandidateChars = 2000

type SectionSpec struct {
	Heading       string
	Kind          string
	AssertionType string
	Predicate     string
}

var sectionSpecs = []SectionSpec{
	{Heading: "Decisions", Kind: store.MemoryKindArchitectureDecision, AssertionType: store.MemoryAssertionDescriptive, Predicate: "has_property"},
	{Heading: "Lessons", Kind: store.MemoryKindFix, AssertionType: store.MemoryAssertionDescriptive, Predicate: "causes"},
	{Heading: "Conventions", Kind: store.MemoryKindConvention, AssertionType: store.MemoryAssertionNormative, Predicate: "requires"},
	{Heading: "Failed approaches", Kind: store.MemoryKindFailedApproach, AssertionType: store.MemoryAssertionDescriptive, Predicate: "has_property"},
	{Heading: "Invariants", Kind: store.MemoryKindInvariant, AssertionType: store.MemoryAssertionNormative, Predicate: "requires"},
	{Heading: "Preferences", Kind: store.MemoryKindPreference, AssertionType: store.MemoryAssertionNormative, Predicate: "recommends"},
	{Heading: "Procedures", Kind: store.MemoryKindProcedure, AssertionType: store.MemoryAssertionDescriptive, Predicate: "has_property"},
	{Heading: "Fixes", Kind: store.MemoryKindFix, AssertionType: store.MemoryAssertionDescriptive, Predicate: "causes"},
}

// SectionSpecs returns a copy of the documented section mapping.
func SectionSpecs() []SectionSpec {
	out := make([]SectionSpec, len(sectionSpecs))
	copy(out, sectionSpecs)
	return out
}

// SectionFor resolves a heading case-insensitively.
func SectionFor(heading string) (SectionSpec, bool) {
	normalized := normalizeHeading(heading)
	for _, spec := range sectionSpecs {
		if strings.EqualFold(spec.Heading, normalized) {
			return spec, true
		}
	}
	return SectionSpec{}, false
}

func normalizeHeading(heading string) string {
	heading = strings.TrimSpace(heading)
	heading = strings.TrimRight(heading, ":")
	return strings.Join(strings.Fields(heading), " ")
}

// Candidate is one parsed bullet ready to become a typed memory.
type Candidate struct {
	Heading       string
	Kind          string
	AssertionType string
	Predicate     string
	Title         string
	Summary       string
	ContentHash   string
	Split         int
}

// ParseResult is the deterministic outcome of one file parse.
type ParseResult struct {
	Candidates []Candidate
	HasSection bool
	Truncated  bool
}

// Parse extracts top-level bullets from recognized Markdown sections only.
// Freeform prose, nested bullets, code fences, tables, and block quotes are
// never guessed into memories.
func Parse(content string, maxCandidates int) ParseResult {
	if maxCandidates <= 0 {
		maxCandidates = DefaultMaxCandidates
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var result ParseResult
	var current *SectionSpec
	var bullet []string
	inFence := false
	stop := false

	flush := func() {
		if stop || len(bullet) == 0 {
			bullet = nil
			return
		}
		text := strings.Join(strings.Fields(strings.Join(bullet, " ")), " ")
		bullet = nil
		if text == "" {
			return
		}
		parts := splitCandidate(text, MaxCandidateChars)
		for i, part := range parts {
			if len(result.Candidates) >= maxCandidates {
				result.Truncated = true
				stop = true
				return
			}
			result.Candidates = append(result.Candidates, Candidate{
				Heading:       current.Heading,
				Kind:          current.Kind,
				AssertionType: current.AssertionType,
				Predicate:     current.Predicate,
				Title:         candidateTitle(part),
				Summary:       part,
				ContentHash:   contentHash(part),
				Split:         i,
			})
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			flush()
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if heading, ok := markdownHeading(line); ok {
			flush()
			current = nil
			if spec, recognized := SectionFor(heading); recognized {
				result.HasSection = true
				current = &spec
			}
			continue
		}
		if current == nil {
			continue
		}
		if isTopLevelBullet(line) {
			flush()
			bullet = []string{strings.TrimSpace(line[2:])}
			continue
		}
		if len(bullet) == 0 {
			continue
		}
		switch {
		case trimmed == "":
			flush()
		case strings.HasPrefix(trimmed, "|"), strings.HasPrefix(trimmed, ">"), strings.HasPrefix(trimmed, "<!--"):
			// Tables and block quotes are not candidates.
		case isNestedBullet(trimmed):
			// A nested bullet belongs to no single memory.
		case strings.HasPrefix(line, "  "), strings.HasPrefix(line, "\t"):
			bullet = append(bullet, trimmed)
		default:
			flush()
		}
	}
	flush()
	return result
}

// markdownHeading returns the heading text for any ATX heading line. Section
// recognition then decides whether the heading is a memory section; every
// heading closes the previous section.
func markdownHeading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	hashes := 0
	for hashes < len(trimmed) && trimmed[hashes] == '#' {
		hashes++
	}
	if hashes > 6 {
		return "", false
	}
	rest := trimmed[hashes:]
	if rest == "" {
		return "", true
	}
	if !strings.HasPrefix(rest, " ") && !strings.HasPrefix(rest, "\t") {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

func isTopLevelBullet(line string) bool {
	if len(line) < 2 {
		return false
	}
	return (line[0] == '-' || line[0] == '*' || line[0] == '+') && line[1] == ' '
}

func isNestedBullet(trimmed string) bool {
	if len(trimmed) < 2 {
		return false
	}
	return (trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+') && trimmed[1] == ' '
}

// splitCandidate splits on sentence boundaries first and whitespace second so
// no text is silently discarded.
func splitCandidate(text string, limit int) []string {
	if utf8.RuneCountInString(text) <= limit {
		return []string{text}
	}
	runes := []rune(text)
	var out []string
	for len(runes) > limit {
		window := string(runes[:limit])
		cut := -1
		for _, sep := range []string{". ", "! ", "? ", "\n"} {
			if i := strings.LastIndex(window, sep); i >= 0 {
				candidate := utf8.RuneCountInString(window[:i+len(sep)])
				if candidate > cut {
					cut = candidate
				}
			}
		}
		if cut <= 0 {
			if i := strings.LastIndex(window, " "); i > 0 {
				cut = utf8.RuneCountInString(window[:i])
			}
		}
		if cut <= 0 {
			cut = limit
		}
		chunk := strings.TrimSpace(string(runes[:cut]))
		if chunk != "" {
			out = append(out, chunk)
		}
		runes = []rune(strings.TrimSpace(string(runes[cut:])))
	}
	if len(runes) > 0 {
		if chunk := strings.TrimSpace(string(runes)); chunk != "" {
			out = append(out, chunk)
		}
	}
	return out
}

// candidateTitle is the first 120 characters with normalized whitespace.
func candidateTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= 120 {
		return text
	}
	return strings.TrimSpace(string(runes[:120]))
}

func contentHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// IdempotencyKey derives the stable import key from a source label and the
// candidate content hash.
func IdempotencyKey(sourceID, hash string) string {
	sum := sha256.Sum256([]byte(sourceID + "\x00" + hash))
	return "import-" + hex.EncodeToString(sum[:])[:32]
}

// BuildEnvelope maps one candidate onto a sila/memory/v1 envelope with
// user-attested evidence and import provenance.
func BuildEnvelope(candidate Candidate, source Source, label, projectID string) *store.MemoryEnvelope {
	locator := label + "#" + candidate.ContentHash
	return &store.MemoryEnvelope{
		SchemaVersion:  store.MemorySchemaVersion,
		ProjectID:      projectID,
		Kind:           candidate.Kind,
		Title:          candidate.Title,
		Summary:        candidate.Summary,
		IdempotencyKey: IdempotencyKey(label, candidate.ContentHash),
		Assertions: []store.MemoryAssertion{{
			ID:            "A-001",
			AssertionType: candidate.AssertionType,
			Predicate:     candidate.Predicate,
			Statement:     candidate.Summary,
			Subject:       store.MemorySubject{ContextEntity: "project", Value: projectID},
			Object: store.MemoryObject{Literal: &store.MemoryLiteral{
				Type:  "string",
				Value: candidate.Heading,
			}},
			Confidence:   0.85,
			Importance:   4,
			EvidenceRefs: []string{"E-001"},
		}},
		Evidence: []store.MemoryEvidence{{
			ID:             "E-001",
			Kind:           store.MemoryEvidenceUserAttestation,
			Locator:        store.MemoryLocator{Path: locator},
			Summary:        locator,
			MissingAtWrite: true,
			MissingReason:  "imported source label, not a repository file",
		}},
		Provenance: &store.MemoryProvenance{
			Agent:        string(source),
			ToolName:     "import",
			ClientKind:   "human",
			OriginEntity: "agent:" + string(source),
		},
	}
}

// StoreProvenance is the bridge-fact provenance for an imported record.
func StoreProvenance(source Source) store.Provenance {
	return store.Provenance{
		ToolName:     "import",
		ClientKind:   "human",
		OriginEntity: "agent:" + string(source),
	}
}
