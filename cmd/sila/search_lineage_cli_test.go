package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestSearchLineageCLI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "search_lineage.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evidence.go"), []byte("package evidence\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertDoc(store.Doc{ID: "doc_arch_spec", ProjectID: p.ID,
		Content: "A synthetic design document describes the assertion linkage contract."}); err != nil {
		t.Fatal(err)
	}
	fact := store.Fact{
		ID:            "fact_lineage_cli_1",
		ProjectID:     p.ID,
		Claim:         "Lineage tracking grounds claims to disk files and commits",
		Kind:          "fact",
		Confidence:    "grounded",
		RepoFiles:     []string{"evidence.go"},
		FileRefsExist: true,
		SourceRef:     "doc_arch_spec",
		ByteStart:     10,
		ByteEnd:       45,
		SessionRef:    "commit:0000000000000000000000000000000000000000",
		FirstSeen:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := st.UpsertFact(fact); err != nil {
		t.Fatal(err)
	}
	other, err := st.EnsureProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFact(store.Fact{ID: "foreign_fact", ProjectID: other.ID,
		Claim: "Private other-project source", RepoFiles: []string{"other.go"}}); err != nil {
		t.Fatal(err)
	}

	search := func(t *testing.T, jsonOut, toonOut bool, terms ...string) string {
		t.Helper()
		return captureStdout(t, func() {
			ui := render.New(os.Stdout, true)
			runSearch(ui, dbPath, p.ID, "", "", time.Time{}, false, false, false, 0, 0,
				false, false, false, jsonOut, toonOut, false, 10, terms)
		})
	}
	check := func(t *testing.T, assertions []store.Assertion) {
		t.Helper()
		if len(assertions) != 3 {
			t.Fatalf("want all three valid/invalid assertions, got %d", len(assertions))
		}
		for _, a := range assertions {
			if a.ProjectID != p.ID {
				t.Fatalf("foreign lineage leaked: %+v", a)
			}
			if a.EvidenceType == "git_commit" {
				if a.Verified || a.VerifiedAt != "" || a.VerificationError != "malformed_commit_id" {
					t.Fatalf("fabricated commit accepted: %+v", a)
				}
			} else if !a.Verified || a.VerifiedAt == "" || a.VerificationError != "" {
				t.Fatalf("real source not verified: %+v", a)
			}
		}
	}

	t.Run("exact fact JSON", func(t *testing.T) {
		output := search(t, true, false, "lineage", fact.ID)
		var result store.FactWithLineage
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			t.Fatalf("decode lineage: %v; %s", err, output)
		}
		if result.Fact.ID != fact.ID {
			t.Fatalf("wrong fact: %s", result.Fact.ID)
		}
		check(t, result.Assertions)
	})
	t.Run("assertions JSON", func(t *testing.T) {
		var assertions []store.Assertion
		if err := json.Unmarshal([]byte(search(t, true, false, "assertions")), &assertions); err != nil {
			t.Fatal(err)
		}
		check(t, assertions)
	})
	t.Run("search JSON", func(t *testing.T) {
		var results []store.FactWithLineage
		if err := json.Unmarshal([]byte(search(t, true, false, "lineage", "tracking", "claims")), &results); err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].Fact.ID != fact.ID {
			t.Fatalf("wrong search result: %+v", results)
		}
		check(t, results[0].Assertions)
	})
	t.Run("human and TOON diagnostics", func(t *testing.T) {
		output := search(t, false, false, "lineage", fact.ID)
		if !strings.Contains(output, "malformed_commit_id") || !strings.Contains(output, "does not prove claim truth") {
			t.Fatalf("missing verification limitation: %s", output)
		}
		output = search(t, false, true, "assertions")
		if !strings.Contains(output, "verification_error") || !strings.Contains(output, "malformed_commit_id") {
			t.Fatalf("missing TOON reason: %s", output)
		}
	})
	t.Run("exact foreign ID stays scoped", func(t *testing.T) {
		output := search(t, true, false, "lineage", "foreign_fact")
		if strings.Contains(output, "Private other-project source") || strings.Contains(output, "other.go") {
			t.Fatalf("foreign project leaked: %s", output)
		}
	})
	t.Run("verify previews and expires deleted sources", func(t *testing.T) {
		before := search(t, true, false, "assertions")
		if _, err := st.DB.Exec(`DELETE FROM docs WHERE id='doc_arch_spec'`); err != nil {
			t.Fatal(err)
		}
		verify := func(dryRun bool) store.ReconcileResult {
			output := captureStdout(t, func() {
				runReconcile(render.New(os.Stdout, true), st, p.ID, dryRun, false, true, false)
			})
			var result store.ReconcileResult
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				t.Fatalf("decode reconciliation: %v; %s", err, output)
			}
			return result
		}
		if result := verify(true); result.AssertionsChecked != 3 || result.AssertionsVerified != 1 {
			t.Fatalf("wrong preview: %+v", result)
		}
		if after := search(t, true, false, "assertions"); after != before {
			t.Fatalf("preview wrote assertion status: before=%s after=%s", before, after)
		}
		if result := verify(false); result.AssertionsChecked != 3 || result.AssertionsVerified != 1 {
			t.Fatalf("wrong reconciliation: %+v", result)
		}
		rows, err := st.AssertionsForStatement(fact.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range rows {
			if a.EvidenceType == "doc" && (a.Verified || a.VerifiedAt != "" || a.VerificationError != "evidence_missing_in_project") {
				t.Fatalf("deleted source survived verify: %+v", a)
			}
		}
	})
}
