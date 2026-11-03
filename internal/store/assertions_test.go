package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLineageRejectsFabricatedReferences(t *testing.T) {
	root := t.TempDir()
	st, err := Open(filepath.Join(root, "lineage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(root)
	if err != nil {
		t.Fatal(err)
	}
	f := Fact{
		ID: "fabricated-lineage", ProjectID: p.ID, Claim: "Unresolved evidence must stay unverified",
		RepoFiles: []string{"missing.go"}, FileRefsExist: true,
		SourceRef: "message:999999999", SessionRef: "commit:0000000000000000000000000000000000000000",
	}
	if err := st.UpsertFact(f); err != nil {
		t.Fatal(err)
	}
	assertions, err := st.AssertionsForStatement(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assertions) != 3 {
		t.Fatalf("want three independently inspectable references, got %d", len(assertions))
	}
	for _, a := range assertions {
		if a.Verified {
			t.Errorf("fabricated %s reference was verified: %s", a.EvidenceType, a.EvidenceID)
		}
		if a.VerifiedAt != "" || a.VerificationError == "" {
			t.Errorf("failed reference lacks conservative status/reason: %+v", a)
		}
	}
}

func TestLineageProjectScopeAndStaleReconciliation(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	otherRoot := filepath.Join(base, "other")
	for _, dir := range []string{root, otherRoot} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Open(filepath.Join(base, "lineage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(root)
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.EnsureProject(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "evidence.go")
	if err := os.WriteFile(file, []byte("package evidence\n"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(otherRoot, "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Sila Fixture")
	git("config", "user.email", "fixture@example.invalid")
	git("add", "evidence.go")
	git("commit", "-m", "fixture evidence")
	sha := git("rev-parse", "HEAD")
	orphan := git("commit-tree", "HEAD^{tree}", "-m", "unreachable object")
	for _, d := range []Doc{
		{ID: "doc_local", ProjectID: p.ID, Title: "Local", Content: "source bytes"},
		{ID: "doc_foreign", ProjectID: other.ID, Title: "Foreign", Content: "source bytes"},
	} {
		if err := st.InsertDoc(d); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	messages := []LoadMessage{
		{ProjectID: p.ID, ContentHash: "local-hash", TS: now, Role: "user", Content: "local source", Tool: "codex"},
		{ProjectID: other.ID, ContentHash: "foreign-hash", TS: now, Role: "user", Content: "foreign source", Tool: "codex"},
	}
	if _, _, err := st.WriteRun("fixture", "codex", "", messages); err != nil {
		t.Fatal(err)
	}
	var localMessage, foreignMessage int64
	if err := st.DB.QueryRow(`SELECT id FROM messages WHERE project_id=?`, p.ID).Scan(&localMessage); err != nil {
		t.Fatal(err)
	}
	if err := st.DB.QueryRow(`SELECT id FROM messages WHERE project_id=?`, other.ID).Scan(&foreignMessage); err != nil {
		t.Fatal(err)
	}
	for _, f := range []Fact{{ID: "local", ProjectID: p.ID, Claim: "local lineage"}, {ID: "foreign", ProjectID: other.ID, Claim: "foreign lineage"}} {
		if err := st.UpsertFact(f); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name, kind, id, span, statement string
		verified                        bool
	}{
		{name: "file", kind: "file", id: "evidence.go", verified: true},
		{name: "absolute-file", kind: "file", id: file, verified: true},
		{name: "doc", kind: "doc", id: "doc_local", span: "bytes:0-6", verified: true},
		{name: "message", kind: "message", id: "msg:" + strconv.FormatInt(localMessage, 10), verified: true},
		{name: "commit", kind: "git_commit", id: sha, verified: true},
		{name: "foreign-doc", kind: "doc", id: "doc_foreign"},
		{name: "foreign-message", kind: "message", id: "message:" + strconv.FormatInt(foreignMessage, 10)},
		{name: "foreign-statement", kind: "doc", id: "doc_local", statement: "foreign"},
		{name: "missing-statement", kind: "doc", id: "doc_local", statement: "absent"},
		{name: "symlink-escape", kind: "file", id: "escape.go"},
		{name: "relative-escape", kind: "file", id: "../other/outside.go"},
		{name: "absolute-escape", kind: "file", id: outside},
		{name: "unreachable-commit", kind: "git_commit", id: orphan},
		{name: "malformed-commit", kind: "git_commit", id: "not-a-commit"},
		{name: "unresolved-commit", kind: "git_commit", id: "123456789abcdef"},
		{name: "malformed-message", kind: "message", id: "msg:fiction"},
		{name: "directory-file", kind: "file", id: "."},
		{name: "invalid-span", kind: "doc", id: "doc_local", span: "bytes:0-1000"},
		{name: "malformed-span", kind: "doc", id: "doc_local", span: "bytes:wrong"},
		{name: "unsupported", kind: "test_run", id: "fiction"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			statement := tc.statement
			if statement == "" {
				statement = "local"
			}
			a := Assertion{ID: tc.name, ProjectID: p.ID, StatementID: statement, EvidenceType: tc.kind, EvidenceID: tc.id, EvidenceRef: tc.span, Verified: true, VerifiedAt: "old"}
			if err := st.InsertAssertion(a); err != nil {
				t.Fatal(err)
			}
			rows, err := st.AssertionsForStatement(statement)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, got := range rows {
				if got.ID == tc.name {
					found = true
					if got.Verified != tc.verified {
						t.Errorf("status mismatch: %+v", got)
					}
					if tc.verified && (got.VerifiedAt == "" || got.VerificationError != "") {
						t.Errorf("missing successful receipt: %+v", got)
					}
					if !tc.verified && (got.VerifiedAt != "" || got.VerificationError == "") {
						t.Errorf("invalid evidence retained a timestamp or lost its reason: %+v", got)
					}
				}
			}
			if !found {
				t.Fatal("assertion was lost")
			}
		})
	}
	// A short read span becomes stale when its source is shortened.
	if _, err := st.ReconcileAssertionLineage(otherRoot, p.ID); err == nil {
		t.Fatal("cross-project reconciliation root was accepted")
	}
	before, err := st.FactByID("local")
	if err != nil || before == nil {
		t.Fatalf("read fact before invalid reconciliation: %+v, %v", before, err)
	}
	if _, err := st.ReconcileFactDrift(p.ID, otherRoot); err == nil {
		t.Fatal("fact reconciliation accepted a different registered project root")
	}
	after, err := st.FactByID("local")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid root changed fact metadata: before=%+v after=%+v err=%v", before, after, err)
	}
	if err := st.InsertAssertion(Assertion{ID: "invalid-span", ProjectID: p.ID, StatementID: "local", EvidenceType: "doc", EvidenceID: "doc_local"}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertAssertion(Assertion{ID: "foreign-doc", ProjectID: p.ID, StatementID: "local", EvidenceType: "doc", EvidenceID: "doc_local"}); err == nil {
		t.Fatal("reused ID silently changed its evidence identity")
	}
	if _, err := st.DB.Exec(`UPDATE docs SET content='x' WHERE id='doc_local'`); err != nil {
		t.Fatal(err)
	}
	if count, err := st.ReconcileAssertionLineage(root, p.ID); err != nil || count != 4 {
		t.Fatalf("span reconciliation: count=%d err=%v", count, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM messages WHERE project_id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM docs WHERE project_id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	git("update-ref", "refs/heads/main", orphan)
	if count, err := st.ReconcileAssertionLineage(root, p.ID); err != nil || count != 1 {
		t.Fatalf("stale reconciliation: count=%d err=%v", count, err)
	}
	// The formerly orphaned object is now reachable; all deleted evidence is false.
	rows, err := st.AssertionsByProject(p.ID, false, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range rows {
		if a.ID != "unreachable-commit" && (a.Verified || a.VerifiedAt != "" || a.VerificationError == "") {
			t.Errorf("stale evidence survived: %+v", a)
		}
	}
}

func TestLineageSourcePrefixesAndByteLengths(t *testing.T) {
	root := t.TempDir()
	st, err := Open(filepath.Join(root, "lineage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(root)
	if err != nil {
		t.Fatal(err)
	}
	content := "أب"
	if err := st.InsertDoc(Doc{ID: "fixture/doc.md", ProjectID: p.ID, Content: content}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("fixture", "codex", "", []LoadMessage{{ProjectID: p.ID,
		ContentHash: "unicode-message", TS: time.Now().UTC().Format(time.RFC3339), Role: "user", Content: content, Tool: "codex"}}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := st.DB.QueryRow(`SELECT id FROM messages WHERE project_id=?`, p.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, source, kind string
		end                int64
		verified           bool
	}{
		{"doc-prefix", "doc:fixture/doc.md", "doc", 4, true},
		{"message-prefix", "message:" + strconv.FormatInt(id, 10), "message", 4, true},
		{"byte-overflow", "doc:fixture/doc.md", "doc", 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := Fact{ID: tc.name, ProjectID: p.ID, Claim: "Synthetic byte lineage " + tc.name,
				SourceRef: tc.source, ByteStart: 0, ByteEnd: tc.end}
			if err := st.UpsertFact(f); err != nil {
				t.Fatal(err)
			}
			rows, err := st.AssertionsForStatement(f.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("source assertion: %+v, %v", rows, err)
			}
			if a := rows[0]; a.EvidenceType != tc.kind || a.Verified != tc.verified ||
				(!tc.verified && a.VerificationError != "invalid_evidence_span") {
				t.Fatalf("wrong source classification or byte length: %+v", a)
			}
		})
	}
}

func TestLegacyAssertionVerificationIsInvalidatedOnUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`ALTER TABLE assertions DROP COLUMN verification_error`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO assertions(id,project_id,statement_id,statement_type,evidence_type,evidence_id,confidence,verified,verified_at,created_at)
	VALUES('legacy','project','statement','fact','git_commit','fiction','grounded',1,'old-verified','historical')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`PRAGMA user_version=27`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.AssertionsForStatement("statement")
	if err != nil || len(rows) != 1 {
		t.Fatalf("legacy lineage lost: %v %+v", err, rows)
	}
	a := rows[0]
	if a.Verified || a.VerifiedAt != "" || a.VerificationError != "not_reverified" || a.CreatedAt != "historical" {
		t.Fatalf("unsafe legacy status or lost history: %+v", a)
	}
}

func TestAssertionLineage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "assertions.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Create physical evidence files on disk
	codeFile := filepath.Join(dir, "server.go")
	if err := os.WriteFile(codeFile, []byte("package main\n\nfunc RunServer() {}\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "main")
	git("config", "user.name", "Sila Fixture")
	git("config", "user.email", "fixture@example.invalid")
	git("add", "server.go")
	git("commit", "-m", "fixture source")
	sha := git("rev-parse", "HEAD")
	if err := st.InsertDoc(Doc{ID: "doc_arch_1", ProjectID: p.ID, Title: "Architecture", Content: "Documented source evidence is project scoped."}); err != nil {
		t.Fatal(err)
	}

	// 1. Upsert a fact with file refs, source_ref, and commit session_ref
	fact := Fact{
		ID:            "fact_lineage_01",
		ProjectID:     p.ID,
		Claim:         "Server entrypoint is located in server.go",
		Kind:          "code_claim",
		Confidence:    "grounded",
		FileRefsExist: true,
		RepoFiles:     []string{"server.go", "nonexistent.go"},
		SourceRef:     "doc_arch_1",
		ByteStart:     0,
		ByteEnd:       25,
		SessionRef:    "commit:" + sha,
	}

	if err := st.UpsertFact(fact); err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	// 2. Verify assertions were automatically created by linkFactEvidenceTx
	assertions, err := st.AssertionsForStatement("fact_lineage_01")
	if err != nil {
		t.Fatalf("AssertionsForStatement failed: %v", err)
	}

	// Expected: server.go (file), nonexistent.go (file), doc_arch_1 (doc), commit (git_commit)
	if len(assertions) != 4 {
		t.Fatalf("expected 4 assertions for statement, got %d", len(assertions))
	}

	// Find the server.go assertion
	var serverAssertion *Assertion
	for i := range assertions {
		if assertions[i].EvidenceType == "file" && assertions[i].EvidenceID == "server.go" {
			serverAssertion = &assertions[i]
			break
		}
	}
	if serverAssertion == nil {
		t.Fatalf("missing assertion for server.go")
	}

	// 3. Test AssertionsForEvidence
	evList, err := st.AssertionsForEvidence("file", "server.go")
	if err != nil || len(evList) != 1 {
		t.Fatalf("AssertionsForEvidence failed: err=%v, count=%d", err, len(evList))
	}

	// 4. Test SearchFactsWithLineage
	lineageResults, err := st.SearchFactsWithLineage("server.go", p.ID, false, 10)
	if err != nil {
		t.Fatalf("SearchFactsWithLineage failed: %v", err)
	}
	if len(lineageResults) != 1 {
		t.Fatalf("expected 1 fact with lineage, got %d", len(lineageResults))
	}
	if len(lineageResults[0].Assertions) != 4 {
		t.Fatalf("expected 4 assertions attached to search hit, got %d", len(lineageResults[0].Assertions))
	}

	// 5. Test ReconcileAssertionLineage
	verifiedCount, err := st.ReconcileAssertionLineage(dir, p.ID)
	if err != nil {
		t.Fatalf("ReconcileAssertionLineage failed: %v", err)
	}
	// Real file, project-scoped doc and reachable commit each verify independently.
	// nonexistent.go does not exist (unverified)
	if verifiedCount != 3 {
		t.Fatalf("expected exactly 3 verified assertions, got %d", verifiedCount)
	}

	// Check nonexistent.go assertion is unverified
	assertionsAfter, _ := st.AssertionsForStatement("fact_lineage_01")
	for _, a := range assertionsAfter {
		if a.EvidenceID == "nonexistent.go" && a.Verified {
			t.Fatalf("nonexistent.go should not be verified after reconcile")
		}
		if a.EvidenceID == "server.go" && !a.Verified {
			t.Fatalf("server.go should be verified after reconcile")
		}
	}
}
