package importmem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func openMirrorStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func mirrorCount(t *testing.T, st *store.Store, projectID string) int {
	t.Helper()
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: projectID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return len(records)
}

func writeMirrorFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMirrorOneCreatesAndDeduplicates(t *testing.T) {
	st := openMirrorStore(t)
	dir := t.TempDir()
	path := writeMirrorFixture(t, dir, "note.md", "## Decisions\n\n- keep the ten-tool surface\n")
	opts := ImportOptions{ProjectID: "p", RepoRoot: dir, MirrorSession: "sess-1"}
	target := MirrorTarget{Harness: "claude", Path: path}

	first, err := MirrorOne(context.Background(), st, opts, target)
	if err != nil {
		t.Fatal(err)
	}
	if first.Mirrored < 1 {
		t.Fatalf("first mirror = %+v", first)
	}
	records := mirrorCount(t, st, "p")
	if records < 1 {
		t.Fatal("no records written")
	}
	var env store.MemoryEnvelope
	if err := json.Unmarshal([]byte(mustRecord(t, st, "p").EnvelopeJSON), &env); err != nil {
		t.Fatal(err)
	}
	if env.Provenance == nil || env.Provenance.SessionRef != "sess-1" || env.Provenance.Agent != "claude" {
		t.Fatalf("provenance = %+v", env.Provenance)
	}

	second, err := MirrorOne(context.Background(), st, opts, target)
	if err != nil {
		t.Fatal(err)
	}
	if second.Duplicates < 1 || second.Mirrored != 0 {
		t.Fatalf("second mirror = %+v", second)
	}
	if got := mirrorCount(t, st, "p"); got != records {
		t.Fatalf("records changed: %d -> %d", records, got)
	}
}

func mustRecord(t *testing.T, st *store.Store, projectID string) store.MemoryRecord {
	t.Helper()
	records, err := st.ListMemoryRecords(store.MemoryListOptions{ProjectID: projectID, Limit: 1})
	if err != nil || len(records) == 0 {
		t.Fatalf("records: %v %v", records, err)
	}
	return records[0]
}

func TestMirrorOneChangedContentWritesNew(t *testing.T) {
	st := openMirrorStore(t)
	dir := t.TempDir()
	path := writeMirrorFixture(t, dir, "note.md", "## Decisions\n\n- first decision\n")
	opts := ImportOptions{ProjectID: "p", RepoRoot: dir, MirrorSession: "sess-1"}
	target := MirrorTarget{Harness: "claude", Path: path}
	if _, err := MirrorOne(context.Background(), st, opts, target); err != nil {
		t.Fatal(err)
	}
	before := mirrorCount(t, st, "p")
	writeMirrorFixture(t, dir, "note.md", "## Decisions\n\n- second decision\n")
	if _, err := MirrorOne(context.Background(), st, opts, target); err != nil {
		t.Fatal(err)
	}
	if after := mirrorCount(t, st, "p"); after <= before {
		t.Fatalf("changed content did not add a record: %d -> %d", before, after)
	}
}

func TestMirrorOneCancelledContextWritesNothing(t *testing.T) {
	st := openMirrorStore(t)
	dir := t.TempDir()
	path := writeMirrorFixture(t, dir, "note.md", "## Decisions\n\n- cancelled decision\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := MirrorOne(ctx, st, ImportOptions{ProjectID: "p", RepoRoot: dir}, MirrorTarget{Harness: "claude", Path: path})
	if err == nil {
		t.Fatalf("expected a timeout error, got %+v", res)
	}
	if got := mirrorCount(t, st, "p"); got != 0 {
		t.Fatalf("cancelled mirror wrote %d records", got)
	}
}

func TestMirrorOneEmptyFileSkips(t *testing.T) {
	st := openMirrorStore(t)
	dir := t.TempDir()
	path := writeMirrorFixture(t, dir, "empty.md", "")
	res, err := MirrorOne(context.Background(), st, ImportOptions{ProjectID: "p", RepoRoot: dir}, MirrorTarget{Harness: "grok", Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Code != SkipNoSection {
		t.Fatalf("result = %+v", res)
	}
}
