package importmem

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func newImportStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "import.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newImportProject(t *testing.T, st *store.Store) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	project, err := st.EnsureProject(repo)
	if err != nil {
		t.Fatal(err)
	}
	return repo, project.ID
}

func importOptions(repo, projectID, home string) ImportOptions {
	return ImportOptions{
		ProjectID: projectID, RepoRoot: repo, HomeDir: home, From: "all",
	}
}

func memoryCount(t *testing.T, st *store.Store, projectID string) int {
	t.Helper()
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: projectID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(records)
}

func TestImportIdempotency(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md")
	writeFixture(t, path, "## Decisions\n- use a local SQLite store\n")

	first, err := Run(context.Background(), st, importOptions(repo, projectID, home))
	if err != nil {
		t.Fatal(err)
	}
	if first.Created != 1 || first.Replayed != 0 || first.Candidates != 1 {
		t.Fatalf("first run: %+v", first)
	}
	if memoryCount(t, st, projectID) != 1 {
		t.Fatal("first run did not write one memory")
	}
	records, _ := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: projectID, Limit: 10})
	rec := records[0]
	if rec.Authority != "user_attested" {
		t.Fatalf("authority: %q", rec.Authority)
	}
	if rec.Envelope == nil || rec.Envelope.Provenance == nil || rec.Envelope.Provenance.ToolName != "import" {
		t.Fatalf("provenance: %+v", rec.Envelope)
	}
	if rec.Envelope.Assertions[0].Confidence != 0.85 || rec.Envelope.Assertions[0].Importance != 4 {
		t.Fatalf("assertion caps: %+v", rec.Envelope.Assertions[0])
	}

	second, err := Run(context.Background(), st, importOptions(repo, projectID, home))
	if err != nil {
		t.Fatal(err)
	}
	if second.Created != 0 || second.Replayed != 1 {
		t.Fatalf("replay: %+v", second)
	}
	if memoryCount(t, st, projectID) != 1 {
		t.Fatal("replay created a duplicate row")
	}

	writeFixture(t, path, "## Decisions\n- use a local SQLite store\n- name the database file sila.db\n")
	third, err := Run(context.Background(), st, importOptions(repo, projectID, home))
	if err != nil {
		t.Fatal(err)
	}
	if third.Created != 1 || third.Replayed != 1 {
		t.Fatalf("changed content: %+v", third)
	}
	if memoryCount(t, st, projectID) != 2 {
		t.Fatalf("changed content count = %d", memoryCount(t, st, projectID))
	}
}

func TestImportDuplicateAndSimilar(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	base := filepath.Join(home, ".claude", "projects", "p", "memory", "a.md")
	writeFixture(t, base, "## Decisions\n- use sqlite for durable local storage\n")

	if _, err := Run(context.Background(), st, importOptions(repo, projectID, home)); err != nil {
		t.Fatal(err)
	}

	duplicate := filepath.Join(home, ".codex", "memories", "b.md")
	writeFixture(t, duplicate, "## Decisions\n- use sqlite for durable local storage\n")
	report, err := Run(context.Background(), st, importOptions(repo, projectID, home))
	if err != nil {
		t.Fatal(err)
	}
	if report.Duplicates != 1 || report.Created != 0 {
		t.Fatalf("duplicate run: %+v", report)
	}
	if memoryCount(t, st, projectID) != 1 {
		t.Fatal("exact duplicate was stored")
	}

	similar := filepath.Join(home, ".grok", "memory", "c.md")
	writeFixture(t, similar, "## Decisions\n- use sqlite for durable local storage of data\n")
	report, err = Run(context.Background(), st, importOptions(repo, projectID, home))
	if err != nil {
		t.Fatal(err)
	}
	if report.Similar != 1 || report.Created != 0 {
		t.Fatalf("similar run: %+v", report)
	}
	if memoryCount(t, st, projectID) != 1 {
		t.Fatal("similar memory was stored without --force-similar")
	}

	opts := importOptions(repo, projectID, home)
	opts.ForceSimilar = true
	report, err = Run(context.Background(), st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 1 || report.Duplicates != 1 {
		t.Fatalf("force similar run: %+v", report)
	}
	if memoryCount(t, st, projectID) != 2 {
		t.Fatalf("force similar count = %d", memoryCount(t, st, projectID))
	}
}

func TestRunDryRunZeroWrites(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md"),
		"## Lessons\n- wrap errors with context\n")

	opts := importOptions(repo, projectID, home)
	opts.DryRun = true
	report, err := Run(context.Background(), st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !report.DryRun || report.Status != "dry_run" {
		t.Fatalf("dry-run report: %+v", report)
	}
	if report.Created != 0 || report.Replayed != 0 || report.Planned != 1 || report.Candidates != 1 {
		t.Fatalf("dry-run counts: %+v", report)
	}
	if memoryCount(t, st, projectID) != 0 {
		t.Fatal("dry-run wrote a memory")
	}
	if _, err := os.Stat(filepath.Join(repo, ManifestRelPath)); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a manifest: %v", err)
	}

	report, err = Run(context.Background(), nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Planned != 1 {
		t.Fatalf("nil-store dry run: %+v", report)
	}
}

func TestRunFailureIsolation(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "good.md"),
		"## Conventions\n- keep handlers small\n")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "bad-utf8.md"),
		"## Decisions\n- "+string([]byte{0xff, 0xfe})+"\n")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "nul.md"),
		"## Decisions\n- secret\x00value\n")

	report, err := Run(context.Background(), st, importOptions(repo, projectID, home))
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 1 {
		t.Fatalf("good file not imported: %+v", report)
	}
	codes := map[string]int{}
	for _, skip := range report.FilesSkipped {
		codes[skip.Code]++
	}
	if codes[CodeMalformedFile] != 1 || codes[CodeRedactionFailed] != 1 {
		t.Fatalf("skip codes: %+v", report.FilesSkipped)
	}
	if report.Status != "ok" {
		t.Fatalf("status: %q", report.Status)
	}
}

type failingStore struct {
	*store.Store
}

func (f failingStore) SaveMemoryEnvelope(context.Context, string, *store.MemoryEnvelope, store.Provenance) (*store.MemoryRecord, bool, error) {
	return nil, false, errors.New("disk full")
}

func TestRunStoreFailureAborts(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md"),
		"## Decisions\n- one decision\n")

	report, err := Run(context.Background(), failingStore{Store: st}, importOptions(repo, projectID, home))
	if err == nil || CodeOf(err) != CodeStore {
		t.Fatalf("store error: %v", err)
	}
	if report.Failed != 1 || report.Status != "failed" {
		t.Fatalf("report: %+v", report)
	}
}

func TestRunManifestBounds(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md"),
		"## Fixes\n- check the exit code\n")
	if _, err := Run(context.Background(), st, importOptions(repo, projectID, home)); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(repo, ManifestRelPath)
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode: %o", info.Mode().Perm())
	}
	status := ReadManifestStatus(repo, time.Now().UTC())
	if !status.Exists || status.Stale || status.Entries != 1 {
		t.Fatalf("manifest status: %+v", status)
	}

	var entries []ManifestEntry
	for i := 0; i < 6000; i++ {
		entries = append(entries, ManifestEntry{
			Source: "claude", Label: "claude:repo:" + strings.Repeat("d/", 40) + strings.Repeat("x", 300),
			ContentHash: strings.Repeat("a", 64), Bytes: 1000,
		})
	}
	if err := WriteManifest(repo, entries, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > ManifestMaxBytes {
		t.Fatalf("manifest size = %d", len(raw))
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.Truncated || len(manifest.Entries) == 0 {
		t.Fatalf("manifest truncation: truncated=%v entries=%d", manifest.Truncated, len(manifest.Entries))
	}

	stale := manifest
	stale.UpdatedAt = time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	raw, _ = json.Marshal(stale)
	if err := os.WriteFile(full, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if status := ReadManifestStatus(repo, time.Now().UTC()); !status.Stale {
		t.Fatalf("old manifest not stale: %+v", status)
	}
}

func TestRunInstructionRefusalReport(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "CLAUDE.md"),
		"## Decisions\n- never import an instruction as memory\n")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "keep.md"),
		"## Decisions\n- keep this decision\n")
	report, err := Run(context.Background(), st, importOptions(repo, projectID, home))
	if err != nil {
		t.Fatal(err)
	}
	if report.InstructionsRefused != 1 || report.Created != 1 {
		t.Fatalf("report: %+v", report)
	}
	for _, item := range report.Items {
		if strings.Contains(item.Label, "CLAUDE.md") {
			t.Fatalf("instruction file produced an item: %+v", item)
		}
	}
}

func TestRunBounds(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	writeFixture(t, filepath.Join(home, ".claude", "projects", "p", "memory", "notes.md"),
		"## Decisions\n- one decision\n- two decision\n- three decision\n")

	opts := importOptions(repo, projectID, home)
	opts.MaxTotalBytes = 8
	report, err := Run(context.Background(), st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 0 || report.Status != "partial" {
		t.Fatalf("total-bound report: %+v", report)
	}
	found := false
	for _, skip := range report.FilesSkipped {
		if skip.Code == CodeBound {
			found = true
		}
	}
	if !found {
		t.Fatalf("total-bound skip missing: %+v", report.FilesSkipped)
	}

	opts = importOptions(repo, projectID, home)
	opts.MaxCandidates = 1
	report, err = Run(context.Background(), st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 1 || report.Status != "partial" || report.Splits != 0 {
		t.Fatalf("candidate-bound report: %+v", report)
	}
	if memoryCount(t, st, projectID) != 1 {
		t.Fatalf("candidate bound wrote %d memories", memoryCount(t, st, projectID))
	}
}

func TestImportAgain(t *testing.T) {
	st := newImportStore(t)
	repo, projectID := newImportProject(t, st)
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(home, ".grok", "memory", "notes.md")
	writeFixture(t, path, "## Preferences\n- prefer explicit exit codes\n")

	opts := importOptions(repo, projectID, home)
	opts.Again = true
	if report, err := Run(context.Background(), st, opts); err != nil || report.Created != 1 {
		t.Fatalf("first again run: %+v %v", report, err)
	}
	report, err := Run(context.Background(), st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 0 || report.Replayed != 1 {
		t.Fatalf("unchanged again run: %+v", report)
	}
	if memoryCount(t, st, projectID) != 1 {
		t.Fatal("--again duplicated an unchanged file")
	}

	writeFixture(t, path, "## Preferences\n- prefer explicit exit codes\n- prefer short messages\n")
	report, err = Run(context.Background(), st, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Created != 1 || report.Replayed != 1 {
		t.Fatalf("changed again run: %+v", report)
	}
	if memoryCount(t, st, projectID) != 2 {
		t.Fatalf("changed again count = %d", memoryCount(t, st, projectID))
	}
}

func TestImportNoNetworkSurface(t *testing.T) {
	for _, name := range []string{"discover.go", "redact.go", "parse.go", "import.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, forbidden := range []string{`"net/http"`, `"net/url"`, `"os/exec"`, "internal/llm", "internal/provider"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains forbidden import %q", name, forbidden)
			}
		}
	}
}
