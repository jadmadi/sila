package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestSyncAndReconcileDryRun(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(nil, true)

	// 1. Test runSync with dryRun=true (preview)
	runSync(ui, dbPath, p.ID, false, false, true, false, false, false, "15s", time.Time{}, false, false, nil)

	// 2. Test runSync with dryRun=true and jsonOut=true
	runSync(ui, dbPath, p.ID, false, false, true, false, true, false, "15s", time.Time{}, false, false, nil)

	// 3. Test runReconcile with dryRun=true
	stRO, err := store.OpenRO(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stRO.Close()

	runReconcile(ui, stRO, p.ID, true, false, false, false)
	runReconcile(ui, stRO, p.ID, true, false, true, false)
}

func TestContextSurfaceInPrime(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()

	ui := render.New(nil, true)
	// runPrime with top=5 and check it executes cleanly
	runPrime(ui, dbPath, p.ID, true, false, 5, "")
}
