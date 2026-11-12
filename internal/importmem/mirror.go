package importmem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/store"
)

// MirrorMaxBytes is the largest markdown file a mirror write may import.
const MirrorMaxBytes = 1 << 20

// Mirror refusal codes. They are stable, lowercase, and content-free.
const (
	MirrorPathEscape   = "MIRROR_PATH_ESCAPE"
	MirrorNotMarkdown  = "MIRROR_NOT_MARKDOWN"
	MirrorTooLarge     = "MIRROR_TOO_LARGE"
	MirrorDisabled     = "MIRROR_DISABLED"
	MirrorTimeout      = "MIRROR_TIMEOUT"
	MirrorStoreFailed  = "MIRROR_STORE_FAILED"
	MirrorImportFailed = "MIRROR_IMPORT_FAILED"
)

// MirrorRootSpec is one harness memory root. Home patterns are relative to the
// user home, repo patterns to the repository root. A pattern containing "*" is
// expanded with filepath.Glob.
type MirrorRootSpec struct {
	Harness string
	Home    []string
	Repo    []string
}

// MirrorRoots is the single allowlist for mirroring. It intentionally matches
// the roots used by discovery so the two cannot drift.
var MirrorRoots = []MirrorRootSpec{
	{Harness: "claude", Home: []string{".claude/projects/*/memory"}, Repo: []string{".claude/memory"}},
	{Harness: "codex", Home: []string{".codex/memories", ".codex/memory"}, Repo: []string{".codex/memories", ".codex/memory"}},
	{Harness: "grok", Home: []string{".grok/topics", ".grok/memory"}, Repo: []string{".grok/topics", ".grok/memory"}},
}

// MirrorTarget is one classified allowlisted file.
type MirrorTarget struct {
	Harness string
	Path    string
}

// MirrorResult reports one mirror attempt without content or absolute paths.
type MirrorResult struct {
	Mirrored   int
	Skipped    int
	Duplicates int
	Failed     int
	Code       string
}

// ClassifyMirrorPath resolves symlinks and reports whether path is an
// allowlisted markdown memory file. Roots are matched relative to home and
// repo, never the process working directory. A missing root is not an error.
func ClassifyMirrorPath(home, repo, path string) (MirrorTarget, bool) {
	if strings.TrimSpace(path) == "" {
		return MirrorTarget{}, false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return MirrorTarget{}, false
	}
	resolved := abs
	if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		resolved = real
	} else if !os.IsNotExist(evalErr) {
		return MirrorTarget{}, false
	}
	if !strings.EqualFold(filepath.Ext(resolved), ".md") {
		return MirrorTarget{}, false
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() || info.Size() > MirrorMaxBytes {
		return MirrorTarget{}, false
	}
	for _, spec := range MirrorRoots {
		for _, root := range mirrorRootsFor(spec, home, repo) {
			if pathWithin(root, resolved) {
				return MirrorTarget{Harness: spec.Harness, Path: resolved}, true
			}
		}
	}
	return MirrorTarget{}, false
}

func mirrorRootsFor(spec MirrorRootSpec, home, repo string) []string {
	var roots []string
	if home != "" {
		for _, pattern := range spec.Home {
			base := filepath.Join(home, filepath.FromSlash(pattern))
			if strings.Contains(pattern, "*") {
				matches, err := filepath.Glob(base)
				if err != nil {
					continue
				}
				for _, match := range matches {
					if isRealDir(match) {
						roots = append(roots, match)
					}
				}
				continue
			}
			if isRealDir(base) {
				roots = append(roots, base)
			}
		}
	}
	if repo != "" {
		for _, pattern := range spec.Repo {
			base := filepath.Join(repo, filepath.FromSlash(pattern))
			if isRealDir(base) {
				roots = append(roots, base)
			}
		}
	}
	return roots
}

func isRealDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// pathWithin reports whether target is inside root after both are resolved.
func pathWithin(root, target string) bool {
	resolvedRoot := root
	if real, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = real
	}
	rel, err := filepath.Rel(resolvedRoot, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// MirrorBudget bounds one file-scoped mirror.
const MirrorBudget = 2 * time.Second

// MirrorOne parses and imports exactly one allowlisted file. Re-mirroring
// identical content counts a duplicate and writes nothing; a changed file
// follows the existing idempotency keys.
func MirrorOne(ctx context.Context, st MemoryStore, opts ImportOptions, target MirrorTarget) (MirrorResult, error) {
	res := MirrorResult{}
	if target.Path == "" || target.Harness == "" {
		res.Skipped = 1
		res.Code = MirrorPathEscape
		return res, nil
	}
	ctx, cancel := context.WithTimeout(ctx, MirrorBudget)
	defer cancel()
	if ctx.Err() != nil {
		res.Code = MirrorTimeout
		return res, ctx.Err()
	}
	data, tooLarge, err := readBounded(target.Path, MirrorMaxBytes)
	if err != nil {
		res.Failed = 1
		res.Code = MirrorImportFailed
		return res, err
	}
	if tooLarge {
		res.Skipped = 1
		res.Code = MirrorTooLarge
		return res, nil
	}
	if !utf8.Valid(data) {
		res.Skipped = 1
		res.Code = CodeMalformedFile
		return res, nil
	}
	redactor, redactorErr := NewRedactor(RedactorOptions{})
	if redactorErr != nil {
		res.Failed = 1
		res.Code = MirrorImportFailed
		return res, redactorErr
	}
	redacted, redactErr := redactor.Redact(string(data), opts.RepoRoot)
	if redactErr != nil {
		res.Failed = 1
		res.Code = CodeRedactionFailed
		return res, nil
	}
	parsed := Parse(redacted.Text, opts.WithDefaults().MaxCandidates)
	if !parsed.HasSection {
		res.Skipped = 1
		res.Code = SkipNoSection
		return res, nil
	}
	idx, indexErr := newMemoryIndex(st, opts.ProjectID)
	if indexErr != nil {
		res.Failed = 1
		res.Code = MirrorStoreFailed
		return res, indexErr
	}
	label := filepath.Base(target.Path)
	prov := mirrorStoreProvenance(target, opts.MirrorSession)
	for _, candidate := range parsed.Candidates {
		env := BuildEnvelope(candidate, Source(target.Harness), label, opts.ProjectID)
		env.Provenance = &store.MemoryProvenance{
			Agent:        target.Harness,
			ToolName:     target.Harness,
			ClientKind:   "ai_agent",
			OriginEntity: "agent:" + target.Harness,
			SessionRef:   opts.MirrorSession,
		}
		digest, digestErr := store.MemoryContentDigest(env)
		if digestErr != nil {
			res.Failed = 1
			res.Code = MirrorImportFailed
			return res, digestErr
		}
		text := normalizeText(candidate.Title + " " + candidate.Summary)
		replayRef, replay := idx.storeKeys[env.IdempotencyKey]
		replay = replay && replayRef.digest == digest
		if !replay && (idx.digests[digest] || idx.texts[text]) {
			res.Duplicates++
			continue
		}
		if ctx.Err() != nil {
			res.Code = MirrorTimeout
			return res, ctx.Err()
		}
		if st == nil {
			res.Failed = 1
			res.Code = MirrorStoreFailed
			return res, nil
		}
		_, created, saveErr := st.SaveMemoryEnvelope(ctx, opts.ProjectID, env, prov)
		if saveErr != nil {
			if store.MemoryCode(saveErr) == store.MemoryCodeDuplicate {
				res.Duplicates++
				continue
			}
			res.Failed = 1
			res.Code = MirrorStoreFailed
			return res, saveErr
		}
		idx.accept(nil, text, digest)
		if created {
			res.Mirrored++
		} else {
			res.Duplicates++
		}
	}
	return res, nil
}

func mirrorStoreProvenance(target MirrorTarget, session string) store.Provenance {
	return store.Provenance{
		ToolName:     target.Harness,
		ClientKind:   "ai_agent",
		OriginEntity: "agent:" + target.Harness,
		SessionRef:   session,
		OriginRole:   "auto_mirror",
	}
}
