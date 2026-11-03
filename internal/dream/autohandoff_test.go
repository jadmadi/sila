package dream

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestBuildAutoHandoffWritesOnceAndIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "auto.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	first, err := BuildAutoHandoff(st, p.ID, tmp, "sess-auto-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Written || first.Existing || first.HandoffID == "" {
		t.Fatalf("expected a written handoff: %+v", first)
	}
	handoff, err := st.LatestHandoff(p.ID)
	if err != nil || handoff == nil {
		t.Fatalf("latest handoff: %v %v", handoff, err)
	}
	if handoff.Agent != "sila-hook" || handoff.SessionID != "sess-auto-1" {
		t.Fatalf("wrong handoff provenance: %+v", handoff)
	}
	if !strings.Contains(handoff.Summary, "sila-hook session handoff") {
		t.Fatalf("unexpected summary: %s", handoff.Summary)
	}

	second, err := BuildAutoHandoff(st, p.ID, tmp, "sess-auto-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Written || !second.Existing || second.HandoffID != first.HandoffID {
		t.Fatalf("second call should be idempotent: %+v", second)
	}
}

func TestBuildAutoHandoffDoesNotSupersedeAnotherAgent(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "other.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: p.ID,
		Agent:     "another-agent",
		Summary:   "pending work from another agent",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := BuildAutoHandoff(st, p.ID, tmp, "sess-auto-2", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Written || !res.Existing {
		t.Fatalf("auto-handoff must not supersede another agent: %+v", res)
	}
	latest, _ := st.LatestHandoff(p.ID)
	if latest == nil || latest.Agent != "another-agent" {
		t.Fatalf("pending handoff was replaced: %+v", latest)
	}
}

func TestBuildAutoHandoffDryRun(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "dry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	res, err := BuildAutoHandoff(st, p.ID, tmp, "sess-auto-3", "dry run summary", true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Written || res.HandoffID == "" || !strings.Contains(res.Reason, "dry-run") {
		t.Fatalf("unexpected dry-run result: %+v", res)
	}
	if h, _ := st.LatestHandoff(p.ID); h != nil {
		t.Fatalf("dry-run wrote a handoff: %+v", h)
	}
}
