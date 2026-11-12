package importmem

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/store"
)

// SkipNoSection is the deterministic reason for a file without a recognized
// section. It is lowercase by contract.
const SkipNoSection = "skipped_no_section"

// ManifestRelPath is the optional project-local import cache.
const ManifestRelPath = ".sila/state/import/manifest.json"

// ManifestMaxBytes bounds the optional cache.
const ManifestMaxBytes = 1 << 20

// ManifestStaleAfter is the age at which doctor reports a manifest as stale.
const ManifestStaleAfter = 7 * 24 * time.Hour

// manifestSchema identifies the cache format; the cache is never the source of
// truth for idempotency.
const manifestSchema = "sila/import-manifest/v1"

// ManifestEntry records one source file and its redacted content hash.
type ManifestEntry struct {
	Source      string `json:"source"`
	Label       string `json:"label"`
	ContentHash string `json:"content_hash"`
	Bytes       int64  `json:"bytes"`
}

// Manifest is the optional project-local import cache.
type Manifest struct {
	Schema    string          `json:"schema"`
	UpdatedAt string          `json:"updated_at"`
	Entries   []ManifestEntry `json:"entries"`
	Truncated bool            `json:"truncated,omitempty"`
}

// ManifestStatus is a content-free doctor report.
type ManifestStatus struct {
	Exists    bool   `json:"exists"`
	Stale     bool   `json:"stale"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Entries   int    `json:"entries"`
	Error     string `json:"error,omitempty"`
}

// MemoryStore is the narrow store seam import needs.
type MemoryStore interface {
	ListMemoryRecords(opts store.MemoryListOptions) ([]store.MemoryRecord, error)
	SaveMemoryEnvelope(ctx context.Context, projectID string, env *store.MemoryEnvelope, prov store.Provenance) (*store.MemoryRecord, bool, error)
}

// ItemResult reports one created, replayed, planned, or skipped candidate
// without content or absolute paths.
type ItemResult struct {
	MemoryID string `json:"memory_id,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Source   string `json:"source"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Code     string `json:"code,omitempty"`
}

// ImportReport is the machine-readable import outcome.
type ImportReport struct {
	From                string         `json:"from"`
	ProjectID           string         `json:"project_id"`
	DryRun              bool           `json:"dry_run"`
	Sources             []SourceReport `json:"sources"`
	FilesScanned        int            `json:"files_scanned"`
	FilesSkipped        []SkippedFile  `json:"files_skipped"`
	Candidates          int            `json:"candidates"`
	Created             int            `json:"created"`
	Replayed            int            `json:"replayed"`
	Duplicates          int            `json:"duplicates"`
	Similar             int            `json:"similar"`
	Failed              int            `json:"failed"`
	Planned             int            `json:"planned"`
	Splits              int            `json:"splits"`
	Redactions          map[string]int `json:"redactions"`
	BytesRead           int64          `json:"bytes_read"`
	DurationMS          int64          `json:"duration_ms"`
	InstructionsRefused int            `json:"instructions_refused"`
	Items               []ItemResult   `json:"items,omitempty"`
	Status              string         `json:"status"`
}

type indexEntry struct {
	id   string
	text string
}

type storeKeyRef struct {
	id     string
	digest string
}

type memoryIndex struct {
	digests   map[string]bool
	texts     map[string]bool
	storeKeys map[string]storeKeyRef
	entries   []indexEntry
}

func normalizeText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func newMemoryIndex(st MemoryStore, projectID string) (*memoryIndex, error) {
	idx := &memoryIndex{digests: map[string]bool{}, texts: map[string]bool{}, storeKeys: map[string]storeKeyRef{}}
	if st == nil {
		return idx, nil
	}
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: projectID, Limit: 500})
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		text := normalizeText(rec.Title + " " + rec.Summary)
		idx.texts[text] = true
		idx.entries = append(idx.entries, indexEntry{id: rec.ID, text: text})
		if rec.Envelope != nil {
			if digest, err := store.MemoryContentDigest(rec.Envelope); err == nil {
				idx.digests[digest] = true
				if rec.Envelope.IdempotencyKey != "" {
					idx.storeKeys[rec.Envelope.IdempotencyKey] = storeKeyRef{id: rec.ID, digest: digest}
				}
			}
		}
	}
	return idx, nil
}

func (idx *memoryIndex) accept(rec *store.MemoryRecord, text string, digest string) {
	if rec != nil && rec.ID != "" {
		idx.entries = append(idx.entries, indexEntry{id: rec.ID, text: text})
	} else {
		idx.entries = append(idx.entries, indexEntry{text: text})
	}
	idx.texts[text] = true
	if digest != "" {
		idx.digests[digest] = true
	}
}

func (idx *memoryIndex) similar(text string) bool {
	for _, entry := range idx.entries {
		if dream.Similarity(text, entry.text) >= 0.6 {
			return true
		}
	}
	return false
}

// Run discovers, redacts, parses, deduplicates, and imports memory files for
// exactly one project. A dry run never calls the writer. A store failure aborts
// the run after recording the first failed candidate.
func Run(ctx context.Context, st MemoryStore, opts ImportOptions) (*ImportReport, error) {
	start := time.Now()
	opts = opts.WithDefaults()
	report := &ImportReport{
		From:         opts.From,
		ProjectID:    opts.ProjectID,
		DryRun:       opts.DryRun,
		Sources:      []SourceReport{},
		FilesSkipped: []SkippedFile{},
		Redactions:   map[string]int{},
		Status:       "ok",
	}
	if strings.TrimSpace(opts.ProjectID) == "" {
		return report, &ImportError{Code: CodeProject, Message: "no registered project; pass --project"}
	}
	if opts.MaxFiles < 1 || opts.MaxFiles > DefaultMaxFiles {
		return report, &ImportError{Code: CodeBound, Message: fmt.Sprintf("--max-files must be 1-%d", DefaultMaxFiles)}
	}
	if opts.MaxCandidates > DefaultMaxCandidates {
		return report, &ImportError{Code: CodeBound, Message: fmt.Sprintf("candidate limit is %d", DefaultMaxCandidates)}
	}
	for _, category := range RedactionCategories {
		report.Redactions[category] = 0
	}

	files, sources, skipped, boundHit, err := Discover(opts)
	if err != nil {
		return report, err
	}
	report.Sources = sources
	report.FilesScanned = len(files)
	report.FilesSkipped = append(report.FilesSkipped, skipped...)
	for _, skip := range skipped {
		if skip.Code == CodeInstructionsRefused {
			report.InstructionsRefused++
		}
	}
	if boundHit {
		report.Status = "partial"
	}

	idx, err := newMemoryIndex(st, opts.ProjectID)
	if err != nil {
		return report, &ImportError{Code: CodeStore, Message: "cannot read existing memories"}
	}

	var manifestEntries []ManifestEntry
	stop := false
	redactor, redactorErr := NewRedactor(RedactorOptions{})
	if redactorErr != nil {
		return report, redactorErr
	}
	for _, file := range files {
		if stop {
			break
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				report.Failed++
				report.Status = "failed"
				return report, &ImportError{Code: CodeStore, Message: "import cancelled"}
			}
		}
		if report.BytesRead+file.Size > opts.MaxTotalBytes {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: string(file.Source), Label: file.Label, Code: CodeBound,
				Reason: "total byte bound reached; run stopped",
			})
			report.Status = "partial"
			stop = true
			break
		}
		data, tooLarge, err := readBounded(file.Path, opts.MaxFileBytes)
		if err != nil {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: string(file.Source), Label: file.Label, Code: CodePathUnsafe,
				Reason: "file cannot be read",
			})
			continue
		}
		if tooLarge {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: string(file.Source), Label: file.Label, Code: CodeBound,
				Reason: "file exceeds the per-file bound",
			})
			report.Status = "partial"
			continue
		}
		if report.BytesRead+int64(len(data)) > opts.MaxTotalBytes {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: string(file.Source), Label: file.Label, Code: CodeBound,
				Reason: "total byte bound reached; run stopped",
			})
			report.Status = "partial"
			stop = true
			break
		}
		report.BytesRead += int64(len(data))

		if !utf8.Valid(data) {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: string(file.Source), Label: file.Label, Code: CodeMalformedFile,
				Reason: "file is not valid UTF-8",
			})
			continue
		}
		redacted, redactErr := redactor.Redact(string(data), opts.RepoRoot)
		if redactErr != nil {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: string(file.Source), Label: file.Label, Code: CodeRedactionFailed,
				Reason: "redaction failed closed",
			})
			continue
		}
		for category, count := range redacted.Counts {
			report.Redactions[category] += count
		}
		manifestEntries = append(manifestEntries, ManifestEntry{
			Source: string(file.Source), Label: file.Label,
			ContentHash: contentHash(redacted.Text), Bytes: int64(len(data)),
		})

		remaining := opts.MaxCandidates - report.Candidates
		if remaining <= 0 {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: "run", Label: "*", Code: CodeBound,
				Reason: "candidate limit reached; run stopped",
			})
			report.Status = "partial"
			stop = true
			break
		}
		parsed := Parse(redacted.Text, remaining)
		if !parsed.HasSection {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: string(file.Source), Label: file.Label, Code: SkipNoSection,
				Reason: "no recognized memory section",
			})
			continue
		}
		report.Splits += splitCount(parsed.Candidates)
		if parsed.Truncated {
			report.FilesSkipped = append(report.FilesSkipped, SkippedFile{
				Source: "run", Label: "*", Code: CodeBound,
				Reason: "candidate limit reached; run stopped",
			})
			report.Status = "partial"
			stop = true
		}
		for _, candidate := range parsed.Candidates {
			report.Candidates++
			text := normalizeText(candidate.Title + " " + candidate.Summary)
			env := BuildEnvelope(candidate, file.Source, file.Label, opts.ProjectID)
			digest, digestErr := store.MemoryContentDigest(env)
			if digestErr != nil {
				report.Failed++
				report.Status = "failed"
				return report, &ImportError{Code: CodeStore, Message: "cannot digest candidate"}
			}
			replayRef, replay := idx.storeKeys[env.IdempotencyKey]
			replay = replay && replayRef.digest == digest
			if !replay {
				if idx.digests[digest] || idx.texts[text] {
					report.Duplicates++
					report.Items = append(report.Items, ItemResult{
						Kind: candidate.Kind, Source: string(file.Source), Label: file.Label,
						Status: "skipped", Code: CodeDuplicate,
					})
					continue
				}
				if !opts.ForceSimilar && idx.similar(text) {
					report.Similar++
					report.Items = append(report.Items, ItemResult{
						Kind: candidate.Kind, Source: string(file.Source), Label: file.Label,
						Status: "skipped", Code: CodeSimilar,
					})
					continue
				}
			}
			if opts.DryRun {
				idx.accept(nil, text, digest)
				if replay {
					report.Replayed++
					report.Items = append(report.Items, ItemResult{
						MemoryID: replayRef.id, Kind: candidate.Kind, Source: string(file.Source),
						Label: file.Label, Status: "replayed",
					})
				} else {
					report.Planned++
					report.Items = append(report.Items, ItemResult{
						Kind: candidate.Kind, Source: string(file.Source), Label: file.Label,
						Status: "planned",
					})
				}
				continue
			}
			if st == nil {
				report.Failed++
				report.Status = "failed"
				return report, &ImportError{Code: CodeStore, Message: "no store available"}
			}
			rec, created, saveErr := st.SaveMemoryEnvelope(ctx, opts.ProjectID, env, StoreProvenance(file.Source))
			if saveErr != nil && opts.Again && store.MemoryCode(saveErr) == store.MemoryCodeIdempotency {
				env.IdempotencyKey += "-again"
				rec, created, saveErr = st.SaveMemoryEnvelope(ctx, opts.ProjectID, env, StoreProvenance(file.Source))
			}
			if saveErr != nil {
				if store.MemoryCode(saveErr) == store.MemoryCodeDuplicate {
					report.Duplicates++
					report.Items = append(report.Items, ItemResult{
						Kind: candidate.Kind, Source: string(file.Source), Label: file.Label,
						Status: "skipped", Code: CodeDuplicate,
					})
					continue
				}
				report.Failed++
				report.Status = "failed"
				report.Items = append(report.Items, ItemResult{
					Kind: candidate.Kind, Source: string(file.Source), Label: file.Label,
					Status: "failed", Code: CodeStore,
				})
				return report, &ImportError{Code: CodeStore, Message: "store write failed for a candidate"}
			}
			idx.accept(rec, text, digest)
			if created {
				report.Created++
				report.Items = append(report.Items, ItemResult{
					MemoryID: rec.ID, Kind: candidate.Kind, Source: string(file.Source),
					Label: file.Label, Status: "created",
				})
			} else {
				report.Replayed++
				report.Items = append(report.Items, ItemResult{
					MemoryID: rec.ID, Kind: candidate.Kind, Source: string(file.Source),
					Label: file.Label, Status: "replayed",
				})
			}
		}
	}

	if !opts.DryRun && opts.RepoRoot != "" && report.Status != "failed" {
		if err := WriteManifest(opts.RepoRoot, manifestEntries, time.Now().UTC()); err != nil {
			// The manifest is optional; a failed cache write never fails import.
			report.Status = "partial"
		}
	}
	report.DurationMS = time.Since(start).Milliseconds()
	if opts.DryRun {
		report.Status = "dry_run"
	}
	return report, nil
}

func splitCount(candidates []Candidate) int {
	extra := 0
	for _, candidate := range candidates {
		extra += candidate.Split
	}
	return extra
}

func readBounded(path string, max int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	limited := io.LimitReader(f, max+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return nil, true, nil
	}
	return data, false, nil
}

// WriteManifest rewrites the optional cache atomically with mode 0600. When the
// encoded cache exceeds the bound it drops the oldest entries and records that
// truncation.
func WriteManifest(projectDir string, entries []ManifestEntry, now time.Time) error {
	if projectDir == "" {
		return nil
	}
	manifest := Manifest{Schema: manifestSchema, UpdatedAt: now.UTC().Format(time.RFC3339), Entries: entries}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	for len(raw) > ManifestMaxBytes && len(manifest.Entries) > 0 {
		drop := len(manifest.Entries) / 10
		if drop < 1 {
			drop = 1
		}
		manifest.Entries = manifest.Entries[drop:]
		manifest.Truncated = true
		raw, err = json.Marshal(manifest)
		if err != nil {
			return err
		}
	}
	if len(raw) > ManifestMaxBytes {
		return &ImportError{Code: CodeBound, Message: "manifest exceeds its bound"}
	}
	full := filepath.Join(projectDir, ManifestRelPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ReadManifestStatus reports cache existence and staleness without reading any
// memory source file.
func ReadManifestStatus(projectDir string, now time.Time) ManifestStatus {
	status := ManifestStatus{}
	if projectDir == "" {
		return status
	}
	full := filepath.Join(projectDir, ManifestRelPath)
	info, err := os.Stat(full)
	if err != nil {
		return status
	}
	status.Exists = true
	if info.Size() > ManifestMaxBytes {
		status.Stale = true
		status.Error = "manifest exceeds its bound"
		return status
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		status.Stale = true
		status.Error = "manifest cannot be read"
		return status
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Schema != manifestSchema {
		status.Stale = true
		status.Error = "manifest cannot be parsed"
		return status
	}
	status.Entries = len(manifest.Entries)
	status.UpdatedAt = manifest.UpdatedAt
	updated, err := time.Parse(time.RFC3339, manifest.UpdatedAt)
	if err != nil {
		status.Stale = true
		status.Error = "manifest timestamp is invalid"
		return status
	}
	if now.Sub(updated) > ManifestStaleAfter {
		status.Stale = true
	}
	return status
}
