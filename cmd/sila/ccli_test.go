package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMainUsesStructuredRewriteArgsAndNotDeprecatedRewriteSilaArgs(t *testing.T) {
	mainSrc, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if strings.Contains(string(mainSrc), "RewriteSilaArgs") {
		t.Fatalf("main.go must not call deprecated ccli.RewriteSilaArgs")
	}
	if !strings.Contains(string(mainSrc), "applyCCLIDispatch") {
		t.Fatalf("main.go must dispatch via applyCCLIDispatch")
	}

	ccliSrc, err := os.ReadFile("ccli.go")
	if err != nil {
		t.Fatalf("read ccli.go: %v", err)
	}
	if !strings.Contains(string(ccliSrc), "ccli.RewriteArgs") {
		t.Fatalf("ccli.go must use structured ccli.RewriteArgs API")
	}
}

func TestCCLIExplainModesAndDecisions(t *testing.T) {
	t.Setenv("SILA_CCLI", "on")
	t.Setenv("SILA_CCLI_DENYLIST", "")

	// 1. Human output
	var out, errBuf bytes.Buffer
	code := executeCCLICommand([]string{"explain", "save", "memo", "use WAL"}, false, true, &out, &errBuf)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, errBuf.String())
	}
	human := out.String()
	for _, want := range []string{
		"sila · ccli explain — save memo use WAL",
		"save(KEYWORD) memo(KEYWORD) use WAL(TARGET:directive)",
		"Production:  sila_memo_save",
		"Action:      memo.save",
		"Tier:        MUTATION (explicit target)",
		`Canonical:   sila memo "use WAL"`,
		"Safety:      dry-run preview available; no confirmation",
		"Decision:    ROUTE",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("human explain missing %q in:\n%s", want, human)
		}
	}

	// 2. JSON output
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "--json", "save", "memo", "use WAL"}, false, true, &out, &errBuf)
	if code != 0 {
		t.Fatalf("expected JSON explain exit 0, got %d", code)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("unmarshal explain JSON: %v\nraw: %s", err, out.String())
	}
	data, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data object in explain JSON: %v", parsed)
	}
	if data["production"] != "sila_memo_save" || data["decision"] != "ROUTE" || data["tier"] != "MUTATION" || data["safety"] != "preview" {
		t.Errorf("unexpected explain JSON data: %v", data)
	}

	// 3. JSON Envelope output
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "--json-envelope", "save", "memo", "use WAL"}, false, true, &out, &errBuf)
	if code != 0 {
		t.Fatalf("expected json-envelope explain exit 0, got %d", code)
	}
	var envParsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &envParsed); err != nil {
		t.Fatalf("unmarshal explain envelope: %v", err)
	}
	if envParsed["schema"] != "sila/json/v1" || envParsed["ok"] != true {
		t.Errorf("unexpected envelope header: %v", envParsed)
	}

	// 4. TOON output
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "--toon", "save", "memo", "use WAL"}, false, true, &out, &errBuf)
	if code != 0 || !strings.Contains(out.String(), "ccli_explain") || !strings.Contains(out.String(), "sila_memo_save") {
		t.Errorf("unexpected TOON explain output (exit %d): %s", code, out.String())
	}

	// 5. CANONICAL decision in explain (exit 0, empty stderr)
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "sync", "rules"}, false, true, &out, &errBuf)
	if code != 0 || errBuf.Len() != 0 || !strings.Contains(out.String(), "Decision:    CANONICAL") {
		t.Errorf("expected CANONICAL decision with exit 0 and empty stderr, got code=%d stderr=%q out=%s", code, errBuf.String(), out.String())
	}

	// 6. DISABLED decision via --no-ccli in explain (exit 0, empty stderr)
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "--no-ccli", "say", "hello"}, false, true, &out, &errBuf)
	if code != 0 || errBuf.Len() != 0 || !strings.Contains(out.String(), "Decision:    DISABLED") {
		t.Errorf("expected DISABLED decision with exit 0 and empty stderr, got code=%d stderr=%q out=%s", code, errBuf.String(), out.String())
	}

	// 7. DENYLISTED decision in explain (exit 0, empty stderr)
	t.Setenv("SILA_CCLI_DENYLIST", "say")
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "say", "hello"}, false, true, &out, &errBuf)
	if code != 0 || errBuf.Len() != 0 || !strings.Contains(out.String(), "Decision:    DENYLISTED") {
		t.Errorf("expected DENYLISTED decision with exit 0 and empty stderr, got code=%d stderr=%q out=%s", code, errBuf.String(), out.String())
	}
	t.Setenv("SILA_CCLI_DENYLIST", "")

	// 8. Unknown phrase in explain (exit 2, empty stdout, suggestion + sila help phrases on stderr)
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "frobnicate", "quantum", "flux"}, false, true, &out, &errBuf)
	if code != 2 || out.Len() != 0 {
		t.Errorf("expected exit 2 and empty stdout on unknown phrase, got code=%d stdout=%q", code, out.String())
	}
	if !strings.Contains(errBuf.String(), "unknown conversational phrase") || !strings.Contains(errBuf.String(), "sila help phrases") || !strings.Contains(errBuf.String(), "did you mean:") {
		t.Errorf("expected suggestion and sila help phrases in stderr, got:\n%s", errBuf.String())
	}

	// 9. Unknown flag in strict explain (exit 2, empty stdout, flag error on stderr)
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"explain", "--strict", "show", "status", "--bogus-flag"}, false, true, &out, &errBuf)
	if code != 2 || out.Len() != 0 || !strings.Contains(errBuf.String(), "Unknown flag --bogus-flag") {
		t.Errorf("expected strict unknown flag exit 2, got code=%d stdout=%q stderr=%q", code, out.String(), errBuf.String())
	}
}

func TestCCLIRewriteAndList(t *testing.T) {
	t.Setenv("SILA_CCLI", "on")
	t.Setenv("SILA_CCLI_DENYLIST", "")

	// 1. Rewrite human output
	var out, errBuf bytes.Buffer
	code := executeCCLICommand([]string{"rewrite", "save", "memo", "use WAL"}, false, true, &out, &errBuf)
	if code != 0 || strings.TrimSpace(out.String()) != `memo "use WAL"` {
		t.Errorf("expected `memo \"use WAL\"`, got code=%d out=%q", code, out.String())
	}

	// 2. List with domain and tier filters
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand([]string{"list", "--domain=Memory", "--tier=QUERY", "--json"}, false, true, &out, &errBuf)
	if code != 0 {
		t.Fatalf("expected ccli list exit 0, got %d: %s", code, errBuf.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("unmarshal ccli list JSON: %v", err)
	}
	if len(items) == 0 {
		t.Fatalf("expected non-empty filtered list for Memory + QUERY")
	}
	for _, it := range items {
		dom, _ := it["domain"].(string)
		if !strings.Contains(strings.ToLower(dom), "memory") || it["tier"] != "QUERY" {
			t.Errorf("unexpected item in filtered list: %v", it)
		}
	}

	// 3. `sila ccli` with no subcommand exits 2
	out.Reset()
	errBuf.Reset()
	code = executeCCLICommand(nil, false, true, &out, &errBuf)
	if code != 2 || out.Len() != 0 {
		t.Errorf("expected bare `sila ccli` to exit 2 with empty stdout, got code=%d", code)
	}
}

func TestCCLIRedactionAndForbiddenRewriteRefusal(t *testing.T) {
	secret := "ghp_SUPERSECRETTOKEN9999"

	// 1. explain redacts in human, JSON, and TOON and sets canonical: null
	for _, modeFlag := range []string{"", "--json", "--json-envelope", "--toon"} {
		var out, errBuf bytes.Buffer
		args := []string{"explain"}
		if modeFlag != "" {
			args = append(args, modeFlag)
		}
		args = append(args, "set", "config", "github_token", "to", secret)
		code := executeCCLICommand(args, false, true, &out, &errBuf)
		if code != 0 {
			t.Errorf("explain (%s) on forbidden phrase: expected exit 0, got %d", modeFlag, code)
		}
		combined := out.String() + errBuf.String()
		if strings.Contains(combined, secret) {
			t.Fatalf("secret leaked in explain (%s) output:\n%s", modeFlag, combined)
		}
		if !strings.Contains(out.String(), "<redacted:24 chars>") {
			t.Errorf("expected <redacted:24 chars> in explain (%s) stdout, got:\n%s", modeFlag, out.String())
		}
		if modeFlag == "--json" || modeFlag == "--json-envelope" {
			if !strings.Contains(out.String(), `"canonical": null`) {
				t.Errorf("expected \"canonical\": null in explain (%s) JSON, got:\n%s", modeFlag, out.String())
			}
		}
	}

	// 2. rewrite refuses forbidden/secret phrase with exit 3 and prints no argv on stdout
	var out, errBuf bytes.Buffer
	code := executeCCLICommand([]string{"rewrite", "set", "config", "github_token", "to", secret}, false, true, &out, &errBuf)
	if code != 3 {
		t.Fatalf("expected rewrite on forbidden/secret phrase to exit 3, got %d", code)
	}
	if out.Len() != 0 {
		t.Errorf("expected empty stdout on forbidden rewrite refusal, got: %q", out.String())
	}
	if strings.Contains(errBuf.String(), secret) {
		t.Fatalf("secret leaked in rewrite stderr: %s", errBuf.String())
	}
}

func TestCCLISideEffectFreeSentinels(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "should-not-exist.db")
	t.Setenv("SILA_DB", dbPath)

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var out, errBuf bytes.Buffer
	if code := executeCCLICommand([]string{"explain", "save", "memo", "use WAL"}, false, true, &out, &errBuf); code != 0 {
		t.Fatalf("explain failed: %d", code)
	}
	if code := executeCCLICommand([]string{"rewrite", "save", "memo", "use WAL"}, false, true, &out, &errBuf); code != 0 {
		t.Fatalf("rewrite failed: %d", code)
	}
	if code := executeCCLICommand([]string{"list"}, false, true, &out, &errBuf); code != 0 {
		t.Fatalf("list failed: %d", code)
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("readdir tmp: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected explain/rewrite/list to create zero files in temp dir, found %d entries", len(entries))
	}
}

func TestSilaDoAndCompletions(t *testing.T) {
	t.Setenv("SILA_CCLI", "on")
	t.Setenv("SILA_CCLI_DENYLIST", "")

	// 1. `sila do --help` prints usage and 3 examples
	var out, errBuf bytes.Buffer
	rewritten, code := handleSilaDo([]string{"--help"}, false, &out, &errBuf)
	if code != 0 || rewritten != nil {
		t.Fatalf("expected do --help to return exit 0, got %d", code)
	}
	if strings.Count(out.String(), "sila do ") < 4 {
		t.Errorf("expected usage + 3 examples in `sila do --help`, got:\n%s", out.String())
	}

	// 2. `sila do` with no phrase exits 2
	out.Reset()
	errBuf.Reset()
	_, code = handleSilaDo(nil, false, &out, &errBuf)
	if code != 2 || out.Len() != 0 || !strings.Contains(errBuf.String(), "example:") {
		t.Errorf("expected bare `sila do` to exit 2 with usage+example on stderr, got code=%d stderr=%q", code, errBuf.String())
	}

	// 3. `sila do` routes overlapping and quoted phrases
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"save", "memo", "use WAL"}, []string{"memo", "use WAL"}},
		{[]string{"save memo", "use WAL"}, []string{"memo", "use WAL"}},
		{[]string{"sync rules", "--project=repo"}, []string{"sync", "--project=repo"}},
		{[]string{"release goal legacy-backlog", "--yes"}, []string{"goals", "release", "legacy-backlog", "--yes"}},
	} {
		got, exitCode := handleSilaDo(tc.args, false, &out, &errBuf)
		if exitCode != -1 || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("handleSilaDo(%v) = (%v, %d), want (%v, -1)", tc.args, got, exitCode, tc.want)
		}
	}

	// 4. `sila do` blocked only by --no-ccli, not by SILA_CCLI=off or SILA_CCLI_DENYLIST
	t.Setenv("SILA_CCLI", "off")
	t.Setenv("SILA_CCLI_DENYLIST", "sync")
	got, exitCode := handleSilaDo([]string{"sync", "rules"}, false, &out, &errBuf)
	if exitCode != -1 || !reflect.DeepEqual(got, []string{"sync"}) {
		t.Errorf("expected `sila do` to bypass SILA_CCLI=off and denylist, got (%v, %d)", got, exitCode)
	}
	_, exitCode = handleSilaDo([]string{"--no-ccli", "sync", "rules"}, false, &out, &errBuf)
	if exitCode != 2 {
		t.Errorf("expected `sila do --no-ccli` to be blocked with exit 2, got %d", exitCode)
	}
}
