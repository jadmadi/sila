package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/jadmadi/sila/internal/ccli"
	"github.com/jadmadi/sila/internal/render"
	"github.com/mattn/go-isatty"
)

// CCLIHostRunner executes a rewritten canonical argv and returns its raw host exit code.
type CCLIHostRunner func(canonicalArgs []string, prod *ccli.Production, streams *ccli.Streams) int

func defaultOSStreams() *ccli.Streams {
	return streamsForWriters(os.Stdin, os.Stdout, os.Stderr)
}

func streamsForWriters(stdin io.Reader, stdout, stderr io.Writer) *ccli.Streams {
	inTTY, errTTY := false, false
	if f, ok := stdin.(*os.File); ok {
		inTTY = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}
	if f, ok := stderr.(*os.File); ok {
		errTTY = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}
	return ccli.NewStreamsWithTTY(stdin, stdout, stderr, inTTY, errTTY)
}

func defaultCCLIHostRunner(canonicalArgs []string, _ *ccli.Production, streams *ccli.Streams) int {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = os.Args[0]
	}
	cmdArgs := append([]string{"--no-ccli"}, canonicalArgs...)
	env := append(os.Environ(), "SILA_CCLI_CHILD=1")
	if strings.HasSuffix(exe, ".test") || strings.HasSuffix(exe, ".test.exe") {
		cmdArgs = []string{"-test.run=^TestCCLIChildHelper$"}
		env = append(env, "SILA_CCLI_CHILD_ARGS="+strings.Join(canonicalArgs, "\x1f"))
	}
	cmd := exec.Command(exe, cmdArgs...)
	cmd.Env = env
	if streams != nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		return int(ccli.ExitSystem)
	}
	return 0
}

func recoverCCLIChildPanic(args []string) {
	if rec := recover(); rec != nil {
		msg := fmt.Sprintf("CCLI internal error: %v", rec)
		if hasVerboseFlag(args) {
			msg = fmt.Sprintf("%s\n%s", msg, string(debug.Stack()))
		}
		emitCCLIError(os.Stdout, os.Stderr, detectCCLIMachineMode(args), ccli.ErrCodeInternal, msg, "")
		os.Exit(int(ccli.ExitSystem))
	}
}

// applyCCLIDispatch processes os.Args at startup using the structured ccli.RewriteArgs API.
func applyCCLIDispatch(osArgs []string) []string {
	if len(osArgs) < 2 || os.Getenv("SILA_CCLI_CHILD") == "1" {
		return osArgs
	}
	rest := osArgs[1:]
	firstPosIdx := -1
	hasLeadingNoCCLI, hasLeadingNoColor := false, false
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "--" {
			break
		}
		if a == "--no-ccli" || strings.EqualFold(a, "--no-ccli=true") || a == "--no-ccli=1" {
			hasLeadingNoCCLI = true
			continue
		}
		if strings.EqualFold(a, "--no-ccli=false") || a == "--no-ccli=0" {
			continue
		}
		if a == "--no-color" {
			hasLeadingNoColor = true
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			if !strings.Contains(a, "=") {
				if spec, ok := ccli.SilaFlagCatalog()[a]; ok && spec.Arity == 1 && i+1 < len(rest) && rest[i+1] != "--" {
					i++
				}
			}
			continue
		}
		firstPosIdx = i
		break
	}

	if firstPosIdx >= 0 {
		switch rest[firstPosIdx] {
		case "do":
			var doArgs []string
			for i := 0; i < firstPosIdx; i++ {
				if a := rest[i]; a != "--no-ccli" && !strings.HasPrefix(strings.ToLower(a), "--no-ccli=") {
					doArgs = append(doArgs, a)
				}
			}
			doArgs = append(doArgs, rest[firstPosIdx+1:]...)
			_, handled, code := executeCCLIDispatch(doArgs, ccli.RewriteDo, hasLeadingNoCCLI, defaultOSStreams(), defaultCCLIHostRunner)
			if handled {
				os.Exit(code)
			}
			return osArgs
		case "ccli":
			ccliArgs := append([]string{}, rest[:firstPosIdx]...)
			ccliArgs = append(ccliArgs, rest[firstPosIdx+1:]...)
			os.Exit(executeCCLICommand(ccliArgs, hasLeadingNoCCLI, hasLeadingNoColor, os.Stdout, os.Stderr))
		}
	}

	if _, handled, code := executeCCLIDispatch(rest, ccli.RewriteAuto, hasLeadingNoCCLI, defaultOSStreams(), defaultCCLIHostRunner); handled {
		os.Exit(code)
	}
	return osArgs
}

// executeCCLIDispatch runs the full CCLI rewrite + guardrail + central dry-run + control-flag strip
// + canonical host dispatch + exit-code mapping pipeline, recovering any panic at the CCLI boundary.
func executeCCLIDispatch(rawArgs []string, mode ccli.RewriteMode, leadingNoCCLI bool, streams *ccli.Streams, hostRunner CCLIHostRunner) (canonicalArgs []string, handled bool, exitCode int) {
	if streams == nil {
		streams = defaultOSStreams()
	}
	mMode := detectCCLIMachineMode(rawArgs)

	defer func() {
		if rec := recover(); rec != nil {
			msg := fmt.Sprintf("CCLI internal error: %v", rec)
			if hasVerboseFlag(rawArgs) {
				msg = fmt.Sprintf("%s\n%s", msg, string(debug.Stack()))
			}
			emitCCLIError(streams.Stdout, streams.Stderr, mMode, ccli.ErrCodeInternal, msg, "")
			handled = true
			exitCode = int(ccli.ExitSystem)
		}
	}()

	if mode == ccli.RewriteDo {
		for _, a := range rawArgs {
			if a == "--" {
				break
			}
			if a == "--help" || a == "-h" {
				printDoHelp(streams.Stdout)
				return nil, true, 0
			}
		}
		if leadingNoCCLI || hasNoCCLIInArgs(rawArgs) {
			emitCCLIError(streams.Stdout, streams.Stderr, mMode, ccli.ErrCodePolicyRefusal, "sila do: conversational CLI is disabled by --no-ccli", "")
			return nil, true, int(ccli.ExitUsage)
		}
		inv := ccli.GetSilaRouter().Inventory
		if pass1 := ccli.ExtractFlagsWithMode(rawArgs, inv.FlagCatalog, ccli.FlagModeLenient); len(pass1.Positional) == 0 {
			printDoShortUsage(streams.Stderr)
			return nil, true, int(ccli.ExitUsage)
		}
	}

	res := ccli.RewriteArgs(rawArgs, mode, ccli.RewriteOptions{
		FlagMode: ccli.FlagModeLenient, NoCCLI: leadingNoCCLI, WarnWriter: streams.Stderr,
	})
	if res.Forbidden || res.ExitCode == ccli.ExitPolicy {
		errCode := ccli.ErrCodePolicyRefusal
		if ccli.DetectSecret(res.AST, rawArgs) || strings.Contains(strings.ToLower(res.Explanation), "secret") {
			errCode = ccli.ErrCodeSecret
		}
		emitCCLIError(streams.Stdout, streams.Stderr, mMode, errCode, res.Explanation, "")
		return nil, true, int(ccli.ExitPolicy)
	}
	if mode == ccli.RewriteDo && (!res.Matched || res.Decision != ccli.DecisionRoute) {
		printPhraseError(streams.Stderr, res)
		code := int(res.ExitCode)
		if code == 0 {
			code = int(ccli.ExitUsage)
		}
		if mMode == "json" || mMode == "json-envelope" {
			_ = render.EmitJSONError(streams.Stdout, ccli.ErrCodePolicyRefusal, res.Explanation, "")
		}
		return nil, true, code
	}
	if !res.Matched || res.Decision != ccli.DecisionRoute || len(res.Canonical) == 0 {
		return nil, false, 0
	}

	matchedProd := findCatalogProduction(res.Production, res.ActionID)
	guardRes := ccli.EvaluateGuardrails(res.AST, ccli.GuardrailOptions{
		Prod: matchedProd, Inventory: ccli.GetSilaRouter().Inventory,
		Streams: streams, RawArgs: rawArgs, Canonical: res.Canonical, MachineMode: mMode,
	})
	if guardRes.ExitCode != ccli.ExitSuccess {
		emitCCLIError(streams.Stdout, streams.Stderr, mMode, guardRes.ErrorCode, guardRes.Message, guardRes.FixHint)
		return nil, true, int(guardRes.ExitCode)
	}
	if guardRes.IsDryRun {
		emitDryRunMachineOutput(streams.Stdout, mMode, guardRes.CanonicalArgs)
		return guardRes.CanonicalArgs, true, 0
	}
	if hostRunner == nil {
		return res.Canonical, false, -1
	}

	cleanCanon := ccli.StripControlFlags(res.Canonical, matchedProd)
	hostExit := hostRunner(cleanCanon, matchedProd, streams)
	return cleanCanon, true, int(ccli.MapHostExitCode(hostExit, matchedProd))
}

// handleSilaDo validates and rewrites an explicit `sila do <phrase...>` invocation.
func handleSilaDo(args []string, leadingNoCCLI bool, stdout, stderr io.Writer) ([]string, int) {
	return handleSilaDoWithStreams(args, leadingNoCCLI, streamsForWriters(os.Stdin, stdout, stderr))
}

func handleSilaDoWithStreams(args []string, leadingNoCCLI bool, streams *ccli.Streams) ([]string, int) {
	canon, handled, code := executeCCLIDispatch(args, ccli.RewriteDo, leadingNoCCLI, streams, nil)
	if handled {
		return nil, code
	}
	return canon, -1
}

func hasNoCCLIInArgs(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--no-ccli" || strings.EqualFold(a, "--no-ccli=true") || a == "--no-ccli=1" {
			return true
		}
	}
	return false
}

func hasVerboseFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--verbose" || a == "-v" || strings.EqualFold(a, "--verbose=true") {
			return true
		}
	}
	return false
}

func detectCCLIMachineMode(args []string) string {
	mode := ""
	for _, a := range args {
		if a == "--" {
			break
		}
		switch a {
		case "--json-envelope", "--json=full":
			return "json-envelope"
		case "--json":
			if mode == "" {
				mode = "json"
			}
		case "--toon":
			return "toon"
		case "--porcelain":
			return "porcelain"
		}
	}
	return mode
}

func findCatalogProduction(prodID, actionID string) *ccli.Production {
	inv := ccli.GetSilaRouter().Inventory
	if inv == nil {
		return nil
	}
	for _, p := range inv.Productions {
		if prodID != "" && p.ID == prodID {
			return p
		}
	}
	for _, p := range inv.Productions {
		if actionID != "" && p.ActionID == actionID {
			return p
		}
	}
	return nil
}

func emitCCLIError(stdout, stderr io.Writer, machineMode, errCode, message, fixHint string) {
	if stderr != nil && message != "" {
		fmt.Fprintf(stderr, "✖ %s\n", message)
	}
	if stdout != nil && (machineMode == "json" || machineMode == "json-envelope") {
		if errCode == "" {
			errCode = ccli.ErrCodePolicyRefusal
		}
		_ = render.EmitJSONError(stdout, errCode, message, fixHint)
	}
}

func emitDryRunMachineOutput(stdout io.Writer, machineMode string, canonical []string) {
	if stdout == nil || machineMode == "" {
		return
	}
	if canonical == nil {
		canonical = []string{}
	}
	switch machineMode {
	case "json":
		writeJSONTo(stdout, map[string]any{"ok": true, "dry_run": true, "canonical": canonical, "would_execute": true})
	case "json-envelope":
		_ = render.EmitJSONSuccess(stdout, map[string]any{"dry_run": true, "canonical": canonical, "would_execute": true}, nil, nil)
	case "toon":
		fmt.Fprint(stdout, render.FormatTOONObject("ccli_dry_run", [][2]string{
			{"dry_run", "true"}, {"canonical", formatShellSafeArgv(canonical)}, {"would_execute", "true"},
		}))
	case "porcelain":
		fmt.Fprintf(stdout, "dry-run\t%s\n", sanitizePorcelain(formatShellSafeArgv(canonical)))
	}
}

// executeCCLICommand implements `sila ccli explain|rewrite|list` without opening any store or mutating state.
func executeCCLICommand(args []string, leadingNoCCLI, leadingNoColor bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printCCLIShortUsage(stderr)
		return int(ccli.ExitUsage)
	}

	var sub string
	var subArgs []string
	jsonOut, jsonEnv, toonOut, porcelainOut := false, false, false, false
	noColor, noCCLI, strictMode := leadingNoColor, leadingNoCCLI, false

	for i := 0; i < len(args); i++ {
		a := args[i]
		if sub == "" {
			switch a {
			case "--help", "-h", "help":
				printCCLIHelp(stdout)
				return 0
			case "--json":
				jsonOut = true
				continue
			case "--json-envelope", "--json=full":
				jsonOut, jsonEnv = true, true
				continue
			case "--toon":
				toonOut = true
				continue
			case "--porcelain":
				porcelainOut = true
				continue
			case "--no-color":
				noColor = true
				continue
			case "--no-ccli", "--no-ccli=true", "--no-ccli=1":
				noCCLI = true
				continue
			case "--strict":
				strictMode = true
				continue
			}
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "✖ sila ccli: unknown flag %s before subcommand\n", a)
				printCCLIShortUsage(stderr)
				return int(ccli.ExitUsage)
			}
			sub, subArgs = a, args[i+1:]
			break
		}
	}

	if sub == "" {
		printCCLIShortUsage(stderr)
		return int(ccli.ExitUsage)
	}

	switch sub {
	case "explain":
		return runCCLIExplain(subArgs, noCCLI, strictMode, jsonOut, jsonEnv, toonOut, porcelainOut, noColor, stdout, stderr)
	case "rewrite":
		return runCCLIRewrite(subArgs, noCCLI, strictMode, jsonOut, jsonEnv, toonOut, porcelainOut, stdout, stderr)
	case "list", "ls":
		return runCCLIList(subArgs, jsonOut, jsonEnv, toonOut, porcelainOut, noColor, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "✖ sila ccli: unknown subcommand %q (expected explain, rewrite, or list)\n", sub)
		printCCLIShortUsage(stderr)
		return int(ccli.ExitUsage)
	}
}

func extractMachineFlagsForExplain(args []string, initNoCCLI, initStrict, initJSON, initEnv, initToon, initPorcelain, initNoColor bool) (phraseArgs []string, noCCLI, strict, jsonOut, jsonEnv, toonOut, porcelain, noColor, help bool) {
	noCCLI, strict, jsonOut, jsonEnv, toonOut, porcelain, noColor = initNoCCLI, initStrict, initJSON, initEnv, initToon, initPorcelain, initNoColor
	afterDash := false
	for _, a := range args {
		if !afterDash && a == "--" {
			afterDash = true
			phraseArgs = append(phraseArgs, a)
			continue
		}
		if !afterDash {
			switch a {
			case "--help", "-h":
				help = true
				continue
			case "--json":
				jsonOut = true
				continue
			case "--json-envelope", "--json=full":
				jsonOut, jsonEnv = true, true
				continue
			case "--toon":
				toonOut = true
				continue
			case "--porcelain":
				porcelain = true
				continue
			case "--no-color":
				noColor = true
				continue
			case "--no-ccli", "--no-ccli=true", "--no-ccli=1":
				noCCLI = true
				continue
			case "--strict":
				strict = true
				continue
			}
		}
		phraseArgs = append(phraseArgs, a)
	}
	return
}

func runCCLIExplain(rawArgs []string, initNoCCLI, initStrict, initJSON, initEnv, initToon, initPorcelain, initNoColor bool, stdout, stderr io.Writer) int {
	phraseArgs, noCCLI, strict, jsonOut, jsonEnv, toonOut, porcelain, noColor, help := extractMachineFlagsForExplain(
		rawArgs, initNoCCLI, initStrict, initJSON, initEnv, initToon, initPorcelain, initNoColor,
	)
	if help {
		fmt.Fprintln(stdout, "Usage: sila ccli explain <phrase...> [--strict] [--json|--toon]")
		fmt.Fprintln(stdout, "Example: sila ccli explain save memo \"use WAL\"")
		return 0
	}
	if err := validateOutputModes(jsonOut && !jsonEnv, jsonEnv, toonOut, porcelain); err != nil {
		fmt.Fprintf(stderr, "sila: %v\n", err)
		return int(ccli.ExitUsage)
	}
	if len(phraseArgs) == 0 {
		fmt.Fprintln(stderr, "✖ sila ccli explain: phrase is required")
		fmt.Fprintln(stderr, "  usage: sila ccli explain <phrase...>")
		fmt.Fprintln(stderr, "  example: sila ccli explain save memo \"use WAL\"")
		return int(ccli.ExitUsage)
	}

	flagMode := ccli.FlagModeLenient
	if strict {
		flagMode = ccli.FlagModeStrict
	}

	res := ccli.RewriteArgs(phraseArgs, ccli.RewriteExplain, ccli.RewriteOptions{
		FlagMode: flagMode, NoCCLI: noCCLI, WarnWriter: stderr,
	})

	if !res.Forbidden && (res.Decision == ccli.DecisionNoMatch || res.Decision == ccli.DecisionClarify) {
		printPhraseError(stderr, res)
		if jsonEnv {
			fix := "run 'sila help phrases' or 'sila ccli list' to see supported conversational phrases"
			if len(res.Suggestions) > 0 {
				fix = fmt.Sprintf("try: sila do %q", res.Suggestions[0])
			}
			_ = render.EmitJSONError(stdout, "CCLI_UNKNOWN_PHRASE", res.Explanation, fix)
		}
		if res.ExitCode == 0 {
			return int(ccli.ExitUsage)
		}
		return int(res.ExitCode)
	}

	var canonicalVal any
	if res.Canonical != nil {
		canonicalVal = res.Canonical
	}

	payloadData := map[string]any{
		"input": res.Input, "phrase": res.Phrase, "matched": res.Matched,
		"production": res.Production, "action": res.ActionID, "tier": res.Tier,
		"target_policy": res.TargetReq, "canonical": canonicalVal, "precedence": res.Precedence,
		"decided_by": res.DecidedBy, "decision": res.Decision, "safety": res.Safety,
		"exit_code": int(res.ExitCode), "tokens": res.Tokens, "explanation": res.Explanation,
	}

	if jsonEnv {
		writeJSONTo(stdout, render.NewSuccessEnvelope(payloadData, res.Warnings, nil))
		return 0
	}
	if jsonOut {
		topPayload := map[string]any{
			"ok": !res.Forbidden, "data": payloadData,
			"input": res.Input, "phrase": res.Phrase, "matched": res.Matched,
			"production": res.Production, "action": res.ActionID, "tier": res.Tier,
			"target_policy": res.TargetReq, "canonical": canonicalVal, "precedence": res.Precedence,
			"decided_by": res.DecidedBy, "decision": res.Decision, "safety": res.Safety,
			"exit_code": int(res.ExitCode), "tokens": res.Tokens, "explanation": res.Explanation,
		}
		writeJSONTo(stdout, topPayload)
		return 0
	}
	if toonOut {
		fmt.Fprint(stdout, render.FormatTOONObject("ccli_explain", [][2]string{
			{"phrase", res.Phrase}, {"matched", strconv.FormatBool(res.Matched)},
			{"production", res.Production}, {"action", res.ActionID}, {"tier", res.Tier},
			{"canonical", formatCanonicalForExplain(res.Canonical)}, {"precedence", res.Precedence},
			{"decided_by", res.DecidedBy}, {"decision", res.Decision}, {"safety", res.Safety},
			{"exit_code", strconv.Itoa(int(res.ExitCode))}, {"explanation", res.Explanation},
		}))
		return 0
	}
	if porcelain {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%d\n",
			sanitizePorcelain(res.Decision), sanitizePorcelain(res.Production),
			sanitizePorcelain(res.ActionID), sanitizePorcelain(res.Tier),
			sanitizePorcelain(formatCanonicalForExplain(res.Canonical)), int(res.ExitCode),
		)
		return 0
	}

	ui := uiForWriter(stdout, noColor)
	fmt.Fprintf(stdout, "%s — %s\n", ui.Bold("sila · ccli explain"), res.Phrase)
	fmt.Fprintf(stdout, "  Tokens:      %s\n", formatExplainTokens(res.Tokens))
	fmt.Fprintf(stdout, "  Production:  %s\n", nonEmptyOrNone(res.Production))
	fmt.Fprintf(stdout, "  Action:      %s\n", nonEmptyOrNone(res.ActionID))
	fmt.Fprintf(stdout, "  Tier:        %s\n", formatExplainTier(res.Tier, res.TargetReq))
	fmt.Fprintf(stdout, "  Canonical:   %s\n", formatCanonicalHuman(res.Canonical, res.Forbidden))
	fmt.Fprintf(stdout, "  Precedence:  %s\n", formatExplainPrecedence(res.Precedence, res.Explanation))
	fmt.Fprintf(stdout, "  Safety:      %s\n", formatExplainSafety(res.Safety))
	fmt.Fprintf(stdout, "  Exit Code:   %d\n", int(res.ExitCode))
	fmt.Fprintf(stdout, "  Decision:    %s\n", res.Decision)
	return 0
}

func runCCLIRewrite(rawArgs []string, initNoCCLI, initStrict, initJSON, initEnv, initToon, initPorcelain bool, stdout, stderr io.Writer) int {
	phraseArgs, noCCLI, strict, jsonOut, jsonEnv, toonOut, porcelain, _, help := extractMachineFlagsForExplain(
		rawArgs, initNoCCLI, initStrict, initJSON, initEnv, initToon, initPorcelain, true,
	)
	if help {
		fmt.Fprintln(stdout, "Usage: sila ccli rewrite <phrase...>")
		fmt.Fprintln(stdout, "Example: sila ccli rewrite save memo \"use WAL\"")
		return 0
	}
	if err := validateOutputModes(jsonOut && !jsonEnv, jsonEnv, toonOut, porcelain); err != nil {
		fmt.Fprintf(stderr, "sila: %v\n", err)
		return int(ccli.ExitUsage)
	}
	if len(phraseArgs) == 0 {
		fmt.Fprintln(stderr, "✖ sila ccli rewrite: phrase is required")
		fmt.Fprintln(stderr, "  usage: sila ccli rewrite <phrase...>")
		return int(ccli.ExitUsage)
	}

	flagMode := ccli.FlagModeLenient
	if strict {
		flagMode = ccli.FlagModeStrict
	}

	res := ccli.RewriteArgs(phraseArgs, ccli.RewriteDo, ccli.RewriteOptions{
		FlagMode: flagMode, NoCCLI: noCCLI, WarnWriter: stderr,
	})

	if res.Forbidden || res.ExitCode == ccli.ExitPolicy {
		fmt.Fprintf(stderr, "✖ %s\n", res.Explanation)
		return int(ccli.ExitPolicy)
	}
	if !res.Matched || res.Canonical == nil {
		printPhraseError(stderr, res)
		if res.ExitCode == 0 {
			return int(ccli.ExitUsage)
		}
		return int(res.ExitCode)
	}

	if jsonEnv {
		_ = render.EmitJSONSuccess(stdout, map[string]any{"canonical": res.Canonical, "production": res.Production, "action": res.ActionID}, res.Warnings, nil)
		return 0
	}
	if jsonOut {
		writeJSONTo(stdout, map[string]any{"ok": true, "canonical": res.Canonical, "production": res.Production, "action": res.ActionID})
		return 0
	}
	if toonOut {
		fmt.Fprint(stdout, render.FormatTOONObject("ccli_rewrite", [][2]string{
			{"canonical", formatShellSafeArgv(res.Canonical)}, {"production", res.Production}, {"action", res.ActionID},
		}))
		return 0
	}

	fmt.Fprintln(stdout, formatShellSafeArgv(res.Canonical))
	return 0
}

func runCCLIList(args []string, jsonOut, jsonEnv, toonOut, porcelain, noColor bool, stdout, stderr io.Writer) int {
	domainFilter, tierFilter := "", ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--help" || a == "-h":
			fmt.Fprintln(stdout, "Usage: sila ccli list [--domain=<domain>] [--tier=<tier>] [--json|--toon]")
			return 0
		case a == "--json":
			jsonOut = true
		case a == "--json-envelope" || a == "--json=full":
			jsonOut, jsonEnv = true, true
		case a == "--toon":
			toonOut = true
		case a == "--porcelain":
			porcelain = true
		case a == "--no-color":
			noColor = true
		case strings.HasPrefix(a, "--domain="):
			domainFilter = strings.TrimSpace(strings.TrimPrefix(a, "--domain="))
		case a == "--domain" && i+1 < len(args):
			i++
			domainFilter = strings.TrimSpace(args[i])
		case strings.HasPrefix(a, "--tier="):
			tierFilter = strings.TrimSpace(strings.TrimPrefix(a, "--tier="))
		case a == "--tier" && i+1 < len(args):
			i++
			tierFilter = strings.TrimSpace(args[i])
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "✖ sila ccli list: unknown flag %s\n", a)
				return int(ccli.ExitUsage)
			}
		}
	}

	if err := validateOutputModes(jsonOut && !jsonEnv, jsonEnv, toonOut, porcelain); err != nil {
		fmt.Fprintf(stderr, "sila: %v\n", err)
		return int(ccli.ExitUsage)
	}

	normTier := normalizeTierFilter(tierFilter)
	inv := ccli.GetSilaRouter().Inventory

	type listItem struct {
		ID             string   `json:"id"`
		ActionID       string   `json:"action_id"`
		Domain         string   `json:"domain"`
		Tier           string   `json:"tier"`
		TargetPolicy   string   `json:"target_policy"`
		Precedence     string   `json:"precedence"`
		CanonicalBase  []string `json:"canonical_base"`
		Conversational string   `json:"conversational"`
		Canonical      string   `json:"canonical"`
	}

	items := make([]listItem, 0, len(inv.Productions))
	toonRows := make([][]string, 0, len(inv.Productions))

	for _, p := range inv.Productions {
		dom := resolveProductionDomain(p, inv.Catalog)
		if domainFilter != "" && !matchesDomainFilter(dom, domainFilter) {
			continue
		}
		if normTier != "" && !strings.EqualFold(string(p.Tier), normTier) {
			continue
		}
		item := listItem{
			ID: p.ID, ActionID: p.ActionID, Domain: dom, Tier: string(p.Tier),
			TargetPolicy: p.TargetPolicy, Precedence: p.Precedence, CanonicalBase: p.CanonicalBase,
			Conversational: p.Examples.Conversational, Canonical: p.Examples.Canonical,
		}
		items = append(items, item)
		toonRows = append(toonRows, []string{item.ID, item.Domain, item.Tier, item.Precedence, item.Conversational, item.Canonical})
	}

	if jsonEnv {
		_ = render.EmitJSONSuccess(stdout, items, nil, nil)
		return 0
	}
	if jsonOut {
		writeJSONTo(stdout, items)
		return 0
	}
	if toonOut {
		fmt.Fprint(stdout, render.FormatTOONTable("ccli_productions", []string{"id", "domain", "tier", "precedence", "conversational", "canonical"}, toonRows))
		return 0
	}
	if porcelain {
		for _, it := range items {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\t%s\n",
				sanitizePorcelain(it.ID), sanitizePorcelain(it.Domain), sanitizePorcelain(it.Tier),
				sanitizePorcelain(it.Precedence), sanitizePorcelain(it.Conversational), sanitizePorcelain(it.Canonical),
			)
		}
		return 0
	}

	ui := uiForWriter(stdout, noColor)
	fmt.Fprintf(stdout, "%s (%d productions)\n", ui.Bold("sila · ccli list"), len(items))
	for _, it := range items {
		fmt.Fprintf(stdout, "  %-34s  %-8s  %-11s  %-18s  %s -> %s\n",
			it.ID, it.Domain, it.Tier, it.Precedence, it.Conversational, it.Canonical)
	}
	return 0
}

func resolveProductionDomain(p *ccli.Production, cat *ccli.Catalog) string {
	if p == nil {
		return ""
	}
	if p.Domain != "" {
		return p.Domain
	}
	if cat != nil && p.PlanRow > 0 {
		for _, r := range cat.PlanRows {
			if r.PlanRow == p.PlanRow {
				return r.Domain
			}
		}
	}
	return ""
}

func matchesDomainFilter(domain, filter string) bool {
	d, f := strings.ToLower(strings.TrimSpace(domain)), strings.ToLower(strings.TrimSpace(filter))
	return d == f || strings.HasPrefix(d, f+" ") || strings.HasPrefix(d, f+"&") || strings.Contains(d, f)
}

func normalizeTierFilter(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "1", "QUERY", "TIER1", "TIER-1":
		return string(ccli.TierQuery)
	case "2", "MUTATION", "TIER2", "TIER-2":
		return string(ccli.TierMutation)
	case "3", "DESTRUCTIVE", "TIER3", "TIER-3":
		return string(ccli.TierDestructive)
	default:
		return strings.ToUpper(strings.TrimSpace(raw))
	}
}

func formatExplainTokens(tokens []ccli.Token) string {
	if len(tokens) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t.SlotName != nil && *t.SlotName != "" {
			parts = append(parts, fmt.Sprintf("%s(%s:%s)", t.Raw, t.Class, *t.SlotName))
		} else {
			parts = append(parts, fmt.Sprintf("%s(%s)", t.Raw, t.Class))
		}
	}
	return strings.Join(parts, " ")
}

func formatExplainTier(tier, targetReq string) string {
	if tier == "" {
		return "none"
	}
	if targetReq == "required" {
		return fmt.Sprintf("%s (explicit target)", tier)
	}
	if targetReq != "" {
		return fmt.Sprintf("%s (%s)", tier, targetReq)
	}
	return tier
}

func formatCanonicalHuman(canon []string, forbidden bool) string {
	if forbidden || canon == nil {
		return "null"
	}
	if len(canon) == 0 {
		return "none"
	}
	return "sila " + formatShellSafeArgv(canon)
}

func formatCanonicalForExplain(canon []string) string {
	if canon == nil {
		return "null"
	}
	return formatShellSafeArgv(canon)
}

func formatExplainPrecedence(prec, expl string) string {
	if prec == "" && expl == "" {
		return "none"
	}
	if prec != "" && expl != "" && !strings.HasPrefix(expl, prec) {
		return fmt.Sprintf("%s (%s)", prec, expl)
	}
	if expl != "" {
		return expl
	}
	return prec
}

func formatExplainSafety(safety string) string {
	switch safety {
	case "preview":
		return "dry-run preview available; no confirmation"
	case "confirm":
		return "confirmation required (--yes/--force to bypass); dry-run preview available"
	case "bypass":
		return "confirmation bypassed by explicit flag (--yes/--force/--confirm)"
	case "dry-run":
		return "central dry-run preview active (no execution)"
	case "refuse":
		return "policy refusal (secret, confirmation, or forbidden phrase)"
	default:
		return "none (read-only query)"
	}
}

func nonEmptyOrNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func formatShellSafeArgv(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n\"'\\") {
			escaped := strings.ReplaceAll(strings.ReplaceAll(a, `\`, `\\`), `"`, `\"`)
			parts[i] = `"` + escaped + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

func uiForWriter(w io.Writer, noColor bool) render.UI {
	if f, ok := w.(*os.File); ok {
		return render.New(f, noColor)
	}
	return render.New(os.Stdout, true)
}

func writeJSONTo(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(render.NormalizeSlices(v)); err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
	}
}

func printPhraseError(stderr io.Writer, res ccli.RewriteResult) {
	lower := strings.ToLower(res.Explanation)
	if res.Decision == ccli.DecisionClarify || strings.Contains(lower, "ambiguous") {
		fmt.Fprintf(stderr, "✖ ambiguous phrase: %s\n", res.Explanation)
		fmt.Fprintln(stderr, "  run 'sila help phrases' or 'sila ccli list' to disambiguate.")
		return
	}
	if strings.HasPrefix(lower, "unknown flag") || strings.Contains(lower, "requires an argument") ||
		strings.Contains(lower, "malformed flag") || strings.Contains(lower, "combined short flags") ||
		strings.HasPrefix(lower, "missing required") || strings.HasPrefix(lower, "directional ") ||
		strings.HasPrefix(lower, "slot ") {
		fmt.Fprintf(stderr, "✖ %s\n", res.Explanation)
		return
	}
	fmt.Fprintf(stderr, "✖ unknown conversational phrase: %q\n", res.Phrase)
	if closePhrases := suggestCloseCatalogPhrases(strings.Fields(res.Phrase)); len(closePhrases) > 0 {
		fmt.Fprintf(stderr, "  did you mean: %q (try: sila do %q)\n", closePhrases[0], closePhrases[0])
	} else if len(res.Suggestions) > 0 {
		fmt.Fprintf(stderr, "  did you mean: sila do %q\n", res.Suggestions[0])
	}
	fmt.Fprintln(stderr, "  run 'sila help phrases' or 'sila ccli list' for supported conversational phrases.")
}

func printDoShortUsage(w io.Writer) {
	fmt.Fprintln(w, "✖ sila do: conversational phrase is required")
	fmt.Fprintln(w, "  usage:   sila do <phrase...> [flags]")
	fmt.Fprintln(w, "  example: sila do save memo \"use WAL\"")
}

func printDoHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: sila do <phrase...> [flags] [-- <positional>...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Execute an explicit conversational CLI phrase, including phrases whose first word")
	fmt.Fprintln(w, "overlaps a canonical command.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  sila do save memo \"use WAL and busy_timeout\"")
	fmt.Fprintln(w, "  sila do \"sync rules\" --project=my-repo")
	fmt.Fprintln(w, "  sila do \"release goal legacy-backlog\" --yes")
}

func printCCLIShortUsage(w io.Writer) {
	fmt.Fprintln(w, "✖ sila ccli: subcommand required (explain, rewrite, or list)")
	fmt.Fprintln(w, "  usage:   sila ccli <explain|rewrite|list> [args...]")
	fmt.Fprintln(w, "  example: sila ccli explain save memo \"use WAL\"")
}

func printCCLIHelp(w io.Writer) {
	fmt.Fprintln(w, "Usage: sila ccli <subcommand> [args...]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  explain <phrase...>              Inspect tokens, matched production, tier, safety, and decision without executing")
	fmt.Fprintln(w, "  rewrite <phrase...>              Print only the canonical argv for a phrase without executing")
	fmt.Fprintln(w, "  list [--domain=...] [--tier=...] List catalogued conversational CLI productions")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  sila ccli explain save memo \"use WAL\"")
	fmt.Fprintln(w, "  sila ccli rewrite refresh context --top=5")
	fmt.Fprintln(w, "  sila ccli list --domain=Memory --tier=QUERY")
}
