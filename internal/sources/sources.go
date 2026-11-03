// Package sources inspects the local host OS environment to discover
// installed AI coding tools, detect their session/log paths, report indexed
// session counts, and produce actionable setup guidance. It is read-only:
// nothing here ever writes to another tool's data.
package sources

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// Status classifies a tool's presence relative to sila's ingestion pipeline.
type Status string

// Status constants classify a tool source presence and ingestion state.
const (
	StatusIndexed  Status = "indexed"  // sila ingests this tool; data present and a watermark exists
	StatusReady    Status = "ready"    // sila ingests this tool; data present but never ingested
	StatusDetected Status = "detected" // installed on host, but sila cannot ingest it yet
	StatusMissing  Status = "missing"  // no trace found on this host
)

// Hit is one probed filesystem location for a tool.
type Hit struct {
	Path    string `json:"path"`
	Role    string `json:"role"` // "data" (primary store) | "sessions" | "config"
	Exists  bool   `json:"exists"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time,omitempty"` // RFC3339, "" if unstatable
}

// Candidate is one filesystem location to probe for a tool.
type Candidate struct {
	Path string // absolute path to probe
	Role string // "data" (primary store) | "sessions" | "config"
}

// Spec describes one known AI CLI tool and where its data typically lives.
type Spec struct {
	Name       string      // canonical sila source name ("" = not ingestable yet)
	Display    string      // human-readable product name
	Ingestable bool        // whether sila can index this tool today
	Candidates []Candidate // probed in order; resolved to Hits by buildTool
	Setup      string      // guidance shown when detected but not ingestable
}

// Tool is the discovery result for one AI CLI tool.
type Tool struct {
	Name     string `json:"name"`     // canonical sila name ("" = not ingestable)
	Display  string `json:"display"`  // human-readable product name
	Status   Status `json:"status"`   // indexed | ready | detected | missing
	Paths    []Hit  `json:"paths"`    // discovered candidate paths
	Sessions int64  `json:"sessions"` // distinct sessions discoverable in the source (best-effort)
	Messages int64  `json:"messages"` // store messages attributed to this tool (0 if unindexed)
	Indexed  bool   `json:"indexed"`  // sila holds a watermark for this tool
	Guidance string `json:"guidance"` // actionable next step
}

// Host describes the inspected OS environment.
type Host struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Home        string `json:"home"`
	Store       string `json:"store"`
	StoreExists bool   `json:"store_exists"`
}

// Matrix is the full discovery result for the host.
type Matrix struct {
	Host  Host   `json:"host"`
	Tools []Tool `json:"tools"`
}

// Discover inspects the host environment and returns the tool matrix.
// st may be nil when no knowledge store exists yet.
func Discover(st *store.Store) Matrix {
	m := discoverWith(homeDir(), st)
	if st == nil {
		if _, err := os.Stat(store.DefaultDBPath()); err == nil {
			m.Host.StoreExists = true
		}
	}
	return m
}

// discoverWith is Discover with an injectable home directory (test hermeticity).
// Store existence is derived solely from st.
func discoverWith(home string, st *store.Store) Matrix {
	m := Matrix{Host: Host{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Home:        home,
		Store:       store.DefaultDBPath(),
		StoreExists: st != nil,
	}}

	toolCounts := map[string]int64{}
	if st != nil {
		if c, err := st.ToolMessageCounts(); err == nil {
			toolCounts = c
		}
	}

	for _, spec := range specs(home) {
		m.Tools = append(m.Tools, buildTool(spec, st, toolCounts))
	}
	return m
}

// homeDir resolves the user's home directory.
func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// hit builds a Hit for one candidate path, statting it when present.
func hit(path, role string) Hit {
	h := Hit{Path: path, Role: role}
	if fi, err := os.Stat(path); err == nil {
		h.Exists = true
		h.Size = fi.Size()
		h.ModTime = fi.ModTime().UTC().Format(time.RFC3339)
	}
	return h
}

// specs returns the known-tool matrix probed against the given home dir.
// Ingestable tools mirror internal/loaders.AllSources(); detected-only tools
// (claude-code, cursor) are reported so agents get a complete picture.
func specs(home string) []Spec {
	join := filepath.Join
	return []Spec{
		{
			Name: "claude-code", Display: "Claude Code", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".claude", "projects"), "sessions"},
				{join(home, ".claude"), "data"},
				{join(home, ".claude.json"), "config"},
			},
		},
		{
			Name: "cursor", Display: "Cursor",
			Candidates: []Candidate{
				{join(home, ".config", "Cursor", "User"), "data"},
				{join(home, ".cursor"), "config"},
				{join(home, ".cursor", "projects"), "sessions"},
			},
			Setup: "sila does not index Cursor chats: agent transcripts at ~/.cursor/projects/<project>/agent-transcripts/<session>/*.jsonl carry no timestamps or session ids and drop tool results",
		},
		{
			Name: "mimocode", Display: "MiMoCode", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".local", "share", "mimocode", "mimocode.db"), "data"},
			},
		},
		{
			Name: "opencode", Display: "OpenCode", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".local", "share", "opencode", "opencode.db"), "data"},
			},
		},
		{
			Name: "devin", Display: "Devin", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".local", "share", "devin", "cli", "sessions.db"), "data"},
			},
		},
		{
			Name: "codex", Display: "Codex", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".codex", "history.jsonl"), "data"},
				{join(home, ".codex", "sessions"), "sessions"},
			},
		},
		{
			Name: "command-code", Display: "Command Code", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".commandcode", "history.jsonl"), "data"},
			},
		},
		{
			Name: "zcode", Display: "ZCode", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".zcode", "cli", "db", "db.sqlite"), "data"},
			},
		},
		{
			Name: "muse", Display: "Muse", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".local", "share", "muse", "sessions"), "sessions"},
				{join(home, ".local", "share", "muse", "session-index.db"), "data"},
			},
		},
		{
			Name: "codewhale", Display: "Codewhale", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".codewhale", "sessions"), "sessions"},
			},
		},
		{
			Name: "agy", Display: "Antigravity (AGY)", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".gemini", "antigravity-cli"), "data"},
				{join(home, ".gemini", "antigravity"), "data"},
			},
		},
		{
			Name: "grok", Display: "Grok CLI",
			Candidates: []Candidate{
				{join(home, ".grok", "sessions"), "sessions"},
			},
			Setup: "sila does not index Grok CLI sessions: chat_history.jsonl has no per-message timestamps (they live in a sibling updates.jsonl) and user turns are double-encoded JSON",
		},
		{
			Name: "droid", Display: "Droid",
			Candidates: []Candidate{
				{join(home, ".factory", "sessions"), "sessions"},
			},
			Setup: "sila does not index Droid sessions: ~/.factory transcripts record user turns only — assistant output is never written to disk, so conversations cannot be reconstructed",
		},
		{
			Name: "zed", Display: "Zed", Ingestable: true,
			Candidates: []Candidate{
				{join(home, ".local", "share", "zed", "threads", "threads.db"), "data"},
				{join(home, ".local", "share", "zed", "db", "0-stable", "db.sqlite"), "data"},
				{join(home, ".local", "share", "zed", "db", "0-global", "db.sqlite"), "data"},
				{join(home, ".local", "share", "zed", "conversations"), "sessions"},
				{join(home, ".config", "zed"), "config"},
				{join(home, ".local", "share", "zed", "prompts"), "prompts"},
			},
		},
		{
			Name: "git", Display: "Git", Ingestable: true,
			Candidates: []Candidate{
				{cwd(), "data"},
			},
		},
	}
}

func cwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// buildTool probes a spec's candidates and classifies the tool.
func buildTool(spec Spec, st *store.Store, toolCounts map[string]int64) Tool {
	t := Tool{Name: spec.Name, Display: spec.Display}
	for _, c := range spec.Candidates {
		if h := hit(c.Path, c.Role); h.Exists {
			t.Paths = append(t.Paths, h)
		}
	}
	installed := len(t.Paths) > 0

	if !spec.Ingestable {
		if installed {
			t.Status = StatusDetected
			t.Guidance = spec.Setup
			t.Sessions = countSessions(spec.Name, spec.Candidates)
		} else {
			t.Status = StatusMissing
			t.Guidance = "not installed on this host — install it to include it in future discovery"
		}
		return t
	}

	t.Messages = toolCounts[spec.Name]
	if st != nil {
		if wm, err := st.GetWatermark("", spec.Name); err == nil && wm != "" {
			t.Indexed = true
		}
	}

	if !installed {
		t.Status = StatusMissing
		t.Guidance = "not installed on this host — sila will pick it up automatically once its data appears"
		return t
	}

	t.Sessions = countSessions(spec.Name, spec.Candidates)
	if t.Indexed {
		t.Status = StatusIndexed
		t.Guidance = "indexed — run `sila sync` to pull new sessions, `sila status` for freshness"
	} else {
		t.Status = StatusReady
		t.Guidance = "data found but never ingested — run `sila sync` to index it"
	}
	return t
}

// countSessions reports a best-effort distinct-session count for a tool by
// inspecting its own source data. Errors and absent sources yield 0.
func countSessions(name string, candidates []Candidate) int64 {
	switch name {
	case "devin":
		return countSQLiteSessions(primary(candidates), `SELECT COUNT(*) FROM sessions WHERE hidden = 0`)
	case "mimocode":
		return countSQLiteSessions(primary(candidates), `SELECT COUNT(*) FROM session`)
	case "opencode":
		return countOpenCodeSessions(primary(candidates))
	case "codex":
		return countDistinctJSONLField(primary(candidates), "session_id")
	case "claude-code":
		return countSuffixRecursive(primary(candidates), ".jsonl")
	case "zcode":
		return countSQLiteSessions(primary(candidates), `SELECT COUNT(*) FROM session`)
	case "muse":
		return countNamedFilesRecursive(primary(candidates), "session.jsonl")
	case "codewhale":
		return countFilesWithSuffix(primary(candidates), ".json")
	case "agy":
		p := primary(candidates)
		dbPath := filepath.Join(p, "conversation_summaries.db")
		if n := countSQLiteSessions(dbPath, `SELECT COUNT(*) FROM conversation_summaries`); n > 0 {
			return n
		}
		return countNamedFilesRecursive(p, "transcript.jsonl")
	case "cursor":
		return countWorkspaceStorages(primary(candidates))
	case "zed":
		return countSQLiteSessions(primary(candidates), `SELECT COUNT(*) FROM threads`)
	default:
		return 0
	}
}

// primary returns the first existing candidate path, or "".
func primary(candidates []Candidate) string {
	for _, c := range candidates {
		if _, err := os.Stat(c.Path); err == nil {
			return c.Path
		}
	}
	return ""
}

// openReadOnly opens a source tool DB strictly read-only with a 256MB mmap,
// mirroring the loaders' contract (never modifies another tool's database).
func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA mmap_size=268435456"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// countSQLiteSessions runs a scalar COUNT query against a read-only SQLite DB.
// countOpenCodeSessions counts sessions in the current session_v2 schema, or
// the legacy session table when the store predates OpenCode 1.18.
func countOpenCodeSessions(path string) int64 {
	if path == "" {
		return 0
	}
	db, err := openReadOnly(path)
	if err != nil {
		return 0
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='session_v2'`).Scan(&n); err == nil && n > 0 {
		if err := db.QueryRow(`SELECT COUNT(*) FROM session_v2`).Scan(&n); err == nil {
			return n
		}
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM session`).Scan(&n); err == nil {
		return n
	}
	return 0
}

func countSQLiteSessions(path, query string) int64 {
	if path == "" {
		return 0
	}
	db, err := openReadOnly(path)
	if err != nil {
		return 0
	}
	defer db.Close()
	var n int64
	if err := db.QueryRow(query).Scan(&n); err != nil {
		return 0
	}
	return n
}

// countDistinctJSONLField counts distinct values of a top-level JSON field
// across a JSONL file, streaming line by line without buffering content.
func countDistinctJSONLField(path, field string) int64 {
	if path == "" {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	seen := map[string]bool{}
	dec := json.NewDecoder(f)
	for {
		var obj map[string]json.RawMessage
		if err := dec.Decode(&obj); err != nil {
			break
		}
		if v, ok := obj[field]; ok {
			var s string
			if json.Unmarshal(v, &s) == nil && s != "" {
				seen[s] = true
			}
		}
	}
	return int64(len(seen))
}

// countJSONLFiles counts .jsonl files directly under dir (one per session).
func countJSONLFiles(dir string) int64 {
	return countFilesWithSuffix(dir, ".jsonl")
}

// countSuffixRecursive counts files with the given suffix anywhere under root.
func countSuffixRecursive(root, suffix string) int64 {
	if root == "" {
		return 0
	}
	var n int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), suffix) {
			n++
		}
		return nil
	})
	return n
}

// countFilesWithSuffix counts files with the given suffix directly under dir.
func countFilesWithSuffix(dir, suffix string) int64 {
	if dir == "" {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), suffix) {
			n++
		}
	}
	return n
}

// countNamedFilesRecursive counts files named name anywhere under root.
func countNamedFilesRecursive(root, name string) int64 {
	if root == "" {
		return 0
	}
	var n int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			n++
		}
		return nil
	})
	return n
}

// countWorkspaceStorages counts Cursor workspaceStorage subdirectories
// (each workspace keeps its own chat state).
func countWorkspaceStorages(userDir string) int64 {
	if userDir == "" {
		return 0
	}
	ws := filepath.Join(userDir, "workspaceStorage")
	entries, err := os.ReadDir(ws)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// SortedTools returns the matrix tools in stable name order.
func (m Matrix) SortedTools() []Tool {
	out := append([]Tool(nil), m.Tools...)
	sort.Slice(out, func(i, j int) bool { return out[i].Display < out[j].Display })
	return out
}
