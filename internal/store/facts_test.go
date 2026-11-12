package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFactsTableMigrated(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.DB.Exec(`SELECT COUNT(*) FROM facts`); err != nil {
		t.Fatalf("facts table not migrated: %v", err)
	}
}

func TestFactsUpsertIdempotent(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f := Fact{
		ID: "fact_abc", ProjectID: "p", Claim: "x", Confidence: "mid",
		Verified: false, RepoFiles: []string{"a.go"}, Tools: []string{"devin"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	}
	if err := s.UpsertFact(f); err != nil {
		t.Fatal(err)
	}
	// update in place (flip verified + add a tool)
	f.Verified = true
	f.Tools = []string{"devin", "mimocode"}
	f.Confidence = "high"
	if err := s.UpsertFact(f); err != nil {
		t.Fatal(err)
	}

	got, err := s.FactsByProject("p")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 fact after upsert, got %d", len(got))
	}
	if !got[0].Verified || got[0].Confidence != "high" {
		t.Fatalf("upsert should have updated verified/confidence: %+v", got[0])
	}
	if len(got[0].Tools) != 2 {
		t.Fatalf("expected 2 tools after update, got %v", got[0].Tools)
	}
	if len(got[0].RepoFiles) != 1 || got[0].RepoFiles[0] != "a.go" {
		t.Fatalf("repo_files malformed: %v", got[0].RepoFiles)
	}
}

func TestSearchFactsFTS(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	f1 := Fact{
		ID: "fact_1", ProjectID: "sila", Claim: "Use pure-Go SQLite with modernc.org/sqlite to avoid CGO issues",
		Confidence: "high", Verified: true, RepoFiles: []string{"go.mod"}, Tools: []string{"devin"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	}
	f2 := Fact{
		ID: "fact_2", ProjectID: "sila", Claim: "Watermarks are written inside the state table in the same transaction",
		Confidence: "mid", Verified: true, RepoFiles: []string{"internal/store/store.go"}, Tools: []string{"mimocode"},
		SessionRef: "s2", FirstSeen: "2026-08-02T00:00:00Z", PromotedAt: "2026-08-02T00:00:00Z",
	}
	if err := s.UpsertFact(f1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertFact(f2); err != nil {
		t.Fatal(err)
	}

	// Match on term "sqlite"
	res, err := s.SearchFactsFTS("sqlite", "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "fact_1" {
		t.Fatalf("expected 1 result for sqlite, got %v", res)
	}

	// Match on prefix "watermark*"
	res, err = s.SearchFactsFTS("watermark", "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "fact_2" {
		t.Fatalf("expected 1 result for watermark, got %v", res)
	}

	// Hashed claim mutation is rejected; append a corrected fact instead and
	// verify that the new claim is searchable.
	f1.Claim = "Use pure-Go SQLite with modernc.org/sqlite and WAL mode"
	if err := s.UpsertFact(f1); !errors.Is(err, ErrFactImmutable) {
		t.Fatalf("in-place claim update error = %v, want ErrFactImmutable", err)
	}
	f3 := Fact{
		ID: "fact_3", ProjectID: "sila", Claim: "Use pure-Go SQLite with modernc.org/sqlite and WAL mode",
		Confidence: "high", Verified: true, RepoFiles: []string{"go.mod"}, Tools: []string{"devin"},
		SessionRef: "s3", FirstSeen: "2026-08-03T00:00:00Z", PromotedAt: "2026-08-03T00:00:00Z",
	}
	if err := s.UpsertFact(f3); err != nil {
		t.Fatal(err)
	}
	res, err = s.SearchFactsFTS("WAL", "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "fact_3" {
		t.Fatalf("expected FTS to match appended claim, got %v", res)
	}

	// Delete fact_1 and verify FTS removes it
	if err := s.PruneFacts("sila", []string{"fact_2"}); err != nil {
		t.Fatal(err)
	}
	res, err = s.SearchFactsFTS("sqlite", "", false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("expected 0 results for deleted fact, got %v", res)
	}
}

func TestSearchMessagesFTS(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, _, err = s.WriteRun("", "devin", "1", []LoadMessage{
		{ProjectID: "p1", ContentHash: "h1", TS: "2026-08-01T00:00:00Z", Role: "assistant", Content: "Configured FTS5 virtual tables with unicode61 tokenizer", Tool: "devin"},
		{ProjectID: "p1", ContentHash: "h2", TS: "2026-08-01T00:01:00Z", Role: "user", Content: "How do we benchmark retrieval performance?", Tool: "devin"},
		{ProjectID: "p1", ContentHash: "h3", TS: "2026-08-02T00:00:00Z", Role: "assistant", Content: "Benchmark results are optimal with mimocode", Tool: "mimocode"},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.SearchMessagesFTS("tokenizer", SearchMessagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ContentHash != "h1" {
		t.Fatalf("expected 1 result for tokenizer, got %v", res)
	}

	res, err = s.SearchMessagesFTS("benchmark", SearchMessagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results for benchmark, got %d", len(res))
	}

	// Filter by Role
	resRole, err := s.SearchMessagesFTS("benchmark", SearchMessagesOptions{Role: "user"})
	if err != nil || len(resRole) != 1 || resRole[0].ContentHash != "h2" {
		t.Fatalf("expected 1 user message, got %v (err: %v)", resRole, err)
	}

	// Filter by Tool
	resTool, err := s.SearchMessagesFTS("benchmark", SearchMessagesOptions{Tool: "mimocode"})
	if err != nil || len(resTool) != 1 || resTool[0].ContentHash != "h3" {
		t.Fatalf("expected 1 mimocode message, got %v (err: %v)", resTool, err)
	}

	// Filter by Since
	resSince, err := s.SearchMessagesFTS("benchmark", SearchMessagesOptions{Since: "2026-08-02T00:00:00Z"})
	if err != nil || len(resSince) != 1 || resSince[0].ContentHash != "h3" {
		t.Fatalf("expected 1 message since 2026-08-02, got %v", resSince)
	}

	// Pagination (Offset)
	resOffset, err := s.SearchMessagesFTS("benchmark", SearchMessagesOptions{Limit: 1, Offset: 1})
	if err != nil || len(resOffset) != 1 {
		t.Fatalf("expected 1 offset result, got %v", resOffset)
	}
}

func TestSearchFactsFTSProjectCaseInsensitiveAndPartial(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.EnsureProject("/home/user/github/example-app-App"); err != nil {
		t.Fatal(err)
	}
	f := Fact{
		ID: "fact_almatjar", ProjectID: "example-app-App", Claim: "Auth route uses refresh tokens with 7d expiry",
		Confidence: "high", Verified: true, RepoFiles: []string{"auth.go"}, Tools: []string{"devin"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	}
	if err := s.UpsertFact(f); err != nil {
		t.Fatal(err)
	}

	selectors := []string{"example-app-App", "almatjar-app", "/home/user/github/example-app-App"}
	for _, sel := range selectors {
		// Test FTS search with project selector
		ftsRes, err := s.SearchFactsFTS("tokens", sel, false, 0)
		if err != nil {
			t.Fatalf("selector %q failed: %v", sel, err)
		}
		if len(ftsRes) != 1 || ftsRes[0].ID != "fact_almatjar" {
			t.Fatalf("SearchFactsFTS selector %q failed to match, got %v", sel, ftsRes)
		}

		// Test FactsByProject with project selector
		byProjRes, err := s.FactsByProject(sel)
		if err != nil {
			t.Fatalf("FactsByProject selector %q failed: %v", sel, err)
		}
		if len(byProjRes) != 1 || byProjRes[0].ID != "fact_almatjar" {
			t.Fatalf("FactsByProject selector %q failed to match, got %v", sel, byProjRes)
		}
	}
}

func TestReconcileFactDrift(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "k.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	projDir := filepath.Join(tmpDir, "my-repo")
	os.MkdirAll(projDir, 0o755)
	fileA := filepath.Join(projDir, "main.go")
	os.WriteFile(fileA, []byte("package main"), 0o644)

	f := Fact{
		ID: "fact_drift", ProjectID: "my-repo", Claim: "Main entry point in main.go",
		Confidence: "high", Verified: true, RepoFiles: []string{"main.go"}, Tools: []string{"devin", "mimocode"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	}
	if err := s.UpsertFact(f); err != nil {
		t.Fatal(err)
	}

	// Delete main.go from disk to simulate drift
	os.Remove(fileA)

	res, err := s.ReconcileFactDrift("my-repo", projDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewlyStale != 1 {
		t.Fatalf("expected 1 fact newly stale, got %+v", res)
	}

	facts, _ := s.FactsByProject("my-repo")
	if len(facts) != 1 || facts[0].FileRefsExist || facts[0].Corroboration != "grounded" {
		t.Fatalf("expected fact to be downgraded to file_refs_exist=false, corroboration=grounded: %+v", facts[0])
	}

	// Recreate main.go and reconcile back
	os.WriteFile(fileA, []byte("package main\nfunc main() {}"), 0o644)
	res, err = s.ReconcileFactDrift("my-repo", projDir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Recovered != 1 {
		t.Fatalf("expected 1 fact recovered back, got %+v", res)
	}
	facts, _ = s.FactsByProject("my-repo")
	if len(facts) != 1 || !facts[0].FileRefsExist || facts[0].Corroboration != "corroborated" {
		t.Fatalf("expected fact to be restored to file_refs_exist=true, corroboration=corroborated: %+v", facts[0])
	}
}

func TestOpenRO(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "k.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s.UpsertFact(Fact{
		ID: "fact_ro", ProjectID: "p", Claim: "RO Claim", Confidence: "mid",
		Verified: false, RepoFiles: []string{}, Tools: []string{"devin"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	})
	s.Close()

	roStore, err := OpenRO(dbPath)
	if err != nil {
		t.Fatalf("OpenRO failed: %v", err)
	}
	defer roStore.Close()

	facts, err := roStore.FactsAll()
	if err != nil {
		t.Fatalf("FactsAll failed on RO store: %v", err)
	}
	if len(facts) != 1 || facts[0].ID != "fact_ro" {
		t.Fatalf("unexpected facts from RO store: %+v", facts)
	}

	// Attempting write on RO store should fail
	err = roStore.UpsertFact(Fact{ID: "fail_write", ProjectID: "p", Claim: "fail"})
	if err == nil {
		t.Fatal("expected write to fail on read-only store handle")
	}
}

func TestMigrationLedger(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "migrate.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var version int
	if err := s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != CurrentSchemaVersion() {
		t.Fatalf("expected PRAGMA user_version = %d, got %d", CurrentSchemaVersion(), version)
	}
}

func TestSupportedSchemaVersionsMigrateForward(t *testing.T) {
	for schema := MinimumSupportedSchemaVersion(); schema < CurrentSchemaVersion(); schema++ {
		t.Run(fmt.Sprintf("v%d", schema), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(fmt.Sprintf("PRAGMA user_version=%d", schema)); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			got, err := s.SchemaVersion()
			if err != nil || got != CurrentSchemaVersion() {
				t.Fatalf("schema=%d, err=%v", got, err)
			}
		})
	}
}

func TestMatchingProjectIDsIsExactAndRejectsAmbiguity(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "projects.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.EnsureProject("/work/sila"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureProject("/work/sila-old"); err != nil {
		t.Fatal(err)
	}
	if ids, err := s.MatchingProjectIDs("sila"); err != nil || len(ids) != 1 || ids[0] != "sila" {
		t.Fatalf("exact ID should resolve only sila, got %v, %v", ids, err)
	}
	if facts, err := s.FactsByProject("sil"); err != nil || len(facts) != 0 {
		t.Fatalf("partial selector must not match registered project data, got %v, %v", facts, err)
	}
	if _, err := s.EnsureProject("/other/sila"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MatchingProjectIDs("sila"); err != nil { // canonical exact ID still wins
		t.Fatalf("exact ID must take precedence: %v", err)
	}
}

func TestOpenSecuresExistingDatabasePermissions(t *testing.T) {
	// An arbitrary --db parent is not sila's directory: the database file is
	// secured, but the operator's directory mode is left alone.
	base := t.TempDir()
	t.Setenv("SILA_DB", "")
	path := filepath.Join(base, "private", "sila.db")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("database mode = %04o, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0755 {
		t.Fatalf("foreign --db parent mode = %04o, want unchanged 0755", di.Mode().Perm())
	}
}

func TestOpenSecuresOwnedDirectory(t *testing.T) {
	// The directory sila owns is tightened to 0700 on open.
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("SILA_DB", "")

	path := filepath.Join(dataHome, "sila", "sila.db")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0700 {
		t.Fatalf("owned directory mode = %04o, want 0700", di.Mode().Perm())
	}
}

func TestVisionFactsAndNonGoals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vision.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, err := s.InsertNonGoal("sila", "No cloud database dependencies (pure-Go SQLite only)", "agent"); err != nil {
		t.Fatalf("InsertNonGoal failed: %v", err)
	}

	if _, err := s.InsertMilestone("sila", "Support live agent swarm memory and standup digest", "agent"); err != nil {
		t.Fatalf("InsertMilestone failed: %v", err)
	}

	vFacts, err := s.VisionFacts("sila")
	if err != nil {
		t.Fatalf("VisionFacts failed: %v", err)
	}
	if len(vFacts) < 2 {
		t.Fatalf("expected at least 2 vision facts, got %d", len(vFacts))
	}

	var foundNonGoal, foundMilestone bool
	for _, f := range vFacts {
		if f.IsNonGoal() {
			foundNonGoal = true
		}
		if f.IsMilestone() {
			foundMilestone = true
		}
	}
	if !foundNonGoal || !foundMilestone {
		t.Fatalf("missing non-goal or milestone in vision facts: %+v", vFacts)
	}
}

// TestTopFactsAuthorityGates pins the prime-tier contract: an invariant or a
// distilled, corroborated, file-grounded fact is a fact; a claim that merely
// quotes a real file is a claim, not a fact; a claim referencing nothing is
// dropped from the briefing entirely (it belongs to the reviewable queue).
func TestTopFactsAuthorityGates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tiers.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Create a real file so file_refs_exist is genuinely true on disk.
	realFile := filepath.Join(t.TempDir(), "real.go")
	if err := os.WriteFile(realFile, []byte("package p\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	seeds := []Fact{
		{
			ID: "f_invariant", ProjectID: "p", Claim: "Decision: ship pure-Go only",
			Kind: "invariant", Confidence: "invariant", Corroboration: "invariant",
			SessionRef: "manual_memo", ReviewStatus: ReviewApproved, DistillStatus: "distilled",
			Distilled: "Sila ships as a single pure-Go binary.",
		},
		{
			ID: "f_distilled", ProjectID: "p", Claim: "raw observation about the loader",
			Confidence: "corroborated", Corroboration: "corroborated",
			RepoFiles: []string{realFile}, FileRefsExist: true, Verified: true,
			SessionRef: "sess-1", ReviewStatus: ReviewApproved,
			DistillStatus: "distilled", Distilled: "The loader strips harness envelopes.",
		},
		{
			// Quotes a real file but was never corroborated or distilled: the
			// exact case that used to be labeled a "Key Architectural Fact".
			ID: "f_fileonly", ProjectID: "p", Claim: "Both records are now in place. Here is the action list.",
			RepoFiles: []string{realFile}, FileRefsExist: true,
			SessionRef: "sess-2", ReviewStatus: ReviewApproved, DistillStatus: "pending",
		},
		{
			// References nothing on disk: conversation noise.
			ID: "f_ungrounded", ProjectID: "p", Claim: "Let me think about this differently.",
			SessionRef: "sess-3", ReviewStatus: ReviewApproved, DistillStatus: "pending",
		},
		{
			// A memo a person wrote, naming a file that is not on disk. It is
			// authorized knowledge rather than session noise, so it must reach
			// the briefing even though IsFact() is false without grounding.
			ID: "f_memo", ProjectID: "p", Claim: "JWT auth is configured in lib/auth.ts",
			RepoFiles:  []string{"lib/auth.ts"},
			SessionRef: "manual_memo", ReviewStatus: ReviewApproved,
			DistillStatus: "distilled", Distilled: "JWT auth is configured in lib/auth.ts",
			Confidence: "corroborated", Corroboration: "corroborated",
		},
		{
			ID: "f_candidate", ProjectID: "p", Claim: "Rule: maybe checkpoint the WAL",
			Kind: "invariant", Confidence: "invariant", Corroboration: "invariant",
			SessionRef: "sess-4", ReviewStatus: ReviewCandidate, DistillStatus: "pending",
		},
	}
	for _, f := range seeds {
		if err := s.UpsertFact(f); err != nil {
			t.Fatalf("upsert %s: %v", f.ID, err)
		}
	}

	got, err := s.TopFactsForProject("p", 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
	}

	// f_ungrounded and f_candidate must never reach the briefing.
	for _, id := range ids {
		if id == "f_ungrounded" {
			t.Fatal("ungrounded claim reached the prime tier; it belongs to the candidate queue")
		}
		if id == "f_candidate" {
			t.Fatal("candidate reached the prime tier")
		}
	}
	if len(ids) != 4 {
		t.Fatalf("prime tier = %v, want the invariant, the memo, the distilled fact, and the file-referenced claim", ids)
	}
	// Authority precedes claims, so the file-referenced claim lands last.
	if ids[len(ids)-1] != "f_fileonly" {
		t.Fatalf("prime tier order = %v, want the file-referenced claim last", ids)
	}
	byID := map[string]Fact{}
	for _, f := range got {
		byID[f.ID] = f
	}
	for _, id := range []string{"f_invariant", "f_memo", "f_distilled"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("prime tier %v is missing authority row %s", ids, id)
		}
	}
	// Each authority row must pass the predicate its renderer keys on, and the
	// memo is the case the grounding-only gate used to drop silently.
	if !byID["f_invariant"].IsInvariant() {
		t.Fatalf("invariant must pass IsInvariant(): %+v", byID["f_invariant"])
	}
	if !byID["f_memo"].IsManualMemo() || byID["f_memo"].IsFact() {
		t.Fatalf("memo must be a manual memo and ungrounded for this test to mean anything: %+v", byID["f_memo"])
	}
	if !byID["f_distilled"].IsFact() {
		t.Fatalf("corroborated distilled fact must pass IsFact(): %+v", byID["f_distilled"])
	}
	if got[3].IsFact() || got[3].IsInvariant() || got[3].IsManualMemo() {
		t.Fatalf("file-referenced claim must not pass an authority predicate: %+v", got[3])
	}
}

func TestCompactFactAndCandidates(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx := context.Background()

	// 1. Insert invariant (should never be a compaction candidate)
	inv := Fact{
		ID:        "f_inv_1",
		ProjectID: "proj_a",
		Claim:     "decision: Always use CGO_ENABLED=0 for standalone binaries",
		Kind:      "invariant",
		FirstSeen: "2026-09-01T00:00:00Z",
	}
	if err := s.SaveFact(inv); err != nil {
		t.Fatal(err)
	}

	// 2. Insert episodic code claim
	claim := Fact{
		ID:        "f_claim_1",
		ProjectID: "proj_a",
		Claim:     "Found exit code 127 in container because PATH missing ~/.local/bin",
		Kind:      "code_claim",
		FirstSeen: "2026-09-02T00:00:00Z",
		RepoFiles: []string{"cmd/sila/main.go"},
	}
	if err := s.SaveFact(claim); err != nil {
		t.Fatal(err)
	}

	// 3. Insert lesson
	lesson := Fact{
		ID:        "f_lesson_1",
		ProjectID: "proj_a",
		Claim:     "symptom: SQLITE_BUSY under parallel load\ncause: deferred transactions\nfix: use immediate",
		Kind:      "lesson",
		FirstSeen: "2026-09-03T00:00:00Z",
		RepoFiles: []string{"internal/store/store.go"},
	}
	if err := s.SaveFact(lesson); err != nil {
		t.Fatal(err)
	}

	// 4. Verify candidate retrieval excludes invariant
	candidates, err := s.GetCompactionCandidates("proj_a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}
	if candidates[0].ID != "f_claim_1" || candidates[1].ID != "f_lesson_1" {
		t.Fatalf("expected candidates [f_claim_1, f_lesson_1], got [%s, %s]", candidates[0].ID, candidates[1].ID)
	}

	// 5. Compact f_claim_1
	compactedText := "[Compacted Memory: A2 Zero-LLM Extractive]\nL0: Container exit code 127\nL2: • Paths: cmd/sila/main.go\n• Errors: exit code 127"
	newFiles := []string{"cmd/sila/main.go", ".github/workflows/ci.yml"}
	if err := s.CompactFact(ctx, "f_claim_1", compactedText, newFiles, "raw_hash_xyz"); err != nil {
		t.Fatal(err)
	}

	// 6. Verify f_claim_1 is now compacted
	facts, err := s.FactsByProject("proj_a")
	if err != nil {
		t.Fatal(err)
	}
	var f1 *Fact
	for _, f := range facts {
		if f.ID == "f_claim_1" {
			fCopy := f
			f1 = &fCopy
			break
		}
	}
	if f1 == nil {
		t.Fatalf("f_claim_1 not found in facts")
	}
	if !f1.IsCompacted() {
		t.Errorf("expected f_claim_1 to report IsCompacted() == true")
	}
	if f1.Claim != compactedText {
		t.Errorf("expected claim to be updated, got:\n%s", f1.Claim)
	}
	if f1.CompactedAt == "" {
		t.Errorf("expected compacted_at timestamp, got empty")
	}
	if f1.SupersedesID != "raw_hash_xyz" {
		t.Errorf("expected supersedes_id 'raw_hash_xyz', got %q", f1.SupersedesID)
	}
	if len(f1.RepoFiles) != 2 {
		t.Errorf("expected 2 repo_files, got %v", f1.RepoFiles)
	}

	// 7. Verify candidates now only contains f_lesson_1
	remaining, err := s.GetCompactionCandidates("proj_a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "f_lesson_1" {
		t.Fatalf("expected only f_lesson_1 remaining, got %+v", remaining)
	}

	// 8. Idempotency test: Re-compacting f_claim_1 does not alter compacted_at
	originalCompactedAt := f1.CompactedAt
	if err := s.CompactFact(ctx, "f_claim_1", "altered text", nil, "new_hash"); err != nil {
		t.Fatal(err)
	}
	rechecked, err := s.FactsByProject("proj_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rechecked {
		if f.ID == "f_claim_1" {
			if f.Claim != compactedText {
				t.Errorf("idempotent compaction re-modified claim")
			}
			if f.CompactedAt != originalCompactedAt {
				t.Errorf("idempotent compaction altered compacted_at")
			}
		}
	}
}

func TestReconcileFactDriftFileSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	s, err := Open(filepath.Join(tmpDir, "sha.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	projDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fileA := filepath.Join(projDir, "service.go")
	if err := os.WriteFile(fileA, []byte("package service\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := Fact{
		ID: "fact_sha", ProjectID: "sha-repo", Claim: "Service lives in service.go",
		Confidence: "grounded", Verified: true, RepoFiles: []string{"service.go"},
		Tools: []string{"devin"}, SessionRef: "s1",
		FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	}
	if err := s.UpsertFact(f); err != nil {
		t.Fatal(err)
	}
	score := func() float64 {
		t.Helper()
		var value float64
		if err := s.DB.QueryRow(`SELECT COALESCE(maturity_score, 0) FROM facts WHERE id = ?`, "fact_sha").Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	before := score()
	if _, err := s.ReconcileFactDrift("sha-repo", projDir); err != nil {
		t.Fatal(err)
	}
	if after := score(); after <= before {
		t.Fatalf("unchanged file did not promote maturity: %v -> %v", before, after)
	}
	digest, err := s.GetState("fact_file_sha256:fact_sha:service.go")
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("missing backfilled digest: %q (%v)", digest, err)
	}

	if err := os.WriteFile(fileA, []byte("package service\n// rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before = score()
	if _, err := s.ReconcileFactDrift("sha-repo", projDir); err != nil {
		t.Fatal(err)
	}
	if after := score(); after != before {
		t.Fatalf("rewritten file promoted maturity: %v -> %v", before, after)
	}
	facts, err := s.FactsByProject("sha-repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || !facts[0].FileRefsExist || !facts[0].Verified {
		t.Fatalf("rewritten file demoted the fact: %+v", facts)
	}
}
