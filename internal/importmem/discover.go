// Package importmem imports durable memory files written by other coding
// agents into Sila's typed memory store. It never invokes a model, never makes
// a network request, and never treats instruction or rule files as memory.
package importmem

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source is one supported agent memory origin.
type Source string

const (
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
	SourceGrok   Source = "grok"
	SourceAll    Source = "all"
)

// Stable import error codes. Scripts branch on these strings.
const (
	CodeInstructionsRefused = "IMPORT_INSTRUCTIONS_REFUSED"
	CodeBound               = "IMPORT_BOUND"
	CodePathUnsafe          = "IMPORT_PATH_UNSAFE"
	CodeRedactionFailed     = "IMPORT_REDACTION_FAILED"
	CodeMalformedFile       = "IMPORT_MALFORMED_FILE"
	CodeProject             = "IMPORT_PROJECT"
	CodeDuplicate           = "IMPORT_DUPLICATE"
	CodeSimilar             = "IMPORT_SIMILAR"
	CodeStore               = "IMPORT_STORE"
)

// Default run bounds.
const (
	DefaultMaxFiles      = 200
	DefaultMaxFileBytes  = 1 << 20
	DefaultMaxTotalBytes = 8 << 20
	DefaultMaxCandidates = 500
)

// SourceOrder is the deterministic discovery order.
var SourceOrder = []Source{SourceClaude, SourceCodex, SourceGrok}

// DiscoveredFile is one safe memory file candidate.
type DiscoveredFile struct {
	Source   Source
	Root     string
	RootKind string // home|repo
	Path     string
	Rel      string
	Label    string
	Size     int64
}

// SkippedFile is a content-free record of a file that was not imported.
type SkippedFile struct {
	Source string `json:"source"`
	Label  string `json:"label"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// SourceReport summarizes one source root.
type SourceReport struct {
	Source string `json:"source"`
	Status string `json:"status"` // found|not_found
	Files  int    `json:"files"`
}

// ImportOptions controls discovery and import.
type ImportOptions struct {
	ProjectID     string
	RepoRoot      string
	HomeDir       string
	From          string
	DryRun        bool
	Again         bool
	ForceSimilar  bool
	MaxFiles      int
	MaxFileBytes  int64
	MaxTotalBytes int64
	MaxCandidates int
	MirrorSession string // hook session used when mirroring a single file
}

// WithDefaults returns a copy with empty bounds replaced by documented limits.
func (o ImportOptions) WithDefaults() ImportOptions {
	if o.From == "" {
		o.From = string(SourceAll)
	}
	if o.MaxFiles <= 0 {
		o.MaxFiles = DefaultMaxFiles
	}
	if o.MaxFileBytes <= 0 {
		o.MaxFileBytes = DefaultMaxFileBytes
	}
	if o.MaxTotalBytes <= 0 {
		o.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if o.MaxCandidates <= 0 {
		o.MaxCandidates = DefaultMaxCandidates
	}
	return o
}

// ParseSources resolves --from into an ordered source list.
func ParseSources(from string) ([]Source, error) {
	switch Source(strings.ToLower(strings.TrimSpace(from))) {
	case "", SourceAll:
		return append([]Source(nil), SourceOrder...), nil
	case SourceClaude:
		return []Source{SourceClaude}, nil
	case SourceCodex:
		return []Source{SourceCodex}, nil
	case SourceGrok:
		return []Source{SourceGrok}, nil
	default:
		return nil, &ImportError{Code: CodeProject, Message: `invalid --from value; use claude, codex, grok, or all`}
	}
}

// ImportError carries a stable code without content or paths.
type ImportError struct {
	Code    string
	Message string
}

func (e *ImportError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// CodeOf returns the stable code of an import error, or "".
func CodeOf(err error) string {
	var ie *ImportError
	if errors.As(err, &ie) {
		return ie.Code
	}
	return ""
}

type memoryRoot struct {
	source Source
	kind   string
	root   string
	starts []string
}

func rootsFor(home, repo string, sources []Source) []memoryRoot {
	allowed := make(map[Source]bool, len(sources))
	for _, source := range sources {
		allowed[source] = true
	}
	var roots []memoryRoot
	for _, spec := range MirrorRoots {
		source := Source(spec.Harness)
		if !allowed[source] {
			continue
		}
		if home != "" {
			base := filepath.Join(home, "."+spec.Harness)
			var starts []string
			for _, pattern := range spec.Home {
				full := filepath.Join(home, filepath.FromSlash(pattern))
				if strings.Contains(pattern, "*") {
					starts = append(starts, globDirs(full)...)
					continue
				}
				starts = append(starts, existingDirs(full)...)
			}
			roots = append(roots, memoryRoot{source: source, kind: "home", root: base, starts: starts})
		}
		if repo != "" {
			base := filepath.Join(repo, "."+spec.Harness)
			var starts []string
			for _, pattern := range spec.Repo {
				starts = append(starts, existingDirs(filepath.Join(repo, filepath.FromSlash(pattern)))...)
			}
			roots = append(roots, memoryRoot{source: source, kind: "repo", root: base, starts: starts})
		}
	}
	return roots
}

func globDirs(pattern string) []string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return existingDirs(matches...)
}

func existingDirs(paths ...string) []string {
	var out []string
	for _, p := range paths {
		if fi, err := os.Lstat(p); err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0 {
			out = append(out, p)
		}
	}
	return out
}

// Discover returns safe memory files plus per-source and per-file skip
// reports. A missing root is reported as not_found, never as an error.
func Discover(opts ImportOptions) ([]DiscoveredFile, []SourceReport, []SkippedFile, bool, error) {
	opts = opts.WithDefaults()
	sources, err := ParseSources(opts.From)
	if err != nil {
		return nil, nil, nil, false, err
	}
	roots := rootsFor(opts.HomeDir, opts.RepoRoot, sources)

	reports := make([]SourceReport, 0, len(sources))
	rootFound := map[Source]bool{}
	rootFiles := map[Source]int{}
	for _, root := range roots {
		if fi, err := os.Stat(root.root); err == nil && fi.IsDir() {
			rootFound[root.source] = true
		}
	}

	var files []DiscoveredFile
	var skipped []SkippedFile
	seen := map[string]bool{}
	boundHit := false

	for _, root := range roots {
		if len(files) >= opts.MaxFiles {
			boundHit = true
			break
		}
		realRoot, realErr := filepath.EvalSymlinks(root.root)
		if realErr != nil {
			realRoot = root.root
		}
		for _, start := range root.starts {
			if len(files) >= opts.MaxFiles {
				boundHit = true
				break
			}
			walkErr := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					if d != nil {
						skipped = append(skipped, SkippedFile{
							Source: string(root.source),
							Label:  compactLabel(root.source, root.kind, path),
							Code:   CodePathUnsafe,
							Reason: "unreadable entry",
						})
					}
					return nil
				}
				if len(files) >= opts.MaxFiles {
					boundHit = true
					return filepath.SkipAll
				}
				rel, relErr := filepath.Rel(root.root, path)
				if relErr != nil || rel == "." {
					return nil
				}
				if d.IsDir() {
					if d.Type()&os.ModeSymlink != 0 {
						return filepath.SkipDir
					}
					if !safeRelative(rel) {
						return filepath.SkipDir
					}
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 {
					skipped = append(skipped, SkippedFile{
						Source: string(root.source),
						Label:  compactLabel(root.source, root.kind, rel),
						Code:   CodePathUnsafe,
						Reason: "symlink is not followed",
					})
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				if !strings.EqualFold(filepath.Ext(rel), ".md") {
					return nil
				}
				label := compactLabel(root.source, root.kind, rel)
				if isInstructionPath(rel) {
					skipped = append(skipped, SkippedFile{
						Source: string(root.source),
						Label:  label,
						Code:   CodeInstructionsRefused,
						Reason: "instruction or rule file is never imported",
					})
					return nil
				}
				if !safeRelative(rel) {
					skipped = append(skipped, SkippedFile{
						Source: string(root.source),
						Label:  label,
						Code:   CodePathUnsafe,
						Reason: "hidden, credential, or sensitive path",
					})
					return nil
				}
				info, statErr := d.Info()
				if statErr != nil {
					skipped = append(skipped, SkippedFile{
						Source: string(root.source),
						Label:  label,
						Code:   CodePathUnsafe,
						Reason: "cannot stat file",
					})
					return nil
				}
				if info.Size() > opts.MaxFileBytes {
					skipped = append(skipped, SkippedFile{
						Source: string(root.source),
						Label:  label,
						Code:   CodeBound,
						Reason: "file exceeds the per-file bound",
					})
					return nil
				}
				realFile, evalErr := filepath.EvalSymlinks(path)
				if evalErr != nil {
					skipped = append(skipped, SkippedFile{
						Source: string(root.source),
						Label:  label,
						Code:   CodePathUnsafe,
						Reason: "cannot resolve file path",
					})
					return nil
				}
				inside, insideErr := pathInside(realRoot, realFile)
				if insideErr != nil || !inside {
					skipped = append(skipped, SkippedFile{
						Source: string(root.source),
						Label:  label,
						Code:   CodePathUnsafe,
						Reason: "path escapes the agent root",
					})
					return nil
				}
				if seen[realFile] {
					return nil
				}
				seen[realFile] = true
				files = append(files, DiscoveredFile{
					Source:   root.source,
					Root:     realRoot,
					RootKind: root.kind,
					Path:     realFile,
					Rel:      filepath.ToSlash(rel),
					Label:    label,
					Size:     info.Size(),
				})
				rootFiles[root.source]++
				return nil
			})
			if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
				return nil, nil, nil, boundHit, &ImportError{Code: CodePathUnsafe, Message: "cannot walk a memory root"}
			}
			if len(files) >= opts.MaxFiles {
				boundHit = true
				break
			}
		}
	}

	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Source != files[j].Source {
			return sourceRank(files[i].Source) < sourceRank(files[j].Source)
		}
		if files[i].Label != files[j].Label {
			return files[i].Label < files[j].Label
		}
		return files[i].Size < files[j].Size
	})
	sort.SliceStable(skipped, func(i, j int) bool {
		if skipped[i].Source != skipped[j].Source {
			return skipped[i].Source < skipped[j].Source
		}
		return skipped[i].Label < skipped[j].Label
	})
	for _, source := range sources {
		status := "not_found"
		if rootFound[source] {
			status = "found"
		}
		reports = append(reports, SourceReport{Source: string(source), Status: status, Files: rootFiles[source]})
	}
	if boundHit {
		skipped = append(skipped, SkippedFile{
			Source: "run",
			Label:  "*",
			Code:   CodeBound,
			Reason: "file limit reached; run stopped",
		})
	}
	return files, reports, skipped, boundHit, nil
}

func sourceRank(s Source) int {
	for i, candidate := range SourceOrder {
		if candidate == s {
			return i
		}
	}
	return len(SourceOrder)
}

// safeRelative rejects hidden components and credential-looking names below
// an explicitly allowed agent root.
func safeRelative(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	base := strings.ToLower(filepath.Base(rel))
	if secretBasenames[base] || strings.HasPrefix(base, ".env.") || secretExtensions[filepath.Ext(base)] {
		return false
	}
	return true
}

var secretBasenames = map[string]bool{
	".env": true, ".npmrc": true, ".netrc": true, ".pgpass": true,
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	"credentials": true, "secrets": true, "secret": true,
}

var secretExtensions = map[string]bool{
	".pem": true, ".key": true, ".p12": true, ".pfx": true, ".kdbx": true, ".jks": true,
}

// isInstructionPath reports whether a path is an instruction or rule file.
func isInstructionPath(rel string) bool {
	slash := strings.ToLower(filepath.ToSlash(rel))
	base := filepath.Base(slash)
	switch base {
	case "claude.md", "agents.md", "gemini.md", "copilot.md", "copilot-instructions.md":
		return true
	}
	parts := strings.Split(slash, "/")
	for _, part := range parts[:len(parts)-1] {
		if part == "rules" || part == "instructions" {
			return true
		}
	}
	return false
}

func pathInside(root, target string) (bool, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

// compactLabel renders a stable, non-absolute source label. Long labels keep a
// digest suffix so two files never collapse onto the same identity.
func compactLabel(source Source, kind, rel string) string {
	label := string(source) + ":" + kind + ":" + filepath.ToSlash(rel)
	const max = 300
	if len(label) <= max {
		return label
	}
	sum := sha256.Sum256([]byte(label))
	head := strings.ToValidUTF8(label[:max-24], "")
	return head + "~" + hex.EncodeToString(sum[:])[:16]
}
