package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/ccli"
	"github.com/jadmadi/sila/internal/cli"
)

const maxPhraseCompletions = 20

const canonicalCompletionCmds = "ingest assess projects overlaps dream compact search sync live watch status intro export mcp completions prime hook handoff memo doc config vacuum backup storage doctor gate update upgrade goals feedback release credits opinions persona personas audit hire reports report skill-create skills-create skill skills do ccli"

// ccliAutoFirstKeywords returns up to 20 deterministic, duplicate-free first keywords
// derived from auto-eligible CCLI productions (excluding phrase-only-via-do and canonical commands).
func ccliAutoFirstKeywords() []string {
	_, cat, err := ccli.LoadCatalog()
	if err != nil || cat == nil {
		return nil
	}
	type kwStat struct {
		kw     string
		count  int
		minRow int
	}
	stats := make(map[string]*kwStat)
	for _, p := range cat.Productions {
		if p.Precedence == "phrase-only-via-do" {
			continue
		}
		for _, ph := range p.Phrases {
			if len(ph) == 0 {
				continue
			}
			el := ph[0]
			if !strings.EqualFold(el.Kind, "keyword") && !strings.EqualFold(el.Kind, "glue") {
				continue
			}
			kw := strings.ToLower(strings.TrimSpace(el.Value))
			if kw == "" {
				continue
			}
			if _, isCanon := cli.Lookup(kw); isCanon {
				continue
			}
			st, exists := stats[kw]
			if !exists {
				st = &kwStat{kw: kw, minRow: p.PlanRow}
				stats[kw] = st
			}
			st.count++
			if p.PlanRow > 0 && (st.minRow == 0 || p.PlanRow < st.minRow) {
				st.minRow = p.PlanRow
			}
		}
	}

	cands := make([]kwStat, 0, len(stats))
	for _, st := range stats {
		cands = append(cands, *st)
	}
	for i := 1; i < len(cands); i++ {
		for j := i; j > 0 && (cands[j].count > cands[j-1].count ||
			(cands[j].count == cands[j-1].count && cands[j].minRow < cands[j-1].minRow) ||
			(cands[j].count == cands[j-1].count && cands[j].minRow == cands[j-1].minRow && cands[j].kw < cands[j-1].kw)); j-- {
			cands[j], cands[j-1] = cands[j-1], cands[j]
		}
	}

	var out []string
	seen := make(map[string]bool)
	for _, c := range cands {
		if c.count >= 2 && len(out) < maxPhraseCompletions {
			seen[c.kw] = true
			out = append(out, c.kw)
		}
	}
	for _, c := range cands {
		if len(out) >= maxPhraseCompletions {
			break
		}
		if !seen[c.kw] {
			seen[c.kw] = true
			out = append(out, c.kw)
		}
	}
	return out
}

// ccliDoFirstKeywords returns up to 20 deterministic, duplicate-free first keywords
// for `sila do <TAB>`, including phrase-only-via-do productions and top auto-eligible productions.
func ccliDoFirstKeywords() []string {
	_, cat, err := ccli.LoadCatalog()
	if err != nil || cat == nil {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	addKeyword := func(kw string) {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw == "" || seen[kw] || len(out) >= maxPhraseCompletions {
			return
		}
		seen[kw] = true
		out = append(out, kw)
	}

	// Include phrase-only-via-do first keywords (up to 10) so overlapping phrases are discoverable under `sila do`.
	for _, p := range cat.Productions {
		if len(out) >= 10 {
			break
		}
		if p.Precedence != "phrase-only-via-do" {
			continue
		}
		for _, ph := range p.Phrases {
			if len(ph) > 0 && (strings.EqualFold(ph[0].Kind, "keyword") || strings.EqualFold(ph[0].Kind, "glue")) {
				addKeyword(ph[0].Value)
			}
		}
	}

	// Fill remaining slots up to 20 with auto-eligible first keywords.
	for _, kw := range ccliAutoFirstKeywords() {
		addKeyword(kw)
	}
	return out
}

// ccliSecondKeywords returns up to 20 deterministic, duplicate-free second keywords (never slots)
// from the catalog for a given leading phrase word.
func ccliSecondKeywords(firstWord string, includeDoOnly bool) []string {
	_, cat, err := ccli.LoadCatalog()
	if err != nil || cat == nil {
		return nil
	}
	firstWord = strings.ToLower(strings.TrimSpace(firstWord))
	if firstWord == "" {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, p := range cat.Productions {
		if !includeDoOnly && p.Precedence == "phrase-only-via-do" {
			continue
		}
		for _, ph := range p.Phrases {
			if len(ph) < 2 {
				continue
			}
			k0 := strings.ToLower(strings.TrimSpace(ph[0].Kind))
			k1 := strings.ToLower(strings.TrimSpace(ph[1].Kind))
			if (k0 != "keyword" && k0 != "glue") || !strings.EqualFold(ph[0].Value, firstWord) {
				continue
			}
			if k1 != "keyword" && k1 != "glue" {
				continue
			}
			val := strings.ToLower(strings.TrimSpace(ph[1].Value))
			if val != "" && !seen[val] && len(out) < maxPhraseCompletions {
				seen[val] = true
				out = append(out, val)
			}
		}
	}
	return out
}

func runCompletions(shell string) {
	renderCompletionsTo(os.Stdout, shell)
}

func renderCompletionsTo(w io.Writer, shell string) {
	if shell == "" {
		shell = "bash"
	}
	cmds := canonicalCompletionCmds
	autoPhrases := strings.Join(ccliAutoFirstKeywords(), " ")
	doPhrases := strings.Join(ccliDoFirstKeywords(), " ")
	saveWords := strings.Join(ccliSecondKeywords("save", false), " ")
	tools := "mimocode opencode devin codex command-code codewhale agy zcode claude-code claude muse git all"
	flags := "--project= -p --global --activity --omni --window= --occurrence= --expose --replace --policy-pack= --repair --title= --crawl --depth= --max-pages= --auto-ingest --auto-sync --stats --analyze --force -f --target= --tool= -t --role= --since= --top= --open= --decisions= --branch= --files= --refs-exist --verified-only --facts-only --stale --unverified --llm= --retry-failed --toon --json --verbose -v --no-color --no-ccli --db= --out= --agent= --version -V --help -h"

	switch shell {
	case "zsh":
		fmt.Fprintf(w, "#compdef sila\n")
		fmt.Fprintf(w, `_sila() {
  local -a cmds; cmds=(%s)
  local -a auto_phrases; auto_phrases=(%s)
  local -a do_phrases; do_phrases=(%s)
  local -a save_words; save_words=(%s)
  local -a subcmds; subcmds=(reconcile query compact list search get sites web-search rules compile add session-start session-end set get explain rewrite)
  local -a tools; tools=(%s)
  if (( CURRENT == 2 )); then
    _describe 'command' cmds
    _describe 'phrase' auto_phrases
  elif [[ ${words[2]} == "do" && CURRENT == 3 ]]; then
    _describe 'phrase' do_phrases
  elif [[ ${words[2]} == "save" && CURRENT == 3 ]]; then
    _describe 'phrase' save_words
  elif [[ ${words[2]} == "do" && ${words[3]} == "save" && CURRENT == 4 ]]; then
    _describe 'phrase' save_words
  elif [[ ${words[2]} == "dream" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "opinions" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "vision" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "doc" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "hook" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "export" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "config" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "ccli" && CURRENT == 3 ]]; then
    _describe 'subcommand' subcmds
  elif [[ ${words[2]} == "ingest" || ${words[2]} == "assess" ]]; then
    _describe 'tool' tools
  fi
}
compdef _sila sila
`, cmds, autoPhrases, doPhrases, saveWords, tools)
	case "fish":
		fmt.Fprintf(w, "complete -c sila -f\n")
		for c := range strings.FieldsSeq(cmds) {
			fmt.Fprintf(w, "complete -c sila -n '__fish_use_subcommand' -a '%s'\n", c)
		}
		for p := range strings.FieldsSeq(autoPhrases) {
			fmt.Fprintf(w, "complete -c sila -n '__fish_use_subcommand' -a '%s'\n", p)
		}
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from do' -a '%s'\n", doPhrases)
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from save' -a '%s'\n", saveWords)
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from ingest assess' -a '%s'\n", tools)
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from dream' -a 'query reconcile compact'\n")
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from opinions' -a 'compile add list'\n")
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from vision' -a 'compile triage add-nongoal add-milestone list'\n")
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from doc' -a 'list search get sites web-search'\n")
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from hook' -a 'session-start session-end'\n")
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from export' -a 'rules opinions vision'\n")
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from config' -a 'set get'\n")
		fmt.Fprintf(w, "complete -c sila -n '__fish_seen_subcommand_from ccli' -a 'explain rewrite list'\n")
	default: // bash
		fmt.Fprintf(w, `_sila() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  local prev="${COMP_WORDS[COMP_CWORD-1]}"
  local cmds="%s"
  local auto_phrases="%s"
  local do_phrases="%s"
  local save_words="%s"
  local tools="%s"
  local flags="%s"
  if [[ $COMP_CWORD -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "$cmds $auto_phrases --help -V" -- "$cur") )
  elif [[ "$prev" == "dream" ]]; then
    COMPREPLY=( $(compgen -W "query reconcile compact $flags" -- "$cur") )
  elif [[ "$prev" == "opinions" ]]; then
    COMPREPLY=( $(compgen -W "compile add list $flags" -- "$cur") )
  elif [[ "$prev" == "vision" ]]; then
    COMPREPLY=( $(compgen -W "compile triage add-nongoal add-milestone list $flags" -- "$cur") )
  elif [[ "$prev" == "doc" ]]; then
    COMPREPLY=( $(compgen -W "list search get sites web-search $flags" -- "$cur") )
  elif [[ "$prev" == "hook" ]]; then
    COMPREPLY=( $(compgen -W "session-start session-end $flags" -- "$cur") )
  elif [[ "$prev" == "export" ]]; then
    COMPREPLY=( $(compgen -W "rules opinions vision $flags" -- "$cur") )
  elif [[ "$prev" == "config" ]]; then
    COMPREPLY=( $(compgen -W "set get $flags" -- "$cur") )
  elif [[ "$prev" == "ccli" ]]; then
    COMPREPLY=( $(compgen -W "explain rewrite list $flags" -- "$cur") )
  elif [[ "$prev" == "do" ]]; then
    COMPREPLY=( $(compgen -W "$do_phrases $flags" -- "$cur") )
  elif [[ "$prev" == "save" ]]; then
    COMPREPLY=( $(compgen -W "$save_words $flags" -- "$cur") )
  elif [[ "$prev" == "ingest" || "$prev" == "assess" ]]; then
    COMPREPLY=( $(compgen -W "$tools $flags" -- "$cur") )
  else
    COMPREPLY=( $(compgen -W "$tools $flags" -- "$cur") )
  fi
}
complete -F _sila sila
`, cmds, autoPhrases, doPhrases, saveWords, tools, flags)
	}
}
