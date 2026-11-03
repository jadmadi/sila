package main

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/jadmadi/sila/internal/cli"
	"github.com/jadmadi/sila/internal/render"
)

func TestHelpDefaultIsCoreAndBounded(t *testing.T) {
	var buf bytes.Buffer
	usageTo(&buf)
	out := buf.String()

	if lines := strings.Count(out, "\n"); lines > 40 {
		t.Fatalf("default help is %d lines, want <=40", lines)
	}
	for _, want := range []string{"Core loop:", "sila help all", "sila <command> --help"} {
		if !strings.Contains(out, want) {
			t.Fatalf("default help missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Agent Continuity & Working Loop:") {
		t.Fatal("default help leaked the full category inventory")
	}
	if strings.Contains(out, "\x1b") {
		t.Fatal("default help contains ANSI escapes")
	}
	for _, r := range out {
		if r > unicode.MaxASCII {
			t.Fatalf("default help contains non-ASCII rune %q", r)
		}
	}
}

func TestHelpAllIsCanonicalAndDeduplicated(t *testing.T) {
	var buf bytes.Buffer
	renderUsage(&buf, true)
	out := buf.String()

	for _, spec := range allCommandSpecs() {
		re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(spec.Name) + `(?: |\()`)
		if got := len(re.FindAllString(out, -1)); got != 1 {
			t.Fatalf("command %q appears %d times, want 1", spec.Name, got)
		}
	}
	for _, alias := range []string{"recap", "skill-create", "skills", "plugin", "cases"} {
		re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(alias) + `(?: |\()`)
		if re.MatchString(out) {
			t.Fatalf("alias %q has its own help row", alias)
		}
	}
	if got := len(regexp.MustCompile(`(?m)^  release(?: |\()`).FindAllString(out, -1)); got != 1 {
		t.Fatalf("release rows = %d, want 1", got)
	}
	if !strings.Contains(out, "standup (deprecated alias: recap)") {
		t.Fatal("standup row is missing its deprecated alias annotation")
	}
	if got := len(regexp.MustCompile(`(?m)^  -v, --verbose`).FindAllString(out, -1)); got != 1 {
		t.Fatalf("-v verbose rows = %d, want 1", got)
	}
	if got := len(regexp.MustCompile(`(?m)^  -V, --version`).FindAllString(out, -1)); got != 1 {
		t.Fatalf("-V version rows = %d, want 1", got)
	}

	var second bytes.Buffer
	renderUsage(&second, true)
	if out != second.String() {
		t.Fatal("full help is not deterministic")
	}
	for _, r := range out {
		if r > unicode.MaxASCII {
			t.Fatalf("full help contains non-ASCII rune %q", r)
		}
	}
}

func TestHelpCoreColumnsAlign(t *testing.T) {
	var buf bytes.Buffer
	usageTo(&buf)
	out := buf.String()
	start := strings.Index(out, "Core loop:")
	end := strings.Index(out, "Run 'sila help all'")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("core section not found:\n%s", out)
	}
	section := out[start:end]
	specs := specsForTier(TierCore)
	nameWidth := 0
	argsWidth := 0
	for _, spec := range specs {
		if width := len(commandNameCell(spec)); width > nameWidth {
			nameWidth = width
		}
		if len(spec.Args) <= 40 && len(spec.Args) > argsWidth {
			argsWidth = len(spec.Args)
		}
	}
	argsColumn := 2 + nameWidth + 1
	for _, spec := range specs {
		if len(spec.Args) > 40 {
			continue
		}
		found := false
		for _, line := range strings.Split(section, "\n") {
			if !strings.HasPrefix(line, "  "+commandNameCell(spec)+" ") {
				continue
			}
			found = true
			if got := strings.Index(line, spec.Args); got != argsColumn {
				t.Fatalf("%s args start at column %d, want %d: %q", spec.Name, got, argsColumn, line)
			}
			break
		}
		if !found {
			t.Fatalf("core row %q not found", spec.Name)
		}
	}
	_ = argsWidth
}

func TestSubcommandHelpIsASCII(t *testing.T) {
	for _, sub := range []string{"gate", "prime", "salam", "handoff", "onboard", "agents", "benchmark", "release", "recap", "dream", "search", "goals", "install"} {
		var buf bytes.Buffer
		subcommandUsageTo(sub, render.New(os.Stdout, true), &buf)
		out := buf.String()
		if strings.Contains(out, "\x1b") {
			t.Fatalf("%s help contains ANSI", sub)
		}
		for _, r := range out {
			if r > unicode.MaxASCII {
				t.Fatalf("%s help contains non-ASCII rune %q", sub, r)
			}
		}
	}
}

func TestHelpCommandUsesProvidedStream(t *testing.T) {
	var buf bytes.Buffer
	subcommandUsageTo("gate", render.New(os.Stdout, true), &buf)
	if !strings.Contains(buf.String(), "sila gate") {
		t.Fatalf("gate help was not written to the provided stream:\n%s", buf.String())
	}
	errOut := captureStderr(t, func() { unknownHelpTopic("frobnicate") })
	if !strings.Contains(errOut, `unknown help topic "frobnicate"`) {
		t.Fatalf("unknown help topic error missing:\n%s", errOut)
	}
	if !strings.Contains(errOut, "sila help all") {
		t.Fatalf("unknown help topic must point at help all:\n%s", errOut)
	}
}

func TestHelpExitAndStreamContract(t *testing.T) {
	if probe := os.Getenv("SILA_HELP_PROBE"); probe != "" {
		os.Args = strings.Split(probe, "\x1f")
		main()
		return
	}

	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout bool
	}{
		{"help", []string{"sila", "help"}, 0, true},
		{"help-all", []string{"sila", "help", "all"}, 0, true},
		{"help-command", []string{"sila", "help", "gate"}, 0, true},
		{"help-flag-all", []string{"sila", "--help", "--all"}, 0, true},
		{"help-unknown", []string{"sila", "help", "frobnicate"}, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestHelpExitAndStreamContract")
			cmd.Env = append(os.Environ(), "SILA_HELP_PROBE="+strings.Join(tc.args, "\x1f"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			code := 0
			if err != nil {
				exitErr, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("run failed: %v", err)
				}
				code = exitErr.ExitCode()
			}
			if code != tc.wantCode {
				t.Fatalf("exit = %d, want %d (stderr: %q)", code, tc.wantCode, stderr.String())
			}
			if tc.wantStdout && stdout.Len() == 0 {
				t.Fatalf("expected stdout, got stderr: %q", stderr.String())
			}
			if !tc.wantStdout {
				if stdout.Len() != 0 {
					t.Fatalf("unknown help wrote stdout: %q", stdout.String())
				}
				if stderr.Len() == 0 {
					t.Fatal("unknown help wrote nothing to stderr")
				}
			}
		})
	}
}

func TestRegistryParityWithDispatchAndFlags(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "dispatch.go", nil, 0)
	if err != nil {
		t.Fatalf("parse dispatch.go: %v", err)
	}

	dispatchCases := make(map[string]int)
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "dispatch" {
			return true
		}
		for _, stmt := range fn.Body.List {
			sw, ok := stmt.(*ast.SwitchStmt)
			if !ok {
				continue
			}
			id, ok := sw.Tag.(*ast.Ident)
			if !ok || id.Name != "sub" {
				continue
			}
			for _, bodyStmt := range sw.Body.List {
				cc, ok := bodyStmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range cc.List {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					val, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatalf("unquote dispatch case %s: %v", lit.Value, err)
					}
					dispatchCases[val]++
				}
			}
		}
		return false
	})

	if len(dispatchCases) == 0 {
		t.Fatal("failed to extract any dispatch cases from dispatch.go")
	}

	for name, count := range dispatchCases {
		if count != 1 {
			t.Fatalf("dispatch case %q appears %d times in dispatch.go, want 1", name, count)
		}
		if _, ok := cli.Lookup(name); !ok {
			t.Fatalf("dispatch case %q is missing from internal/cli registry", name)
		}
		if !isValidSubcommand(name) {
			t.Fatalf("isValidSubcommand(%q) = false for dispatched command", name)
		}
	}

	for _, spec := range cli.All() {
		if dispatchCases[spec.Name] != 1 {
			t.Fatalf("registry command %q is missing from dispatch.go switch", spec.Name)
		}
		for _, alias := range spec.Aliases {
			if dispatchCases[alias] != 1 {
				t.Fatalf("registry alias %q (for %q) is missing from dispatch.go switch", alias, spec.Name)
			}
		}
	}

	// Verify 1-to-1 parity between newRootFlags across subcommand contexts and cli.Flags()
	subContexts := []string{"", "changelog", "feedback", "clean", "agents", "goals", "crew", "unslop", "doctor", "mcp", "skill"}
	registeredFlags := make(map[string]bool)
	for _, sub := range subContexts {
		fs := flag.NewFlagSet("test-"+sub, flag.ContinueOnError)
		_ = newRootFlags(fs, sub)
		fs.VisitAll(func(f *flag.Flag) {
			registeredFlags[f.Name] = true
			if _, ok := cli.LookupFlag(f.Name); !ok {
				t.Fatalf("flag --%s registered in newRootFlags(sub=%q) is missing from internal/cli.Flags()", f.Name, sub)
			}
		})
	}

	// Pre-subcommand flags handled directly in main.go
	preSubFlags := map[string]bool{"help": true, "h": true, "version": true, "V": true}
	for _, fSpec := range cli.Flags() {
		if !registeredFlags[fSpec.Name] && !preSubFlags[fSpec.Name] {
			t.Fatalf("registry flag --%s is not registered in newRootFlags", fSpec.Name)
		}
		for _, alias := range fSpec.Aliases {
			if !registeredFlags[alias] && !preSubFlags[alias] {
				t.Fatalf("registry flag alias -%s (for --%s) is not registered in newRootFlags", alias, fSpec.Name)
			}
		}
	}
}
