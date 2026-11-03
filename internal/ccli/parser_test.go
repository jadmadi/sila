package ccli

import (
	"reflect"
	"strings"
	"testing"
)

func TestParserTableCases(t *testing.T) {
	inv := NewSilaInventory()

	cases := []struct {
		name       string
		args       []string
		mode       FlagExtractionMode
		wantExit   ExitCode
		wantAction string
		wantCanon  []string
		wantSlots  map[string]string
		wantErr    string
	}{
		{
			name:       "remember error multi-word symptom wins over remember directive",
			args:       []string{"remember", "error", "disk", "full"},
			wantExit:   ExitSuccess,
			wantAction: "recall.symptom",
			wantCanon:  []string{"recall", "disk full"},
			wantSlots:  map[string]string{"symptom": "disk full"},
		},
		{
			name:       "remember use WAL routes to memo",
			args:       []string{"remember", "use", "WAL"},
			wantExit:   ExitSuccess,
			wantAction: "memo.save",
			wantCanon:  []string{"memo", "use WAL"},
			wantSlots:  map[string]string{"directive": "use WAL"},
		},
		{
			name:     "remember error without symptom reports missing symptom instead of falling back to memo",
			args:     []string{"remember", "error"},
			wantExit: ExitUsage,
			wantErr:  "symptom",
		},
		{
			name:       "set config key to value binds until and rest slots",
			args:       []string{"set", "config", "search.provider", "to", "brave"},
			wantExit:   ExitSuccess,
			wantAction: "config.set",
			wantCanon:  []string{"config", "set", "search.provider", "brave"},
			wantSlots:  map[string]string{"key": "search.provider", "value": "brave"},
		},
		{
			name:       "merge projects keep into drop binds two directional targets",
			args:       []string{"merge", "projects", "keep", "into", "drop"},
			wantExit:   ExitSuccess,
			wantAction: "projects.merge",
			wantCanon:  []string{"projects", "merge", "keep", "drop"},
			wantSlots:  map[string]string{"source": "keep", "destination": "drop"},
		},
		{
			name:     "merge projects inverted ordering exits 2",
			args:     []string{"merge", "projects", "drop", "from", "keep"},
			wantExit: ExitUsage,
			wantErr:  "Directional commands do not permit inverted ordering",
		},
		{
			name:     "merge projects identical source and destination exits 2",
			args:     []string{"merge", "projects", "same", "into", "same"},
			wantExit: ExitUsage,
			wantErr:  "distinct source and destination",
		},
		{
			name:       "snapshot store to path binds path slot and maps to --out",
			args:       []string{"snapshot", "store", "to", "/tmp/a.bak"},
			wantExit:   ExitSuccess,
			wantAction: "backup.store",
			wantCanon:  []string{"backup", "--out=/tmp/a.bak"},
			wantSlots:  map[string]string{"out_path": "/tmp/a.bak"},
		},
		{
			name:       "explain config with omitted optional slot uses empty default",
			args:       []string{"explain", "config"},
			wantExit:   ExitSuccess,
			wantAction: "config.explain",
			wantCanon:  []string{"config", "explain"},
			wantSlots:  map[string]string{"key": ""},
		},
		{
			name:       "explain config with explicit optional slot binds key",
			args:       []string{"explain", "config", "search.provider"},
			wantExit:   ExitSuccess,
			wantAction: "config.explain",
			wantCanon:  []string{"config", "explain", "search.provider"},
			wantSlots:  map[string]string{"key": "search.provider"},
		},
		{
			name:       "ingest transcripts from known tool enum accepted",
			args:       []string{"ingest", "transcripts", "from", "codex"},
			wantExit:   ExitSuccess,
			wantAction: "ingest.tool",
			wantCanon:  []string{"ingest", "codex"},
			wantSlots:  map[string]string{"tool": "codex"},
		},
		{
			name:     "ingest transcripts from unknown tool rejected with exit 2",
			args:     []string{"ingest", "transcripts", "from", "nope"},
			wantExit: ExitUsage,
			wantErr:  "slot tool: unknown tool",
		},
		{
			name:     "save memo without directive reports missing target",
			args:     []string{"save", "memo"},
			wantExit: ExitUsage,
			wantErr:  "Missing required target",
		},
		{
			name:       "save memo quoted multi-word token binds as single value",
			args:       []string{"save", "memo", "a b"},
			wantExit:   ExitSuccess,
			wantAction: "memo.save",
			wantCanon:  []string{"memo", "a b"},
			wantSlots:  map[string]string{"directive": "a b"},
		},
		{
			name:       "save memo unquoted multi-token binds via rest slot",
			args:       []string{"save", "memo", "a", "b"},
			wantExit:   ExitSuccess,
			wantAction: "memo.save",
			wantCanon:  []string{"memo", "a b"},
			wantSlots:  map[string]string{"directive": "a b"},
		},
		{
			name:       "global flag before phrase extracted cleanly",
			args:       []string{"--json", "save", "memo", "x"},
			wantExit:   ExitSuccess,
			wantAction: "memo.save",
			wantCanon:  []string{"memo", "x", "--json"},
			wantSlots:  map[string]string{"directive": "x"},
		},
		{
			name:       "global flag between phrase tokens extracted cleanly",
			args:       []string{"save", "--json", "memo", "x"},
			wantExit:   ExitSuccess,
			wantAction: "memo.save",
			wantCanon:  []string{"memo", "x", "--json"},
			wantSlots:  map[string]string{"directive": "x"},
		},
		{
			name:     "unknown flag in strict mode exits 2",
			args:     []string{"save", "memo", "x", "--bogus"},
			mode:     FlagModeStrict,
			wantExit: ExitUsage,
			wantErr:  "Unknown flag --bogus",
		},
		{
			name:       "unknown flag in lenient mode preserved verbatim in RawFlags without consuming next token",
			args:       []string{"--bogus", "save", "memo", "x", "--custom=val"},
			mode:       FlagModeLenient,
			wantExit:   ExitSuccess,
			wantAction: "memo.save",
			wantCanon:  []string{"memo", "x", "--bogus", "--custom=val"},
			wantSlots:  map[string]string{"directive": "x"},
		},
		{
			name:     "combined short flags rejected with exit 2",
			args:     []string{"-abc", "show", "status"},
			wantExit: ExitUsage,
			wantErr:  "Combined short flags (-abc) are not supported",
		},
		{
			name:       "double dash treats leading dash token as positional value",
			args:       []string{"save", "memo", "--", "-x"},
			wantExit:   ExitSuccess,
			wantAction: "memo.save",
			wantCanon:  []string{"memo", "-x"},
			wantSlots:  map[string]string{"directive": "-x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			if mode == "" {
				mode = FlagModeStrict
			}
			res := ParseWithOptions(tc.args, inv, ParseOptions{FlagMode: mode})
			if res.ExitCode != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d (err=%q)", res.ExitCode, tc.wantExit, res.ErrorMessage)
			}
			if tc.wantExit != ExitSuccess {
				if tc.wantErr != "" && !strings.Contains(res.ErrorMessage, tc.wantErr) {
					t.Fatalf("ErrorMessage = %q, want substring %q", res.ErrorMessage, tc.wantErr)
				}
				return
			}
			if res.AST == nil {
				t.Fatal("expected non-nil AST on ExitSuccess")
			}
			if res.AST.ActionID != tc.wantAction {
				t.Fatalf("ActionID = %q, want %q", res.AST.ActionID, tc.wantAction)
			}
			if !reflect.DeepEqual(res.CanonicalArgs, tc.wantCanon) {
				t.Fatalf("CanonicalArgs = %v, want %v", res.CanonicalArgs, tc.wantCanon)
			}
			for k, wantV := range tc.wantSlots {
				if gotV := res.AST.Operands[k]; gotV != wantV {
					t.Fatalf("Operands[%q] = %q, want %q", k, gotV, wantV)
				}
			}
			if res.Specificity.TotalScore <= 0 {
				t.Fatalf("expected positive Specificity score, got %+v", res.Specificity)
			}
		})
	}
}

func TestDryRunAliasNormalizationAndRepeatedFlags(t *testing.T) {
	cat := SilaFlagCatalog()

	for _, alias := range []string{"--dry-run", "--preview", "-n", "--dry-run=true"} {
		res := ExtractFlagsWithMode([]string{"show", "status", alias}, cat, FlagModeStrict)
		if res.ExitCode != ExitSuccess {
			t.Fatalf("ExtractFlags(%s) failed: %s", alias, res.ErrorMessage)
		}
		if res.Flags["--dry-run"] != "true" {
			t.Fatalf("alias %s did not normalize Flags[\"--dry-run\"] to \"true\": got %q", alias, res.Flags["--dry-run"])
		}
		if len(res.RawFlags) != 1 || res.RawFlags[0] != alias {
			t.Fatalf("RawFlags did not preserve original spelling %q: %v", alias, res.RawFlags)
		}
	}

	// Repeated flags: last value wins in Flags, all occurrences remain in RawFlags in original order
	res := ExtractFlagsWithMode([]string{"--project=alpha", "show", "status", "--project=beta"}, cat, FlagModeStrict)
	if res.ExitCode != ExitSuccess {
		t.Fatalf("unexpected error: %s", res.ErrorMessage)
	}
	if res.Flags["--project"] != "beta" {
		t.Fatalf("Flags[\"--project\"] = %q, want \"beta\"", res.Flags["--project"])
	}
	wantRaw := []string{"--project=alpha", "--project=beta"}
	if !reflect.DeepEqual(res.RawFlags, wantRaw) {
		t.Fatalf("RawFlags = %v, want %v", res.RawFlags, wantRaw)
	}
}

func TestTypedSlotValidationMatrix(t *testing.T) {
	stdInv := NewStandardInventory()

	// Add enum and path test productions to standard inventory
	stdInv.Productions = append(stdInv.Productions,
		&Production{
			ID:           "prod_enum_test",
			ActionID:     "enum.test",
			Tier:         TierQuery,
			TargetPolicy: "permitted-default",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "format"},
				{Kind: ElementKeyword, Value: "output"},
				{Kind: ElementSlot, Slot: &Slot{Name: "fmt", Type: SlotTypeEnum, Enum: []string{"json", "toon", "text"}, Required: true}},
			},
			CanonicalBase: []string{"status"},
			ArgsOrder:     []string{"fmt"},
		},
	)

	// Invalid int
	resInt := Parse([]string{"scale", "service", "api", "to", "not-an-int"}, stdInv)
	if resInt.ExitCode != ExitUsage || !strings.Contains(resInt.ErrorMessage, "slot replicas: expected integer") {
		t.Fatalf("expected int validation error, got code=%d msg=%q", resInt.ExitCode, resInt.ErrorMessage)
	}

	// Valid int
	resIntOK := Parse([]string{"scale", "service", "api", "to", "4"}, stdInv)
	if resIntOK.ExitCode != ExitSuccess {
		t.Fatalf("expected valid int to succeed, got %d (%s)", resIntOK.ExitCode, resIntOK.ErrorMessage)
	}

	// Invalid enum
	resEnum := Parse([]string{"format", "output", "xml"}, stdInv)
	if resEnum.ExitCode != ExitUsage || !strings.Contains(resEnum.ErrorMessage, "slot fmt: expected one of json, toon, text") {
		t.Fatalf("expected enum validation error, got code=%d msg=%q", resEnum.ExitCode, resEnum.ErrorMessage)
	}

	// Invalid path with NUL
	silaInv := NewSilaInventory()
	resPath := Parse([]string{"snapshot", "store", "to", "bad\x00path"}, silaInv)
	if resPath.ExitCode != ExitUsage || !strings.Contains(resPath.ErrorMessage, "slot out_path: invalid path") {
		t.Fatalf("expected path validation error, got code=%d msg=%q", resPath.ExitCode, resPath.ErrorMessage)
	}
}

func TestIdempotenceAndDeterminism(t *testing.T) {
	// Canonical input passes through unchanged
	canonicalInputs := [][]string{
		{"memo", "x"},
		{"status", "--scan"},
		{"goals", "ready"},
		{"config", "set", "search.provider", "brave"},
		{"projects", "merge", "keep", "drop"},
	}
	for _, in := range canonicalInputs {
		got := RewriteSilaArgs(in)
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("canonical input %v was modified to %v", in, got)
		}
	}

	// Rewriting a rewritten argv is idempotent: rewrite(rewrite(x)) == rewrite(x)
	phrases := [][]string{
		{"show", "status"},
		{"remember", "error", "disk", "full"},
		{"remember", "use", "WAL"},
		{"set", "config", "search.provider", "to", "brave"},
		{"merge", "projects", "keep", "into", "drop"},
		{"snapshot", "store", "to", "/tmp/a.bak"},
	}
	for _, ph := range phrases {
		once := RewriteSilaArgs(ph)
		twice := RewriteSilaArgs(once)
		if !reflect.DeepEqual(once, twice) {
			t.Fatalf("non-idempotent rewrite for %v: once=%v twice=%v", ph, once, twice)
		}
	}

	// Determinism: 20 repeated parses produce identical AST and CanonicalArgs
	inv := NewSilaInventory()
	sample := []string{"remember", "error", "sqlite", "busy", "timeout"}
	first := Parse(sample, inv)
	for i := 0; i < 20; i++ {
		next := Parse(sample, inv)
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("non-deterministic Parse on iteration %d: %+v != %+v", i, first, next)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("remember error disk full")
	f.Add("remember use WAL")
	f.Add("remember error")
	f.Add("set config search.provider to brave")
	f.Add("merge projects keep into drop")
	f.Add("snapshot store to /tmp/a.bak")
	f.Add("save memo -- -x")
	f.Add("-abc show status")
	f.Add("login user admin password ghp_secret123456")

	inv := NewSilaInventory()
	f.Fuzz(func(t *testing.T, raw string) {
		args := strings.Fields(raw)
		r1 := Parse(args, inv)
		r2 := Parse(args, inv)
		if r1.ExitCode != r2.ExitCode || r1.ErrorMessage != r2.ErrorMessage || !reflect.DeepEqual(r1.CanonicalArgs, r2.CanonicalArgs) {
			t.Fatalf("non-deterministic FuzzParse for %q", raw)
		}
		// Ensure secret values are never echoed in ErrorMessage
		for _, a := range args {
			if strings.HasPrefix(a, "ghp_") || strings.HasPrefix(a, "sk_live_") {
				if strings.Contains(r1.ErrorMessage, a) {
					t.Fatalf("ErrorMessage leaked secret token %q: %s", a, r1.ErrorMessage)
				}
			}
		}
	})
}
