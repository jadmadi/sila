package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/ccli"
	"github.com/jadmadi/sila/internal/cli"
	"github.com/jadmadi/sila/internal/render"
)

type CommandGroup struct {
	Name        string
	Description string
	Commands    [][3]string // {name, args, description}
}

// commandGroups is derived from the canonical internal/cli registry for visible tiers.
var commandGroups = buildCommandGroupsFromRegistry()

func buildCommandGroupsFromRegistry() []CommandGroup {
	var groups []CommandGroup
	idxByName := make(map[string]int)
	for _, spec := range cli.All() {
		if spec.VisibleTier == "hidden" || spec.Category == "" {
			continue
		}
		idx, exists := idxByName[spec.Category]
		if !exists {
			idx = len(groups)
			idxByName[spec.Category] = idx
			groups = append(groups, CommandGroup{Name: spec.Category})
		}
		groups[idx].Commands = append(groups[idx].Commands, [3]string{spec.Name, spec.Usage, spec.Synopsis})
	}
	return groups
}

// commandHelp lists all subcommands for gate checks and test verification.
var commandHelp = func() [][3]string {
	var all [][3]string
	for _, g := range commandGroups {
		all = append(all, g.Commands...)
	}
	return all
}()

// optionHelp lists the common and global flags shown under Common Options, derived from internal/cli.Flags().
var optionHelp = buildOptionHelpFromRegistry(false)

var coreOptionHelp = buildOptionHelpFromRegistry(true)

func buildOptionHelpFromRegistry(coreOnly bool) [][2]string {
	var out [][2]string
	for _, f := range cli.Flags() {
		if f.HelpSyntax == "" {
			continue
		}
		if coreOnly && !f.CoreOption {
			continue
		}
		out = append(out, [2]string{f.HelpSyntax, f.Description})
	}
	return out
}

// unknownCommandError prints the short, actionable unknown-command contract:
// the command, an optional nearest suggestion, and one usage hint. It never
// dumps the full help.
func unknownCommandError(input string) {
	unknownCommandErrorWithArgs(input, nil)
}

func unknownCommandErrorWithArgs(input string, rest []string) {
	fmt.Fprintf(os.Stderr, "sila: unknown command %q\n", input)
	if strings.EqualFold(input, "list") {
		fmt.Fprintln(os.Stderr, "did you mean 'sila goals list' or 'sila skill list'?")
		fmt.Fprintln(os.Stderr, "run 'sila --help' for usage.")
		return
	}
	cmdSuggestion := suggestCommand(input)
	words := strings.Fields(input)
	for _, r := range rest {
		if !strings.HasPrefix(r, "-") {
			words = append(words, strings.Fields(r)...)
		}
	}
	phraseHints := suggestClosePhraseHints(words)
	if cmdSuggestion != "" {
		fmt.Fprintf(os.Stderr, "did you mean %q?\n", cmdSuggestion)
	}
	if len(phraseHints) > 0 {
		h := phraseHints[0]
		fmt.Fprintf(os.Stderr, "did you mean 'sila do %s' or '%s'?\n", h.PhraseTemplate, h.Canonical)
	}
	if cmdSuggestion != "" || len(phraseHints) > 0 {
		fmt.Fprintln(os.Stderr, "run 'sila --help' for usage, or 'sila help phrases' for conversational phrases.")
		return
	}
	fmt.Fprintln(os.Stderr, "run 'sila --help' for usage, or 'sila help phrases' for conversational phrases.")
}

// PhraseHint pairs a close catalog phrase template with its canonical command.
type PhraseHint struct {
	KeywordPrefix  string
	PhraseTemplate string
	Canonical      string
	Distance       int
}

func suggestClosePhraseHints(words []string) []PhraseHint {
	if len(words) == 0 {
		return nil
	}
	_, cat, err := ccli.LoadCatalog()
	if err != nil || cat == nil {
		return nil
	}
	type candidate struct {
		hint PhraseHint
	}
	var cands []PhraseHint
	seen := make(map[string]bool)

	inputOne := strings.ToLower(strings.TrimSpace(words[0]))
	inputTwo := inputOne
	if len(words) >= 2 {
		inputTwo = inputOne + " " + strings.ToLower(strings.TrimSpace(words[1]))
	}
	inputThree := inputTwo
	if len(words) >= 3 {
		inputThree = inputTwo + " " + strings.ToLower(strings.TrimSpace(words[2]))
	}

	for _, p := range cat.Productions {
		canon := formatProductionCanonicalSig(p)
		for _, ph := range p.Phrases {
			kwPrefix, kwCount := extractLeadingKeywords(ph)
			if kwPrefix == "" {
				continue
			}
			tmpl := formatCatalogPhraseElements(ph)
			if seen[kwPrefix] {
				continue
			}
			var target string
			switch kwCount {
			case 1:
				target = inputOne
			case 2:
				target = inputTwo
			default:
				target = inputThree
			}
			dist := levenshtein(target, kwPrefix)
			maxAllowed := 2
			if len(kwPrefix) <= 4 {
				maxAllowed = 1
			}
			if dist <= maxAllowed {
				seen[kwPrefix] = true
				cands = append(cands, PhraseHint{
					KeywordPrefix:  kwPrefix,
					PhraseTemplate: tmpl,
					Canonical:      canon,
					Distance:       dist,
				})
			}
		}
	}

	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && (cands[j].Distance < cands[j-1].Distance ||
			(cands[j].Distance == cands[j-1].Distance && cands[j].KeywordPrefix < cands[j-1].KeywordPrefix)); j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}
	if len(cands) > 3 {
		cands = cands[:3]
	}
	return cands
}

// suggestCloseCatalogPhrases returns up to 3 deterministic closest phrase suggestions (or empty when none is close).
func suggestCloseCatalogPhrases(words []string) []string {
	hints := suggestClosePhraseHints(words)
	if len(hints) == 0 {
		return nil
	}
	out := make([]string, 0, len(hints))
	for _, h := range hints {
		out = append(out, h.KeywordPrefix)
	}
	return out
}

func extractLeadingKeywords(ph []ccli.CatalogElement) (string, int) {
	var kws []string
	for _, el := range ph {
		k := strings.ToLower(strings.TrimSpace(el.Kind))
		if k == "keyword" || k == "glue" {
			kws = append(kws, strings.ToLower(strings.TrimSpace(el.Value)))
		} else {
			break
		}
	}
	return strings.Join(kws, " "), len(kws)
}

func suggestCommand(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return ""
	}
	best := ""
	bestDistance := 3
	seen := map[string]bool{}
	for _, group := range commandGroups {
		for _, c := range group.Commands {
			name := strings.ToLower(strings.TrimSpace(c[0]))
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			if distance := levenshtein(input, name); distance < bestDistance {
				best, bestDistance = name, distance
			}
		}
	}
	if bestDistance <= 2 {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// CommandTier controls which help surface a command appears on.
type CommandTier string

const (
	TierCore     CommandTier = "core"
	TierStandard CommandTier = "standard"
	TierAdvanced CommandTier = "advanced"
)

// CommandSpec is the canonical help row. Aliases never get their own row.
type CommandSpec struct {
	Name              string
	Args              string
	Description       string
	Category          string
	Aliases           []string
	DeprecatedAliases []string
	Tier              CommandTier
}

// allCommandSpecs projects visible canonical commands from internal/cli.All().
func allCommandSpecs() []CommandSpec {
	regSpecs := cli.All()
	out := make([]CommandSpec, 0, len(regSpecs))
	for _, s := range regSpecs {
		if s.VisibleTier == "hidden" {
			continue
		}
		out = append(out, CommandSpec{
			Name:              s.Name,
			Args:              s.Usage,
			Description:       s.Synopsis,
			Category:          s.Category,
			Aliases:           s.HelpAliases,
			DeprecatedAliases: s.DeprecatedAliases,
			Tier:              CommandTier(s.VisibleTier),
		})
	}
	return out
}

func specsForTier(tier CommandTier) []CommandSpec {
	all := allCommandSpecs()
	out := make([]CommandSpec, 0, len(all))
	for _, spec := range all {
		if spec.Tier == tier {
			out = append(out, spec)
		}
	}
	return out
}

func commandNameCell(spec CommandSpec) string {
	if len(spec.DeprecatedAliases) > 0 {
		return spec.Name + " (deprecated alias: " + strings.Join(spec.DeprecatedAliases, ", ") + ")"
	}
	if len(spec.Aliases) > 0 {
		return spec.Name + " (alias: " + strings.Join(spec.Aliases, ", ") + ")"
	}
	return spec.Name
}

func groupedSpecs(specs []CommandSpec) ([]string, map[string][]CommandSpec) {
	order := make([]string, 0, 8)
	groups := make(map[string][]CommandSpec)
	for _, spec := range specs {
		if _, ok := groups[spec.Category]; !ok {
			order = append(order, spec.Category)
		}
		groups[spec.Category] = append(groups[spec.Category], spec)
	}
	return order, groups
}

func usageTo(o io.Writer) {
	renderUsage(o, false)
}

func usageAll() {
	renderUsage(os.Stdout, true)
}

func renderUsage(o io.Writer, full bool) {
	specs := specsForTier(TierCore)
	if full {
		specs = allCommandSpecs()
	}

	fmt.Fprintln(o, "sila"+" - one shared memory, rulebook, and plan for every AI coding agent.")
	fmt.Fprintln(o, "  Part of the devtools family by Jad Madi (@jadmadi on X.com)")
	fmt.Fprintln(o)
	fmt.Fprintln(o, "Usage:")
	fmt.Fprintln(o, "  sila <command> [args...] [flags]")
	fmt.Fprintln(o)

	order, groups := groupedSpecs(specs)
	if !full {
		order = []string{"Core loop"}
		groups = map[string][]CommandSpec{"Core loop": specs}
	}
	for _, category := range order {
		rows := groups[category]
		fmt.Fprintln(o, category+":")
		nameWidth := 0
		for _, spec := range rows {
			if width := len(commandNameCell(spec)); width > nameWidth {
				nameWidth = width
			}
		}
		argsWidth := 0
		for _, spec := range rows {
			if len(spec.Args) > 40 {
				continue
			}
			if len(spec.Args) > argsWidth {
				argsWidth = len(spec.Args)
			}
		}
		for _, spec := range rows {
			name := commandNameCell(spec)
			if len(spec.Args) > 40 {
				fmt.Fprintf(o, "  %-*s %s\n  %-*s %s\n", nameWidth, name, spec.Args, nameWidth, "", spec.Description)
				continue
			}
			fmt.Fprintf(o, "  %-*s %-*s  %s\n", nameWidth, name, argsWidth, spec.Args, spec.Description)
		}
		fmt.Fprintln(o)
	}

	if !full {
		fmt.Fprintln(o, "Run 'sila help all' for the full command inventory, or 'sila <command> --help' for flags.")
		fmt.Fprintln(o)
	}

	fmt.Fprintln(o, "Common Options:")
	options := optionHelp
	if !full {
		options = coreOptionHelp
	}
	for _, option := range options {
		fmt.Fprintf(o, "  %-26s %s\n", option[0], option[1])
	}
	fmt.Fprintln(o)

	if full {
		fmt.Fprintln(o, "Examples:")
		fmt.Fprintf(o, "  %-30s %s\n", "sila salam", "# Orient: working loop and repo status")
		fmt.Fprintf(o, "  %-30s %s\n", "sila prime", "# Prime: concise session-start context")
		fmt.Fprintf(o, "  %-30s %s\n", `sila recall "database locked"`, "# Recall: prior solutions before searching")
		fmt.Fprintln(o)
	}

	fmt.Fprintln(o, "Conversational phrases:")
	fmt.Fprintln(o, "  sila say hello              -> sila salam")
	fmt.Fprintln(o, "  sila remember error <text>  -> sila recall <text>")
	fmt.Fprintln(o, "  sila save memo <text>       -> sila memo <text>")
	fmt.Fprintln(o, "  Run 'sila help phrases' for all 76 phrase families, or 'sila ccli explain ...' to preview.")

	if full {
		fmt.Fprintln(o)
		fmt.Fprintln(o, "Learn More:")
		fmt.Fprintln(o, "  sila help phrases        All 76 conversational CLI phrase families")
		fmt.Fprintln(o, "  sila <command> --help    Flags and examples for one command")
		fmt.Fprintln(o, "  sila guide               Developer Operational Guidebook (10 domains)")
	}
}

func usage() {
	o := os.Stderr
	for _, a := range os.Args[1:] {
		if a == "--help" || a == "-h" || a == "help" {
			o = os.Stdout
			break
		}
	}
	usageTo(o)
}

func unknownHelpTopic(input string) {
	fmt.Fprintf(os.Stderr, "sila: unknown help topic %q\n", input)
	if suggestion := suggestCommand(input); suggestion != "" {
		fmt.Fprintf(os.Stderr, "did you mean %q?\n", suggestion)
	}
	fmt.Fprintln(os.Stderr, "run 'sila help all' for the full command inventory.")
}

// PhraseVariant describes one formatted conversational phrase variant and its routing marker.
type PhraseVariant struct {
	Text string
	Mode string // "auto", "sila do", or "forbidden"
}

// PhraseFamilyRow describes one of the 76 CCLI plan rows grouped by domain for `sila help phrases`.
type PhraseFamilyRow struct {
	PlanRow     int
	Domain      string
	Command     string
	Tier        string // "query", "mutation", or "destructive"
	Mode        string // "auto", "sila do", or "forbidden"
	Canonical   string
	Description string
	Variants    []PhraseVariant
}

func formatCatalogPhraseElements(ph []ccli.CatalogElement) string {
	parts := make([]string, 0, len(ph))
	for _, el := range ph {
		k := strings.ToLower(strings.TrimSpace(el.Kind))
		if k == "slot" || k == "target" {
			parts = append(parts, "<"+el.Slot+">")
		} else {
			parts = append(parts, el.Value)
		}
	}
	return strings.Join(parts, " ")
}

func formatProductionCanonicalSig(p ccli.CatalogProduction) string {
	parts := append([]string{"sila"}, p.CanonicalBase...)
	for _, s := range p.ArgsOrder {
		parts = append(parts, "<"+s+">")
	}
	return strings.Join(parts, " ")
}

func sanitizeASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '—' || r == '–':
			b.WriteString("--")
		case r == '·':
			b.WriteByte('-')
		case r == '→':
			b.WriteString("->")
		case r <= 127:
			b.WriteRune(r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

func buildPhraseFamilyRows() ([]string, map[string][]PhraseFamilyRow, string, int) {
	_, cat, err := ccli.LoadCatalog()
	if err != nil || cat == nil {
		return nil, nil, "1.0.0", 0
	}
	prodByID := make(map[string]ccli.CatalogProduction, len(cat.Productions))
	for _, p := range cat.Productions {
		prodByID[p.ID] = p
	}
	forbiddenByID := make(map[string]ccli.ForbiddenPhraseSpec, len(cat.ForbiddenPhrases))
	for _, fp := range cat.ForbiddenPhrases {
		forbiddenByID[fp.ID] = fp
	}

	var domainOrder []string
	byDomain := make(map[string][]PhraseFamilyRow)

	for _, pr := range cat.PlanRows {
		if _, exists := byDomain[pr.Domain]; !exists {
			domainOrder = append(domainOrder, pr.Domain)
		}
		var row PhraseFamilyRow
		row.PlanRow = pr.PlanRow
		row.Domain = pr.Domain
		row.Command = sanitizeASCII(pr.Command)

		if pr.RowStatus == "forbidden" {
			fp := forbiddenByID[pr.ForbiddenID]
			row.Tier = "mutation"
			row.Mode = "forbidden"
			row.Canonical = "sila config set <key> <val>"
			row.Description = sanitizeASCII(fp.Reason)
			for _, ph := range fp.Phrases {
				row.Variants = append(row.Variants, PhraseVariant{
					Text: formatCatalogPhraseElements(ph),
					Mode: "forbidden",
				})
			}
			byDomain[pr.Domain] = append(byDomain[pr.Domain], row)
			continue
		}

		var prods []ccli.CatalogProduction
		for _, pid := range pr.ProductionIDs {
			if p, ok := prodByID[pid]; ok {
				prods = append(prods, p)
			}
		}
		if len(prods) == 0 {
			continue
		}

		row.Tier = strings.ToLower(string(prods[0].Tier))
		allDo := true
		for _, p := range prods {
			if p.Precedence != "phrase-only-via-do" {
				allDo = false
				break
			}
		}
		if allDo {
			row.Mode = "sila do"
		} else {
			row.Mode = "auto"
		}
		row.Canonical = sanitizeASCII(formatProductionCanonicalSig(prods[0]))
		row.Description = sanitizeASCII(prods[0].Description)

		for _, p := range prods {
			vMode := "auto"
			if p.Precedence == "phrase-only-via-do" {
				vMode = "sila do"
			}
			for _, ph := range p.Phrases {
				row.Variants = append(row.Variants, PhraseVariant{
					Text: formatCatalogPhraseElements(ph),
					Mode: vMode,
				})
			}
		}
		byDomain[pr.Domain] = append(byDomain[pr.Domain], row)
	}

	return domainOrder, byDomain, cat.CatalogVersion, len(cat.PlanRows)
}

// runCCLIHelpPhrases prints the full 76-family conversational phrase inventory grouped by domain.
func runCCLIHelpPhrases(w io.Writer) {
	domainOrder, byDomain, catVer, totalFamilies := buildPhraseFamilyRows()
	fmt.Fprintf(w, "sila - help phrases -- %d families - %d domains - catalog %s\n\n",
		totalFamilies, len(domainOrder), catVer)

	for _, dom := range domainOrder {
		rows := byDomain[dom]
		fmt.Fprintf(w, "%s (%d)\n", sanitizeASCII(dom), len(rows))
		for _, r := range rows {
			fmt.Fprintf(w, "  %-24s  %-12s  %-10s  %-34s  %s\n",
				r.Command, r.Tier, r.Mode, r.Canonical, r.Description)
			parts := make([]string, 0, len(r.Variants))
			for _, v := range r.Variants {
				switch v.Mode {
				case "sila do":
					parts = append(parts, v.Text+" [sila do]")
				case "forbidden":
					parts = append(parts, v.Text+" [forbidden]")
				default:
					parts = append(parts, v.Text)
				}
			}
			writeWrappedPhraseVariants(w, parts)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, "Precedence & Opt-Out:")
	fmt.Fprintln(w, "  Canonical commands always win when the first word matches a canonical command or alias.")
	fmt.Fprintln(w, "  Use 'sila do \"<phrase>\"' for phrases marked [sila do], or 'sila ccli explain <phrase...>' to inspect.")
	fmt.Fprintln(w, "  Disable auto-rewrite with '--no-ccli', 'SILA_CCLI=off', or 'SILA_CCLI_DENYLIST=verb1,verb2'.")
}

func writeWrappedPhraseVariants(w io.Writer, parts []string) {
	if len(parts) == 0 {
		return
	}
	const prefixFirst = "                  phrases: "
	const prefixCont = "                           "
	const maxWidth = 104

	curLine := prefixFirst + parts[0]
	for i := 1; i < len(parts); i++ {
		cand := curLine + " | " + parts[i]
		if len(cand) > maxWidth {
			fmt.Fprintln(w, curLine)
			curLine = prefixCont + parts[i]
		} else {
			curLine = cand
		}
	}
	fmt.Fprintln(w, curLine)
}

// extractHelpPhraseVariants parses `sila help phrases` output and returns every emitted phrase variant.
func extractHelpPhraseVariants(helpOut string) []string {
	var out []string
	inPhrases := false
	for _, line := range strings.Split(helpOut, "\n") {
		var body string
		if strings.HasPrefix(line, "                  phrases: ") {
			body = strings.TrimPrefix(line, "                  phrases: ")
			inPhrases = true
		} else if inPhrases && strings.HasPrefix(line, "                           ") {
			body = strings.TrimPrefix(line, "                           ")
		} else {
			inPhrases = false
			continue
		}
		for _, seg := range strings.Split(body, " | ") {
			seg = strings.TrimSpace(seg)
			seg = strings.TrimSuffix(seg, " [sila do]")
			seg = strings.TrimSuffix(seg, " [forbidden]")
			if seg != "" {
				out = append(out, seg)
			}
		}
	}
	return out
}

// subcommandUsageRegistry renders canonical CommandSpec help for any registered command
// not already handled by subcommandUsageCore or subcommandUsageOps.
func subcommandUsageRegistry(sub string, ui render.UI, o io.Writer) bool {
	switch sub {
	case "ccli":
		printCCLIHelp(o)
		return true
	case "do":
		printDoHelp(o)
		return true
	}
	spec, ok := cli.Lookup(sub)
	if !ok {
		return false
	}
	fmt.Fprintf(o, "%s  -  %s\n\n", ui.Cyan(ui.Bold("sila "+spec.Name)), sanitizeASCII(spec.Synopsis))
	fmt.Fprintln(o, ui.Bold("Usage"))
	if spec.Usage != "" {
		fmt.Fprintf(o, "  sila %s %s\n", spec.Name, sanitizeASCII(spec.Usage))
	} else {
		fmt.Fprintf(o, "  sila %s [options]\n", spec.Name)
	}
	for _, sc := range spec.Subcommands {
		if len(sc.Path) > 0 {
			fmt.Fprintf(o, "  sila %s\n", sanitizeASCII(strings.Join(sc.Path, " ")))
		}
	}
	return true
}

// appendSubcommandCCLIHelp appends Conversational and Safety sections to per-command help
// when the command has at least one CCLI production in the catalog.
func appendSubcommandCCLIHelp(sub string, ui render.UI, o io.Writer) {
	_, cat, err := ccli.LoadCatalog()
	if err != nil || cat == nil {
		return
	}
	targetSpec, hasSpec := cli.Lookup(sub)
	targetCanon := strings.ToLower(strings.TrimSpace(sub))
	if hasSpec {
		targetCanon = strings.ToLower(targetSpec.Name)
	}

	var autoProds, doProds []ccli.CatalogProduction
	hasDestructive, hasMutation := false, false
	subLower := strings.ToLower(strings.TrimSpace(sub))
	for _, exactPass := range []bool{true, false} {
		for _, p := range cat.Productions {
			if len(p.CanonicalBase) == 0 {
				continue
			}
			baseCmd := strings.ToLower(p.CanonicalBase[0])
			isExact := baseCmd == subLower
			matched := isExact || baseCmd == targetCanon
			if !matched && hasSpec {
				if pSpec, ok := cli.Lookup(p.CanonicalBase[0]); ok && strings.EqualFold(pSpec.Name, targetSpec.Name) {
					matched = true
				}
			}
			if !matched || isExact != exactPass {
				continue
			}
			switch p.Tier {
			case ccli.TierDestructive:
				hasDestructive = true
			case ccli.TierMutation:
				hasMutation = true
			}
			if p.Precedence == "phrase-only-via-do" {
				doProds = append(doProds, p)
			} else {
				autoProds = append(autoProds, p)
			}
		}
	}

	if len(autoProds) == 0 && len(doProds) == 0 {
		return
	}

	type exampleItem struct {
		cmdLine string
		label   string
	}
	var examples []exampleItem
	addFromProd := func(p ccli.CatalogProduction, maxAdd int) {
		tierStr := strings.ToLower(string(p.Tier))
		for _, ph := range p.Phrases {
			if len(examples) >= 3 || maxAdd <= 0 {
				break
			}
			phText := formatCatalogPhraseElements(ph)
			if p.Precedence == "phrase-only-via-do" {
				examples = append(examples, exampleItem{
					cmdLine: fmt.Sprintf("sila do %q", phText),
					label:   fmt.Sprintf("(%s - sila do; use 'sila do %q')", tierStr, phText),
				})
			} else {
				examples = append(examples, exampleItem{
					cmdLine: "sila " + phText,
					label:   fmt.Sprintf("(%s - auto)", tierStr),
				})
			}
			maxAdd--
		}
	}

	if len(autoProds) > 0 && len(doProds) > 0 {
		for _, p := range autoProds {
			if len(examples) >= 2 {
				break
			}
			addFromProd(p, 2-len(examples))
		}
		for _, p := range doProds {
			if len(examples) >= 3 {
				break
			}
			addFromProd(p, 1)
		}
	} else if len(autoProds) > 0 {
		for _, p := range autoProds {
			if len(examples) >= 3 {
				break
			}
			addFromProd(p, 3-len(examples))
		}
	} else {
		for _, p := range doProds {
			if len(examples) >= 3 {
				break
			}
			addFromProd(p, 3-len(examples))
		}
	}

	fmt.Fprintln(o)
	fmt.Fprintln(o, ui.Bold("Conversational"))
	for _, ex := range examples {
		fmt.Fprintf(o, "  %-44s %s\n", ex.cmdLine, ex.label)
	}

	fmt.Fprintln(o)
	fmt.Fprintln(o, ui.Bold("Safety"))
	if hasDestructive {
		fmt.Fprintln(o, "  Tier: destructive (interactive confirmation or --yes/--force required).")
	} else if hasMutation {
		fmt.Fprintln(o, "  Tier: mutation (explicit target or arguments required where applicable).")
	} else {
		fmt.Fprintln(o, "  Tier: query (read-only; executes immediately).")
	}
	if len(doProds) > 0 {
		fmt.Fprintln(o, "  Precedence: canonical-first; overlapping phrases require 'sila do \"<phrase>\"'.")
	}
	fmt.Fprintln(o, "  Phrase preview: pass --dry-run to a conversational phrase for a central preview without writing.")
	fmt.Fprintln(o, "  Secrets are not accepted in positional arguments.")
}
