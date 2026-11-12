package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/version"
)

// VerificationVersion is the content-bound receipt schema.
const VerificationVersion = "sila/verification/v2"

// Verification levels. Only L2 and above satisfy a release gate.
const (
	LevelL0 = "L0"
	LevelL1 = "L1"
	LevelL2 = "L2"
	LevelL3 = "L3"
)

// Verification states derived from content, never from commit ancestry.
const (
	StateVerified  = "verified"
	StateStale     = "stale"
	StateStaleSpec = "stale_spec"
	StateUnbound   = "unbound"
)

// VerificationRecord binds a verification to the exact content it covered.
type VerificationRecord struct {
	Version      string      `json:"version"`
	GoalSlug     string      `json:"goal_slug"`
	Level        string      `json:"level"`
	Outcome      string      `json:"outcome"`
	Method       string      `json:"method"` // audit | evidence | reanchor
	SpecHash     string      `json:"spec_hash"`
	Subject      Subject     `json:"subject"`
	Environment  Environment `json:"environment"`
	ReceiptHash  string      `json:"receipt_sha256,omitempty"`
	PreviousHash string      `json:"previous,omitempty"`
	VerifiedAt   time.Time   `json:"verified_at"`
}

// Subject is the sorted content fingerprint of every declared touch path.
type Subject struct {
	Algorithm string        `json:"algorithm"`
	Hash      string        `json:"hash"`
	Paths     []SubjectPath `json:"paths"`
}

// SubjectPath is one file, directory, symlink, or missing path in the subject.
type SubjectPath struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // file | dir | symlink | missing
	Hash string `json:"hash"`
}

// Environment records what the verification ran on.
type Environment struct {
	GoVersion   string `json:"go_version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	SilaVersion string `json:"sila_version"`
	Bwrap       string `json:"bwrap,omitempty"`
}

// VerificationPath returns the canonical verification record path.
func VerificationPath(rootDir, slug string) string {
	return filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug, "verification.json")
}

// LoadVerification reads a verification record, returning nil when absent.
func LoadVerification(rootDir, slug string) (*VerificationRecord, error) {
	p := VerificationPath(rootDir, slug)
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec VerificationRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("parse verification.json: %w", err)
	}
	if rec.Version != VerificationVersion {
		return nil, fmt.Errorf("unsupported verification version %q", rec.Version)
	}
	return &rec, nil
}

// SaveVerification writes a verification record atomically.
func SaveVerification(rootDir, slug string, rec *VerificationRecord) error {
	if rec == nil {
		return fmt.Errorf("verification record is nil")
	}
	if rec.GoalSlug == "" {
		rec.GoalSlug = slug
	}
	rec.Version = VerificationVersion
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(VerificationPath(rootDir, slug), append(data, '\n'), 0o644)
}

// GoalSpecHash hashes the contract and plan files so spec edits invalidate.
func GoalSpecHash(rootDir, slug string) (string, error) {
	dir := filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug)
	h := sha256.New()
	for _, name := range []string{"goal.md", "facts.md", "plan.md"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		h.Write([]byte(name + "\x00"))
		h.Write([]byte(normalizeText(data)))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DeclaredTouchPaths parses Files To Touch from goal.md, accepting both the
// inline `- **Files To Touch**: a, b` form and a `## Files to Touch` section of
// bullets. Internal goal artifacts are dropped, because they must never be
// part of the verified subject.
func DeclaredTouchPaths(rootDir, slug string) ([]string, error) {
	dir := filepath.Join(resolveGoalsRootDir(rootDir), "goals", slug)
	data, err := os.ReadFile(filepath.Join(dir, "goal.md"))
	if err != nil {
		return nil, err
	}
	_, _, _, _, _, parsed, _, _, _, _ := parseGoalMetadataFull(string(data))
	seen := map[string]bool{}
	var out []string
	for _, p := range parsed {
		p = filepath.ToSlash(filepath.Clean(strings.TrimSpace(strings.TrimSuffix(p, "."))))
		if p == "" || p == "." || p == "goals" || strings.HasPrefix(p, "goals/") {
			continue
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// nonSubstantivePaths change with every release and must not invalidate a
// verification. They are excluded from the subject even when declared.
var nonSubstantivePaths = map[string]bool{
	"README.md":                     true,
	"CHANGELOG.md":                  true,
	"project.jsonc":                 true,
	"internal/release/CHANGELOG.md": true,
}

// HashSubject hashes the declared paths plus dependency files at HEAD. When
// HEAD is unavailable it falls back to the worktree.
func HashSubject(rootDir string, paths []string) (Subject, error) {
	root := resolveGoalsRootDir(rootDir)
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}
	for _, dep := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		set[dep] = true
	}
	var entries []SubjectPath
	for p := range set {
		if nonSubstantivePaths[p] {
			continue
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		if os.IsNotExist(err) {
			entries = append(entries, SubjectPath{Path: p, Kind: "missing"})
			continue
		}
		if err != nil {
			return Subject{}, err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(filepath.Join(root, filepath.FromSlash(p)))
			entries = append(entries, SubjectPath{Path: p, Kind: "symlink", Hash: hashString(target)})
		case info.IsDir():
			files, err := listSubjectDir(root, p)
			if err != nil {
				return Subject{}, err
			}
			entries = append(entries, SubjectPath{Path: p, Kind: "dir", Hash: hashStrings(files)})
		default:
			hash, err := hashRepoFile(root, p)
			if err != nil {
				return Subject{}, err
			}
			entries = append(entries, SubjectPath{Path: p, Kind: "file", Hash: hash})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var lines []string
	for _, e := range entries {
		lines = append(lines, e.Path+"\x00"+e.Kind+"\x00"+e.Hash)
	}
	return Subject{Algorithm: "sha256", Hash: hashStrings(lines), Paths: entries}, nil
}

func listSubjectDir(root, rel string) ([]string, error) {
	base := filepath.Join(root, filepath.FromSlash(rel))
	var out []string
	err := filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			if fi.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		r, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		sub := filepath.ToSlash(r)
		if isGoalArtifact(sub) {
			return nil
		}
		hash, err := hashRepoFile(root, filepath.ToSlash(filepath.Join(rel, sub)))
		if err != nil {
			return err
		}
		out = append(out, sub+"\x00"+hash)
		return nil
	})
	sort.Strings(out)
	return out, err
}

func isGoalArtifact(rel string) bool {
	base := filepath.Base(rel)
	switch base {
	case "audit-receipt.json", "certification.json", "verification.json",
		"defect-dossier.json", "reopen.json", "retrospective.json",
		"baseline.json", "remediation.md":
		return true
	}
	return false
}

func hashRepoFile(root, rel string) (string, error) {
	if data, err := exec.Command("git", "-C", root, "show", "HEAD:"+rel).Output(); err == nil {
		return hashBytes(normalizeText(data)), nil
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	return hashBytes(normalizeText(data)), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hashString(s string) string { return hashBytes([]byte(s)) }

func hashStrings(lines []string) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func normalizeText(b []byte) []byte {
	return []byte(strings.ReplaceAll(string(b), "\r\n", "\n"))
}

// ComputeVerificationState derives the state from content alone.
func ComputeVerificationState(rootDir, slug string, rec *VerificationRecord) (string, []string, error) {
	if rec == nil {
		return StateUnbound, nil, nil
	}
	spec, err := GoalSpecHash(rootDir, slug)
	if err != nil {
		return "", nil, err
	}
	if spec != rec.SpecHash {
		return StateStaleSpec, nil, nil
	}
	paths := make([]string, 0, len(rec.Subject.Paths))
	for _, p := range rec.Subject.Paths {
		paths = append(paths, p.Path)
	}
	subj, err := HashSubject(rootDir, paths)
	if err != nil {
		return "", nil, err
	}
	changed := diffSubject(rec.Subject, subj)
	if len(changed) > 0 {
		return StateStale, changed, nil
	}
	return StateVerified, nil, nil
}

func diffSubject(old, cur Subject) []string {
	oldMap := map[string]string{}
	for _, p := range old.Paths {
		oldMap[p.Path] = p.Kind + "\x00" + p.Hash
	}
	var changed []string
	seen := map[string]bool{}
	for _, p := range cur.Paths {
		seen[p.Path] = true
		if oldMap[p.Path] != p.Kind+"\x00"+p.Hash {
			changed = append(changed, p.Path)
		}
	}
	for p := range oldMap {
		if !seen[p] {
			changed = append(changed, p)
		}
	}
	sort.Strings(changed)
	return changed
}

// CaptureEnvironment records the toolchain the verification ran on.
func CaptureEnvironment() Environment {
	env := Environment{
		GoVersion:   runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		SilaVersion: version.Version,
	}
	if out, err := exec.Command("bwrap", "--version").Output(); err == nil {
		env.Bwrap = strings.TrimSpace(string(out))
	}
	return env
}

// RecordVerification writes a new content-bound record for a goal that was
// just verified by an audit or an evidence review.
func RecordVerification(rootDir, slug, level, method string) (*VerificationRecord, error) {
	paths, err := DeclaredTouchPaths(rootDir, slug)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("goal %q declares no Files To Touch; cannot bind content", slug)
	}
	spec, err := GoalSpecHash(rootDir, slug)
	if err != nil {
		return nil, err
	}
	subj, err := HashSubject(rootDir, paths)
	if err != nil {
		return nil, err
	}
	prev, _ := LoadVerification(rootDir, slug)
	rec := &VerificationRecord{
		GoalSlug:    slug,
		Level:       level,
		Outcome:     "passed",
		Method:      method,
		SpecHash:    spec,
		Subject:     subj,
		Environment: CaptureEnvironment(),
		VerifiedAt:  time.Now().UTC().Truncate(time.Second),
	}
	if prev != nil {
		data, _ := json.Marshal(prev)
		rec.PreviousHash = hashBytes(data)
	}
	if err := SaveVerification(rootDir, slug, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// Reverify re-anchors an existing record only when content is unchanged.
func Reverify(rootDir, slug string) (*VerificationRecord, error) {
	rec, err := LoadVerification(rootDir, slug)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, fmt.Errorf("goal %q has no verification record to re-anchor", slug)
	}
	state, changed, err := ComputeVerificationState(rootDir, slug, rec)
	if err != nil {
		return nil, err
	}
	if state != StateVerified {
		if len(changed) > 0 {
			return nil, fmt.Errorf("refusing to re-anchor %q: content changed (%s)", slug, strings.Join(changed, ", "))
		}
		return nil, fmt.Errorf("refusing to re-anchor %q: state is %s", slug, state)
	}
	prevData, _ := json.Marshal(rec)
	rec.PreviousHash = hashBytes(prevData)
	rec.Method = "reanchor"
	rec.VerifiedAt = time.Now().UTC().Truncate(time.Second)
	rec.Environment = CaptureEnvironment()
	if err := SaveVerification(rootDir, slug, rec); err != nil {
		return nil, err
	}
	return rec, nil
}
