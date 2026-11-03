package ccli

import (
	"errors"
	"fmt"
	"strings"
)

// ExitInterrupt is the POSIX signal exit code (128 + SIGINT) when a prompt is aborted by Ctrl-C.
// Documented as an OS signal footnote outside the normative 0/2/3/4/5 CCLI exit table.
const ExitInterrupt ExitCode = 130

// CCLI JSON error envelope codes required by R6.4.
const (
	ErrCodePolicyRefusal        = "CCLI_POLICY_REFUSAL"
	ErrCodeSecret               = "CCLI_SECRET"
	ErrCodeConfirmationRequired = "CCLI_CONFIRMATION_REQUIRED"
	ErrCodeRuntime              = "CCLI_RUNTIME"
	ErrCodeInternal             = "CCLI_INTERNAL"
)

// GuardrailOptions configures a guardrail evaluation pass.
type GuardrailOptions struct {
	Prod        *Production
	Inventory   *Inventory
	Streams     *Streams
	RawArgs     []string
	Canonical   []string
	PreviewOnly bool
	MachineMode string // "", "json", "json-envelope", "toon", "porcelain"
}

// GuardrailResult holds the structured decision of CheckGuardrails / EvaluateGuardrails.
type GuardrailResult struct {
	ExitCode      ExitCode `json:"exit_code"`
	ErrorCode     string   `json:"error_code,omitempty"`
	Message       string   `json:"message,omitempty"`
	FixHint       string   `json:"fix_hint,omitempty"`
	IsDryRun      bool     `json:"is_dry_run,omitempty"`
	SafetyAction  string   `json:"safety_action,omitempty"` // none, preview, confirm, bypass, refuse, dry-run
	Target        string   `json:"target,omitempty"`
	Scope         string   `json:"scope,omitempty"`
	Prompted      bool     `json:"prompted,omitempty"`
	Confirmed     bool     `json:"confirmed,omitempty"`
	CanonicalArgs []string `json:"canonical_args,omitempty"`
}

// CheckGuardrails enforces operational safety tiers, targets, confirmation gates, and secrets.
// Returns (exitCode, errorMessage, isDryRun).
func CheckGuardrails(ast *AST, streams *Streams, rawArgs []string) (ExitCode, string, bool) {
	res := EvaluateGuardrails(ast, GuardrailOptions{
		Streams: streams,
		RawArgs: rawArgs,
	})
	return res.ExitCode, res.Message, res.IsDryRun
}

// EvaluateGuardrails runs the full CCLI safety pipeline in order:
// secret check -> catalog safety -> target policy -> dry-run -> confirmation.
func EvaluateGuardrails(ast *AST, opts GuardrailOptions) GuardrailResult {
	if ast == nil {
		return GuardrailResult{ExitCode: ExitSuccess, SafetyAction: "none"}
	}

	prod := opts.Prod
	if prod == nil && opts.Inventory != nil {
		for _, p := range opts.Inventory.Productions {
			if p.ID == ast.ProductionID || (ast.ProductionID == "" && p.ActionID == ast.ActionID) {
				prod = p
				break
			}
		}
	}

	canon := opts.Canonical
	if len(canon) == 0 && prod != nil {
		canon = Rewrite(ast, prod)
	}
	cleanCanon := StripControlFlags(canon, prod)

	// 1. Secret & Forbidden Phrase Check (runs before target and confirmation per R5.1)
	if hasSec, secMsg := DetectSecretDetailed(ast, prod, opts.RawArgs, opts.Inventory); hasSec {
		safeMsg := redactSecretStringsInText(firstNonEmptyStr(secMsg, PositionalSecretViolationMsg), opts.RawArgs, opts.Inventory)
		return GuardrailResult{
			ExitCode:     ExitPolicy,
			ErrorCode:    ErrCodeSecret,
			Message:      safeMsg,
			SafetyAction: "refuse",
		}
	}

	// 2. Production Static Bypass Invariant (R3.5)
	if prod != nil && prod.Tier != TierDestructive {
		for _, sf := range prod.StaticFlags {
			if sf == "--yes" || sf == "-y" || sf == "--force" || sf == "-f" {
				return GuardrailResult{
					ExitCode:     ExitPolicy,
					ErrorCode:    ErrCodePolicyRefusal,
					Message:      fmt.Sprintf("%s: production %s emits %s in non-destructive tier %s", DiagImplicitBypass, prod.ID, sf, prod.Tier),
					SafetyAction: "refuse",
				}
			}
		}
	}

	userFlags := extractUserSuppliedFlags(ast, opts.RawArgs, opts.Inventory)

	// 3. Target Policy Enforcement by Tier (R2, R3.1, R4.1)
	policy := ast.TargetPolicy
	scope := ast.TargetScope
	isDir := false
	if prod != nil {
		if policy == "" {
			policy = prod.TargetPolicy
		}
		if scope == "" {
			scope = prod.TargetScope
		}
		isDir = prod.Directional
	}
	if policy == "directional" {
		isDir = true
	}

	src, dst := resolveDirectionalOperands(ast, prod)
	if !isDir && (src != "" || dst != "") && ast.Target == "" {
		isDir = true
	}

	if isDir {
		if src == "" || dst == "" {
			return GuardrailResult{
				ExitCode:     ExitUsage,
				Message:      "Missing required target: directional command requires both source and destination",
				SafetyAction: "refuse",
			}
		}
		if strings.EqualFold(src, dst) {
			return GuardrailResult{
				ExitCode:     ExitUsage,
				Message:      "Directional command requires distinct source and destination targets",
				SafetyAction: "refuse",
			}
		}
		if _, hasConfirm := userFlags["--confirm"]; hasConfirm {
			return GuardrailResult{
				ExitCode:     ExitUsage,
				Message:      "Directional destructive actions do not accept --confirm; use --yes or --force",
				SafetyAction: "refuse",
			}
		}
	} else {
		switch policy {
		case "required":
			if strings.TrimSpace(ast.Target) == "" {
				return GuardrailResult{
					ExitCode:     ExitUsage,
					Message:      "Missing required target",
					SafetyAction: "refuse",
				}
			}
		case "implicit":
			if strings.TrimSpace(scope) == "" {
				return GuardrailResult{
					ExitCode:     ExitUsage,
					Message:      "Missing target scope for implicit target policy",
					SafetyAction: "refuse",
				}
			}
			if ast.Target == "" {
				ast.Target = scope
			}
		case "permitted-default":
			if ast.Target == "" && ast.Tier == TierQuery {
				ast.Target = "default"
				if ast.Operands == nil {
					ast.Operands = make(map[string]string)
				}
				if _, exists := ast.Operands["target"]; !exists {
					ast.Operands["target"] = "default"
				}
			}
		default:
			if ast.Tier == TierQuery {
				if ast.Target == "" {
					ast.Target = "default"
					if ast.Operands == nil {
						ast.Operands = make(map[string]string)
					}
					if _, exists := ast.Operands["target"]; !exists {
						ast.Operands["target"] = "default"
					}
				}
			} else if ast.Tier == TierMutation || ast.Tier == TierDestructive {
				if strings.TrimSpace(ast.Target) == "" {
					return GuardrailResult{
						ExitCode:     ExitUsage,
						Message:      "Missing required target",
						SafetyAction: "refuse",
					}
				}
			}
		}
	}

	effTarget := ast.Target
	if isDir && effTarget == "" {
		effTarget = fmt.Sprintf("source=%s destination=%s", src, dst)
	}
	if effTarget == "" && scope != "" {
		effTarget = scope
	}
	if effTarget == "" {
		effTarget = "default"
	}

	// 4. Central Dry-Run Simulation Mode (R3.2, R4.2)
	if IsDryRunRequested(ast, opts.RawArgs, opts.Inventory) {
		if !opts.PreviewOnly && opts.Streams != nil && opts.Streams.Stderr != nil {
			fmt.Fprint(opts.Streams.Stderr, FormatDryRunPreview(cleanCanon, effTarget, scope))
		}
		return GuardrailResult{
			ExitCode:      ExitSuccess,
			IsDryRun:      true,
			SafetyAction:  "dry-run",
			Target:        effTarget,
			Scope:         scope,
			CanonicalArgs: cleanCanon,
		}
	}

	// 5. Confirmation Gate (R3.3, R3.4, R4.3-R4.7)
	needsConfirm := ast.Tier == TierDestructive ||
		(ast.Tier == TierMutation && prod != nil && prod.ConfirmationPolicy == "optional")

	if !needsConfirm {
		// Announce implicit scope for Tier 2 self-scoped mutations (R3.1)
		if policy == "implicit" && scope != "" && !opts.PreviewOnly && opts.MachineMode == "" && opts.Streams != nil && opts.Streams.Stderr != nil {
			fmt.Fprintf(opts.Streams.Stderr, "Scope: %s\n", scope)
		}
		safetyAct := "none"
		if ast.Tier == TierMutation {
			safetyAct = "preview"
		}
		return GuardrailResult{
			ExitCode:      ExitSuccess,
			SafetyAction:  safetyAct,
			Target:        effTarget,
			Scope:         scope,
			CanonicalArgs: cleanCanon,
		}
	}

	// 5a. Check --confirm=<target> (single-target only; directional was already rejected above with ExitUsage)
	if confirmVal, hasConfirm := userFlags["--confirm"]; hasConfirm {
		expectedTarget := firstNonEmptyStr(ast.Target, scope)
		if confirmVal != "" && confirmVal == expectedTarget {
			if policy == "implicit" && scope != "" && !opts.PreviewOnly && opts.MachineMode == "" && opts.Streams != nil && opts.Streams.Stderr != nil {
				fmt.Fprintf(opts.Streams.Stderr, "Scope: %s\n", scope)
			}
			return GuardrailResult{
				ExitCode:      ExitSuccess,
				SafetyAction:  "bypass",
				Target:        effTarget,
				Scope:         scope,
				Confirmed:     true,
				CanonicalArgs: cleanCanon,
			}
		}
		return GuardrailResult{
			ExitCode:     ExitPolicy,
			ErrorCode:    ErrCodeConfirmationRequired,
			Message:      fmt.Sprintf("refusing destructive action: --confirm=%q does not match target %q", confirmVal, expectedTarget),
			FixHint:      fmt.Sprintf("re-run with --confirm=%s or --yes", expectedTarget),
			SafetyAction: "refuse",
			Target:       effTarget,
			Scope:        scope,
		}
	}

	// 5b. Check user-supplied --yes / -y / --force / -f (never from RawFlags or prod.StaticFlags per R4.3)
	if hasTruthyUserFlag(userFlags, "--yes", "-y", "--force", "-f") {
		if policy == "implicit" && scope != "" && !opts.PreviewOnly && opts.MachineMode == "" && opts.Streams != nil && opts.Streams.Stderr != nil {
			fmt.Fprintf(opts.Streams.Stderr, "Scope: %s\n", scope)
		}
		return GuardrailResult{
			ExitCode:      ExitSuccess,
			SafetyAction:  "bypass",
			Target:        effTarget,
			Scope:         scope,
			Confirmed:     true,
			CanonicalArgs: cleanCanon,
		}
	}

	// 5c. Check --no-input (always exits 3 when confirmation would be required without bypass per R3.4 & R4.4)
	if hasTruthyUserFlag(userFlags, "--no-input") {
		return GuardrailResult{
			ExitCode:     ExitPolicy,
			ErrorCode:    ErrCodeConfirmationRequired,
			Message:      "refusing destructive action with --no-input and no --yes/--force bypass",
			FixHint:      "re-run with --yes, --force, or --confirm=<target>",
			SafetyAction: "refuse",
			Target:       effTarget,
			Scope:        scope,
		}
	}

	// 5d. PreviewOnly (`sila ccli explain`) reports confirm without prompting
	if opts.PreviewOnly {
		return GuardrailResult{
			ExitCode:      ExitSuccess,
			SafetyAction:  "confirm",
			Target:        effTarget,
			Scope:         scope,
			CanonicalArgs: cleanCanon,
		}
	}

	// 5e. Machine output modes never prompt (R6.3)
	if opts.MachineMode != "" || hasTruthyUserFlag(userFlags, "--json", "--json-envelope", "--toon", "--porcelain") {
		return GuardrailResult{
			ExitCode:     ExitPolicy,
			ErrorCode:    ErrCodeConfirmationRequired,
			Message:      "refusing destructive action without --yes in non-interactive mode",
			FixHint:      "re-run with --yes, --force, or --confirm=<target>",
			SafetyAction: "refuse",
			Target:       effTarget,
			Scope:        scope,
		}
	}

	// 5f. Non-interactive terminal check (R4.3, R4.7)
	if opts.Streams == nil || !opts.Streams.Interactive() {
		return GuardrailResult{
			ExitCode:     ExitPolicy,
			ErrorCode:    ErrCodeConfirmationRequired,
			Message:      "refusing destructive action without --yes in non-interactive mode",
			FixHint:      "re-run with --yes, --force, or --confirm=<target>",
			SafetyAction: "refuse",
			Target:       effTarget,
			Scope:        scope,
		}
	}

	// 5g. Interactive confirmation prompt on stderr (R4.3, R4.5, R4.6)
	prompt := FormatDestructivePrompt(cleanCanon, ast.Target, scope, src, dst, isDir, policy == "implicit")
	confirmed, err := opts.Streams.PromptConfirm(prompt)
	if errors.Is(err, ErrInterrupted) {
		return GuardrailResult{
			ExitCode:     ExitInterrupt,
			ErrorCode:    ErrCodeConfirmationRequired,
			Message:      "destructive action interrupted",
			SafetyAction: "refuse",
			Target:       effTarget,
			Scope:        scope,
			Prompted:     true,
		}
	}
	if err != nil || !confirmed {
		return GuardrailResult{
			ExitCode:     ExitPolicy,
			ErrorCode:    ErrCodeConfirmationRequired,
			Message:      "Action cancelled by user",
			SafetyAction: "refuse",
			Target:       effTarget,
			Scope:        scope,
			Prompted:     true,
		}
	}

	return GuardrailResult{
		ExitCode:      ExitSuccess,
		SafetyAction:  "confirm",
		Target:        effTarget,
		Scope:         scope,
		Prompted:      true,
		Confirmed:     true,
		CanonicalArgs: cleanCanon,
	}
}

// IsDryRunRequested reports whether user-supplied flags request a dry-run preview.
// Recognizes --dry-run, --dry-run=true, --preview, -n; --dry-run=false does NOT preview.
func IsDryRunRequested(ast *AST, rawArgs []string, inv ...*Inventory) bool {
	var invPtr *Inventory
	if len(inv) > 0 {
		invPtr = inv[0]
	}
	flags := extractUserSuppliedFlags(ast, rawArgs, invPtr)
	if v, ok := flags["--dry-run"]; ok {
		return !strings.EqualFold(v, "false") && v != "0"
	}
	for _, alias := range []string{"--preview", "-n"} {
		if v, ok := flags[alias]; ok {
			return !strings.EqualFold(v, "false") && v != "0"
		}
	}
	return false
}

// FormatDryRunPreview formats the standard stderr preview block for central dry-run.
func FormatDryRunPreview(canonical []string, target, scope string) string {
	var b strings.Builder
	b.WriteString("sila · ccli dry-run\n")
	if len(canonical) > 0 {
		b.WriteString(fmt.Sprintf("  Canonical: sila %s\n", formatArgvForPreview(canonical)))
	} else {
		b.WriteString("  Canonical: sila\n")
	}
	if scope != "" && scope != target {
		b.WriteString(fmt.Sprintf("  Target:    %s\n", target))
		b.WriteString(fmt.Sprintf("  Scope:     %s\n", scope))
	} else if scope != "" {
		b.WriteString(fmt.Sprintf("  Target:    %s\n", scope))
		b.WriteString(fmt.Sprintf("  Scope:     %s\n", scope))
	} else {
		b.WriteString(fmt.Sprintf("  Target:    %s\n", firstNonEmptyStr(target, "default")))
	}
	b.WriteString("  Safety:    no changes applied\n")
	return b.String()
}

// FormatDestructivePrompt formats the standard Tier 3 stderr confirmation prompt.
func FormatDestructivePrompt(canonical []string, target, scope, src, dst string, isDirectional, isImplicit bool) string {
	var b strings.Builder
	if len(canonical) > 0 {
		b.WriteString(fmt.Sprintf("Destructive action: sila %s\n", formatArgvForPreview(canonical)))
	} else {
		b.WriteString("Destructive action: sila\n")
	}
	if isDirectional {
		b.WriteString(fmt.Sprintf("Targets: source=%s destination=%s\n", src, dst))
	} else if isImplicit && scope != "" {
		b.WriteString(fmt.Sprintf("Scope: %s\n", scope))
	} else {
		b.WriteString(fmt.Sprintf("Target: %s\n", firstNonEmptyStr(target, scope)))
	}
	b.WriteString("Type 'yes' to continue: ")
	return b.String()
}

// StripControlFlags removes CCLI-specific control flags (--no-input, --confirm, --no-ccli, --ccli-strict-exit,
// dry-run aliases, and user-supplied --yes/-y/--force/-f when not part of prod.StaticFlags or prod.CanonicalBase)
// before canonical dispatch, while preserving production-emitted static flags (such as vacuum --force) once.
func StripControlFlags(canonical []string, prod *Production) []string {
	if len(canonical) == 0 {
		return nil
	}
	staticKeep := make(map[string]bool)
	if prod != nil {
		for _, sf := range prod.StaticFlags {
			staticKeep[sf] = true
		}
		for _, cb := range prod.CanonicalBase {
			if strings.HasPrefix(cb, "-") {
				staticKeep[cb] = true
			}
		}
	}

	out := make([]string, 0, len(canonical))
	keptStatic := make(map[string]bool)
	afterDash := false

	for i := 0; i < len(canonical); i++ {
		arg := canonical[i]
		if !afterDash && arg == "--" {
			afterDash = true
			out = append(out, arg)
			continue
		}
		if afterDash {
			out = append(out, arg)
			continue
		}

		// Always strip CCLI control flags
		if arg == "--no-input" || strings.HasPrefix(strings.ToLower(arg), "--no-input=") ||
			arg == "--no-ccli" || strings.HasPrefix(strings.ToLower(arg), "--no-ccli=") ||
			arg == "--ccli-strict-exit" || strings.HasPrefix(strings.ToLower(arg), "--ccli-strict-exit=") {
			continue
		}
		if arg == "--confirm" {
			if i+1 < len(canonical) && canonical[i+1] != "--" {
				i++
			}
			continue
		}
		if strings.HasPrefix(strings.ToLower(arg), "--confirm=") {
			continue
		}

		// Strip dry-run flags unless the production itself emits --dry-run in StaticFlags
		if arg == "--dry-run" || arg == "--preview" || arg == "-n" ||
			strings.HasPrefix(strings.ToLower(arg), "--dry-run=") ||
			strings.HasPrefix(strings.ToLower(arg), "--preview=") ||
			strings.HasPrefix(strings.ToLower(arg), "-n=") {
			if staticKeep[arg] && !keptStatic[arg] {
				keptStatic[arg] = true
				out = append(out, arg)
			}
			continue
		}

		// Strip user-supplied --yes/-y/--force/-f unless declared on the production
		if arg == "--yes" || arg == "-y" || arg == "--force" || arg == "-f" ||
			strings.HasPrefix(strings.ToLower(arg), "--yes=") ||
			strings.HasPrefix(strings.ToLower(arg), "--force=") {
			if staticKeep[arg] {
				if !keptStatic[arg] {
					keptStatic[arg] = true
					out = append(out, arg)
				}
			}
			continue
		}

		out = append(out, arg)
	}
	return out
}

// MapHostExitCode maps a canonical command's exit code to the CCLI exit-code contract
// using only catalog-declared PreserveExit metadata on the production (no ad-hoc allowlist).
func MapHostExitCode(hostExit int, prod *Production) ExitCode {
	switch hostExit {
	case 0:
		return ExitSuccess
	case 1:
		if prod != nil && prod.PreserveExit == 1 {
			return ExitCode(1)
		}
		return ExitRuntime
	case 2:
		return ExitUsage
	case 3:
		return ExitPolicy
	case 4:
		return ExitRuntime
	case 5:
		return ExitSystem
	case int(ExitInterrupt):
		return ExitInterrupt
	default:
		if hostExit < 0 {
			return ExitSystem
		}
		return ExitRuntime
	}
}

func extractUserSuppliedFlags(ast *AST, rawArgs []string, inv *Inventory) map[string]string {
	if ast != nil && ast.Flags != nil {
		return ast.Flags
	}
	if len(rawArgs) == 0 {
		return map[string]string{}
	}
	var cat FlagCatalog
	if inv != nil && inv.FlagCatalog != nil {
		cat = inv.FlagCatalog
	} else {
		cat = SilaFlagCatalog()
	}
	pass1 := ExtractFlagsWithMode(rawArgs, cat, FlagModeLenient)
	if pass1 != nil && pass1.Flags != nil {
		return pass1.Flags
	}
	return map[string]string{}
}

func hasTruthyUserFlag(flags map[string]string, names ...string) bool {
	for _, n := range names {
		if val, ok := flags[n]; ok {
			if !strings.EqualFold(val, "false") && val != "0" {
				return true
			}
		}
	}
	return false
}

func resolveDirectionalOperands(ast *AST, prod *Production) (string, string) {
	if ast == nil || ast.Operands == nil {
		return "", ""
	}
	src := ast.Operands["source"]
	dst := ast.Operands["destination"]
	if (src == "" || dst == "") && prod != nil && len(prod.ArgsOrder) >= 2 {
		if src == "" {
			src = ast.Operands[prod.ArgsOrder[0]]
		}
		if dst == "" {
			dst = ast.Operands[prod.ArgsOrder[1]]
		}
	}
	return src, dst
}

func formatArgvForPreview(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" {
			parts[i] = `""`
		} else if strings.ContainsAny(a, " \t\n\"'") {
			parts[i] = fmt.Sprintf("%q", a)
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
