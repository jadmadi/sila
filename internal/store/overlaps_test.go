package store

import (
	"path/filepath"
	"testing"
)

func TestOverlapsTableMigrated(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB.Exec(`SELECT COUNT(*) FROM overlaps`); err != nil {
		t.Fatalf("overlaps table not migrated: %v", err)
	}
}

func TestRecordOverlapIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	o := Overlap{ProjectID: "p", MsgA: "bb", MsgB: "aa", ToolsA: []string{"devin"}, ToolsB: []string{"mimocode"}, Similarity: 0.8}
	if err := s.RecordOverlap(o); err != nil {
		t.Fatal(err)
	}
	o.Similarity = 0.9 // re-record should update, not duplicate
	if err := s.RecordOverlap(o); err != nil {
		t.Fatal(err)
	}
	got, err := s.Overlaps("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 overlap after idempotent record, got %d", len(got))
	}
	if got[0].Similarity != 0.9 {
		t.Fatalf("similarity should have updated to 0.9, got %v", got[0].Similarity)
	}
}

func TestMessagesNearWindow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.WriteRun("", "devin", "9", []LoadMessage{
		{ProjectID: "p", ContentHash: "h1", TS: "2026-08-01T00:05:00Z", Role: "assistant", Content: "hello", Tool: "devin"},
		{ProjectID: "p", ContentHash: "h2", TS: "2026-08-01T00:09:00Z", Role: "assistant", Content: "other", Tool: "mimocode"},
		{ProjectID: "q", ContentHash: "h3", TS: "2026-08-01T00:06:00Z", Role: "assistant", Content: "other-proj", Tool: "devin"},
	})

	got, err := s.MessagesNear("p", "assistant", "2026-08-01T00:04:00Z", "2026-08-01T00:06:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "h1" || len(got[0].Tools) != 1 || got[0].Tools[0] != "devin" {
		t.Fatalf("window query wrong: %+v", got)
	}
}
