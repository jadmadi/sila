package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestHandoffRevisionCanonical(t *testing.T) {
	first := Handoff{ProjectID: "p", Summary: "s", OpenItems: []string{"b", "a"}, Decisions: []string{"d2", "d1"}, RepoFiles: []string{"z.go", "a.go"}, Status: "open"}
	second := Handoff{ProjectID: "p", Summary: "s", OpenItems: []string{"a", "b"}, Decisions: []string{"d1", "d2"}, RepoFiles: []string{"a.go", "z.go"}, Status: "open"}
	if HandoffRevision(first) != HandoffRevision(second) {
		t.Fatal("reordered slices changed the hash")
	}
	third := second
	third.Summary = "changed"
	if HandoffRevision(first) == HandoffRevision(third) {
		t.Fatal("summary change did not change the hash")
	}
	empty := Handoff{ProjectID: "p", Status: "open"}
	nilItems := Handoff{ProjectID: "p", Status: "open", OpenItems: nil, Decisions: []string{}}
	if HandoffRevision(empty) != HandoffRevision(nilItems) {
		t.Fatal("nil and empty slices differ")
	}
	hash := HandoffRevision(first)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Fatalf("hash = %q", hash)
	}
}

func TestMemoryRecordRevisionCanonical(t *testing.T) {
	env := &MemoryEnvelope{
		SchemaVersion: "sila/memory/v1", Kind: "memo", Title: "t", Summary: "s",
		Assertions: []MemoryAssertion{{ID: "A-001", AssertionType: "fact", Statement: "x", EvidenceRefs: []string{"E-2", "E-1"}}},
		Evidence: []MemoryEvidence{
			{ID: "E-1", Kind: "file", Locator: MemoryLocator{Path: "a.go"}, Summary: "a"},
			{ID: "E-2", Kind: "file", Locator: MemoryLocator{Path: "b.go"}, Summary: "b"},
		},
	}
	reordered := *env
	reordered.Evidence = []MemoryEvidence{env.Evidence[1], env.Evidence[0]}
	reordered.Assertions = []MemoryAssertion{{ID: "A-001", AssertionType: "fact", Statement: "x", EvidenceRefs: []string{"E-1", "E-2"}}}
	first, err := MemoryRecordRevision(MemoryRecord{ID: "m", Envelope: env})
	if err != nil {
		t.Fatal(err)
	}
	second, err := MemoryRecordRevision(MemoryRecord{ID: "m", Envelope: &reordered})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatalf("hashes = %q %q", first, second)
	}
	changed := *env
	changed.Title = "other"
	third, _ := MemoryRecordRevision(MemoryRecord{ID: "m", Envelope: &changed})
	if third == first {
		t.Fatal("title change did not change the hash")
	}
}

func TestMemoryRevisionIncludesState(t *testing.T) {
	env := &MemoryEnvelope{SchemaVersion: "sila/memory/v1", Kind: "memo", Title: "t", Summary: "s"}
	active, _ := MemoryRecordRevision(MemoryRecord{ID: "m", Envelope: env, State: "current"})
	archived, _ := MemoryRecordRevision(MemoryRecord{ID: "m", Envelope: env, State: "archived"})
	if active == archived {
		t.Fatal("state change did not change the hash")
	}
}

func TestMemoryRevisionOwner(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "mr.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	env := &MemoryEnvelope{SchemaVersion: MemorySchemaVersion, ProjectID: "p", Kind: "memo", Title: "t", Summary: "s",
		Provenance: &MemoryProvenance{Agent: "claude"}}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.DB.Exec(`INSERT INTO memory_records (id, project_id, fact_id, kind, title, summary, digest, envelope_json, state, created_at, updated_at)
		VALUES ('m1','p','f1','memo','t','s','digest',?, 'current', ?, ?)`, string(raw), now, now); err != nil {
		t.Fatal(err)
	}
	revision, owner, exists, err := st.MemoryRevision(ctx, "m1")
	if err != nil || !exists || len(revision) != 64 || owner != "claude" {
		t.Fatalf("rev=%q owner=%q exists=%v err=%v", revision, owner, exists, err)
	}
	if _, _, exists, err := st.MemoryRevision(ctx, "missing"); err != nil || exists {
		t.Fatalf("missing exists=%v err=%v", exists, err)
	}
}
