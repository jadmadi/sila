package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/jadmadi/sila/internal/ccli"
	"github.com/jadmadi/sila/internal/cli"
	"github.com/jadmadi/sila/internal/render"
)

func TestCCLIDefaultHelpPhraseSectionAndBudget(t *testing.T) {
	var buf bytes.Buffer
	usageTo(&buf)
	out := buf.String()

	lines := strings.Count(out, "\n")
	if lines > 40 {
		t.Fatalf("default sila --help is %d lines, want <= 40:\n%s", lines, out)
	}
	for _, want := range []string{
		"Core loop:",
		"Conversational phrases:",
		"sila say hello",
		"sila remember error <text>",
		"sila save memo <text>",
		"sila help phrases",
		"sila ccli explain",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("default sila --help missing %q:\n%s", want, out)
		}
	}
	assertASCIIAndNoANSI(t, "sila --help", out)

	var allBuf bytes.Buffer
	renderUsage(&allBuf, true)
	allOut := allBuf.String()
	if !strings.Contains(allOut, "sila help phrases") {
		t.Fatalf("sila help all missing pointer to sila help phrases:\n%s", allOut)
	}
	assertASCIIAndNoANSI(t, "sila help all", allOut)
}

func TestCCLIHelpPhrasesCompleteInventoryAndNoDuplicates(t *testing.T) {
	var buf bytes.Buffer
	runCCLIHelpPhrases(&buf)
	out := buf.String()

	assertASCIIAndNoANSI(t, "sila help phrases", out)

	// Deterministic check
	var buf2 bytes.Buffer
	runCCLIHelpPhrases(&buf2)
	if out != buf2.String() {
		t.Fatal("sila help phrases output is not deterministic")
	}

	_, cat, err := ccli.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	if !strings.Contains(out, "76 families - 6 domains - catalog "+cat.CatalogVersion) {
		t.Fatalf("sila help phrases header missing expected counts/version:\n%s", out)
	}

	expectedDomains := []string{
		"Memory & Epistemic Core (11)",
		"Goals & Coordination (21)",
		"Quality Gates & Health (8)",
		"Setup & Onboarding (6)",
		"Store & Ingestion (17)",
		"Surfaces, Cloud & Configuration (13)",
	}
	lastIdx := -1
	for _, dom := range expectedDomains {
		idx := strings.Index(out, dom)
		if idx < 0 {
			t.Fatalf("sila help phrases missing domain heading %q", dom)
		}
		if idx <= lastIdx {
			t.Fatalf("domain heading %q appeared out of deterministic order", dom)
		}
		lastIdx = idx
	}

	// Verify every catalog variant appears exactly once in phrases: lines.
	emitted := extractHelpPhraseVariants(out)
	emittedCounts := make(map[string]int, len(emitted))
	for _, v := range emitted {
		emittedCounts[v]++
	}

	wantTotalVariants := 0
	for _, p := range cat.Productions {
		for _, ph := range p.Phrases {
			v := formatCatalogPhraseElements(ph)
			wantTotalVariants++
			if got := emittedCounts[v]; got != 1 {
				t.Fatalf("catalog production %s phrase %q appeared %d times in sila help phrases, want 1", p.ID, v, got)
			}
		}
	}
	for _, fp := range cat.ForbiddenPhrases {
		for _, ph := range fp.Phrases {
			v := formatCatalogPhraseElements(ph)
			wantTotalVariants++
			if got := emittedCounts[v]; got != 1 {
				t.Fatalf("forbidden phrase %s variant %q appeared %d times in sila help phrases, want 1", fp.ID, v, got)
			}
		}
	}
	if len(emitted) != wantTotalVariants {
		t.Fatalf("emitted %d phrase variants in sila help phrases, want %d", len(emitted), wantTotalVariants)
	}

	// Verify column alignment of family header rows (2 leading spaces + fixed columns).
	familyLines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") &&
			!strings.HasPrefix(line, "  Canonical commands") &&
			!strings.HasPrefix(line, "  Use 'sila do") &&
			!strings.HasPrefix(line, "  Disable auto-rewrite") {
			familyLines++
			if !strings.Contains(line, "query") && !strings.Contains(line, "mutation") && !strings.Contains(line, "destructive") {
				t.Fatalf("family row missing tier badge: %q", line)
			}
			if !strings.Contains(line, "auto") && !strings.Contains(line, "sila do") && !strings.Contains(line, "forbidden") {
				t.Fatalf("family row missing routing marker: %q", line)
			}
		}
	}
	if familyLines != len(cat.PlanRows) {
		t.Fatalf("sila help phrases rendered %d family rows, want %d", familyLines, len(cat.PlanRows))
	}
}

func TestCCLIPerCommandHelpExamplesTiersAndDoGuidance(t *testing.T) {
	ui := render.New(os.Stdout, true)

	// Representative commands with auto-only, mixed auto+do, and do-only productions
	cases := []struct {
		sub         string
		wantPhrase  string
		wantTier    string
		wantDoGuide bool
	}{
		{"memo", "sila save memo <directive>", "mutation", false},
		{"recall", "sila remember error <symptom>", "query", false},
		{"prime", `use 'sila do "prime context"'`, "query", true},
		{"sync", `use 'sila do "sync rules"'`, "mutation", true},
		{"goals", `use 'sila do "release goal <slug>"'`, "destructive", true},
		{"clean", `use 'sila do "clean caches"'`, "destructive", true},
		{"eval", "sila run eval", "query", false},
		{"overlaps", "sila show overlaps", "query", false},
		{"reconcile", `use 'sila do "reconcile drift"'`, "query", true},
	}

	for _, tc := range cases {
		var buf bytes.Buffer
		subcommandUsageTo(tc.sub, ui, &buf)
		out := buf.String()

		assertASCIIAndNoANSI(t, "sila "+tc.sub+" --help", out)
		if !strings.Contains(out, "Usage") {
			t.Fatalf("%s help missing canonical Usage section:\n%s", tc.sub, out)
		}
		if !strings.Contains(out, "Conversational") {
			t.Fatalf("%s help missing Conversational section:\n%s", tc.sub, out)
		}
		if !strings.Contains(out, "Safety") {
			t.Fatalf("%s help missing Safety section:\n%s", tc.sub, out)
		}
		if !strings.Contains(out, tc.wantPhrase) {
			t.Fatalf("%s help missing expected phrase %q:\n%s", tc.sub, tc.wantPhrase, out)
		}
		if !strings.Contains(out, "Tier: "+tc.wantTier) {
			t.Fatalf("%s help missing Tier: %s:\n%s", tc.sub, tc.wantTier, out)
		}
		if tc.wantDoGuide && !strings.Contains(out, "Precedence: canonical-first; overlapping phrases require 'sila do") {
			t.Fatalf("%s help missing sila do precedence guidance:\n%s", tc.sub, out)
		}
		if !strings.Contains(out, "Phrase preview: pass --dry-run to a conversational phrase for a central preview") {
			t.Fatalf("%s help missing phrase-scoped dry-run note:\n%s", tc.sub, out)
		}
	}

	// Verify EVERY canonical command with at least one CCLI production renders Conversational help,
	// and commands with zero CCLI productions (like ccli, do, completions) render only canonical usage.
	_, cat, err := ccli.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	cmdsWithProd := make(map[string]bool)
	for _, p := range cat.Productions {
		if len(p.CanonicalBase) > 0 {
			cmdsWithProd[strings.ToLower(p.CanonicalBase[0])] = true
			if spec, ok := cli.Lookup(p.CanonicalBase[0]); ok {
				cmdsWithProd[strings.ToLower(spec.Name)] = true
			}
		}
	}
	for cmdName := range cmdsWithProd {
		var buf bytes.Buffer
		subcommandUsageTo(cmdName, ui, &buf)
		out := buf.String()
		if !strings.Contains(out, "Conversational") {
			t.Fatalf("command %q has CCLI production(s) but its --help lacks Conversational section:\n%s", cmdName, out)
		}
		assertASCIIAndNoANSI(t, "sila "+cmdName+" --help", out)
	}

	for _, noProdCmd := range []string{"ccli", "do", "completions", "version"} {
		var buf bytes.Buffer
		subcommandUsageTo(noProdCmd, ui, &buf)
		out := buf.String()
		if strings.Contains(out, "\nConversational\n") {
			t.Fatalf("command %q has no CCLI productions but showed Conversational section:\n%s", noProdCmd, out)
		}
		assertASCIIAndNoANSI(t, "sila "+noProdCmd+" --help", out)
	}
}

func TestCCLIListFiltersAndMachineOutput(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	code := executeCCLICommand([]string{"list", "--domain=Memory", "--tier=QUERY", "--json"}, false, true, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("ccli list --domain=Memory --tier=QUERY --json exited %d: %s", code, errBuf.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(outBuf.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal ccli list JSON: %v\n%s", err, outBuf.String())
	}
	if len(items) == 0 {
		t.Fatal("expected non-empty Memory QUERY productions")
	}
	seenIDs := make(map[string]bool)
	for _, it := range items {
		id, _ := it["id"].(string)
		if seenIDs[id] {
			t.Fatalf("duplicate production id %q in ccli list", id)
		}
		seenIDs[id] = true
		if dom, _ := it["domain"].(string); !strings.Contains(dom, "Memory") {
			t.Fatalf("unexpected domain %q for --domain=Memory", dom)
		}
		if tier, _ := it["tier"].(string); tier != "QUERY" {
			t.Fatalf("unexpected tier %q for --tier=QUERY", tier)
		}
	}

	// JSON envelope contract
	outBuf.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"list", "--tier=DESTRUCTIVE", "--json-envelope"}, false, true, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("ccli list --json-envelope exited %d: %s", code, errBuf.String())
	}
	var env map[string]any
	if err := json.Unmarshal(outBuf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal ccli list envelope: %v", err)
	}
	if okVal, _ := env["ok"].(bool); !okVal {
		t.Fatalf("expected ok=true in ccli list envelope: %v", env)
	}
}

func TestCCLICompletionsCatalogDerivedBoundedAndDeduped(t *testing.T) {
	autoFirst := ccliAutoFirstKeywords()
	doFirst := ccliDoFirstKeywords()
	saveSecond := ccliSecondKeywords("save", false)

	if len(autoFirst) == 0 || len(autoFirst) > 20 {
		t.Fatalf("autoFirst count = %d, want 1..20", len(autoFirst))
	}
	if len(doFirst) == 0 || len(doFirst) > 20 {
		t.Fatalf("doFirst count = %d, want 1..20", len(doFirst))
	}
	if len(saveSecond) == 0 || len(saveSecond) > 20 {
		t.Fatalf("saveSecond count = %d, want 1..20", len(saveSecond))
	}

	assertNoDuplicatesAndDeterministic(t, "autoFirst", autoFirst, ccliAutoFirstKeywords())
	assertNoDuplicatesAndDeterministic(t, "doFirst", doFirst, ccliDoFirstKeywords())
	assertNoDuplicatesAndDeterministic(t, "saveSecond", saveSecond, ccliSecondKeywords("save", false))

	// Expected auto-eligible first keywords
	autoSet := make(map[string]bool, len(autoFirst))
	for _, kw := range autoFirst {
		autoSet[kw] = true
		if strings.HasPrefix(kw, "<") {
			t.Fatalf("autoFirst leaked slot placeholder %q", kw)
		}
	}
	for _, want := range []string{"save", "remember", "say", "show", "list", "refresh", "run", "check", "record", "create"} {
		if !autoSet[want] {
			t.Fatalf("autoFirst missing expected keyword %q (got %v)", want, autoFirst)
		}
	}

	// phrase-only-via-do keywords must be absent from autoFirst and present in doFirst
	doSet := make(map[string]bool, len(doFirst))
	for _, kw := range doFirst {
		doSet[kw] = true
	}
	for _, doOnlyKw := range []string{"prime", "search", "verify", "release", "clean"} {
		if autoSet[doOnlyKw] {
			t.Fatalf("phrase-only-via-do keyword %q leaked into autoFirst", doOnlyKw)
		}
		if !doSet[doOnlyKw] {
			t.Fatalf("phrase-only-via-do keyword %q missing from doFirst (got %v)", doOnlyKw, doFirst)
		}
	}

	// After `sila save `, complete memo, lesson, handoff without slot values
	saveSet := make(map[string]bool, len(saveSecond))
	for _, w := range saveSecond {
		saveSet[w] = true
		if strings.HasPrefix(w, "<") {
			t.Fatalf("saveSecond leaked slot value %q", w)
		}
	}
	for _, want := range []string{"memo", "lesson", "handoff"} {
		if !saveSet[want] {
			t.Fatalf("saveSecond missing %q (got %v)", want, saveSecond)
		}
	}

	// Verify bash, zsh, and fish completion scripts all contain ccli, do, autoPhrases, doPhrases, and saveWords
	for _, sh := range []string{"bash", "zsh", "fish"} {
		var b1, b2 bytes.Buffer
		renderCompletionsTo(&b1, sh)
		renderCompletionsTo(&b2, sh)
		script := b1.String()
		if script != b2.String() {
			t.Fatalf("%s completion output is not deterministic", sh)
		}
		for _, want := range []string{"ccli", "do", "save", "remember", "show", "memo", "lesson", "handoff"} {
			if !strings.Contains(script, want) {
				t.Fatalf("%s completion script missing %q:\n%s", sh, want, script)
			}
		}
	}
}

func TestCCLIUnknownCommandAndPhraseSuggestions(t *testing.T) {
	// 1. Unknown canonical command near-miss ("goalz")
	nearCmd := captureStderr(t, func() { unknownCommandErrorWithArgs("goalz", nil) })
	if !strings.Contains(nearCmd, `did you mean "goals"?`) || !strings.Contains(nearCmd, "sila help phrases") {
		t.Fatalf("expected goals suggestion and sila help phrases pointer for goalz:\n%s", nearCmd)
	}

	// 2. Unknown command that is a close phrase ("savve memo")
	nearPhraseCmd := captureStderr(t, func() { unknownCommandErrorWithArgs("savve", []string{"memo", "use WAL"}) })
	if !strings.Contains(nearPhraseCmd, "sila do save memo <directive>") || !strings.Contains(nearPhraseCmd, "sila memo <directive>") {
		t.Fatalf("expected phrase and canonical suggestion for 'savve memo':\n%s", nearPhraseCmd)
	}

	// 3. Unknown phrase in `sila do` ("sila do save memoo x")
	var outBuf, errBuf bytes.Buffer
	_, code := handleSilaDo([]string{"save", "memoo", "x"}, false, &outBuf, &errBuf)
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown phrase 'save memoo x'")
	}
	if !strings.Contains(errBuf.String(), `did you mean: "save memo"`) || !strings.Contains(errBuf.String(), "sila help phrases") {
		t.Fatalf("expected 'save memo' suggestion and 'sila help phrases' pointer:\n%s", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "Core loop:") {
		t.Fatalf("failed phrase must never dump full help:\n%s", errBuf.String())
	}

	// 4. Canonical-first block in `sila ccli explain` ("sync rules" and "release goal x")
	outBuf.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "sync", "rules"}, false, true, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("ccli explain sync rules exited %d: %s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), `use 'sila do "sync rules"'`) {
		t.Fatalf("expected canonical-first block guidance for 'sync rules':\n%s", outBuf.String())
	}

	outBuf.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "release", "goal", "x"}, false, true, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("ccli explain release goal x exited %d: %s", code, errBuf.String())
	}
	if !strings.Contains(outBuf.String(), `use 'sila do "release goal x"'`) {
		t.Fatalf("expected canonical-first block guidance for 'release goal x':\n%s", outBuf.String())
	}

	// 5. No close match on unknown command: must point to sila help phrases without bogus "did you mean" or full help
	farCmd := captureStderr(t, func() { unknownCommandErrorWithArgs("definitely-not-a-command", nil) })
	if strings.Contains(farCmd, "did you mean") {
		t.Fatalf("expected no 'did you mean' for unrelated command:\n%s", farCmd)
	}
	if !strings.Contains(farCmd, "sila help phrases") {
		t.Fatalf("expected sila help phrases pointer for unrelated command:\n%s", farCmd)
	}

	// 6. Suggestions bounded <= 3 and deterministic
	hints1 := suggestCloseCatalogPhrases([]string{"save", "memoo"})
	hints2 := suggestCloseCatalogPhrases([]string{"save", "memoo"})
	if len(hints1) > 3 {
		t.Fatalf("suggestions count = %d, want <= 3", len(hints1))
	}
	assertNoDuplicatesAndDeterministic(t, "suggestCloseCatalogPhrases", hints1, hints2)
}

func TestCCLIDocsCoverDualSyntaxPrecedenceTiersAndOptOut(t *testing.T) {
	for _, path := range []string{"../../README.md", "../../docs/reference.md"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		doc := string(raw)
		for _, want := range []string{
			"Two ways to run Sila",
			"Choosing a syntax",
			"canonical-first",
			"sila do",
			"sila ccli explain",
			"--no-ccli",
			"SILA_CCLI=off",
			"SILA_CCLI_DENYLIST",
			"1.0.0",
		} {
			if !strings.Contains(doc, want) {
				t.Fatalf("%s is missing documented section/term %q", path, want)
			}
		}
	}
}

func assertASCIIAndNoANSI(t *testing.T, label, s string) {
	t.Helper()
	if strings.Contains(s, "\x1b") {
		t.Fatalf("%s contains ANSI escape sequences", label)
	}
	for _, r := range s {
		if r > unicode.MaxASCII {
			t.Fatalf("%s contains non-ASCII rune %q (U+%04X)", label, r, r)
		}
	}
}

func assertNoDuplicatesAndDeterministic(t *testing.T, label string, a, b []string) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("%s length mismatch across calls (%d vs %d)", label, len(a), len(b))
	}
	seen := make(map[string]bool, len(a))
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("%s not deterministic at index %d: %q vs %q", label, i, a[i], b[i])
		}
		if seen[a[i]] {
			t.Fatalf("%s contains duplicate entry %q", label, a[i])
		}
		seen[a[i]] = true
	}
}
