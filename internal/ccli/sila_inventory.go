package ccli

import (
	"sync"

	"github.com/jadmadi/sila/internal/cli"
)

var (
	silaRouterOnce sync.Once
	silaRouterInst *Router
)

// SilaFlagCatalog returns the flag arity catalog for Sila CLI, derived directly
// from internal/cli.Flags() so there is a single source of truth for flags.
func SilaFlagCatalog() FlagCatalog {
	regFlags := cli.Flags()
	out := make(FlagCatalog, len(regFlags)*2)
	for _, f := range regFlags {
		spec := FlagSpec{
			Arity:       f.Arity,
			Type:        f.Type,
			Description: f.Description,
		}
		out[formatFlagName(f.Name)] = spec
		for _, alias := range f.Aliases {
			out[formatFlagName(alias)] = spec
		}
	}
	// Subcommand-specific flags parsed outside root flag set (e.g., decision subcommand).
	subcommandFlags := []struct {
		name    string
		aliases []string
		arity   int
		typ     string
		desc    string
	}{
		{"option", []string{"o"}, 1, "string", "Lettered decision option"},
		{"options", nil, 1, "string", "Semicolon- or comma-separated decision options"},
		{"rationale", nil, 1, "string", "Rationale for recommended option"},
		{"address", nil, 1, "string", "Explicit decision address"},
		{"choice", nil, 1, "string", "Chosen option letter or resolution summary"},
		{"notes", nil, 1, "string", "Decision resolution notes"},
	}
	for _, sf := range subcommandFlags {
		spec := FlagSpec{Arity: sf.arity, Type: sf.typ, Description: sf.desc}
		out[formatFlagName(sf.name)] = spec
		for _, alias := range sf.aliases {
			out[formatFlagName(alias)] = spec
		}
	}
	return out
}

func formatFlagName(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// NewSilaInventory returns the production inventory for Sila's operational phrases,
// loaded and validated from the embedded catalog.json.
func NewSilaInventory() *Inventory {
	inv, _, err := LoadCatalog()
	if err != nil {
		panic("sila: invalid embedded CCLI catalog: " + err.Error())
	}
	return inv
}

// GetSilaRouter returns the lazily initialized Sila conversational router singleton.
func GetSilaRouter() *Router {
	silaRouterOnce.Do(func() {
		inv := NewSilaInventory()
		silaRouterInst = NewRouter(inv)
	})
	return silaRouterInst
}

// RewriteSilaArgs inspects command line arguments and rewrites conversational phrases
// to canonical subcommands. If the arguments do not match any conversational phrase
// or are already a canonical command, it returns the input unchanged (fail-open).
//
// Deprecated: Use RewriteArgs(args, RewriteAuto, RewriteOptions{}) instead.
func RewriteSilaArgs(rawArgs []string) []string {
	if len(rawArgs) == 0 {
		return rawArgs
	}
	res := RewriteArgs(rawArgs, RewriteAuto, RewriteOptions{FlagMode: FlagModeLenient})
	if res.Matched && res.Decision == DecisionRoute && len(res.Canonical) > 0 {
		return res.Canonical
	}
	return rawArgs
}
