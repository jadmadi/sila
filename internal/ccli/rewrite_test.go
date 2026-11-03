package ccli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/cli"
)

func TestRewriteArgsPrecedenceMatrix(t *testing.T) {
	opts := RewriteOptions{
		FlagMode: FlagModeLenient,
		EnvSet:   true,
		CCLIEnv:  "on",
		Denylist: "",
	}

	// Facts.md §3 Precedence Matrix: Canonical-first (C), Direct phrase (D), and Explicit Do (DO)
	canonicalInputs := [][]string{
		{"status"},
		{"goals", "--json"},
		{"sync", "rules"},
		{"prime", "context"},
		{"heal", "rules"},
		{"release", "goal"},
		{"release", "goal", "x"},
		{"search", "for", "x"},
		{"watch", "live"},
		{"dream", "claims"},
		{"clean", "caches"},
		{"vacuum", "database"},
		{"backup", "database"},
		{"reconcile", "drift"},
		{"ingest", "transcripts", "from", "codex"},
		{"install", "rules"},
	}

	for _, in := range canonicalInputs {
		res := RewriteArgs(in, RewriteAuto, opts)
		if res.Matched {
			t.Errorf("RewriteAuto(%v): expected Matched=false under canonical-first, got Matched=true (%s -> %v)", in, res.Production, res.Canonical)
		}
		if res.Decision != DecisionCanonical {
			t.Errorf("RewriteAuto(%v): expected Decision=%q, got %q", in, DecisionCanonical, res.Decision)
		}
		if res.DecidedBy != DecidedByCanonical {
			t.Errorf("RewriteAuto(%v): expected DecidedBy=%q, got %q", in, DecidedByCanonical, res.DecidedBy)
		}
		if !reflect.DeepEqual(res.Canonical, in) {
			t.Errorf("RewriteAuto(%v): expected unchanged Canonical=%v, got %v", in, in, res.Canonical)
		}

		// Explain mode must also report CANONICAL with exit 0
		expRes := RewriteArgs(in, RewriteExplain, opts)
		if expRes.Decision != DecisionCanonical {
			t.Errorf("RewriteExplain(%v): expected Decision=%q, got %q", in, DecisionCanonical, expRes.Decision)
		}
		if expRes.ExitCode != ExitSuccess {
			t.Errorf("RewriteExplain(%v): expected ExitCode=0, got %d", in, expRes.ExitCode)
		}
	}

	directPhrases := []struct {
		input []string
		want  []string
	}{
		{[]string{"say", "hello"}, []string{"salam"}},
		{[]string{"remember", "error", "disk", "full"}, []string{"recall", "disk full"}},
		{[]string{"save", "memo", "use WAL"}, []string{"memo", "use WAL"}},
		{[]string{"claim", "goal", "my-slug"}, []string{"goals", "claim", "my-slug"}},
		{[]string{"drop", "goal", "my-slug"}, []string{"goals", "release", "my-slug"}},
		{[]string{"find", "sqlite"}, []string{"search", "sqlite"}},
		{[]string{"refresh", "memory"}, []string{"sync"}},
		{[]string{"start", "live", "cockpit"}, []string{"live"}},
	}

	for _, tc := range directPhrases {
		res := RewriteArgs(tc.input, RewriteAuto, opts)
		if !res.Matched || res.Decision != DecisionRoute {
			t.Fatalf("RewriteAuto(%v): expected ROUTE match, got Decision=%s Err=%v", tc.input, res.Decision, res.Err)
		}
		if !reflect.DeepEqual(res.Canonical, tc.want) {
			t.Errorf("RewriteAuto(%v): got Canonical=%v, want %v", tc.input, res.Canonical, tc.want)
		}
	}

	doOverrides := []struct {
		input []string
		want  []string
	}{
		{[]string{"sync", "rules"}, []string{"sync"}},
		{[]string{"sync rules"}, []string{"sync"}},
		{[]string{"release", "goal", "x"}, []string{"goals", "release", "x"}},
		{[]string{"release goal x"}, []string{"goals", "release", "x"}},
		{[]string{"prime", "context"}, []string{"prime"}},
		{[]string{"heal", "rules"}, []string{"heal"}},
		{[]string{"reconcile", "drift"}, []string{"reconcile"}},
		{[]string{"vacuum", "database"}, []string{"vacuum"}},
		{[]string{"save memo", "use WAL"}, []string{"memo", "use WAL"}},
	}

	for _, tc := range doOverrides {
		res := RewriteArgs(tc.input, RewriteDo, opts)
		if !res.Matched || res.Decision != DecisionRoute {
			t.Fatalf("RewriteDo(%v): expected ROUTE match, got Decision=%s Err=%v", tc.input, res.Decision, res.Err)
		}
		if !reflect.DeepEqual(res.Canonical, tc.want) {
			t.Errorf("RewriteDo(%v): got Canonical=%v, want %v", tc.input, res.Canonical, tc.want)
		}
	}
}

func TestCanonicalCommandsAndAliasesNeverAutoRewritten(t *testing.T) {
	opts := RewriteOptions{
		FlagMode: FlagModeLenient,
		EnvSet:   true,
		CCLIEnv:  "on",
	}

	for _, spec := range cli.All() {
		names := append([]string{spec.Name}, spec.Aliases...)
		for _, name := range names {
			in := []string{name}
			res := RewriteArgs(in, RewriteAuto, opts)
			if res.Matched || res.Decision != DecisionCanonical {
				t.Errorf("canonical command/alias %q was rewritten or not marked CANONICAL: Decision=%s Canonical=%v", name, res.Decision, res.Canonical)
			}
			if !reflect.DeepEqual(res.Canonical, in) {
				t.Errorf("canonical command/alias %q changed: got %v, want %v", name, res.Canonical, in)
			}
		}
	}

	// Verify all 13 originally shipped productions:
	// 9 non-overlapping remain auto-routed; 4 overlapping (prime context, heal rules, sync rules, reconcile drift)
	// are canonical-first in auto mode and routed in do mode.
	nonOverlapping9 := []struct {
		in   []string
		want []string
	}{
		{[]string{"show", "status"}, []string{"status"}},
		{[]string{"list", "goals"}, []string{"goals"}},
		{[]string{"show", "ready", "goals"}, []string{"goals", "--ready"}},
		{[]string{"list", "pending", "goals"}, []string{"goals", "--pending"}},
		{[]string{"list", "claimed", "goals"}, []string{"goals", "--claimed"}},
		{[]string{"run", "gate"}, []string{"gate"}},
		{[]string{"list", "projects"}, []string{"projects"}},
		{[]string{"run", "doctor"}, []string{"doctor"}},
		{[]string{"say", "salam"}, []string{"salam"}},
	}
	for _, tc := range nonOverlapping9 {
		res := RewriteArgs(tc.in, RewriteAuto, opts)
		if !res.Matched || !reflect.DeepEqual(res.Canonical, tc.want) {
			t.Errorf("shipped production %v: got (%v, %v), want (true, %v)", tc.in, res.Matched, res.Canonical, tc.want)
		}
	}

	overlapping4 := []struct {
		in     []string
		direct []string
		want   []string
	}{
		{[]string{"prime", "context"}, []string{"refresh", "context"}, []string{"prime"}},
		{[]string{"heal", "rules"}, []string{"fix", "rules"}, []string{"heal"}},
		{[]string{"sync", "rules"}, []string{"refresh", "memory"}, []string{"sync"}},
		{[]string{"reconcile", "drift"}, []string{"check", "drift"}, []string{"reconcile"}},
	}
	for _, tc := range overlapping4 {
		autoRes := RewriteArgs(tc.in, RewriteAuto, opts)
		if autoRes.Matched || autoRes.Decision != DecisionCanonical {
			t.Errorf("overlapping shipped production %v should stay canonical in auto mode, got Decision=%s", tc.in, autoRes.Decision)
		}
		doRes := RewriteArgs(tc.in, RewriteDo, opts)
		if !doRes.Matched || !reflect.DeepEqual(doRes.Canonical, tc.want) {
			t.Errorf("overlapping shipped production %v should route in do mode to %v, got %v", tc.in, tc.want, doRes.Canonical)
		}
		dirRes := RewriteArgs(tc.direct, RewriteAuto, opts)
		if !dirRes.Matched || !reflect.DeepEqual(dirRes.Canonical, tc.want) {
			t.Errorf("direct alternative %v should route in auto mode to %v, got %v", tc.direct, tc.want, dirRes.Canonical)
		}
	}
}

func TestRewriteArgsIdempotenceAndDeterminism(t *testing.T) {
	opts := RewriteOptions{
		FlagMode: FlagModeLenient,
		EnvSet:   true,
		CCLIEnv:  "on",
	}

	phrases := [][]string{
		{"say", "hello"},
		{"save", "memo", "use WAL"},
		{"refresh", "context", "--top=5"},
		{"claim", "goal", "my-goal", "--agent=codex"},
		{"find", "race detector"},
		{"run", "doctor", "--repair"},
	}

	for _, p := range phrases {
		r1 := RewriteArgs(p, RewriteAuto, opts)
		if !r1.Matched {
			t.Fatalf("expected phrase %v to match", p)
		}
		// Rewriting the canonical output again must yield the exact same canonical output (idempotence).
		r2 := RewriteArgs(r1.Canonical, RewriteAuto, opts)
		if !reflect.DeepEqual(r1.Canonical, r2.Canonical) {
			t.Errorf("idempotence failed for %v: first=%v, second=%v", p, r1.Canonical, r2.Canonical)
		}
		// Determinism across 10 runs
		for i := 0; i < 10; i++ {
			rn := RewriteArgs(p, RewriteAuto, opts)
			if !reflect.DeepEqual(r1.Canonical, rn.Canonical) || r1.Production != rn.Production {
				t.Fatalf("non-deterministic result on iteration %d for %v", i, p)
			}
		}
	}
}

func TestOptOutAndDenylistPrecedence(t *testing.T) {
	phrase := []string{"say", "hello"}

	// 1. Synonyms for SILA_CCLI=off
	for _, offVal := range []string{"off", "OFF", "0", "false", "FALSE", "no", "disable"} {
		res := RewriteArgs(phrase, RewriteAuto, RewriteOptions{EnvSet: true, CCLIEnv: offVal})
		if res.Decision != DecisionDisabled || res.DecidedBy != DecidedByEnv || res.Matched {
			t.Errorf("SILA_CCLI=%q: expected DISABLED by env, got Decision=%s DecidedBy=%s Matched=%v", offVal, res.Decision, res.DecidedBy, res.Matched)
		}
		if !reflect.DeepEqual(res.Canonical, phrase) {
			t.Errorf("SILA_CCLI=%q: expected unchanged %v, got %v", offVal, phrase, res.Canonical)
		}
	}

	// 2. Synonyms for SILA_CCLI=on
	for _, onVal := range []string{"", "on", "ON", "1", "true", "TRUE", "yes", "enable"} {
		res := RewriteArgs(phrase, RewriteAuto, RewriteOptions{EnvSet: true, CCLIEnv: onVal})
		if res.Decision != DecisionRoute || !res.Matched {
			t.Errorf("SILA_CCLI=%q: expected ROUTE, got Decision=%s", onVal, res.Decision)
		}
	}

	// 3. Invalid SILA_CCLI value warns once and behaves as on
	ResetEnvWarningsForTest()
	var warnBuf bytes.Buffer
	resWarn1 := RewriteArgs(phrase, RewriteAuto, RewriteOptions{EnvSet: true, CCLIEnv: "banana", WarnWriter: &warnBuf})
	if resWarn1.Decision != DecisionRoute || !resWarn1.Matched {
		t.Errorf("invalid SILA_CCLI should behave as on, got Decision=%s", resWarn1.Decision)
	}
	if !strings.Contains(warnBuf.String(), "banana") {
		t.Errorf("expected warning for invalid SILA_CCLI value 'banana', got %q", warnBuf.String())
	}
	warnBuf.Reset()
	_ = RewriteArgs(phrase, RewriteAuto, RewriteOptions{EnvSet: true, CCLIEnv: "banana", WarnWriter: &warnBuf})
	if warnBuf.Len() != 0 {
		t.Errorf("expected invalid SILA_CCLI value to warn only once, got second warning: %q", warnBuf.String())
	}

	// 4. Denylist: exact token, multi-token prefix, case-insensitive, token-boundary, post-flag extraction
	resDeny1 := RewriteArgs([]string{"--json", "SAY", "hello"}, RewriteAuto, RewriteOptions{EnvSet: true, CCLIEnv: "on", Denylist: "say"})
	if resDeny1.Decision != DecisionDenylisted || resDeny1.DecidedBy != DecidedByDenylist || resDeny1.Matched {
		t.Errorf("expected DENYLISTED after flag extraction, got Decision=%s DecidedBy=%s", resDeny1.Decision, resDeny1.DecidedBy)
	}

	resDenyFull := RewriteArgs([]string{"refresh", "context", "--top=3"}, RewriteAuto, RewriteOptions{EnvSet: true, CCLIEnv: "on", Denylist: "drop, refresh context"})
	if resDenyFull.Decision != DecisionDenylisted || resDenyFull.DecidedBy != DecidedByDenylist {
		t.Errorf("expected multi-word denylist match, got Decision=%s", resDenyFull.Decision)
	}

	// Token boundary: "sa" must NOT match "say"
	resBoundary := RewriteArgs([]string{"say", "hello"}, RewriteAuto, RewriteOptions{EnvSet: true, CCLIEnv: "on", Denylist: "sa"})
	if resBoundary.Decision != DecisionRoute || !resBoundary.Matched {
		t.Errorf("partial token 'sa' should not denylist 'say hello', got Decision=%s", resBoundary.Decision)
	}

	// 5. Precedence order: --no-ccli (flag) > SILA_CCLI=off (env) > denylist > auto
	resPrec1 := RewriteArgs([]string{"--no-ccli", "say", "hello"}, RewriteAuto, RewriteOptions{
		EnvSet:   true,
		CCLIEnv:  "off",
		Denylist: "say",
	})
	if resPrec1.Decision != DecisionDisabled || resPrec1.DecidedBy != DecidedByFlag {
		t.Errorf("expected --no-ccli (flag) to win over env and denylist, got Decision=%s DecidedBy=%s", resPrec1.Decision, resPrec1.DecidedBy)
	}

	resPrec2 := RewriteArgs([]string{"say", "hello"}, RewriteAuto, RewriteOptions{
		EnvSet:   true,
		CCLIEnv:  "off",
		Denylist: "say",
	})
	if resPrec2.Decision != DecisionDisabled || resPrec2.DecidedBy != DecidedByEnv {
		t.Errorf("expected SILA_CCLI=off (env) to win over denylist, got Decision=%s DecidedBy=%s", resPrec2.Decision, resPrec2.DecidedBy)
	}

	// 6. All four `do` x flag/env combinations (R3.5 / AC-010):
	// Combination 1: do + env=on + no flag -> ROUTE
	do1 := RewriteArgs(phrase, RewriteDo, RewriteOptions{EnvSet: true, CCLIEnv: "on"})
	if do1.Decision != DecisionRoute || !do1.Matched {
		t.Errorf("do (env=on, no-ccli=false): expected ROUTE, got %s", do1.Decision)
	}
	// Combination 2: do + env=off + denylisted -> ROUTE (do ignores env and denylist)
	do2 := RewriteArgs(phrase, RewriteDo, RewriteOptions{EnvSet: true, CCLIEnv: "off", Denylist: "say"})
	if do2.Decision != DecisionRoute || !do2.Matched {
		t.Errorf("do (env=off, denylist=say): expected ROUTE, got %s", do2.Decision)
	}
	// Combination 3: do + env=on + --no-ccli -> DISABLED (exit 2)
	do3 := RewriteArgs([]string{"--no-ccli", "say", "hello"}, RewriteDo, RewriteOptions{EnvSet: true, CCLIEnv: "on"})
	if do3.Decision != DecisionDisabled || do3.DecidedBy != DecidedByFlag || do3.ExitCode != ExitUsage {
		t.Errorf("do (--no-ccli, env=on): expected DISABLED exit 2, got Decision=%s Exit=%d", do3.Decision, do3.ExitCode)
	}
	// Combination 4: do + env=off + --no-ccli -> DISABLED by flag (exit 2)
	do4 := RewriteArgs(phrase, RewriteDo, RewriteOptions{NoCCLI: true, EnvSet: true, CCLIEnv: "off", Denylist: "say"})
	if do4.Decision != DecisionDisabled || do4.DecidedBy != DecidedByFlag || do4.ExitCode != ExitUsage {
		t.Errorf("do (--no-ccli, env=off): expected DISABLED by flag exit 2, got Decision=%s DecidedBy=%s Exit=%d", do4.Decision, do4.DecidedBy, do4.ExitCode)
	}
}

func TestFlagsAndDoubleDashHandling(t *testing.T) {
	opts := RewriteOptions{FlagMode: FlagModeLenient, EnvSet: true, CCLIEnv: "on"}

	// Global flags before, between, and after phrase tokens
	for _, in := range [][]string{
		{"--json", "show", "status"},
		{"show", "--json", "status"},
		{"show", "status", "--json"},
	} {
		res := RewriteArgs(in, RewriteAuto, opts)
		if !res.Matched || !reflect.DeepEqual(res.Canonical, []string{"status", "--json"}) {
			t.Errorf("flag placement %v: got %v, want [status --json]", in, res.Canonical)
		}
	}

	// Unknown flags preserved in order in lenient mode
	lenientRes := RewriteArgs([]string{"show", "status", "--future-flag=1", "--another-flag"}, RewriteAuto, opts)
	wantLenient := []string{"status", "--future-flag=1", "--another-flag"}
	if !lenientRes.Matched || !reflect.DeepEqual(lenientRes.Canonical, wantLenient) {
		t.Errorf("lenient unknown flags: got %v, want %v", lenientRes.Canonical, wantLenient)
	}

	// Strict mode rejects unknown flags with ExitUsage (2)
	strictRes := RewriteArgs([]string{"show", "status", "--future-flag=1"}, RewriteExplain, RewriteOptions{
		FlagMode: FlagModeStrict,
		EnvSet:   true,
		CCLIEnv:  "on",
	})
	if strictRes.ExitCode != ExitUsage || strictRes.Matched {
		t.Errorf("strict unknown flag: expected ExitUsage (2) and Matched=false, got Exit=%d Matched=%v", strictRes.ExitCode, strictRes.Matched)
	}

	// `--` stops flag extraction so `--not-a-flag` is treated as positional operand
	ddRes := RewriteArgs([]string{"save", "memo", "--", "--not-a-flag"}, RewriteDo, opts)
	if !ddRes.Matched || !reflect.DeepEqual(ddRes.Canonical, []string{"memo", "--not-a-flag"}) {
		t.Errorf("double-dash operand: got %v, want [memo --not-a-flag]", ddRes.Canonical)
	}
}

func TestRedactionAndForbiddenPhrases(t *testing.T) {
	opts := RewriteOptions{FlagMode: FlagModeLenient, EnvSet: true, CCLIEnv: "on"}
	secretSentinel := "ghp_SUPERSECRETTOKEN9999"

	// 1. Forbidden config secret phrase
	res1 := RewriteArgs([]string{"set", "config", "github_token", "to", secretSentinel}, RewriteExplain, opts)
	if !res1.Forbidden || res1.ExitCode != ExitPolicy || res1.Canonical != nil {
		t.Fatalf("expected forbidden config phrase to have Forbidden=true, ExitCode=3, Canonical=nil; got Forbidden=%v Exit=%d Canonical=%v", res1.Forbidden, res1.ExitCode, res1.Canonical)
	}
	var buf1 bytes.Buffer
	enc1 := json.NewEncoder(&buf1)
	enc1.SetEscapeHTML(false)
	_ = enc1.Encode(res1)
	rawJSON := buf1.String()
	if strings.Contains(rawJSON, secretSentinel) {
		t.Fatalf("secret sentinel leaked into RewriteResult JSON: %s", rawJSON)
	}
	wantRedacted := "<redacted:24 chars>"
	if !strings.Contains(rawJSON, wantRedacted) {
		t.Errorf("expected %q in RewriteResult JSON, got: %s", wantRedacted, rawJSON)
	}

	// 2. Positional secret prefix in arbitrary phrase
	awsSecret := "AKIA1234567890ABCDEF"
	res2 := RewriteArgs([]string{"save", "memo", awsSecret}, RewriteExplain, opts)
	if !res2.Forbidden || res2.ExitCode != ExitPolicy || res2.Canonical != nil {
		t.Fatalf("expected positional secret to be refused with ExitCode=3 and Canonical=nil")
	}
	var buf2 bytes.Buffer
	enc2 := json.NewEncoder(&buf2)
	enc2.SetEscapeHTML(false)
	_ = enc2.Encode(res2)
	rawJSON2 := buf2.String()
	if strings.Contains(rawJSON2, awsSecret) {
		t.Fatalf("AWS secret leaked into JSON: %s", rawJSON2)
	}
	if !strings.Contains(rawJSON2, "<redacted:20 chars>") {
		t.Errorf("expected <redacted:20 chars> in JSON, got %s", rawJSON2)
	}
}

func TestRegistryResolutionCases(t *testing.T) {
	// Valid canonical path
	res1, ok1 := cli.Resolve([]string{"goals", "claim", "my-slug"})
	if !ok1 || !res1.KnownPath || !cli.CanonicalAccepts([]string{"goals", "claim", "my-slug"}) {
		t.Errorf("expected 'goals claim my-slug' to be a valid canonical path")
	}

	// Unknown subcommand on command with subcommands and AcceptsPositional=false
	res2, ok2 := cli.Resolve([]string{"release", "goal"})
	if !ok2 || res2.KnownPath || res2.UnknownSubcommand != "goal" || cli.CanonicalAccepts([]string{"release", "goal"}) {
		t.Errorf("expected 'release goal' to resolve command 'release' with KnownPath=false and UnknownSubcommand='goal', got %+v", res2)
	}

	// Extra positional on command with AcceptsPositional=false and no subcommands
	res3, ok3 := cli.Resolve([]string{"vacuum", "database"})
	if !ok3 || res3.KnownPath || res3.ExtraPositionals != 1 || cli.CanonicalAccepts([]string{"vacuum", "database"}) {
		t.Errorf("expected 'vacuum database' to have KnownPath=false and ExtraPositionals=1, got %+v", res3)
	}
}
