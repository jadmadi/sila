package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func bundleFixture(t *testing.T) (string, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "b.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(query, args...); err != nil {
			t.Fatalf("%v\n%s", err, query)
		}
	}
	exec(`INSERT INTO handoffs (id, project_id, agent, summary, created_at, status) VALUES
		('h1','p','agent-a','deploy with password=hunter2','2026-11-06T09:00:00Z','open')`)
	exec(`INSERT INTO session_cards (id, project_id, session_id, agent, payload_json, created_at) VALUES
		('c1','p','s1','agent-a','{"schema":"sila/session-card/v1"}',1762419600)`)
	exec(`INSERT INTO memory_records (id, project_id, fact_id, kind, title, summary, digest, envelope_json, created_at, updated_at) VALUES
		('m1','p','f1','memo','decision','keep the surface small','d1','{}','2026-11-06T09:10:00Z','2026-11-06T09:10:00Z')`)
	exec(`INSERT INTO goal_audit_receipts (project_id, goal_slug, commit_sha, spec_hash, sandbox_nonce, ccdsi_score, certified_at)
		VALUES ('p','goal-x','abcdef0123456789','spec','nonce',0.83,'2026-11-06T09:20:00Z')`)
	return dbPath, st
}

func TestContinuityBundleLayoutAndManifest(t *testing.T) {
	dbPath, _ := bundleFixture(t)
	out := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() {
		runExportContinuity(render.New(os.Stdout, true), dbPath, "p", out, false, true)
	})
	for _, rel := range []string{"README.md", "manifest.json", "handoffs/h1.md", "cards/c1.md", "memories/m1.md", "goals.md"} {
		if _, err := os.Stat(filepath.Join(out, rel)); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest continuityBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != continuityBundleSchema || manifest.Counts["handoffs"] != 1 || manifest.Counts["cards"] != 1 || manifest.Counts["memories"] != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	for rel, want := range manifest.Files {
		body, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		sum := sha256.Sum256(body)
		if "sha256:"+hex.EncodeToString(sum[:]) != want {
			t.Fatalf("%s hash mismatch", rel)
		}
	}
}

func TestContinuityBundleRedactsSecrets(t *testing.T) {
	dbPath, _ := bundleFixture(t)
	out := filepath.Join(t.TempDir(), "bundle")
	captureStdout(t, func() {
		runExportContinuity(render.New(os.Stdout, true), dbPath, "p", out, false, true)
	})
	body, _ := os.ReadFile(filepath.Join(out, "handoffs", "h1.md"))
	if strings.Contains(string(body), "hunter2") {
		t.Fatalf("secret leaked: %s", body)
	}
}

func TestContinuityBundleDeterministic(t *testing.T) {
	t.Setenv("SILA_BUNDLE_TIME", time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC).Format(time.RFC3339))
	dbPath, _ := bundleFixture(t)
	dir := t.TempDir()
	first := filepath.Join(dir, "a")
	second := filepath.Join(dir, "b")
	captureStdout(t, func() {
		runExportContinuity(render.New(os.Stdout, true), dbPath, "p", first, false, true)
		runExportContinuity(render.New(os.Stdout, true), dbPath, "p", second, false, true)
	})
	err := filepath.Walk(first, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(first, path)
		other, readErr := os.ReadFile(filepath.Join(second, rel))
		if readErr != nil {
			t.Fatalf("%s: %v", rel, readErr)
		}
		body, _ := os.ReadFile(path)
		if string(body) != string(other) {
			t.Fatalf("%s differs between exports", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestContinuityDestRefused(t *testing.T) {
	dir := t.TempDir()
	if continuityDestRefused(dir, false) {
		t.Fatal("empty dir refused")
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !continuityDestRefused(dir, false) {
		t.Fatal("non-empty dir accepted")
	}
	if continuityDestRefused(dir, true) {
		t.Fatal("--force did not override")
	}
}
