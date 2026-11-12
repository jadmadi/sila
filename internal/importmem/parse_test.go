package importmem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestParseSections(t *testing.T) {
	content := `# Intro
freeform prose

## Decisions
- use SQLite for the local store
- avoid network calls
  - nested bullet is ignored
  continuation text belongs to its parent
### Lessons
* check errors with errors.Is
| col | col |
|-----|-----|
> block quote is ignored
` + "```" + `
- code fence bullet is ignored
` + "```" + `
## Conventions
+ name files by domain
## Failed approaches
- retrying without backoff
## Invariants
- imports never mutate originals
## Preferences
- short commit subjects
## Procedures
- run gofmt before commit
## Fixes
- wrap the error with %w
## Unknown
- not a candidate
`
	result := Parse(content, 100)
	if !result.HasSection {
		t.Fatal("no section recognized")
	}
	if result.Truncated {
		t.Fatal("unexpected truncation")
	}
	if len(result.Candidates) != 9 {
		t.Fatalf("candidates = %d: %+v", len(result.Candidates), result.Candidates)
	}
	byHeading := map[string][]string{}
	for _, candidate := range result.Candidates {
		byHeading[candidate.Heading] = append(byHeading[candidate.Heading], candidate.Summary)
		if strings.Contains(candidate.Summary, "nested bullet") || strings.Contains(candidate.Summary, "code fence") || strings.Contains(candidate.Summary, "block quote") {
			t.Fatalf("ignored markdown leaked: %+v", candidate)
		}
	}
	decisions := byHeading["Decisions"]
	if len(decisions) != 2 {
		t.Fatalf("decisions: %+v", decisions)
	}
	if decisions[0] != "use SQLite for the local store" {
		t.Fatalf("first decision: %q", decisions[0])
	}
	if !strings.HasPrefix(decisions[1], "avoid network calls") || !strings.Contains(decisions[1], "continuation text belongs to its parent") {
		t.Fatalf("continuation handling: %q", decisions[1])
	}
	if len(byHeading["Unknown"]) != 0 {
		t.Fatalf("unknown heading produced a candidate: %+v", result.Candidates)
	}
}

func TestParseIgnoresFreeform(t *testing.T) {
	result := Parse("just notes\n- not under a heading\n## Notes\n- still not a memory\n", 100)
	if result.HasSection {
		t.Fatal("freeform file reported a section")
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("candidates: %+v", result.Candidates)
	}
	result = Parse("## Decisions\nplain paragraph\nmore prose\n", 100)
	if !result.HasSection || len(result.Candidates) != 0 {
		t.Fatalf("prose under a section: %+v", result)
	}
}

func TestParseLongBullet(t *testing.T) {
	sentence := strings.Repeat("word ", 300) + "."
	long := "## Lessons\n- " + strings.Repeat(sentence+" ", 4) + "tail marker\n"
	result := Parse(long, 100)
	if len(result.Candidates) < 2 {
		t.Fatalf("long bullet was not split: %d", len(result.Candidates))
	}
	var joined strings.Builder
	for _, candidate := range result.Candidates {
		if len([]rune(candidate.Summary)) > MaxCandidateChars {
			t.Fatalf("candidate exceeds bound: %d", len([]rune(candidate.Summary)))
		}
		joined.WriteString(candidate.Summary)
		joined.WriteString(" ")
	}
	if !strings.Contains(joined.String(), "tail marker") {
		t.Fatal("long bullet tail was silently discarded")
	}
	for i, candidate := range result.Candidates {
		if candidate.Split != i {
			t.Fatalf("split index = %d, want %d", candidate.Split, i)
		}
	}
}

func TestFixtureFormats(t *testing.T) {
	cases := map[string]string{
		"claude-decisions.md":       "Decisions",
		"codex-conventions.md":      "Conventions",
		"grok-failed-approaches.md": "Failed approaches",
	}
	for name, heading := range cases {
		raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		result := Parse(string(raw), 10)
		if !result.HasSection || len(result.Candidates) == 0 {
			t.Fatalf("%s did not parse: %+v", name, result)
		}
		for _, candidate := range result.Candidates {
			if candidate.Heading != heading {
				t.Fatalf("%s heading = %q, want %q", name, candidate.Heading, heading)
			}
		}
	}
}

func TestSectionKindMapping(t *testing.T) {
	want := map[string]SectionSpec{
		"Decisions":         {Heading: "Decisions", Kind: store.MemoryKindArchitectureDecision, AssertionType: store.MemoryAssertionDescriptive, Predicate: "has_property"},
		"Lessons":           {Heading: "Lessons", Kind: store.MemoryKindFix, AssertionType: store.MemoryAssertionDescriptive, Predicate: "causes"},
		"Conventions":       {Heading: "Conventions", Kind: store.MemoryKindConvention, AssertionType: store.MemoryAssertionNormative, Predicate: "requires"},
		"Failed approaches": {Heading: "Failed approaches", Kind: store.MemoryKindFailedApproach, AssertionType: store.MemoryAssertionDescriptive, Predicate: "has_property"},
		"Invariants":        {Heading: "Invariants", Kind: store.MemoryKindInvariant, AssertionType: store.MemoryAssertionNormative, Predicate: "requires"},
		"Preferences":       {Heading: "Preferences", Kind: store.MemoryKindPreference, AssertionType: store.MemoryAssertionNormative, Predicate: "recommends"},
		"Procedures":        {Heading: "Procedures", Kind: store.MemoryKindProcedure, AssertionType: store.MemoryAssertionDescriptive, Predicate: "has_property"},
		"Fixes":             {Heading: "Fixes", Kind: store.MemoryKindFix, AssertionType: store.MemoryAssertionDescriptive, Predicate: "causes"},
	}
	if len(SectionSpecs()) != len(want) {
		t.Fatalf("sections: %+v", SectionSpecs())
	}
	for heading, expected := range want {
		for _, variant := range []string{heading, strings.ToUpper(heading), strings.ToLower(heading)} {
			spec, ok := SectionFor(variant)
			if !ok || spec != expected {
				t.Fatalf("SectionFor(%q) = %+v, %v", variant, spec, ok)
			}
		}
	}
	if _, ok := SectionFor("Random"); ok {
		t.Fatal("unknown heading recognized")
	}
}

func TestImportedEnvelopeProvenance(t *testing.T) {
	candidate := Candidate{
		Heading: "Decisions", Kind: store.MemoryKindArchitectureDecision,
		AssertionType: store.MemoryAssertionDescriptive, Predicate: "has_property",
		Title: "Use a local store", Summary: "Use a local store for durable memory.",
		ContentHash: contentHash("Use a local store for durable memory."),
	}
	env := BuildEnvelope(candidate, SourceClaude, "claude:repo:memory/notes.md", "sila-cli")
	if env.SchemaVersion != store.MemorySchemaVersion || env.Kind != store.MemoryKindArchitectureDecision {
		t.Fatalf("envelope: %+v", env)
	}
	if env.IdempotencyKey != IdempotencyKey("claude:repo:memory/notes.md", candidate.ContentHash) {
		t.Fatalf("idempotency key: %q", env.IdempotencyKey)
	}
	if len(env.Assertions) != 1 || len(env.Evidence) != 1 {
		t.Fatalf("assertions/evidence: %+v %+v", env.Assertions, env.Evidence)
	}
	assertion := env.Assertions[0]
	if assertion.AssertionType != store.MemoryAssertionDescriptive || assertion.Predicate != "has_property" {
		t.Fatalf("assertion: %+v", assertion)
	}
	if assertion.Confidence != 0.85 || assertion.Importance != 4 {
		t.Fatalf("confidence/importance: %v %v", assertion.Confidence, assertion.Importance)
	}
	if assertion.Subject.ContextEntity != "project" || assertion.Subject.Value != "sila-cli" {
		t.Fatalf("subject: %+v", assertion.Subject)
	}
	if literal, ok := assertion.Object.Literal.Value.(string); !ok || literal != "Decisions" {
		t.Fatalf("object: %+v", assertion.Object)
	}
	evidence := env.Evidence[0]
	if evidence.Kind != store.MemoryEvidenceUserAttestation {
		t.Fatalf("evidence kind: %q", evidence.Kind)
	}
	if evidence.Locator.Path != "claude:repo:memory/notes.md#"+candidate.ContentHash {
		t.Fatalf("locator: %q", evidence.Locator.Path)
	}
	if strings.Contains(evidence.Locator.Path, "/home/") {
		t.Fatalf("locator leaked a home path: %q", evidence.Locator.Path)
	}
	if env.Provenance == nil || env.Provenance.ToolName != "import" || env.Provenance.ClientKind != "human" || env.Provenance.OriginEntity != "agent:claude" {
		t.Fatalf("provenance: %+v", env.Provenance)
	}
	prov := StoreProvenance(SourceCodex)
	if prov.ToolName != "import" || prov.OriginEntity != "agent:codex" {
		t.Fatalf("store provenance: %+v", prov)
	}

	projectDir := t.TempDir()
	if diags := store.ValidateMemoryEnvelope(env, store.MemoryValidateOptions{ProjectDir: projectDir}); len(diags) != 0 {
		t.Fatalf("validation: %+v", diags)
	}
	if store.MemoryAuthority(env) != "user_attested" {
		t.Fatalf("authority: %q", store.MemoryAuthority(env))
	}

	other := candidate
	other.Summary = "A different decision."
	other.ContentHash = contentHash(other.Summary)
	if IdempotencyKey("claude:repo:memory/notes.md", candidate.ContentHash) == IdempotencyKey("claude:repo:memory/notes.md", other.ContentHash) {
		t.Fatal("idempotency key ignores content")
	}
}
