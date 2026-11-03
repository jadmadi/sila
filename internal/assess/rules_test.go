package assess

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestDefaultHardProhibitions(t *testing.T) {
	rules := DefaultHardProhibitions()
	if len(rules) < 8 {
		t.Fatalf("expected at least 8 default hard prohibitions, got %d", len(rules))
	}

	ruleMap := make(map[string]ScarTissueRule)
	for _, r := range rules {
		ruleMap[r.ID] = r
	}

	required := []string{
		"no-github-workflows",
		"no-npm-yarn-lockfile",
		"no-npm-yarn-commands",
		"no-cgo",
		"no-physical-css",
		"no-eastern-arabic-numerals",
		"no-silent-telemetry",
		"no-hardcoded-secrets",
		"no-automation-scripts",
	}

	for _, req := range required {
		if _, ok := ruleMap[req]; !ok {
			t.Errorf("missing required default prohibition %q", req)
		}
	}
}

func TestExtractRulesFromAgentsMD(t *testing.T) {
	sampleAgentsMD := `# AGENTS.md

## 0. Hard Prohibitions (NEVER Do This)

- **Zero GitHub Workflows / CI Sprawl**: NEVER create or keep .github/workflows/ yaml files.
- **No CGO dependencies**: Pure-Go SQLite only.
- **No unapproved HTTP clients**: NEVER use ` + "`" + `github.com/go-resty/resty` + "`" + `; use standard net/http only.

## 1. Working Relationship
- Always be concise.
`
	rules := ExtractRulesFromAgentsMD(sampleAgentsMD)
	foundCustom := false
	for _, r := range rules {
		if r.ID == "agents:no-unapproved-http-clients:github-com-go-resty-resty" {
			foundCustom = true
			if !r.Regex.MatchString(`import "github.com/go-resty/resty"`) {
				t.Errorf("expected custom resty regex to match import line")
			}
		}
	}
	if !foundCustom {
		t.Errorf("expected custom prohibition rule extracted from AGENTS.md backtick tokens")
	}
}

func TestExtractRulesFromMemos(t *testing.T) {
	facts := []store.Fact{
		{
			ID:            "f_memo1",
			Claim:         "Never import github.com/mattn/go-sqlite3; use modernc.org/sqlite instead",
			Kind:          "invariant",
			SessionRef:    "manual_memo",
			Confidence:    "invariant",
			Corroboration: "grounded",
		},
		{
			ID:            "f_memo2",
			Claim:         "Banned: axios; always rely on browser native fetch API",
			Kind:          "invariant",
			SessionRef:    "manual_memo",
			Confidence:    "invariant",
			Corroboration: "grounded",
		},
		{
			ID:            "f_memo3",
			Claim:         "Do not use `sync/atomic.Value` without type assertion guards",
			Kind:          "invariant",
			SessionRef:    "manual_memo",
			Confidence:    "invariant",
			Corroboration: "grounded",
		},
		{
			ID:            "f_neutral",
			Claim:         "Sila uses SQLite FTS5 for full-text search",
			Kind:          "fact",
			Confidence:    "grounded",
			Corroboration: "grounded",
		},
	}

	rules := ExtractRulesFromMemos(facts)
	if len(rules) < 3 {
		t.Fatalf("expected at least 3 memo rules, got %d", len(rules))
	}

	var foundMattn, foundAxios, foundAtomic bool
	for _, r := range rules {
		if r.Regex.MatchString("github.com/mattn/go-sqlite3") {
			foundMattn = true
			if r.Source != "memo:f_memo1" {
				t.Errorf("expected source memo:f_memo1, got %s", r.Source)
			}
		}
		if r.Regex.MatchString("axios") {
			foundAxios = true
		}
		if r.Regex.MatchString("sync/atomic.Value") {
			foundAtomic = true
		}
		if r.Regex.MatchString("SQLite FTS5") {
			t.Errorf("neutral non-prohibitive fact was erroneously turned into a rule")
		}
	}

	if !foundMattn {
		t.Errorf("expected rule banning github.com/mattn/go-sqlite3")
	}
	if !foundAxios {
		t.Errorf("expected rule banning axios")
	}
	if !foundAtomic {
		t.Errorf("expected rule banning sync/atomic.Value")
	}
}

func TestCompileProjectRules(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	_, err = st.InsertMemo("testproj", "Never import libgit2; use os/exec git wrapper", "user", "", nil)
	if err != nil {
		t.Fatalf("InsertMemo: %v", err)
	}

	rules := CompileProjectRules(tmp, st, "testproj")
	if len(rules) == 0 {
		t.Fatalf("expected compiled rules, got 0")
	}

	foundLibgit2 := false
	for _, r := range rules {
		if r.Regex != nil && r.Regex.MatchString("libgit2") {
			foundLibgit2 = true
			break
		}
	}
	if !foundLibgit2 {
		t.Errorf("expected compiled project rules to contain libgit2 memo rule")
	}
}
