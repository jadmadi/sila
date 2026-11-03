package onboard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestSaveAndLoadDeferrals(t *testing.T) {
	tmp := t.TempDir()

	// Initial load should be empty
	defs, err := LoadDeferrals(tmp)
	if err != nil {
		t.Fatalf("LoadDeferrals failed: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected 0 deferrals, got %d", len(defs))
	}

	// Save first deferral
	rec, err := SaveDeferral(tmp, "TODO.md", "Migrated to Jira", "tester")
	if err != nil {
		t.Fatalf("SaveDeferral failed: %v", err)
	}
	if rec.Surface != "TODO.md" || rec.Reason != "Migrated to Jira" {
		t.Fatalf("unexpected record: %+v", rec)
	}

	// Read back
	defs, err = LoadDeferrals(tmp)
	if err != nil {
		t.Fatalf("LoadDeferrals failed: %v", err)
	}
	if len(defs) != 1 || defs[0].Surface != "TODO.md" {
		t.Fatalf("expected 1 deferral for TODO.md, got %+v", defs)
	}

	// Overwrite/update reason
	_, err = SaveDeferral(tmp, "TODO.md", "Tracked in Linear instead", "tester2")
	if err != nil {
		t.Fatalf("SaveDeferral update failed: %v", err)
	}
	defs, err = LoadDeferrals(tmp)
	if err != nil {
		t.Fatalf("LoadDeferrals after update failed: %v", err)
	}
	if len(defs) != 1 || defs[0].Reason != "Tracked in Linear instead" {
		t.Fatalf("expected updated reason, got %+v", defs)
	}
}

func TestIsSurfaceDeferred(t *testing.T) {
	defs := []Deferral{
		{Surface: "TODO.md", Reason: "in Linear"},
		{Surface: "brain", Reason: "archived brain"},
	}

	// Exact match
	if ok, r := IsSurfaceDeferred("TODO.md", defs); !ok || r != "in Linear" {
		t.Fatalf("expected TODO.md to be deferred with 'in Linear', got ok=%v, r=%q", ok, r)
	}
	if ok, _ := IsSurfaceDeferred("/path/to/TODO.md", defs); !ok {
		t.Fatalf("expected path to TODO.md to match base name")
	}

	// Generic kind "brain"
	if ok, r := IsSurfaceDeferred(".brain", defs); !ok || r != "archived brain" {
		t.Fatalf("expected .brain to match 'brain' deferral, got ok=%v, r=%q", ok, r)
	}
	if ok, r := IsSurfaceDeferred("brain.json", defs); !ok || r != "archived brain" {
		t.Fatalf("expected brain.json to match 'brain' deferral, got ok=%v, r=%q", ok, r)
	}

	// Non-matching
	if ok, _ := IsSurfaceDeferred("TASKS.md", defs); ok {
		t.Fatalf("TASKS.md should not be deferred")
	}

	// Generic "all"
	allDefs := []Deferral{{Surface: "all", Reason: "ignoring legacy"}}
	if ok, r := IsSurfaceDeferred("anything.md", allDefs); !ok || r != "ignoring legacy" {
		t.Fatalf("expected all to match any surface")
	}
}

func TestProbeWithDeferral(t *testing.T) {
	tmp := t.TempDir()

	// Create legacy surfaces
	if err := os.WriteFile(filepath.Join(tmp, "TODO.md"), []byte("- [ ] item 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "brain.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	rep, err := Probe(tmp)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	// TODO.md + brain.json = 2 unmanaged legacy surfaces
	if rep.CountLegacyUnmanaged() != 2 {
		t.Fatalf("expected 2 unmanaged legacy surfaces before deferral, got %d", rep.CountLegacyUnmanaged())
	}

	// Defer TODO.md
	if _, err := SaveDeferral(tmp, "TODO.md", "deferring todo", "agent"); err != nil {
		t.Fatal(err)
	}

	rep2, err := Probe(tmp)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if rep2.CountLegacyUnmanaged() != 1 {
		t.Fatalf("expected 1 unmanaged legacy surface after deferring TODO.md, got %d", rep2.CountLegacyUnmanaged())
	}
	if rep2.Counts["deferred"] != 1 {
		t.Fatalf("expected 1 deferred count, got %d", rep2.Counts["deferred"])
	}

	// Defer brain
	if _, err := SaveDeferral(tmp, "brain", "deferring brain", "agent"); err != nil {
		t.Fatal(err)
	}

	rep3, err := Probe(tmp)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}
	if rep3.CountLegacyUnmanaged() != 0 {
		t.Fatalf("expected 0 unmanaged legacy surfaces after deferring brain, got %d", rep3.CountLegacyUnmanaged())
	}
	if rep3.Counts["deferred"] != 2 {
		t.Fatalf("expected 2 deferred count, got %d", rep3.Counts["deferred"])
	}
}

func TestDeferSurfaceWithStore(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// Create legacy TODO.md and goals dir
	if err := os.WriteFile(filepath.Join(tmp, "TODO.md"), []byte("- [ ] task\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "goals"), 0755); err != nil {
		t.Fatal(err)
	}

	// Defer surface via DeferSurface
	rec, err := DeferSurface(st, tmp, p.ID, "TODO.md", "accepted risk", "tester")
	if err != nil {
		t.Fatalf("DeferSurface failed: %v", err)
	}
	if rec.Surface != "TODO.md" {
		t.Fatalf("expected TODO.md, got %s", rec.Surface)
	}

	// Check store memos
	storeDefs := LoadStoreDeferrals(st, p.ID)
	if len(storeDefs) != 1 || storeDefs[0].Surface != "TODO.md" {
		t.Fatalf("expected 1 store deferral, got %+v", storeDefs)
	}

	// Verify Check passes
	chk := Check(st, tmp, p.ID, false)
	foundLegacy := false
	for _, item := range chk.Items {
		if item.Name == "Legacy inventory" {
			foundLegacy = true
			if item.Status != "pass" {
				t.Fatalf("expected Legacy inventory pass, got %s (%s)", item.Status, item.Detail)
			}
		}
	}
	if !foundLegacy {
		t.Fatalf("expected Legacy inventory item in check report")
	}
}
