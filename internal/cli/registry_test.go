package cli

import (
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRegistryAllNoDuplicatesAndDeterministic(t *testing.T) {
	first := All()
	second := All()
	if len(first) == 0 {
		t.Fatal("All() returned empty registry")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("All() order is not deterministic across calls")
	}

	seen := make(map[string]string)
	for _, spec := range first {
		if spec.Name == "" {
			t.Fatal("CommandSpec with empty Name")
		}
		if prev, ok := seen[spec.Name]; ok {
			t.Fatalf("duplicate command name %q (previously registered by %q)", spec.Name, prev)
		}
		seen[spec.Name] = spec.Name
		for _, alias := range spec.Aliases {
			if prev, ok := seen[alias]; ok {
				t.Fatalf("duplicate alias %q on %q (previously registered by %q)", alias, spec.Name, prev)
			}
			seen[alias] = spec.Name
			lookedUp, ok := Lookup(alias)
			if !ok || lookedUp.Name != spec.Name {
				t.Fatalf("Lookup(%q) = (%q, %v), want (%q, true)", alias, lookedUp.Name, ok, spec.Name)
			}
		}
	}

	// Reserved CCLI commands must exist in registry
	for _, want := range []string{"do", "ccli"} {
		if _, ok := Lookup(want); !ok {
			t.Fatalf("missing reserved command %q in registry", want)
		}
	}
}

func TestRegistryFlagsNoDuplicatesAndReservedControls(t *testing.T) {
	flags := Flags()
	if len(flags) == 0 {
		t.Fatal("Flags() returned empty list")
	}

	seen := make(map[string]string)
	for _, f := range flags {
		if f.Name == "" {
			t.Fatal("FlagSpec with empty Name")
		}
		if prev, ok := seen[f.Name]; ok {
			t.Fatalf("duplicate flag %q (previously on %q)", f.Name, prev)
		}
		seen[f.Name] = f.Name
		for _, alias := range f.Aliases {
			if prev, ok := seen[alias]; ok {
				t.Fatalf("duplicate flag alias %q on %q (previously on %q)", alias, f.Name, prev)
			}
			seen[alias] = f.Name
			lookedUp, ok := LookupFlag("--" + alias)
			if !ok || lookedUp.Name != f.Name {
				t.Fatalf("LookupFlag(%q) = (%q, %v), want (%q, true)", alias, lookedUp.Name, ok, f.Name)
			}
		}
	}

	// Reserved CCLI control/machine flags per R2.7
	for _, req := range []string{
		"json", "json-envelope", "toon", "porcelain", "quiet",
		"no-ccli", "ccli-strict-exit", "no-input", "confirm",
		"dry-run", "preview", "n", "yes", "y", "force", "f",
	} {
		if _, ok := LookupFlag(req); !ok {
			t.Fatalf("missing required flag/alias %q in registry", req)
		}
	}
}

func TestResolveTableAndCanonicalWitnesses(t *testing.T) {
	cases := []struct {
		name              string
		argv              []string
		wantOK            bool
		wantCmd           string
		wantAlias         string
		wantSub           []string
		wantUnknownSub    string
		wantPositionals   int
		wantKnownPath     bool
		wantCanonicalWins bool
	}{
		{
			name:              "status zero args",
			argv:              []string{"status"},
			wantOK:            true,
			wantCmd:           "status",
			wantPositionals:   0,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "goals with flag",
			argv:              []string{"goals", "--json"},
			wantOK:            true,
			wantCmd:           "goals",
			wantPositionals:   0,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "sync rules positional",
			argv:              []string{"sync", "rules"},
			wantOK:            true,
			wantCmd:           "sync",
			wantPositionals:   1,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "release goal x unknown subcommand",
			argv:              []string{"release", "goal", "x"},
			wantOK:            true,
			wantCmd:           "release",
			wantUnknownSub:    "goal",
			wantPositionals:   2,
			wantKnownPath:     false,
			wantCanonicalWins: false,
		},
		{
			name:              "release preflight valid subcommand",
			argv:              []string{"release", "preflight"},
			wantOK:            true,
			wantCmd:           "release",
			wantSub:           []string{"release", "preflight"},
			wantPositionals:   0,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "search for x",
			argv:              []string{"search", "for", "x"},
			wantOK:            true,
			wantCmd:           "search",
			wantPositionals:   2,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "test coverage",
			argv:              []string{"test", "coverage"},
			wantOK:            true,
			wantCmd:           "test",
			wantPositionals:   1,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "verify store",
			argv:              []string{"verify", "store"},
			wantOK:            true,
			wantCmd:           "verify",
			wantPositionals:   1,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "prime context",
			argv:              []string{"prime", "context"},
			wantOK:            true,
			wantCmd:           "prime",
			wantPositionals:   1,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "watch live alias",
			argv:              []string{"watch", "live"},
			wantOK:            true,
			wantCmd:           "live",
			wantAlias:         "watch",
			wantPositionals:   1,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
		{
			name:              "unknown command",
			argv:              []string{"definitely-not"},
			wantOK:            false,
			wantCanonicalWins: false,
		},
		{
			name:              "no abbreviation guessing",
			argv:              []string{"stat"},
			wantOK:            false,
			wantCanonicalWins: false,
		},
		{
			name:              "double dash before command is invalid",
			argv:              []string{"--", "status"},
			wantOK:            false,
			wantCanonicalWins: false,
		},
		{
			name:              "double dash after command stops flag parsing",
			argv:              []string{"search", "--", "--omni"},
			wantOK:            true,
			wantCmd:           "search",
			wantPositionals:   1,
			wantKnownPath:     true,
			wantCanonicalWins: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, ok := Resolve(tc.argv)
			if ok != tc.wantOK {
				t.Fatalf("Resolve(%v) ok = %v, want %v", tc.argv, ok, tc.wantOK)
			}
			if got := CanonicalAccepts(tc.argv); got != tc.wantCanonicalWins {
				t.Fatalf("CanonicalAccepts(%v) = %v, want %v", tc.argv, got, tc.wantCanonicalWins)
			}
			if !tc.wantOK {
				return
			}
			if res.Command.Name != tc.wantCmd {
				t.Fatalf("Command.Name = %q, want %q", res.Command.Name, tc.wantCmd)
			}
			if res.AliasUsed != tc.wantAlias {
				t.Fatalf("AliasUsed = %q, want %q", res.AliasUsed, tc.wantAlias)
			}
			if !reflect.DeepEqual(res.Subcommand, tc.wantSub) {
				t.Fatalf("Subcommand = %v, want %v", res.Subcommand, tc.wantSub)
			}
			if res.UnknownSubcommand != tc.wantUnknownSub {
				t.Fatalf("UnknownSubcommand = %q, want %q", res.UnknownSubcommand, tc.wantUnknownSub)
			}
			if res.Positionals != tc.wantPositionals {
				t.Fatalf("Positionals = %d, want %d", res.Positionals, tc.wantPositionals)
			}
			if res.KnownPath != tc.wantKnownPath {
				t.Fatalf("KnownPath = %v, want %v", res.KnownPath, tc.wantKnownPath)
			}
		})
	}
}

func TestRegistryConcurrentReadSafety(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = All()
			_ = Flags()
			_, _ = Lookup("goals")
			_, _ = Lookup("recap")
			_, _ = LookupFlag("--json")
			_, _ = Resolve([]string{"release", "preflight", "--json"})
			_ = CanonicalAccepts([]string{"sync", "rules"})
		}()
	}
	wg.Wait()
}

func TestRegistryNoImportCycleOrForbiddenDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps .: %v\n%s", err, out)
	}
	deps := string(out)
	for _, forbidden := range []string{
		"github.com/jadmadi/sila/internal/ccli",
		"github.com/jadmadi/sila/cmd/sila",
	} {
		if strings.Contains(deps, forbidden) {
			t.Fatalf("internal/cli must not import %q, got deps:\n%s", forbidden, deps)
		}
	}
}
