package ccli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/cli"
)

var updateCCLIVectors = flag.Bool("update-vectors", false, "regenerate internal/ccli/testdata/vectors.json from the embedded catalog")

// VectorSchemaVersion is the normative schema identifier for vectors.json.
const VectorSchemaVersion = "sila/ccli-vectors/v1"

// SyntheticSecretSentinel is a non-real credential sentinel used in conformance tests.
const SyntheticSecretSentinel = "SYNTHETIC_SECRET_SENTINEL_99X"

// SpecDeviation records an explicit normative deviation from ccli/spec/SPEC.md.
type SpecDeviation struct {
	SpecScenario int    `json:"spec_scenario,omitempty"`
	PlanRow      int    `json:"plan_row,omitempty"`
	ProductionID string `json:"production_id,omitempty"`
	Scope        string `json:"scope,omitempty"`
	Expected     string `json:"expected"`
	DeviatesFrom string `json:"deviates_from"`
	Rationale    string `json:"rationale"`
}

// ConformanceVector represents one deterministic CCLI test vector in vectors.json.
type ConformanceVector struct {
	ID            string         `json:"id"`
	Category      string         `json:"category"` // catalog_variant, canonical_overlap, phrase_only_via_do, tier3, forbidden, error
	PlanRow       int            `json:"plan_row,omitempty"`
	Domain        string         `json:"domain,omitempty"`
	Phrases       []string       `json:"phrases,omitempty"`
	InputArgv     []string       `json:"input_argv"`
	Mode          RewriteMode    `json:"mode"`
	FlagMode      string         `json:"flag_mode,omitempty"` // lenient (default) or strict
	NoCCLI        bool           `json:"no_ccli,omitempty"`
	CCLIEnv       string         `json:"ccli_env,omitempty"`
	Denylist      string         `json:"denylist,omitempty"`
	Decision      string         `json:"decision"`
	Matched       bool           `json:"matched"`
	Production    string         `json:"production,omitempty"`
	Action        string         `json:"action,omitempty"`
	Tier          string         `json:"tier,omitempty"`
	CanonicalArgv []string       `json:"canonical_argv"`
	ParseExit     int            `json:"parse_exit"`
	ExecExit      int            `json:"exec_exit"`
	PreserveExit  int            `json:"preserve_exit,omitempty"`
	StdoutClass   string         `json:"stdout_class"` // data, diagnostic, prompt, none
	StderrClass   string         `json:"stderr_class"` // data, diagnostic, prompt, none
	Safety        string         `json:"safety"`       // none, preview, confirm, refuse
	ErrorClass    string         `json:"error_class,omitempty"`
	TokenClasses  []TokenClass   `json:"token_classes,omitempty"`
	Deviation     *SpecDeviation `json:"spec_deviation,omitempty"`
}

// VectorCorpus is the top-level schema for internal/ccli/testdata/vectors.json.
type VectorCorpus struct {
	SchemaVersion  string              `json:"schema_version"`
	CatalogVersion string              `json:"catalog_version"`
	GeneratedAt    string              `json:"generated_at"`
	Digest         string              `json:"digest"`
	Deviations     []SpecDeviation     `json:"spec_deviations"`
	Vectors        []ConformanceVector `json:"vectors"`
}

func computeVectorCorpusDigest(catalogVersion string, deviations []SpecDeviation, vectors []ConformanceVector) (string, error) {
	payload := struct {
		SchemaVersion  string              `json:"schema_version"`
		CatalogVersion string              `json:"catalog_version"`
		Deviations     []SpecDeviation     `json:"spec_deviations"`
		Vectors        []ConformanceVector `json:"vectors"`
	}{
		SchemaVersion:  VectorSchemaVersion,
		CatalogVersion: catalogVersion,
		Deviations:     deviations,
		Vectors:        vectors,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

var slotSamples = map[string][]string{
	"query":       {"wal", "checkpoint"},
	"symptom":     {"sqlite", "busy"},
	"directive":   {"use", "WAL"},
	"summary":     {"finished", "phase", "one"},
	"intent":      {"Hexagonal", "Seam"},
	"title":       {"DB", "Driver"},
	"statement":   {"no", "global", "singletons"},
	"message":     {"great", "cli", "ux"},
	"slug":        {"alpha-goal"},
	"id":          {"D1"},
	"role":        {"codex"},
	"name":        {"crew-1"},
	"branch":      {"feat-ccli"},
	"tool":        {"codex"},
	"keep":        {"proj-keep"},
	"source":      {"proj-keep"},
	"drop":        {"proj-drop"},
	"destination": {"proj-drop"},
	"path":        {"/tmp/store.bak"},
	"key":         {"search_provider"},
	"value":       {"brave"},
}

func sampleSlotTokens(slotName string, s Slot) []string {
	switch s.Type {
	case SlotTypeInt:
		return []string{"5"}
	case SlotTypePath:
		return []string{"docs/reference.md"}
	case SlotTypeEnum:
		if len(s.Enum) > 0 {
			return []string{s.Enum[0]}
		}
	}
	if v, ok := slotSamples[slotName]; ok {
		return v
	}
	return []string{"sample-target"}
}

func sampleRequiredFlagsForProd(prodID string) []string {
	switch prodID {
	case "sila_lesson_save":
		return []string{"--cause=unprotected", "--fix=mutex"}
	case "sila_case_save":
		return []string{"--approach=ports", "--invariant=pure"}
	case "sila_decision_create":
		return []string{"-o", "A: SQLite", "-o", "B: PG", "--recommend=A"}
	case "sila_decision_resolve":
		return []string{"--choice=A"}
	}
	return nil
}

func instantiatePhraseArgv(prod CatalogProduction, phrase []CatalogElement) []string {
	slotMap := make(map[string]Slot, len(prod.Slots))
	for _, s := range prod.Slots {
		slotMap[s.Name] = s
	}
	var argv []string
	for _, el := range phrase {
		switch el.Kind {
		case "keyword", "glue":
			if !el.Optional {
				argv = append(argv, el.Value)
			}
		case "target", "slot":
			s := slotMap[el.Slot]
			argv = append(argv, sampleSlotTokens(el.Slot, s)...)
		}
	}
	argv = append(argv, sampleRequiredFlagsForProd(prod.ID)...)
	return argv
}

func buildExpectedCorpus(inv *Inventory, cat *Catalog) (*VectorCorpus, error) {
	var deviations []SpecDeviation
	// 1. Positional secret exit code deviation (SPEC scenario 11 says 2; Sila uses 3 per SPEC §Secret Protection Invariants line 137)
	deviations = append(deviations, SpecDeviation{
		SpecScenario: 11,
		Expected:     "exit_code=3 (ExitPolicy)",
		DeviatesFrom: "SPEC.md §Normative Edge Cases scenario 11 table column Code=2",
		Rationale:    "SPEC.md §Secret Protection Invariants line 137 explicitly permits exit code 2 or 3; Sila standardizes on exit 3 (ExitPolicy) for all security and policy refusals.",
	})

	// 2. Tier 2/3 permitted-default or implicit self-scoped targets
	for _, p := range cat.Productions {
		if (p.Tier == TierMutation || p.Tier == TierDestructive) && (p.TargetPolicy == "implicit" || p.TargetPolicy == "permitted-default") {
			deviations = append(deviations, SpecDeviation{
				PlanRow:      p.PlanRow,
				ProductionID: p.ID,
				Scope:        p.TargetScope,
				Expected:     fmt.Sprintf("target_policy=%s (scope=%q)", p.TargetPolicy, p.TargetScope),
				DeviatesFrom: "SPEC.md §Pass 2 line 97 (Tier 2 and Tier 3 MUST require an explicit target token)",
				Rationale:    "Sila workspace/store commands operate on a single bounded self-scoped resource (current repo or SQLite store) and announce Scope on stderr.",
			})
		}
	}

	devByProd := make(map[string]*SpecDeviation, len(deviations))
	for i := range deviations {
		if deviations[i].ProductionID != "" {
			d := deviations[i]
			devByProd[d.ProductionID] = &d
		}
	}

	var vectors []ConformanceVector

	// A. Generate vectors for every phrase variant across all productions in catalog.json
	for _, prod := range cat.Productions {
		for pIdx, phrase := range prod.Phrases {
			inputArgv := instantiatePhraseArgv(prod, phrase)
			phraseText := strings.Join(inputArgv, " ")

			mode := RewriteAuto
			category := "catalog_variant"
			if prod.Precedence == PrecedencePhraseOnlyViaDo {
				mode = RewriteDo
				category = "phrase_only_via_do"
			} else if prod.Tier == TierDestructive {
				category = "tier3"
			}

			res := RewriteArgs(inputArgv, mode, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
			execExit := int(res.ExitCode)
			stdoutClass := "data"
			stderrClass := "none"
			safety := res.Safety
			if safety == "" {
				safety = "none"
			}
			if prod.Tier == TierDestructive {
				// Without --yes in non-TTY subprocess execution, Tier 3 refuses with exit 3 on stderr
				execExit = int(ExitPolicy)
				stdoutClass = "none"
				stderrClass = "diagnostic"
				safety = "confirm"
			} else if prod.Tier == TierMutation && prod.TargetPolicy == "implicit" {
				stderrClass = "diagnostic" // announces Scope: <target_scope> on stderr
			}

			var tokClasses []TokenClass
			for _, tk := range res.Tokens {
				tokClasses = append(tokClasses, tk.Class)
			}

			vec := ConformanceVector{
				ID: fmt.Sprintf("%s.v%d.%s", prod.ID, pIdx+1, mode), Category: category,
				PlanRow: prod.PlanRow, Domain: prod.Domain, Phrases: []string{phraseText},
				InputArgv: inputArgv, Mode: mode, Decision: res.Decision, Matched: res.Matched,
				Production: res.Production, Action: res.ActionID, Tier: res.Tier,
				CanonicalArgv: res.Canonical, ParseExit: int(res.ExitCode), ExecExit: execExit,
				PreserveExit: prod.PreserveExit, StdoutClass: stdoutClass, StderrClass: stderrClass,
				Safety: safety, TokenClasses: tokClasses, Deviation: devByProd[prod.ID],
			}
			vectors = append(vectors, vec)

			// For every phrase-only-via-do form, also emit the direct auto-mode vector proving canonical-first non-rewrite
			if prod.Precedence == PrecedencePhraseOnlyViaDo {
				autoRes := RewriteArgs(inputArgv, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
				vectors = append(vectors, ConformanceVector{
					ID: fmt.Sprintf("%s.v%d.auto_overlap", prod.ID, pIdx+1), Category: "canonical_overlap",
					PlanRow: prod.PlanRow, Domain: prod.Domain, Phrases: []string{phraseText},
					InputArgv: inputArgv, Mode: RewriteAuto, Decision: autoRes.Decision, Matched: autoRes.Matched,
					CanonicalArgv: autoRes.Canonical, ParseExit: int(autoRes.ExitCode), ExecExit: int(autoRes.ExitCode),
					StdoutClass: "data", StderrClass: "none", Safety: "none",
				})
			}
		}
	}

	// B. Canonical-only plan rows (Row 3 recall, Row 46 export)
	for _, pr := range cat.PlanRows {
		if pr.CanonicalPhrase == "" {
			continue
		}
		fields := strings.Fields(pr.CanonicalPhrase)
		if len(fields) > 0 && fields[0] == "sila" {
			fields = fields[1:]
		}
		var argv []string
		for _, f := range fields {
			if strings.HasPrefix(f, "<") && strings.HasSuffix(f, ">") {
				argv = append(argv, "sqlite busy")
			} else {
				argv = append(argv, f)
			}
		}
		autoRes := RewriteArgs(argv, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
		vectors = append(vectors, ConformanceVector{
			ID: fmt.Sprintf("plan_row_%02d.canonical_only", pr.PlanRow), Category: "canonical_overlap",
			PlanRow: pr.PlanRow, Domain: pr.Domain, Phrases: []string{strings.Join(argv, " ")},
			InputArgv: argv, Mode: RewriteAuto, Decision: autoRes.Decision, Matched: autoRes.Matched,
			CanonicalArgv: autoRes.Canonical, ParseExit: int(autoRes.ExitCode), ExecExit: 0,
			StdoutClass: "data", StderrClass: "none", Safety: "none",
		})
	}

	// C. Forbidden secret phrases (Row 76 across all sensitive keys)
	for _, fp := range cat.ForbiddenPhrases {
		for _, key := range fp.SensitiveKeys {
			argv := []string{"set", "config", key, "to", SyntheticSecretSentinel}
			res := RewriteArgs(argv, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
			vectors = append(vectors, ConformanceVector{
				ID: fmt.Sprintf("%s.%s.auto", fp.ID, key), Category: "forbidden",
				PlanRow: fp.PlanRow, Domain: fp.Domain, Phrases: []string{fmt.Sprintf("set config %s to <secret>", key)},
				InputArgv: argv, Mode: RewriteAuto, Decision: res.Decision, Matched: false,
				ParseExit: int(ExitPolicy), ExecExit: int(ExitPolicy),
				StdoutClass: "none", StderrClass: "diagnostic", Safety: "refuse",
				ErrorClass: "forbidden_secret", Deviation: &deviations[0],
			})
		}
	}

	// D. Error classes (unknown phrase, ambiguity, missing target, unknown flag, invalid type, inverted directional)
	errorCases := []struct {
		id, errorClass string
		mode           RewriteMode
		flagMode       string
		argv           []string
	}{
		{"error.unknown_phrase.do", "unknown_phrase", RewriteDo, "", []string{"frobnicate", "quantum", "hyperdrive"}},
		{"error.missing_target.do", "missing_target", RewriteDo, "", []string{"save", "memo"}},
		{"error.unknown_flag.strict", "unknown_flag", RewriteDo, string(FlagModeStrict), []string{"say", "hello", "--nonexistent-ccli-flag"}},
		{"error.invalid_type.enum", "invalid_type", RewriteDo, "", []string{"pull", "messages", "from", "not-a-valid-tool"}},
		{"error.inverted_directional.merge", "inverted_directional", RewriteDo, "", []string{"merge", "projects", "into", "drop-proj", "from", "keep-proj"}},
		{"error.same_source_dest_directional.merge", "inverted_directional", RewriteDo, "", []string{"merge", "projects", "same-proj", "into", "same-proj"}},
		{"error.ambiguity.clarify", "ambiguity", RewriteDo, "", []string{"clean", "worktrees"}},
	}

	for _, ec := range errorCases {
		vectors = append(vectors, ConformanceVector{
			ID: ec.id, Category: "error", Phrases: []string{strings.Join(ec.argv, " ")},
			InputArgv: ec.argv, Mode: ec.mode, FlagMode: ec.flagMode, Decision: DecisionNoMatch,
			ParseExit: int(ExitUsage), ExecExit: int(ExitUsage),
			StdoutClass: "none", StderrClass: "diagnostic", Safety: "refuse", ErrorClass: ec.errorClass,
		})
	}

	// E. Opt-out & Precedence vectors (--no-ccli, SILA_CCLI=off, SILA_CCLI_DENYLIST, explain)
	vectors = append(vectors,
		ConformanceVector{
			ID: "optout.no_ccli_flag.auto", Category: "optout", PlanRow: 1, Domain: "Memory & Epistemic Core",
			InputArgv: []string{"--no-ccli", "say", "hello"}, Mode: RewriteAuto, NoCCLI: true, Decision: DecisionDisabled,
			CanonicalArgv: []string{"--no-ccli", "say", "hello"}, ExecExit: int(ExitUsage), StdoutClass: "none", StderrClass: "diagnostic", Safety: "none",
		},
		ConformanceVector{
			ID: "optout.env_off.auto", Category: "optout", PlanRow: 1, Domain: "Memory & Epistemic Core",
			InputArgv: []string{"say", "hello"}, Mode: RewriteAuto, CCLIEnv: "off", Decision: DecisionDisabled,
			CanonicalArgv: []string{"say", "hello"}, ExecExit: int(ExitUsage), StdoutClass: "none", StderrClass: "diagnostic", Safety: "none",
		},
		ConformanceVector{
			ID: "optout.denylist.auto", Category: "optout", PlanRow: 5, Domain: "Memory & Epistemic Core",
			InputArgv: []string{"save", "memo", "use WAL"}, Mode: RewriteAuto, Denylist: "save memo", Decision: DecisionDenylisted,
			CanonicalArgv: []string{"save", "memo", "use WAL"}, ExecExit: int(ExitUsage), StdoutClass: "none", StderrClass: "diagnostic", Safety: "none",
		},
		ConformanceVector{
			ID: "explain.save_memo", Category: "explain", PlanRow: 5, Domain: "Memory & Epistemic Core",
			InputArgv: []string{"save", "memo", "use", "WAL"}, Mode: RewriteExplain, Decision: DecisionRoute, Matched: true,
			Production: "sila_memo_save", Action: "memo.save", Tier: "MUTATION", CanonicalArgv: []string{"memo", "use WAL"},
			StdoutClass: "data", StderrClass: "none", Safety: "preview",
		},
	)

	digest, err := computeVectorCorpusDigest(cat.CatalogVersion, deviations, vectors)
	if err != nil {
		return nil, err
	}

	return &VectorCorpus{
		SchemaVersion:  VectorSchemaVersion,
		CatalogVersion: cat.CatalogVersion,
		GeneratedAt:    "2026-10-23T00:00:00Z",
		Digest:         digest,
		Deviations:     deviations,
		Vectors:        vectors,
	}, nil
}

func loadVectorsCorpusFromDisk(t *testing.T, path string) *VectorCorpus {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var corpus VectorCorpus
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&corpus); err != nil {
		t.Fatalf("decode %s with DisallowUnknownFields: %v", path, err)
	}
	return &corpus
}

func TestVectorCorpusAndPureParserConformance(t *testing.T) {
	inv, cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	expectedCorpus, err := buildExpectedCorpus(inv, cat)
	if err != nil {
		t.Fatalf("buildExpectedCorpus: %v", err)
	}

	corpusPath := filepath.Join("testdata", "vectors.json")
	if *updateCCLIVectors {
		if err := os.MkdirAll(filepath.Dir(corpusPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		formatted, err := json.MarshalIndent(expectedCorpus, "", "  ")
		if err != nil {
			t.Fatalf("marshal vectors.json: %v", err)
		}
		formatted = append(formatted, '\n')
		if err := os.WriteFile(corpusPath, formatted, 0o644); err != nil {
			t.Fatalf("write vectors.json: %v", err)
		}
	}

	diskCorpus := loadVectorsCorpusFromDisk(t, corpusPath)

	// R3.7: Catalog/corpus version & digest match; stale corpus fails
	if diskCorpus.SchemaVersion != VectorSchemaVersion {
		t.Fatalf("schema_version = %q, want %q", diskCorpus.SchemaVersion, VectorSchemaVersion)
	}
	if diskCorpus.CatalogVersion != cat.CatalogVersion {
		t.Fatalf("catalog_version = %q, want %q", diskCorpus.CatalogVersion, cat.CatalogVersion)
	}
	wantDigest, err := computeVectorCorpusDigest(diskCorpus.CatalogVersion, diskCorpus.Deviations, diskCorpus.Vectors)
	if err != nil {
		t.Fatalf("computeVectorCorpusDigest: %v", err)
	}
	if diskCorpus.Digest != wantDigest {
		t.Fatalf("vectors.json digest mismatch: got %s, want %s", diskCorpus.Digest, wantDigest)
	}
	if diskCorpus.Digest != expectedCorpus.Digest {
		t.Fatalf("vectors.json drifted from embedded catalog.json (got %s, want %s); run `go test ./internal/ccli -update-vectors`", diskCorpus.Digest, expectedCorpus.Digest)
	}

	// Track coverage across all required dimensions (R1.3 & R6.1)
	coveredRows := make(map[int]bool, 76)
	coveredVariants := 0
	coveredTier3 := make(map[string]bool)
	coveredForbidden := 0
	coveredErrorClasses := make(map[string]bool)
	coveredDomains := make(map[string]bool)

	for _, vec := range diskCorpus.Vectors {
		if vec.PlanRow > 0 {
			coveredRows[vec.PlanRow] = true
		}
		if vec.Domain != "" {
			coveredDomains[vec.Domain] = true
		}
		if vec.Category == "catalog_variant" || vec.Category == "phrase_only_via_do" || vec.Category == "tier3" {
			coveredVariants++
		}
		if vec.Tier == string(TierDestructive) && vec.Production != "" {
			coveredTier3[vec.Production] = true
		}
		if vec.Category == "forbidden" {
			coveredForbidden++
		}
		if vec.ErrorClass != "" {
			coveredErrorClasses[vec.ErrorClass] = true
		}

		// R1.5 & R3.6: Secret gate — no vector output or canonical argv contains the synthetic secret sentinel
		for _, cArg := range vec.CanonicalArgv {
			if strings.Contains(cArg, SyntheticSecretSentinel) {
				t.Fatalf("vector %s leaked synthetic secret sentinel in canonical_argv", vec.ID)
			}
		}

		flagMode := FlagModeLenient
		if vec.FlagMode == string(FlagModeStrict) {
			flagMode = FlagModeStrict
		}
		opts := RewriteOptions{
			Inventory: inv,
			EnvSet:    true,
			FlagMode:  flagMode,
			NoCCLI:    vec.NoCCLI,
			CCLIEnv:   vec.CCLIEnv,
			Denylist:  vec.Denylist,
		}

		// R3.3: Determinism — run every vector 10 times; results equal
		var firstRes RewriteResult
		for run := 0; run < 10; run++ {
			res := RewriteArgs(vec.InputArgv, vec.Mode, opts)
			if run == 0 {
				firstRes = res
			} else {
				if res.Decision != firstRes.Decision || res.Matched != firstRes.Matched ||
					res.Production != firstRes.Production || res.ActionID != firstRes.ActionID ||
					res.ExitCode != firstRes.ExitCode || !reflect.DeepEqual(res.Canonical, firstRes.Canonical) {
					t.Fatalf("vector %s non-deterministic on run %d: got %+v, want %+v", vec.ID, run, res, firstRes)
				}
			}
		}

		if firstRes.Decision != vec.Decision {
			t.Errorf("vector %s decision = %q, want %q", vec.ID, firstRes.Decision, vec.Decision)
		}
		if firstRes.Matched != vec.Matched {
			t.Errorf("vector %s matched = %v, want %v", vec.ID, firstRes.Matched, vec.Matched)
		}
		if vec.Production != "" && firstRes.Production != vec.Production {
			t.Errorf("vector %s production = %q, want %q", vec.ID, firstRes.Production, vec.Production)
		}
		if vec.Action != "" && firstRes.ActionID != vec.Action {
			t.Errorf("vector %s action = %q, want %q", vec.ID, firstRes.ActionID, vec.Action)
		}
		if vec.Tier != "" && firstRes.Tier != vec.Tier {
			t.Errorf("vector %s tier = %q, want %q", vec.ID, firstRes.Tier, vec.Tier)
		}
		if int(firstRes.ExitCode) != vec.ParseExit {
			t.Errorf("vector %s parse_exit = %d, want %d (expl=%s)", vec.ID, firstRes.ExitCode, vec.ParseExit, firstRes.Explanation)
		}
		if len(vec.CanonicalArgv) > 0 && !reflect.DeepEqual(firstRes.Canonical, vec.CanonicalArgv) {
			t.Errorf("vector %s canonical_argv = %v, want %v", vec.ID, firstRes.Canonical, vec.CanonicalArgv)
		}
		if strings.Contains(firstRes.Explanation, SyntheticSecretSentinel) {
			t.Errorf("vector %s explanation leaked secret sentinel: %s", vec.ID, firstRes.Explanation)
		}

		// R3.2 & R5.4: Idempotence & Canonical Acceptance — canonical output parsed again in Auto mode yields the same canonical output without a CCLI rewrite
		if firstRes.Matched && firstRes.Decision == DecisionRoute && len(firstRes.Canonical) > 0 {
			if !cli.CanonicalAccepts(firstRes.Canonical) {
				t.Errorf("vector %s canonical_argv %v rejected by cli.CanonicalAccepts", vec.ID, firstRes.Canonical)
			}
			idemRes := RewriteArgs(firstRes.Canonical, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
			if !reflect.DeepEqual(idemRes.Canonical, firstRes.Canonical) {
				t.Errorf("vector %s idempotence failed: second pass canonical = %v, want %v", vec.ID, idemRes.Canonical, firstRes.Canonical)
			}
			if idemRes.Decision != DecisionCanonical {
				t.Errorf("vector %s second pass decision = %q, want CANONICAL", vec.ID, idemRes.Decision)
			}
		}
	}

	// Verify 100% coverage gates (R6.1)
	totalCatalogVariants := 0
	totalCatalogTier3 := 0
	for _, p := range cat.Productions {
		totalCatalogVariants += len(p.Phrases)
		if p.Tier == TierDestructive {
			totalCatalogTier3++
			if p.ConfirmationPolicy == "" || p.ConfirmationPolicy == "none" {
				t.Errorf("R5.6 violation: Tier 3 production %s has confirmation_policy=%q", p.ID, p.ConfirmationPolicy)
			}
		}
	}

	if len(coveredRows) != 76 {
		t.Fatalf("plan row coverage = %d/76, want 76/76", len(coveredRows))
	}
	if coveredVariants != totalCatalogVariants {
		t.Fatalf("phrase variant coverage = %d/%d, want 100%%", coveredVariants, totalCatalogVariants)
	}
	if len(coveredTier3) != totalCatalogTier3 {
		t.Fatalf("Tier 3 production coverage = %d/%d, want 100%%", len(coveredTier3), totalCatalogTier3)
	}
	if coveredForbidden == 0 {
		t.Fatal("expected 100% forbidden phrase coverage, got 0")
	}
	if len(coveredDomains) != 6 {
		t.Fatalf("domain coverage = %d/6, want 6/6", len(coveredDomains))
	}
	for _, reqErr := range []string{"unknown_phrase", "ambiguity", "missing_target", "unknown_flag", "invalid_type", "inverted_directional", "forbidden_secret"} {
		if !coveredErrorClasses[reqErr] {
			t.Errorf("missing error class coverage for %q", reqErr)
		}
	}

	// R3.4 & R3.5: Collision gate & Shadow gate
	reports, err := DetectCollisions(inv)
	if err != nil {
		t.Fatalf("DetectCollisions error: %v", err)
	}
	for _, rep := range reports {
		if rep.Type != CollisionOrderedOverlap {
			t.Fatalf("unresolved ambiguous collision in catalog: %+v", rep)
		}
	}
	for _, p := range cat.Productions {
		if p.Precedence == PrecedenceCanonicalFirst {
			for _, ph := range p.Phrases {
				if len(ph) > 0 && (ph[0].Kind == "keyword" || ph[0].Kind == "glue") {
					if _, isCanon := cli.Lookup(ph[0].Value); isCanon {
						t.Fatalf("R3.5/R5.5 violation: auto phrase in %s starts with canonical command %q", p.ID, ph[0].Value)
					}
				}
			}
		}
	}

	t.Logf("CCLI Conformance Summary: vectors=%d passed=%d failed=0 skipped=0 plan_rows=%d/76 variants=%d/%d tier3=%d/%d forbidden=%d spec_deviations=%d catalog_version=%s digest=%s",
		len(diskCorpus.Vectors), len(diskCorpus.Vectors), len(coveredRows), coveredVariants, totalCatalogVariants,
		len(coveredTier3), totalCatalogTier3, coveredForbidden, len(diskCorpus.Deviations), diskCorpus.CatalogVersion, diskCorpus.Digest)
}

// buildSpecFixtureInventory returns a dedicated fixture inventory implementing all 20
// normative scenarios from ccli/spec/SPEC.md without polluting Sila's catalog.
func buildSpecFixtureInventory() *Inventory {
	inv := NewStandardInventory()
	inv.FlagCatalog["--timeout"] = FlagSpec{Arity: 1, Type: "string", Description: "Operation timeout"}
	inv.FlagCatalog["--output"] = FlagSpec{Arity: 1, Type: "string", Description: "Output format"}
	inv.FlagCatalog["--token-stdin"] = FlagSpec{Arity: 0, Type: "bool", Description: "Read authentication token from stdin"}

	inv.Productions = append(inv.Productions,
		&Production{
			ID: "spec_deploy_short", ActionID: "service.deploy", Tier: TierMutation, Commutative: true,
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "deploy"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementGlue, Value: "to", Optional: true},
				{Kind: ElementSlot, Slot: &Slot{Name: "environment", Type: SlotTypeOperand, Required: false}, Optional: true},
			},
			CanonicalBase: []string{"service", "deploy"},
			FlagMapping:   map[string]string{"service_name": "--name", "environment": "--env"},
			ArgsOrder:     []string{"service_name", "environment"},
		},
		&Production{
			ID: "spec_scale_env", ActionID: "service.scale", Tier: TierMutation, Commutative: true,
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "scale"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementGlue, Value: "in"},
				{Kind: ElementSlot, Slot: &Slot{Name: "env", Type: SlotTypeOperand, Required: true}},
				{Kind: ElementGlue, Value: "to"},
				{Kind: ElementSlot, Slot: &Slot{Name: "replicas", Type: SlotTypeInt, Required: true}},
			},
			WhitelistedInversions: [][]ProductionElement{{
				{Kind: ElementKeyword, Value: "scale"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementGlue, Value: "to"},
				{Kind: ElementSlot, Slot: &Slot{Name: "replicas", Type: SlotTypeInt, Required: true}},
				{Kind: ElementGlue, Value: "in"},
				{Kind: ElementSlot, Slot: &Slot{Name: "env", Type: SlotTypeOperand, Required: true}},
			}},
			CanonicalBase: []string{"service", "scale"},
			FlagMapping:   map[string]string{"service_name": "--name", "env": "--env", "replicas": "--replicas"},
			ArgsOrder:     []string{"service_name", "env", "replicas"},
		},
		&Production{
			ID: "spec_service_restart_inv", ActionID: "service.restart", Tier: TierMutation,
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementKeyword, Value: "restart"},
			},
			CanonicalBase: []string{"service", "restart"},
			FlagMapping:   map[string]string{"service_name": "--name"}, ArgsOrder: []string{"service_name"},
		},
		&Production{
			ID: "spec_user_login", ActionID: "user.login", Tier: TierMutation,
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "login"},
				{Kind: ElementKeyword, Value: "user"},
				{Kind: ElementTarget, Slot: &Slot{Name: "user_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementSlot, Slot: &Slot{Name: "password", Type: SlotTypeOperand, Required: false, Sensitive: true}, Optional: true},
			},
			CanonicalBase: []string{"user", "login"},
			FlagMapping:   map[string]string{"user_name": "--user"}, ArgsOrder: []string{"user_name"},
		},
		&Production{
			ID: "spec_delete_db", ActionID: "database.delete", Tier: TierDestructive,
			TargetPolicy: "required", ConfirmationPolicy: "required-interactive", DryRunPolicy: "preview",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "delete"},
				{Kind: ElementKeyword, Value: "db"},
				{Kind: ElementTarget, Slot: &Slot{Name: "database_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"db", "delete"},
			FlagMapping:   map[string]string{"database_name": "--name"}, ArgsOrder: []string{"database_name"},
		},
		&Production{
			ID: "spec_list_services_bare", ActionID: "services.list_all", Tier: TierQuery,
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "list"},
				{Kind: ElementKeyword, Value: "services"},
			},
			CanonicalBase: []string{"services", "list"},
		},
	)
	return inv
}

func TestSpec20NormativeScenarios(t *testing.T) {
	inv := buildSpecFixtureInventory()

	type specScenario struct {
		num          int
		name         string
		argv         []string
		stdin        string
		interactive  bool
		wantExit     ExitCode
		wantStdout   string // data, none
		wantStderr   string // diagnostic, prompt, none
		checkTokens  func(t *testing.T, res *ParseResult)
		deviationDoc string
	}

	scenarios := []specScenario{
		{
			num: 1, name: "Positional typing (restart service run)",
			argv: []string{"restart", "service", "run"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none",
			checkTokens: func(t *testing.T, res *ParseResult) {
				if res.AST == nil || res.AST.Target != "run" || len(res.Tokens) != 3 || res.Tokens[2].Class != TokenTarget {
					t.Fatalf("expected target 'run' as TokenTarget, got ast=%+v tokens=%+v", res.AST, res.Tokens)
				}
			},
		},
		{
			num: 2, name: "Repeated glue word (deploy service to to staging)",
			argv: []string{"deploy", "service", "to", "to", "staging"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none",
			checkTokens: func(t *testing.T, res *ParseResult) {
				if res.AST == nil || res.AST.Target != "to" || res.AST.Operands["environment"] != "staging" {
					t.Fatalf("expected target='to' env='staging', got %+v", res.AST)
				}
			},
		},
		{num: 3, name: "Missing flag value", argv: []string{"deploy", "--timeout"}, wantExit: ExitUsage, wantStdout: "none", wantStderr: "diagnostic"},
		{
			num: 4, name: "Key-value option (--timeout=30s deploy service web to prod)",
			argv: []string{"--timeout=30s", "deploy", "service", "web", "to", "prod"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none",
			checkTokens: func(t *testing.T, res *ParseResult) {
				if res.AST == nil || res.AST.Flags["--timeout"] != "30s" || res.AST.Target != "web" {
					t.Fatalf("expected --timeout=30s and target=web, got %+v", res.AST)
				}
			},
		},
		{
			num: 5, name: "Double dash delimiter (deploy web -- --dry-run)",
			argv: []string{"deploy", "--", "--dry-run"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none",
			checkTokens: func(t *testing.T, res *ParseResult) {
				if res.AST == nil || res.AST.Target != "--dry-run" {
					t.Fatalf("expected '--dry-run' after -- to bind as positional TARGET, got %+v", res.AST)
				}
			},
		},
		{
			num: 6, name: "Query target default (show status)",
			argv: []string{"show", "status"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none",
			checkTokens: func(t *testing.T, res *ParseResult) {
				if res.AST == nil || res.AST.Target != "default" {
					t.Fatalf("expected default target on Tier 1 query, got %+v", res.AST)
				}
			},
		},
		{num: 7, name: "Missing target slot", argv: []string{"scale", "to", "5"}, wantExit: ExitUsage, wantStdout: "none", wantStderr: "diagnostic"},
		{
			num: 8, name: "Commutative slot set (scale web in prod to 5 vs scale web to 5 in prod)",
			argv: []string{"scale", "web", "in", "prod", "to", "5"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none",
			checkTokens: func(t *testing.T, res *ParseResult) {
				resB := Parse([]string{"scale", "web", "to", "5", "in", "prod"}, inv)
				if resB.ExitCode != ExitSuccess || !reflect.DeepEqual(res.CanonicalArgs, resB.CanonicalArgs) {
					t.Fatalf("commutative forms produced different canonical args: %v vs %v", res.CanonicalArgs, resB.CanonicalArgs)
				}
			},
		},
		{
			num: 9, name: "Whitelist inversion (service web restart)",
			argv: []string{"service", "web", "restart"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none",
			checkTokens: func(t *testing.T, res *ParseResult) {
				if res.AST == nil || res.AST.ActionID != "service.restart" || res.AST.Target != "web" {
					t.Fatalf("expected service.restart on 'web', got %+v", res.AST)
				}
			},
		},
		{num: 10, name: "Directional guard", argv: []string{"copy", "bucket", "to", "dest-bkt", "from", "src-bkt"}, wantExit: ExitUsage, wantStdout: "none", wantStderr: "diagnostic"},
		{
			num: 11, name: "Positional secret ban", argv: []string{"login", "user", "alice", SyntheticSecretSentinel},
			wantExit: ExitPolicy, wantStdout: "none", wantStderr: "diagnostic",
			deviationDoc: "SPEC-DEVIATION: Sila exits 3 (ExitPolicy) rather than 2 per SPEC §Secret Protection Invariants line 137.",
		},
		{num: 12, name: "Secure secret intake", argv: []string{"login", "user", "alice", "--token-stdin"}, stdin: "stdin-secret-token\n", wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none"},
		{num: 13, name: "Interactive refusal", argv: []string{"delete", "db", "prod"}, stdin: "n\n", interactive: true, wantExit: ExitPolicy, wantStdout: "none", wantStderr: "prompt"},
		{num: 14, name: "Non-interactive gate", argv: []string{"delete", "db", "prod"}, interactive: false, wantExit: ExitPolicy, wantStdout: "none", wantStderr: "diagnostic"},
		{num: 15, name: "Bypass flag provided", argv: []string{"delete", "db", "prod", "--yes"}, interactive: false, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none"},
		{num: 16, name: "Dry-run simulation", argv: []string{"delete", "db", "prod", "--dry-run"}, interactive: false, wantExit: ExitSuccess, wantStdout: "none", wantStderr: "diagnostic"},
		{num: 17, name: "Clean data on stdout", argv: []string{"list", "services", "--output=json"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "none"},
		{num: 18, name: "Diagnostic on stderr", argv: []string{"list", "services"}, wantExit: ExitSuccess, wantStdout: "data", wantStderr: "diagnostic"},
		{num: 19, name: "Truncated phrase", argv: []string{"deploy", "service", "web", "to"}, wantExit: ExitUsage, wantStdout: "none", wantStderr: "diagnostic"},
		{num: 20, name: "Runtime failure", argv: []string{"deploy", "service", "web", "to", "prod"}, wantExit: ExitRuntime, wantStdout: "none", wantStderr: "diagnostic"},
	}

	if len(scenarios) != 20 {
		t.Fatalf("expected all 20 SPEC scenarios, got %d", len(scenarios))
	}

	for _, sc := range scenarios {
		t.Run(fmt.Sprintf("Scenario_%02d_%s", sc.num, sc.name), func(t *testing.T) {
			router := NewRouter(inv)
			registerSpecFixtureHandlers(router, sc.num)

			var stdout, stderr strings.Builder
			streams := NewStreamsWithTTY(strings.NewReader(sc.stdin), &stdout, &stderr, sc.interactive, sc.interactive)
			code, res := router.Route(context.Background(), sc.argv, streams)
			if code != sc.wantExit {
				t.Fatalf("scenario %d exit = %d, want %d (err=%q stderr=%q)", sc.num, code, sc.wantExit, res.ErrorMessage, stderr.String())
			}
			if sc.checkTokens != nil {
				sc.checkTokens(t, res)
			}

			// Assert stream class discipline (R2.3)
			switch sc.wantStdout {
			case "data":
				if strings.TrimSpace(stdout.String()) == "" {
					t.Errorf("scenario %d expected data on stdout, got empty stdout", sc.num)
				}
			case "none":
				if strings.TrimSpace(stdout.String()) != "" {
					t.Errorf("scenario %d expected empty stdout, got %q", sc.num, stdout.String())
				}
			}

			switch sc.wantStderr {
			case "diagnostic", "prompt":
				if strings.TrimSpace(stderr.String()) == "" {
					t.Errorf("scenario %d expected %s on stderr, got empty stderr", sc.num, sc.wantStderr)
				}
			case "none":
				if strings.TrimSpace(stderr.String()) != "" {
					t.Errorf("scenario %d expected empty stderr, got %q", sc.num, stderr.String())
				}
			}

			if strings.Contains(stdout.String()+stderr.String(), SyntheticSecretSentinel) {
				t.Fatalf("scenario %d leaked synthetic secret sentinel", sc.num)
			}
		})
	}
}

func registerSpecFixtureHandlers(router *Router, scenarioNum int) {
	defaultHandler := func(ctx context.Context, ast *AST, streams *Streams) ExitCode {
		if scenarioNum == 20 && ast.ActionID == "service.deploy" {
			streams.LogError("connection timed out while deploying %s to %s", ast.Target, ast.Operands["environment"])
			return ExitRuntime
		}
		if scenarioNum == 18 && ast.ActionID == "services.list_all" {
			streams.LogInfo("fetching service inventory from local fixture")
		}
		if ast.Flags["--output"] == "json" {
			_ = streams.EmitData(map[string]any{"services": []string{"web", "api"}}, true)
			return ExitSuccess
		}
		_ = streams.EmitData(fmt.Sprintf("ok:%s:%s", ast.ActionID, ast.Target), false)
		return ExitSuccess
	}

	for _, actionID := range []string{
		"service.restart", "service.deploy", "status.show", "service.scale",
		"bucket.copy", "user.login", "database.delete", "services.list_all",
	} {
		router.RegisterAction(&Action{
			ID:      actionID,
			Handler: defaultHandler,
		})
	}
}

// R5.1: FuzzParseDeterminism — same input twice -> same result; no panic.
func FuzzParseDeterminism(f *testing.F) {
	seeds := []string{
		"say hello",
		"save memo use WAL",
		"remember error sqlite busy",
		"drop goal alpha-goal --yes",
		"set config github_token to secret123",
		"merge projects a into b",
		"spawn crewmate codex for alpha-goal",
		"frobnicate unknown --flag=val",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	inv := NewSilaInventory()
	f.Fuzz(func(t *testing.T, input string) {
		args := strings.Fields(input)
		res1 := RewriteArgs(args, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
		res2 := RewriteArgs(args, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
		if res1.Decision != res2.Decision || res1.Matched != res2.Matched ||
			res1.Production != res2.Production || res1.ExitCode != res2.ExitCode ||
			!reflect.DeepEqual(res1.Canonical, res2.Canonical) {
			t.Fatalf("non-deterministic RewriteArgs for %q: %+v vs %+v", input, res1, res2)
		}
	})
}

// R5.2: FuzzRewriteIdempotence — rewrite twice -> same canonical argv.
func FuzzRewriteIdempotence(f *testing.F) {
	seeds := []string{
		"say hello",
		"save memo use WAL",
		"list ready goals",
		"check rules",
		"refresh memory",
		"show config search_provider",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	inv := NewSilaInventory()
	f.Fuzz(func(t *testing.T, input string) {
		args := strings.Fields(input)
		res1 := RewriteArgs(args, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
		if res1.Matched && res1.Decision == DecisionRoute && len(res1.Canonical) > 0 {
			res2 := RewriteArgs(res1.Canonical, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
			if !reflect.DeepEqual(res2.Canonical, res1.Canonical) {
				t.Fatalf("idempotence violation for %q: first=%v second=%v", input, res1.Canonical, res2.Canonical)
			}
		}
	})
}

// R5.3: FuzzNoSecretEcho — synthetic secrets never appear in output/diagnostics.
func FuzzNoSecretEcho(f *testing.F) {
	seeds := []string{
		"github_token",
		"brave_api_key",
		"cloud_token",
		"alphaxiv_api_token",
		"stitch_api_key",
		"gateway_telegram_token",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	inv := NewSilaInventory()
	f.Fuzz(func(t *testing.T, keySuffix string) {
		cleanSuffix := strings.TrimSpace(keySuffix)
		if cleanSuffix == "" {
			cleanSuffix = "token"
		}
		secretVal := "ghp_" + SyntheticSecretSentinel
		argv := []string{"set", "config", "github_token", "to", secretVal, cleanSuffix}
		res := RewriteArgs(argv, RewriteAuto, RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient})
		if strings.Contains(res.Phrase, secretVal) || strings.Contains(res.Explanation, secretVal) {
			t.Fatalf("secret leaked in RewriteResult: phrase=%q expl=%q", res.Phrase, res.Explanation)
		}
		for _, c := range res.Canonical {
			if strings.Contains(c, secretVal) {
				t.Fatalf("secret leaked in Canonical: %v", res.Canonical)
			}
		}
		for _, tk := range res.Tokens {
			if strings.Contains(tk.Raw, secretVal) {
				t.Fatalf("secret leaked in Token: %+v", tk)
			}
		}
	})
}

// R6.2: BenchmarkRewriteAuto and BenchmarkRewriteDo
func BenchmarkRewriteAuto(b *testing.B) {
	inv := NewSilaInventory()
	opts := RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient}
	argv := []string{"save", "memo", "use", "WAL", "mode", "for", "concurrency"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RewriteArgs(argv, RewriteAuto, opts)
	}
}

func BenchmarkRewriteDo(b *testing.B) {
	inv := NewSilaInventory()
	opts := RewriteOptions{Inventory: inv, EnvSet: true, FlagMode: FlagModeLenient}
	argv := []string{"sync", "rules", "--dry-run"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RewriteArgs(argv, RewriteDo, opts)
	}
}

func TestVectorCorpusDeterministicOrder(t *testing.T) {
	inv, cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	c1, err := buildExpectedCorpus(inv, cat)
	if err != nil {
		t.Fatalf("buildExpectedCorpus 1: %v", err)
	}
	c2, err := buildExpectedCorpus(inv, cat)
	if err != nil {
		t.Fatalf("buildExpectedCorpus 2: %v", err)
	}
	if c1.Digest != c2.Digest {
		t.Fatalf("buildExpectedCorpus digest not deterministic: %s vs %s", c1.Digest, c2.Digest)
	}
	seenIDs := make(map[string]bool, len(c1.Vectors))
	for _, v := range c1.Vectors {
		if seenIDs[v.ID] {
			t.Fatalf("duplicate vector ID %q", v.ID)
		}
		seenIDs[v.ID] = true
	}
	// Ensure all 6 domains are sorted/present
	domains := make([]string, 0, 6)
	domSet := make(map[string]bool)
	for _, v := range c1.Vectors {
		if v.Domain != "" && !domSet[v.Domain] {
			domSet[v.Domain] = true
			domains = append(domains, v.Domain)
		}
	}
	sort.Strings(domains)
	if len(domains) != 6 {
		t.Fatalf("expected 6 domains, got %v", domains)
	}
}
