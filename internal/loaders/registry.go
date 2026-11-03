package loaders

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// Source describes where a tool's data lives and how to build its loader.
type Source struct {
	Name    string // canonical name (also the tool label)
	DBPath  string // set for sqlite tools
	File    string // set for single-file (JSONL) sources
	DataDir string // set for directory-scanned tools
	New     func(path string) Loader
}

// AllSources returns the ingestable tool sources keyed by canonical name.
func AllSources() map[string]*Source {
	home := homeDir()
	return map[string]*Source{
		"mimocode": {
			Name: "mimocode", DBPath: filepath.Join(home, ".local", "share", "mimocode", "mimocode.db"),
			New: NewMiMoCodeLoader,
		},
		"opencode": {
			Name: "opencode", DBPath: filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
			New: NewOpenCodeLoader,
		},
		"devin": {
			Name: "devin", DBPath: filepath.Join(home, ".local", "share", "devin", "cli", "sessions.db"),
			New: NewDevinLoader,
		},
		"codex": {
			Name: "codex", File: filepath.Join(home, ".codex", "history.jsonl"),
			New: NewCodexLoader,
		},
		"command-code": {
			Name: "command-code", File: filepath.Join(home, ".commandcode", "history.jsonl"),
			New: NewCommandCodeLoader,
		},
		"codewhale": {
			Name: "codewhale", DataDir: filepath.Join(home, ".codewhale", "sessions"),
			New: NewCodewhaleLoader,
		},
		"agy": {
			Name: "agy", DataDir: defaultAgyDataDir(home),
			New: NewAgyLoader,
		},
		"zcode": {
			Name: "zcode", DBPath: filepath.Join(home, ".zcode", "cli", "db", "db.sqlite"),
			New: NewZCodeLoader,
		},
		"claude-code": {
			Name: "claude-code", DataDir: filepath.Join(home, ".claude", "projects"),
			New: NewClaudeCodeLoader,
		},
		"muse": {
			Name: "muse", DataDir: filepath.Join(home, ".local", "share", "muse", "sessions"),
			New: NewMuseLoader,
		},
		"zed": {
			Name: "zed", DBPath: filepath.Join(home, ".local", "share", "zed", "threads", "threads.db"),
			New: NewZedLoader,
		},
		"harness2": {
			Name: "harness2", DataDir: filepath.Join(home, ".local", "share", "harness2", "runs"),
			New: NewHarness2Loader,
		},
		"git": {
			Name: "git",
			New:  NewGitLoader,
		},
	}
}

func defaultAgyDataDir(home string) string {
	cli := filepath.Join(home, ".gemini", "antigravity-cli")
	if fi, err := os.Stat(cli); err == nil && fi.IsDir() {
		return cli
	}
	return filepath.Join(home, ".gemini", "antigravity")
}

var aliases = map[string]string{
	"mimo": "mimocode", "mimocode": "mimocode",
	"oc": "opencode", "opencode": "opencode",
	"devin": "devin",
	"codex": "codex",
	"cmd":   "command-code", "commandcode": "command-code", "command-code": "command-code",
	"whale": "codewhale", "codewhale": "codewhale",
	"zcode": "zcode", "zc": "zcode",
	"claude": "claude-code", "claudecode": "claude-code", "claude-code": "claude-code",
	"muse":     "muse",
	"agy":      "agy",
	"zed":      "zed",
	"harness2": "harness2", "h2": "harness2",
	"git": "git",
	"all": "all",
}

// ResolveTool maps a CLI arg to a canonical source name, or "" if unknown.
func ResolveTool(name string) string {
	return aliases[strings.ToLower(name)]
}

// ResolvePath returns the concrete source file/dir for a Source and whether it exists.
func (s *Source) ResolvePath() (string, bool) {
	if s.Name == "git" {
		if wd, err := os.Getwd(); err == nil {
			return wd, true
		}
		return ".", true
	}
	var p string
	switch {
	case s.DBPath != "":
		p = s.DBPath
	case s.File != "":
		p = s.File
	default:
		p = s.DataDir
	}
	if p == "" {
		return "", false
	}
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return "", false
	}
	return p, true
}

// BuildLoader builds the loader for a source or returns errMissingDB when absent.
func BuildLoader(s *Source) (Loader, error) {
	p, ok := s.ResolvePath()
	if !ok {
		return nil, errMissingDB
	}
	return s.New(p), nil
}

// SortedNames returns tool source names in stable order.
func SortedNames(m map[string]*Source) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
