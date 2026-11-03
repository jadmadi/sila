package ccli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jadmadi/sila/internal/cli"
)

func TestLoadCatalogEmbeddedAndFromFile(t *testing.T) {
	inv, cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog() failed: %v", err)
	}
	if cat.SchemaVersion != CatalogSchemaVersion {
		t.Fatalf("SchemaVersion = %q, want %q", cat.SchemaVersion, CatalogSchemaVersion)
	}
	if len(inv.Productions) == 0 {
		t.Fatal("LoadCatalog() returned 0 productions")
	}

	// Verify all 13 initial productions are present
	wantIDs := []string{
		"sila_status_show",
		"sila_goals_list",
		"sila_goals_ready",
		"sila_goals_pending",
		"sila_goals_claimed",
		"sila_prime_context",
		"sila_gate_run",
		"sila_heal_rules",
		"sila_sync_rules",
		"sila_projects_list",
		"sila_doctor_run",
		"sila_salam_say",
		"sila_reconcile_drift",
	}
	gotIDs := make(map[string]bool, len(inv.Productions))
	for _, p := range inv.Productions {
		gotIDs[p.ID] = true
	}
	for _, id := range wantIDs {
		if !gotIDs[id] {
			t.Fatalf("missing initial production %q in loaded catalog", id)
		}
	}

	// Test loading from a fixture file on disk
	tmp := filepath.Join(t.TempDir(), "fixture-catalog.json")
	if err := os.WriteFile(tmp, embeddedCatalogBytes, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fileInv, fileCat, diags, err := LoadCatalogFromFile(tmp)
	if err != nil || len(diags) > 0 {
		t.Fatalf("LoadCatalogFromFile failed: %v (diags=%v)", err, diags)
	}
	if CatalogDigest(cat) != CatalogDigest(fileCat) {
		t.Fatalf("digest mismatch between embedded and file catalog")
	}
	if len(fileInv.Productions) != len(inv.Productions) {
		t.Fatalf("fileInv productions = %d, want %d", len(fileInv.Productions), len(inv.Productions))
	}
}

func TestCatalogDigestDeterministicAcrossReorderedArrays(t *testing.T) {
	_, cat, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	d1 := CatalogDigest(cat)
	if len(d1) != 64 {
		t.Fatalf("unexpected digest length %d: %q", len(d1), d1)
	}

	reordered := cloneCatalog(cat)
	// Reverse productions, plan_rows, and phrases within each production
	for i, j := 0, len(reordered.Productions)-1; i < j; i, j = i+1, j-1 {
		reordered.Productions[i], reordered.Productions[j] = reordered.Productions[j], reordered.Productions[i]
	}
	for i, j := 0, len(reordered.PlanRows)-1; i < j; i, j = i+1, j-1 {
		reordered.PlanRows[i], reordered.PlanRows[j] = reordered.PlanRows[j], reordered.PlanRows[i]
	}
	for idx := range reordered.Productions {
		phrases := reordered.Productions[idx].Phrases
		for i, j := 0, len(phrases)-1; i < j; i, j = i+1, j-1 {
			phrases[i], phrases[j] = phrases[j], phrases[i]
		}
		aliases := reordered.Productions[idx].DryRunAliases
		for i, j := 0, len(aliases)-1; i < j; i, j = i+1, j-1 {
			aliases[i], aliases[j] = aliases[j], aliases[i]
		}
	}

	d2 := CatalogDigest(reordered)
	if d1 != d2 {
		t.Fatalf("CatalogDigest changed after reordering arrays: %q != %q", d1, d2)
	}
}

func TestNewSilaInventoryAndFlagCatalogParity(t *testing.T) {
	silaInv := NewSilaInventory()
	catInv, _, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(silaInv.Productions) != len(catInv.Productions) {
		t.Fatalf("NewSilaInventory productions = %d, want %d", len(silaInv.Productions), len(catInv.Productions))
	}
	for i := range silaInv.Productions {
		if !reflect.DeepEqual(silaInv.Productions[i], catInv.Productions[i]) {
			t.Fatalf("production[%d] mismatch between NewSilaInventory and LoadCatalog", i)
		}
	}

	flags := SilaFlagCatalog()
	for _, rf := range cli.Flags() {
		primary := formatFlagName(rf.Name)
		got, ok := flags[primary]
		if !ok {
			t.Fatalf("SilaFlagCatalog missing flag %q from internal/cli", primary)
		}
		if got.Arity != rf.Arity || got.Type != rf.Type {
			t.Fatalf("flag %q mismatch: got %+v, want arity=%d type=%s", primary, got, rf.Arity, rf.Type)
		}
		for _, alias := range rf.Aliases {
			aKey := formatFlagName(alias)
			if _, ok := flags[aKey]; !ok {
				t.Fatalf("SilaFlagCatalog missing alias %q for %q", aKey, primary)
			}
		}
	}

	// Verify all required machine/control/safety flags from R4.3
	for _, req := range []string{
		"--json", "--json-envelope", "--toon", "--porcelain", "--quiet",
		"--project", "--db", "--no-color", "--dry-run", "--preview", "-n",
		"--yes", "--force", "--help", "--verbose", "-v", "--version", "-V",
		"--no-input", "--confirm", "--no-ccli", "--ccli-strict-exit",
	} {
		if _, ok := flags[req]; !ok {
			t.Fatalf("SilaFlagCatalog missing required flag %q", req)
		}
	}
}

func TestStaticFlagsReplacesHardcodedForceWithoutDoubleEmission(t *testing.T) {
	prod := &Production{
		ID:            "fixture_static_flag",
		ActionID:      "fixture.static",
		Tier:          TierDestructive,
		CanonicalBase: []string{"vacuum"},
		StaticFlags:   []string{"--force"},
	}
	ast := &AST{
		ActionID:     "fixture.static",
		ProductionID: "fixture_static_flag",
		Tier:         TierDestructive,
		Operands:     map[string]string{"force": "true"},
		RawFlags:     []string{"--force", "--json"},
	}
	got := Rewrite(ast, prod)
	want := []string{"vacuum", "--force", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Rewrite with StaticFlags = %v, want %v (no duplicate --force)", got, want)
	}

	// Without StaticFlags, operands["force"] == "true" alone does NOT synthesize --force
	prodNoStatic := &Production{
		ID:            "fixture_no_static",
		ActionID:      "fixture.nostatic",
		Tier:          TierQuery,
		CanonicalBase: []string{"status"},
	}
	astNoStatic := &AST{
		ActionID:     "fixture.nostatic",
		ProductionID: "fixture_no_static",
		Tier:         TierQuery,
		Operands:     map[string]string{"force": "true"},
	}
	gotNoStatic := Rewrite(astNoStatic, prodNoStatic)
	if !reflect.DeepEqual(gotNoStatic, []string{"status"}) {
		t.Fatalf("Rewrite synthesized --force without StaticFlags: %v", gotNoStatic)
	}
}

func validMinimalCatalog() *Catalog {
	return &Catalog{
		SchemaVersion:  CatalogSchemaVersion,
		CatalogVersion: "1.0.0",
		MinSilaVersion: "0.17.0",
		Description:    "test catalog",
		PlanRows: []PlanRowSpec{
			{PlanRow: 1, Domain: "store", Command: "status", RowStatus: "implemented", ProductionIDs: []string{"p_status"}},
		},
		Productions: []CatalogProduction{
			{
				ID:                 "p_status",
				ActionID:           "status.show",
				Domain:             "store",
				Description:        "show status",
				Tier:               TierQuery,
				Precedence:         "canonical-first",
				TargetPolicy:       "permitted-default",
				DryRunPolicy:       "none",
				ConfirmationPolicy: "none",
				PlanRow:            1,
				Phrases: [][]CatalogElement{
					{{Kind: "keyword", Value: "show"}, {Kind: "keyword", Value: "status"}},
				},
				CanonicalBase: []string{"status"},
			},
		},
	}
}

func TestValidateCatalogNegativeMatrix(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(c *Catalog)
		wantCode string
	}{
		{
			name: "unsupported schema version",
			mutate: func(c *Catalog) {
				c.SchemaVersion = "sila/ccli-catalog/v99"
			},
			wantCode: DiagCatalogSchema,
		},
		{
			name: "duplicate production ID",
			mutate: func(c *Catalog) {
				dup := c.Productions[0]
				dup.ActionID = "status.other"
				dup.Phrases = [][]CatalogElement{{{Kind: "keyword", Value: "inspect"}, {Kind: "keyword", Value: "status"}}}
				c.Productions = append(c.Productions, dup)
			},
			wantCode: DiagDuplicateID,
		},
		{
			name: "duplicate action ID without variant_of",
			mutate: func(c *Catalog) {
				dup := c.Productions[0]
				dup.ID = "p_status_2"
				dup.Phrases = [][]CatalogElement{{{Kind: "keyword", Value: "inspect"}, {Kind: "keyword", Value: "status"}}}
				c.Productions = append(c.Productions, dup)
			},
			wantCode: DiagDuplicateAction,
		},
		{
			name: "duplicate phrase skeleton",
			mutate: func(c *Catalog) {
				c.Productions[0].Phrases = append(c.Productions[0].Phrases,
					[]CatalogElement{{Kind: "keyword", Value: "show"}, {Kind: "keyword", Value: "status"}},
				)
			},
			wantCode: DiagDuplicatePhrase,
		},
		{
			name: "grammar collision",
			mutate: func(c *Catalog) {
				c.Productions[0].Slots = []Slot{{Name: "item", Type: SlotTypeString, Required: true}}
				c.Productions[0].ArgsOrder = []string{"item"}
				c.Productions[0].Phrases = [][]CatalogElement{
					{{Kind: "keyword", Value: "inspect"}, {Kind: "slot", Slot: "item"}},
				}
				c.Productions = append(c.Productions, CatalogProduction{
					ID:                 "p_collide",
					ActionID:           "goals.inspect",
					Domain:             "goals",
					Description:        "inspect goals",
					Tier:               TierQuery,
					Precedence:         "canonical-first",
					TargetPolicy:       "permitted-default",
					DryRunPolicy:       "none",
					ConfirmationPolicy: "none",
					Phrases: [][]CatalogElement{
						{{Kind: "keyword", Value: "inspect"}, {Kind: "keyword", Value: "goals"}},
					},
					CanonicalBase: []string{"goals"},
				})
			},
			wantCode: DiagCollision,
		},
		{
			name: "forbidden secret slot",
			mutate: func(c *Catalog) {
				c.Productions[0].Slots = []Slot{{Name: "api_key", Type: SlotTypeString, Required: true}}
				c.Productions[0].ArgsOrder = []string{"api_key"}
				c.Productions[0].Phrases = [][]CatalogElement{
					{{Kind: "keyword", Value: "show"}, {Kind: "slot", Slot: "api_key"}},
				}
			},
			wantCode: DiagSecretSlot,
		},
		{
			name: "sensitive slot true",
			mutate: func(c *Catalog) {
				c.Productions[0].Slots = []Slot{{Name: "custom_secret", Type: SlotTypeString, Required: true, Sensitive: true}}
				c.Productions[0].ArgsOrder = []string{"custom_secret"}
				c.Productions[0].Phrases = [][]CatalogElement{
					{{Kind: "keyword", Value: "show"}, {Kind: "slot", Slot: "custom_secret"}},
				}
			},
			wantCode: DiagSensitiveSlot,
		},
		{
			name: "unknown canonical command",
			mutate: func(c *Catalog) {
				c.Productions[0].CanonicalBase = []string{"not-a-command"}
			},
			wantCode: DiagUnknownCommand,
		},
		{
			name: "unknown slot in args_order",
			mutate: func(c *Catalog) {
				c.Productions[0].ArgsOrder = []string{"ghost_slot"}
			},
			wantCode: DiagUnknownSlot,
		},
		{
			name: "unmapped declared slot",
			mutate: func(c *Catalog) {
				c.Productions[0].Slots = []Slot{{Name: "orphan", Type: SlotTypeString}}
			},
			wantCode: DiagUnmappedSlot,
		},
		{
			name: "required slot absent from phrase",
			mutate: func(c *Catalog) {
				c.Productions[0].Slots = []Slot{{Name: "req_slot", Type: SlotTypeString, Required: true}}
				c.Productions[0].ArgsOrder = []string{"req_slot"}
			},
			wantCode: DiagMissingSlot,
		},
		{
			name: "mutation without target policy",
			mutate: func(c *Catalog) {
				c.Productions[0].Tier = TierMutation
				c.Productions[0].TargetPolicy = ""
			},
			wantCode: DiagTargetPolicy,
		},
		{
			name: "implicit target without target_scope",
			mutate: func(c *Catalog) {
				c.Productions[0].Tier = TierMutation
				c.Productions[0].TargetPolicy = "implicit"
				c.Productions[0].TargetScope = ""
			},
			wantCode: DiagImplicitTarget,
		},
		{
			name: "destructive without confirmation policy",
			mutate: func(c *Catalog) {
				c.Productions[0].Tier = TierDestructive
				c.Productions[0].TargetPolicy = "required"
				c.Productions[0].ConfirmationPolicy = "none"
			},
			wantCode: DiagConfirmationPolicy,
		},
		{
			name: "preview policy missing dry-run aliases",
			mutate: func(c *Catalog) {
				c.Productions[0].DryRunPolicy = "preview"
				c.Productions[0].DryRunAliases = []string{"--dry-run"}
			},
			wantCode: DiagDryRunAliases,
		},
		{
			name: "phrase-first without rejection witness",
			mutate: func(c *Catalog) {
				c.Productions[0].Precedence = "phrase-first"
				c.Productions[0].PrecedenceWitness = ""
			},
			wantCode: DiagPrecedenceUnproven,
		},
		{
			name: "unknown flag in static_flags",
			mutate: func(c *Catalog) {
				c.Productions[0].StaticFlags = []string{"--nonexistent-flag"}
			},
			wantCode: DiagUnknownFlag,
		},
		{
			name: "canonical-first shadow",
			mutate: func(c *Catalog) {
				c.Productions[0].Phrases = [][]CatalogElement{
					{{Kind: "keyword", Value: "sync"}, {Kind: "keyword", Value: "rules"}},
				}
				c.Productions[0].Precedence = "canonical-first"
			},
			wantCode: DiagCanonicalFirstShadow,
		},
		{
			name: "implicit bypass on non-destructive",
			mutate: func(c *Catalog) {
				c.Productions[0].StaticFlags = []string{"--force"}
			},
			wantCode: DiagImplicitBypass,
		},
		{
			name: "inconsistent PlanRowSpec blocked without reason",
			mutate: func(c *Catalog) {
				c.PlanRows = append(c.PlanRows, PlanRowSpec{
					PlanRow:   99,
					Domain:    "store",
					Command:   "cloud",
					RowStatus: "blocked",
				})
			},
			wantCode: DiagCatalogSchema,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat := validMinimalCatalog()
			tc.mutate(cat)
			diags, err := ValidateCatalog(cat)
			if err == nil {
				t.Fatalf("expected validation error %s, got nil", tc.wantCode)
			}
			found := false
			for _, d := range diags {
				if d.Code == tc.wantCode {
					found = true
					if d.Path == "" || d.Remediation == "" {
						t.Fatalf("diagnostic missing Path or Remediation: %+v", d)
					}
				}
			}
			if !found {
				t.Fatalf("expected diagnostic code %s, got: %v", tc.wantCode, diags)
			}
		})
	}
}

func TestDisallowUnknownFieldsAndRowStatusOnProduction(t *testing.T) {
	rawWithRowStatus := []byte(`{
		"schema_version": "sila/ccli-catalog/v1",
		"catalog_version": "1.0.0",
		"min_sila_version": "0.17.0",
		"description": "bad",
		"productions": [
			{
				"id": "p_bad",
				"action_id": "status.show",
				"domain": "store",
				"description": "bad",
				"tier": "QUERY",
				"precedence": "canonical-first",
				"row_status": "implemented",
				"phrases": [[{"kind": "keyword", "value": "show"}, {"kind": "keyword", "value": "status"}]],
				"canonical_base": ["status"],
				"args_order": [],
				"flag_mapping": {},
				"slots": []
			}
		]
	}`)
	_, _, diags, err := LoadCatalogFromBytes(rawWithRowStatus)
	if err == nil || len(diags) == 0 {
		t.Fatal("expected error when production declares row_status")
	}
	if !strings.Contains(err.Error(), "row_status") {
		t.Fatalf("expected error to name row_status, got: %v", err)
	}

	// Verify VariantOf positive case
	cat := validMinimalCatalog()
	cat.Productions = append(cat.Productions, CatalogProduction{
		ID:                 "p_status_variant",
		ActionID:           "status.show",
		VariantOf:          "p_status",
		Domain:             "store",
		Description:        "status variant",
		Tier:               TierQuery,
		Precedence:         "canonical-first",
		TargetPolicy:       "permitted-default",
		DryRunPolicy:       "none",
		ConfirmationPolicy: "none",
		PlanRow:            1,
		Phrases: [][]CatalogElement{
			{{Kind: "keyword", Value: "display"}, {Kind: "keyword", Value: "status"}},
		},
		CanonicalBase: []string{"status"},
	})
	cat.PlanRows[0].ProductionIDs = []string{"p_status", "p_status_variant"}
	raw, _ := json.Marshal(cat)
	if _, _, diags, err := LoadCatalogFromBytes(raw); err != nil {
		t.Fatalf("expected valid variant_of production to pass, got: %v (%v)", err, diags)
	}
}

func TestConcurrentLoadCatalog(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inv, cat, err := LoadCatalog()
			if err != nil {
				t.Errorf("LoadCatalog: %v", err)
				return
			}
			_ = CatalogDigest(cat)
			_ = len(inv.Productions)
		}()
	}
	wg.Wait()
}
