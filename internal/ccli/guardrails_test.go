package ccli

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/config"
)

func TestTier1TargetPoliciesAndNoPrompt(t *testing.T) {
	inv := NewSilaInventory()

	// 1. Permitted-default Tier 1 query ("show status") succeeds with no prompt
	var out, errBuf bytes.Buffer
	streams := NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)
	res := Parse([]string{"show", "status"}, inv)
	if res.ExitCode != ExitSuccess {
		t.Fatalf("Parse(show status) failed: %v", res.ErrorMessage)
	}
	gRes := EvaluateGuardrails(res.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   streams,
		RawArgs:   []string{"show", "status"},
		Canonical: res.CanonicalArgs,
	})
	if gRes.ExitCode != ExitSuccess || gRes.Prompted || gRes.SafetyAction != "none" {
		t.Fatalf("expected Tier 1 query to succeed without prompt, got %+v", gRes)
	}

	// 2. Tier 1 with target_policy: required ("audit goal <slug>") missing target exits 2
	resMissing := Parse([]string{"audit", "goal"}, inv)
	if resMissing.ExitCode != ExitUsage {
		t.Fatalf("expected missing required Tier 1 target to exit 2, got %d (%s)", resMissing.ExitCode, resMissing.ErrorMessage)
	}

	// 3. Direct EvaluateGuardrails on Tier 1 AST with required target missing exits 2
	astReq := &AST{
		ActionID:     "goals.audit",
		Tier:         TierQuery,
		TargetPolicy: "required",
		Target:       "",
	}
	gReq := EvaluateGuardrails(astReq, GuardrailOptions{Streams: streams})
	if gReq.ExitCode != ExitUsage {
		t.Fatalf("expected EvaluateGuardrails on missing required Tier 1 target to exit 2, got %d", gReq.ExitCode)
	}

	// 4. Implicit self-scope Tier 1
	astImplicit := &AST{
		ActionID:     "doctor.run",
		Tier:         TierQuery,
		TargetPolicy: "implicit",
		TargetScope:  "current workspace",
	}
	gImp := EvaluateGuardrails(astImplicit, GuardrailOptions{Streams: streams})
	if gImp.ExitCode != ExitSuccess || gImp.Target != "current workspace" {
		t.Fatalf("expected implicit Tier 1 to resolve TargetScope, got %+v", gImp)
	}
}

func TestTier2TargetPoliciesScopeAnnouncementAndDryRunSpellings(t *testing.T) {
	inv := NewSilaInventory()

	// 1. Required target missing ("save memo") exits 2
	resMissing := Parse([]string{"save", "memo"}, inv)
	if resMissing.ExitCode != ExitUsage {
		t.Fatalf("expected Tier 2 missing required target to exit 2, got %d", resMissing.ExitCode)
	}

	// 2. Implicit self-scope Tier 2 ("refresh memory") announces scope on stderr and never prompts
	var out, errBuf bytes.Buffer
	streams := NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)
	resImplicit := Parse([]string{"refresh", "memory"}, inv)
	if resImplicit.ExitCode != ExitSuccess {
		t.Fatalf("Parse(refresh memory) failed: %s", resImplicit.ErrorMessage)
	}
	gImp := EvaluateGuardrails(resImplicit.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   streams,
		RawArgs:   []string{"refresh", "memory"},
		Canonical: resImplicit.CanonicalArgs,
	})
	if gImp.ExitCode != ExitSuccess || gImp.Prompted || gImp.SafetyAction != "preview" {
		t.Fatalf("unexpected Tier 2 implicit result: %+v", gImp)
	}
	if !strings.Contains(errBuf.String(), "Scope: ") {
		t.Errorf("expected implicit Tier 2 to announce Scope on stderr, got %q", errBuf.String())
	}

	// 3. Dry-run spellings: --dry-run, --dry-run=true, --preview, -n trigger preview; --dry-run=false does not
	spellings := []struct {
		flag       string
		wantDryRun bool
	}{
		{"--dry-run", true},
		{"--dry-run=true", true},
		{"--preview", true},
		{"-n", true},
		{"--dry-run=false", false},
	}

	for _, tc := range spellings {
		out.Reset()
		errBuf.Reset()
		raw := []string{"save", "memo", "use WAL", tc.flag}
		pRes := ParseWithOptions(raw, inv, ParseOptions{FlagMode: FlagModeLenient})
		if pRes.ExitCode != ExitSuccess {
			t.Fatalf("Parse(%v) failed: %s", raw, pRes.ErrorMessage)
		}
		gRes := EvaluateGuardrails(pRes.AST, GuardrailOptions{
			Inventory: inv,
			Streams:   streams,
			RawArgs:   raw,
			Canonical: pRes.CanonicalArgs,
		})
		if gRes.IsDryRun != tc.wantDryRun {
			t.Errorf("flag %s: IsDryRun = %v, want %v", tc.flag, gRes.IsDryRun, tc.wantDryRun)
		}
		if tc.wantDryRun {
			if gRes.ExitCode != ExitSuccess || gRes.SafetyAction != "dry-run" {
				t.Errorf("flag %s: unexpected dry-run result: %+v", tc.flag, gRes)
			}
			preview := errBuf.String()
			for _, want := range []string{
				"sila · ccli dry-run",
				`Canonical: sila memo "use WAL"`,
				"Target:    use WAL",
				"Safety:    no changes applied",
			} {
				if !strings.Contains(preview, want) {
					t.Errorf("flag %s: dry-run preview missing %q in:\n%s", tc.flag, want, preview)
				}
			}
		} else if strings.Contains(errBuf.String(), "ccli dry-run") {
			t.Errorf("flag %s: expected no dry-run preview on --dry-run=false, got:\n%s", tc.flag, errBuf.String())
		}
	}
}

func TestTier3ConfirmationMatrixAndStaticFlagsInvariants(t *testing.T) {
	inv := NewSilaInventory()

	// 1. Interactive TTY: "yes", "y", "no", EOF, and Ctrl-C (\x03)
	ttyCases := []struct {
		name      string
		input     string
		wantExit  ExitCode
		wantConf  bool
		wantError string
	}{
		{"tty yes", "yes\n", ExitSuccess, true, ""},
		{"tty y", "y\n", ExitSuccess, true, ""},
		{"tty no", "no\n", ExitPolicy, false, "Action cancelled by user"},
		{"tty EOF", "", ExitPolicy, false, "Action cancelled by user"},
		{"tty Ctrl-C", "\x03", ExitInterrupt, false, "destructive action interrupted"},
	}

	for _, tc := range ttyCases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			streams := NewStreamsWithTTY(strings.NewReader(tc.input), &out, &errBuf, true, true)
			raw := []string{"drop", "goal", "old-goal"}
			pRes := Parse(raw, inv)
			if pRes.ExitCode != ExitSuccess {
				t.Fatalf("Parse failed: %s", pRes.ErrorMessage)
			}
			gRes := EvaluateGuardrails(pRes.AST, GuardrailOptions{
				Inventory: inv,
				Streams:   streams,
				RawArgs:   raw,
				Canonical: pRes.CanonicalArgs,
			})
			if gRes.ExitCode != tc.wantExit || gRes.Confirmed != tc.wantConf {
				t.Fatalf("got (exit=%d, confirmed=%v, msg=%q), want (exit=%d, confirmed=%v)",
					gRes.ExitCode, gRes.Confirmed, gRes.Message, tc.wantExit, tc.wantConf)
			}
			if !strings.Contains(errBuf.String(), "Destructive action: sila goals release old-goal") ||
				!strings.Contains(errBuf.String(), "Target: old-goal") ||
				!strings.Contains(errBuf.String(), "Type 'yes' to continue:") {
				t.Errorf("expected destructive prompt on stderr, got:\n%s", errBuf.String())
			}
		})
	}

	// 2. Non-TTY without bypass exits 3 with exact refusal message
	var out, errBuf bytes.Buffer
	nonTTY := NewStreamsWithTTY(strings.NewReader("yes\n"), &out, &errBuf, false, false)
	pRes := Parse([]string{"drop", "goal", "old-goal"}, inv)
	gNonTTY := EvaluateGuardrails(pRes.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   nonTTY,
		RawArgs:   []string{"drop", "goal", "old-goal"},
		Canonical: pRes.CanonicalArgs,
	})
	if gNonTTY.ExitCode != ExitPolicy || gNonTTY.Message != "refusing destructive action without --yes in non-interactive mode" {
		t.Fatalf("unexpected non-TTY refusal: %+v", gNonTTY)
	}

	// 3. --no-input refuses even when StdinIsTTY and StderrIsTTY are true
	ttyStreams := NewStreamsWithTTY(strings.NewReader("yes\n"), &out, &errBuf, true, true)
	rawNoInput := []string{"drop", "goal", "old-goal", "--no-input"}
	pNoInput := ParseWithOptions(rawNoInput, inv, ParseOptions{FlagMode: FlagModeLenient})
	gNoInput := EvaluateGuardrails(pNoInput.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   ttyStreams,
		RawArgs:   rawNoInput,
		Canonical: pNoInput.CanonicalArgs,
	})
	if gNoInput.ExitCode != ExitPolicy || gNoInput.ErrorCode != ErrCodeConfirmationRequired {
		t.Fatalf("expected --no-input without bypass to exit 3 on TTY, got %+v", gNoInput)
	}

	// 4. --confirm=<target> match vs mismatch on single-target Tier 3
	rawMatch := []string{"drop", "goal", "old-goal", "--confirm=old-goal"}
	pMatch := ParseWithOptions(rawMatch, inv, ParseOptions{FlagMode: FlagModeLenient})
	gMatch := EvaluateGuardrails(pMatch.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   nonTTY,
		RawArgs:   rawMatch,
		Canonical: pMatch.CanonicalArgs,
	})
	if gMatch.ExitCode != ExitSuccess || gMatch.SafetyAction != "bypass" {
		t.Fatalf("expected matching --confirm to bypass on non-TTY, got %+v", gMatch)
	}

	rawMismatch := []string{"drop", "goal", "old-goal", "--confirm=wrong-goal"}
	pMismatch := ParseWithOptions(rawMismatch, inv, ParseOptions{FlagMode: FlagModeLenient})
	gMismatch := EvaluateGuardrails(pMismatch.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   nonTTY,
		RawArgs:   rawMismatch,
		Canonical: pMismatch.CanonicalArgs,
	})
	if gMismatch.ExitCode != ExitPolicy || gMismatch.ErrorCode != ErrCodeConfirmationRequired {
		t.Fatalf("expected mismatching --confirm to exit 3, got %+v", gMismatch)
	}

	// 5. Directional Tier 3 ("merge projects a into b") rejects --confirm with exit 2 and requires --yes/--force
	rawDirConfirm := []string{"merge", "projects", "proj-a", "into", "proj-b", "--confirm=proj-a"}
	pDirConfirm := ParseWithOptions(rawDirConfirm, inv, ParseOptions{FlagMode: FlagModeLenient})
	gDirConfirm := EvaluateGuardrails(pDirConfirm.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   ttyStreams,
		RawArgs:   rawDirConfirm,
		Canonical: pDirConfirm.CanonicalArgs,
	})
	if gDirConfirm.ExitCode != ExitUsage || !strings.Contains(gDirConfirm.Message, "do not accept --confirm") {
		t.Fatalf("expected directional --confirm to exit 2, got %+v", gDirConfirm)
	}

	// 6. Production static_flags: ["--force"] ("force vacuum database") is NOT user consent:
	// it must still prompt (or refuse on non-TTY), while keeping "--force" in canonical argv!
	rawForceVacuum := []string{"force", "vacuum", "database"}
	pForceVacuum := ParseWithOptions(rawForceVacuum, inv, ParseOptions{FlagMode: FlagModeLenient})
	if pForceVacuum.ExitCode != ExitSuccess {
		t.Fatalf("Parse(force vacuum database) failed: %s", pForceVacuum.ErrorMessage)
	}
	gForceVacuumNonTTY := EvaluateGuardrails(pForceVacuum.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   nonTTY,
		RawArgs:   rawForceVacuum,
		Canonical: pForceVacuum.CanonicalArgs,
	})
	if gForceVacuumNonTTY.ExitCode != ExitPolicy {
		t.Fatalf("expected 'force vacuum database' (static --force) to still require confirmation and exit 3 on non-TTY, got %+v", gForceVacuumNonTTY)
	}
	// When user also passes --yes, it proceeds and keeps exactly one --force in canonical argv
	rawForceVacuumYes := []string{"force", "vacuum", "database", "--yes"}
	pForceVacuumYes := ParseWithOptions(rawForceVacuumYes, inv, ParseOptions{FlagMode: FlagModeLenient})
	gForceVacuumYes := EvaluateGuardrails(pForceVacuumYes.AST, GuardrailOptions{
		Inventory: inv,
		Streams:   nonTTY,
		RawArgs:   rawForceVacuumYes,
		Canonical: pForceVacuumYes.CanonicalArgs,
	})
	if gForceVacuumYes.ExitCode != ExitSuccess {
		t.Fatalf("expected 'force vacuum database --yes' to succeed, got %+v", gForceVacuumYes)
	}
	wantCanon := []string{"vacuum", "--force"}
	if !reflect.DeepEqual(gForceVacuumYes.CanonicalArgs, wantCanon) {
		t.Fatalf("CanonicalArgs = %v, want %v (keeping static --force and stripping user --yes)", gForceVacuumYes.CanonicalArgs, wantCanon)
	}
}

func TestSecretsAndForbiddenConfigPhrasesSharedMatcher(t *testing.T) {
	inv := NewSilaInventory()

	// 1. All real secret config keys from internal/config must be detected by IsSensitiveKeyName
	// and refused with exit 3 when passed to "set config <key> to <value>"
	secretKeys := config.SecretKeys()
	if len(secretKeys) == 0 {
		t.Fatal("expected non-empty config.SecretKeys()")
	}
	for _, key := range secretKeys {
		if !IsSensitiveKeyName(key) {
			t.Errorf("IsSensitiveKeyName(%q) = false, want true", key)
		}
		if IsAllowedNonSecretConfigKey(key) {
			t.Errorf("IsAllowedNonSecretConfigKey(%q) = true, want false", key)
		}
		secretVal := "my-super-confidential-value-98765"
		raw := []string{"set", "config", key, "to", secretVal}
		pRes := Parse(raw, inv)
		if pRes.ExitCode != ExitPolicy {
			t.Errorf("Parse(set config %s) ExitCode = %d, want 3 (ExitPolicy)", key, pRes.ExitCode)
		}
		if strings.Contains(pRes.ErrorMessage, secretVal) {
			t.Fatalf("secret value leaked in Parse error message for key %s: %s", key, pRes.ErrorMessage)
		}
		if strings.Contains(pRes.ErrorMessage, "--token-env") || strings.Contains(pRes.ErrorMessage, "--token-file") || strings.Contains(pRes.ErrorMessage, "--token-stdin") {
			t.Errorf("remediation message must not invent non-existent flags: %s", pRes.ErrorMessage)
		}
	}

	// 2. All non-secret config keys are allowed by IsAllowedNonSecretConfigKey
	for _, key := range config.NonSecretKeys() {
		if !IsAllowedNonSecretConfigKey(key) {
			t.Errorf("IsAllowedNonSecretConfigKey(%q) = false, want true", key)
		}
	}

	// 3. Provider token prefixes anywhere in positional arguments exit 3 before target/confirmation and never echo
	prefixes := []string{
		"ghp_1234567890abcdef",
		"github_pat_11AA22BB",
		"sk_live_9988776655",
		"sk_test_9988776655",
		"xoxb-111-222-333",
		"xoxp-111-222-333",
		"AKIAIOSFODNN7EXAMPLE",
	}
	for _, tok := range prefixes {
		raw := []string{"save", "memo", tok}
		pRes := Parse(raw, inv)
		if pRes.ExitCode != ExitPolicy {
			t.Errorf("expected prefix token %q to exit 3, got %d", tok, pRes.ExitCode)
		}
		if strings.Contains(pRes.ErrorMessage, tok) {
			t.Fatalf("secret token %q echoed in error message: %s", tok, pRes.ErrorMessage)
		}
	}

	// 4. Slot.Sensitive == true is rejected at catalog validation (ValidateCatalog)
	_, _, err := LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	catCopy := *inv.Catalog
	catCopy.Productions = append([]CatalogProduction{}, inv.Catalog.Productions...)
	badProd := catCopy.Productions[0]
	badProd.ID = "sila_bad_sensitive_slot"
	badProd.Slots = []Slot{{Name: "api_secret", Type: SlotTypeString, Arity: SlotSingle, Required: true, Sensitive: true}}
	catCopy.Productions = append(catCopy.Productions, badProd)
	diags, _ := ValidateCatalog(&catCopy)
	foundSensitive := false
	for _, d := range diags {
		if d.Code == DiagSensitiveSlot {
			foundSensitive = true
			break
		}
	}
	if !foundSensitive {
		t.Fatalf("expected ValidateCatalog to reject Slot.Sensitive == true with %s, got %+v", DiagSensitiveSlot, diags)
	}
}

func TestExitCodeMappingAndRouterPanicRecovery(t *testing.T) {
	inv := NewSilaInventory()

	// Find a production without preserve_exit (e.g. sila_memo_save) and one with preserve_exit: 1 (e.g. sila_gate_run)
	var stdProd, preserve1Prod *Production
	for _, p := range inv.Productions {
		if p.ID == "sila_memo_save" {
			stdProd = p
		}
		if p.ID == "sila_gate_run" {
			preserve1Prod = p
		}
	}
	if stdProd == nil || preserve1Prod == nil {
		t.Fatalf("missing expected productions in catalog: std=%v preserve1=%v", stdProd, preserve1Prod)
	}
	if preserve1Prod.PreserveExit != 1 {
		t.Fatalf("expected sila_gate_run to declare PreserveExit=1, got %d", preserve1Prod.PreserveExit)
	}

	// Verify MapHostExitCode matrix
	if got := MapHostExitCode(0, stdProd); got != ExitSuccess {
		t.Errorf("MapHostExitCode(0, std) = %d, want 0", got)
	}
	if got := MapHostExitCode(1, stdProd); got != ExitRuntime {
		t.Errorf("MapHostExitCode(1, std) = %d, want 4 (ExitRuntime)", got)
	}
	if got := MapHostExitCode(1, preserve1Prod); got != ExitCode(1) {
		t.Errorf("MapHostExitCode(1, preserve_exit:1) = %d, want 1", got)
	}
	if got := MapHostExitCode(2, stdProd); got != ExitUsage {
		t.Errorf("MapHostExitCode(2, std) = %d, want 2", got)
	}
	if got := MapHostExitCode(3, stdProd); got != ExitPolicy {
		t.Errorf("MapHostExitCode(3, std) = %d, want 3", got)
	}
	if got := MapHostExitCode(4, stdProd); got != ExitRuntime {
		t.Errorf("MapHostExitCode(4, std) = %d, want 4", got)
	}
	if got := MapHostExitCode(5, stdProd); got != ExitSystem {
		t.Errorf("MapHostExitCode(5, std) = %d, want 5", got)
	}
	if got := MapHostExitCode(130, stdProd); got != ExitInterrupt {
		t.Errorf("MapHostExitCode(130, std) = %d, want 130", got)
	}

	// Router panic recovery returns ExitSystem (5)
	router := NewRouter(inv)
	router.RegisterAction(&Action{
		ID:   "memo.save",
		Tier: TierMutation,
		Handler: func(ctx context.Context, ast *AST, streams *Streams) ExitCode {
			panic("simulated handler crash")
		},
	})
	var out, errBuf bytes.Buffer
	streams := NewStreamsWithTTY(strings.NewReader(""), &out, &errBuf, false, false)
	code, _ := router.Route(context.Background(), []string{"save", "memo", "use WAL"}, streams)
	if code != ExitSystem {
		t.Fatalf("expected Router.Route to recover panic and return ExitSystem (5), got %d", code)
	}
	if !strings.Contains(errBuf.String(), "CCLI internal error: simulated handler crash") {
		t.Errorf("expected panic error message on stderr, got %q", errBuf.String())
	}
}
