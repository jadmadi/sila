package store

import (
	"path/filepath"
	"testing"
)

func TestSymbolNotesCRUD(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	proj, err := st.EnsureProject(tmpDir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}
	projID := proj.ID

	note := &SymbolNote{
		ProjectID:     projID,
		CanonicalID:   "github.com/example/engine::Engine.Start",
		SymbolName:    "Start",
		PackagePath:   "github.com/example/engine",
		FilePath:      "engine/engine.go",
		SymbolKind:    "method",
		Receiver:      "Engine",
		SpanStartLine: 25,
		SpanEndLine:   40,
		NoteText:      "Ensure engine lock is held before Start",
		NoteKind:      "invariant",
		GitSHA:        "1234567",
		GitBranch:     "main",
		CommitterDate: "2026-10-06T12:00:00Z",
	}

	if err := st.InsertSymbolNote(note); err != nil {
		t.Fatalf("InsertSymbolNote failed: %v", err)
	}

	// 1. List notes with target filter
	notes, err := st.ListSymbolNotes(projID, "Engine.Start")
	if err != nil {
		t.Fatalf("ListSymbolNotes failed: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note for Engine.Start, got %d", len(notes))
	}
	if notes[0].NoteText != note.NoteText {
		t.Errorf("expected note text %q, got %q", note.NoteText, notes[0].NoteText)
	}

	// 2. Query by file
	byFile, err := st.GetSymbolNotesByFile(projID, "engine/engine.go")
	if err != nil {
		t.Fatalf("GetSymbolNotesByFile failed: %v", err)
	}
	if len(byFile) != 1 {
		t.Errorf("expected 1 note by file, got %d", len(byFile))
	}

	// 3. FTS search
	ftsMatches, err := st.SearchSymbolNotesFTS(projID, "engine lock", 5)
	if err != nil {
		t.Fatalf("SearchSymbolNotesFTS failed: %v", err)
	}
	if len(ftsMatches) != 1 {
		t.Errorf("expected 1 FTS match, got %d", len(ftsMatches))
	}

	// 4. Export & Import
	exported, err := st.ExportSymbolNotes(projID)
	if err != nil {
		t.Fatalf("ExportSymbolNotes failed: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("expected 1 exported note, got %d", len(exported))
	}

	targetProj, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatalf("EnsureProject target failed: %v", err)
	}
	newProjID := targetProj.ID
	importedCount, err := st.ImportSymbolNotes(newProjID, exported)
	if err != nil {
		t.Fatalf("ImportSymbolNotes failed: %v", err)
	}
	if importedCount != 1 {
		t.Errorf("expected 1 imported note, got %d", importedCount)
	}

	targetNotes, err := st.ListSymbolNotes(newProjID, "")
	if err != nil {
		t.Fatalf("ListSymbolNotes on target project failed: %v", err)
	}
	if len(targetNotes) != 1 {
		t.Errorf("expected 1 note in target project, got %d", len(targetNotes))
	}

	// 5. Cross-project vs scoped search
	if err := st.InsertSymbolNote(&SymbolNote{
		ProjectID:   projID,
		CanonicalID: "Engine.Stop",
		SymbolName:  "Stop",
		PackagePath: "pkg/engine",
		FilePath:    "engine/engine.go",
		SymbolKind:  "method",
		Receiver:    "Engine",
		NoteText:    "Always release engine lock before stop",
		NoteKind:    "invariant",
	}); err != nil {
		t.Fatalf("InsertSymbolNote for projID failed: %v", err)
	}

	// Scoped search for projID returns 1 note
	scopedProj1, err := st.SearchSymbolNotesFTS(projID, "engine lock", 5)
	if err != nil {
		t.Fatalf("SearchSymbolNotesFTS projID failed: %v", err)
	}
	if len(scopedProj1) != 1 {
		t.Errorf("expected 1 note for projID, got %d", len(scopedProj1))
	}

	// Scoped search for newProjID returns 1 note
	scopedProj2, err := st.SearchSymbolNotesFTS(newProjID, "engine lock", 5)
	if err != nil {
		t.Fatalf("SearchSymbolNotesFTS newProjID failed: %v", err)
	}
	if len(scopedProj2) != 1 {
		t.Errorf("expected 1 note for newProjID, got %d", len(scopedProj2))
	}

	// Global search with empty projectID matches across projects
	globalFTS, err := st.SearchSymbolNotesFTS("", "engine lock", 5)
	if err != nil {
		t.Fatalf("SearchSymbolNotesFTS global failed: %v", err)
	}
	if len(globalFTS) != 2 {
		t.Errorf("expected 2 global FTS matches (one from each project), got %d", len(globalFTS))
	}

	globalList, err := st.ListSymbolNotes("", "")
	if err != nil {
		t.Fatalf("ListSymbolNotes global failed: %v", err)
	}
	if len(globalList) != 2 {
		t.Errorf("expected 2 global notes across projects, got %d", len(globalList))
	}
}

func TestSymbolNote_UnregisteredProjectAutoProvision(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	// Insert note for a project ID that was never passed to EnsureProject
	note := &SymbolNote{
		ProjectID:   "brand-new-project-never-registered",
		CanonicalID: "pkg/core::Process",
		SymbolName:  "Process",
		NoteText:    "Never mutate input buffer in-place",
		NoteKind:    "invariant",
		FilePath:    "pkg/core/process.go",
	}

	if err := st.InsertSymbolNote(note); err != nil {
		t.Fatalf("InsertSymbolNote on unregistered project failed: %v", err)
	}

	notes, err := st.ListSymbolNotes("brand-new-project-never-registered", "")
	if err != nil {
		t.Fatalf("ListSymbolNotes failed: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].NoteText != "Never mutate input buffer in-place" {
		t.Errorf("unexpected note text: %s", notes[0].NoteText)
	}
}
