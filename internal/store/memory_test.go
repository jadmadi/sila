package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const memoryMinimalFixture = `{
  "schema_version": "sila/memory/v1",
  "project_id": "sila-cli",
  "kind": "root_cause",
  "title": "Deploy smoke check fails because migrations run after deploy",
  "summary": "scripts/deploy.sh runs the deploy before pnpm db:migrate.",
  "assertions": [
    {
      "id": "A-001",
      "assertion_type": "descriptive",
      "predicate": "causes",
      "statement": "Running deploy before migrations exposes new code to the old schema.",
      "subject": {"context_entity": "file", "value": "scripts/deploy.sh"},
      "object": {"literal": {"type": "string", "value": "run migrations first"}, "qualifier": "deploy_order"},
      "confidence": 0.8,
      "importance": 4,
      "evidence_refs": ["E-001"]
    }
  ],
  "evidence": [
    {
      "id": "E-001",
      "kind": "agent_initiated",
      "locator": {},
      "summary": "Observed in the deploy script."
    }
  ],
  "relations": [],
  "scope": {"paths": ["scripts/deploy.sh"]},
  "provenance": {"agent": "codex", "client_kind": "ai_agent"}
}`

func memoryFullFixture(projectID string) string {
	return `{
  "schema_version": "sila/memory/v1",
  "project_id": "` + projectID + `",
  "kind": "invariant",
  "title": "Every additive migration is ordered by version",
  "summary": "The migration runner sorts registrations before applying them.",
  "assertions": [
    {
      "id": "A-002",
      "assertion_type": "normative",
      "predicate": "requires",
      "statement": "The migration runner must sort steps by version.",
      "subject": {"context_entity": "symbol", "value": "store.migrate"},
      "object": {"literal": {"type": "boolean", "value": true}, "qualifier": "ordering"},
      "confidence": 0.8,
      "importance": 4,
      "evidence_refs": ["E-002"]
    },
    {
      "id": "A-001",
      "assertion_type": "descriptive",
      "predicate": "has_property",
      "statement": "Registration order follows Go file-name order.",
      "subject": {"context_entity": "file", "value": "internal/store/store.go"},
      "object": {"literal": {"type": "number", "value": 41}},
      "confidence": 0.5,
      "importance": 2,
      "evidence_refs": ["E-001", "E-002"]
    }
  ],
  "evidence": [
    {
      "id": "E-002",
      "kind": "test",
      "locator": {"path": "internal/store/memory_schema_test.go", "line_start": 1, "line_end": 20},
      "summary": "Migration ordering test."
    },
    {
      "id": "E-001",
      "kind": "commit",
      "locator": {"commit": "7ff1874f"},
      "summary": "Current head."
    }
  ],
  "relations": [{"type": "refines", "target_memory_id": "mem_parent"}],
  "scope": {"paths": ["internal/store/store.go"], "symbols": ["store.migrate"]},
  "valid_from": "2026-01-01T00:00:00Z",
  "provenance": {"agent": "codex", "tool_name": "opencode", "client_kind": "ai_agent", "session_ref": "s1"},
  "idempotency_key": "fixture-key-00000001"
}`
}

func testMemoryEnvelope(projectID, title, summary string) *MemoryEnvelope {
	return &MemoryEnvelope{
		SchemaVersion: MemorySchemaVersion,
		ProjectID:     projectID,
		Kind:          MemoryKindRootCause,
		Title:         title,
		Summary:       summary,
		Assertions: []MemoryAssertion{{
			ID:            "A-001",
			AssertionType: MemoryAssertionDescriptive,
			Predicate:     "causes",
			Statement:     summary,
			Subject:       MemorySubject{ContextEntity: "project", Value: projectID},
			Object:        MemoryObject{Literal: &MemoryLiteral{Type: "string", Value: "fix"}},
			Confidence:    0.8,
			Importance:    4,
			EvidenceRefs:  []string{"E-001"},
		}},
		Evidence: []MemoryEvidence{{
			ID:      "E-001",
			Kind:    MemoryEvidenceAgentInitiated,
			Summary: "Recorded during a test.",
		}},
	}
}

func openMemoryStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMemorySchemaFixtures(t *testing.T) {
	minimal, err := DecodeMemoryEnvelope([]byte(memoryMinimalFixture))
	if err != nil {
		t.Fatalf("minimal fixture: %v", err)
	}
	if diags := ValidateMemoryEnvelope(minimal, MemoryValidateOptions{}); len(diags) > 0 {
		t.Fatalf("minimal fixture diagnostics: %v", diags)
	}
	full, err := DecodeMemoryEnvelope([]byte(memoryFullFixture("sila-cli")))
	if err != nil {
		t.Fatalf("full fixture: %v", err)
	}
	if diags := ValidateMemoryEnvelope(full, MemoryValidateOptions{}); len(diags) > 0 {
		t.Fatalf("full fixture diagnostics: %v", diags)
	}
	first, err := EncodeMemoryEnvelope(full)
	if err != nil {
		t.Fatal(err)
	}
	round, err := DecodeMemoryEnvelope(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeMemoryEnvelope(round)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("round trip is not byte stable:\n%s\n%s", first, second)
	}
}

func TestDecodeMemoryEnvelopeRejectsUnknown(t *testing.T) {
	cases := map[string]struct {
		payload string
		code    string
	}{
		"unknown top field": {strings.Replace(memoryMinimalFixture, `"summary"`, `"extra": 1, "summary"`, 1), MemoryCodeFieldUnknown},
		"unknown nested":    {strings.Replace(memoryMinimalFixture, `"confidence"`, `"unexpected": true, "confidence"`, 1), MemoryCodeFieldUnknown},
		"wrong version":     {strings.Replace(memoryMinimalFixture, "sila/memory/v1", "sila/memory/v2", 1), MemoryCodeSchemaVersion},
		"missing version":   {strings.Replace(memoryMinimalFixture, `"schema_version": "sila/memory/v1",`, "", 1), MemoryCodeSchemaUnknown},
		"malformed":         {`{"schema_version": `, MemoryCodeJSONInvalid},
		"trailing":          {memoryMinimalFixture + `{"x":1}`, MemoryCodeJSONInvalid},
		"duplicate key":     {strings.Replace(memoryMinimalFixture, `"title":`, `"title": "a", "title":`, 1), MemoryCodeJSONInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeMemoryEnvelope([]byte(tc.payload))
			if err == nil {
				t.Fatal("expected rejection")
			}
			if got := MemoryCode(err); got != tc.code {
				t.Fatalf("code = %q, want %q (%v)", got, tc.code, err)
			}
		})
	}
	big := append([]byte(memoryMinimalFixture), []byte(strings.Repeat(" ", MemoryMaxBytes))...)
	if _, err := DecodeMemoryEnvelope(big); MemoryCode(err) != MemoryCodeJSONInvalid {
		t.Fatalf("oversized envelope code = %q", MemoryCode(err))
	}
}

func TestDecodeMemoryEnvelopeBounds(t *testing.T) {
	base := testMemoryEnvelope("p", "title", "summary")
	if err := ValidateMemoryEnvelope(base, MemoryValidateOptions{}); len(err) > 0 {
		t.Fatalf("baseline invalid: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*MemoryEnvelope)
		code   string
	}{
		{"zero assertions", func(e *MemoryEnvelope) { e.Assertions = nil }, MemoryCodeLimit},
		{"nine assertions", func(e *MemoryEnvelope) {
			for i := 0; i < 9; i++ {
				a := e.Assertions[0]
				a.ID = "A-00" + string(rune('1'+i))
				e.Assertions = append(e.Assertions, a)
			}
		}, MemoryCodeLimit},
		{"zero evidence", func(e *MemoryEnvelope) { e.Evidence = nil }, MemoryCodeLimit},
		{"nine evidence", func(e *MemoryEnvelope) {
			for i := 0; i < 9; i++ {
				ev := e.Evidence[0]
				ev.ID = "E-00" + string(rune('1'+i))
				e.Evidence = append(e.Evidence, ev)
			}
		}, MemoryCodeLimit},
		{"nine relations", func(e *MemoryEnvelope) {
			for i := 0; i < 9; i++ {
				e.Relations = append(e.Relations, MemoryRelation{Type: MemoryRelationRelatedTo, TargetMemoryID: "mem_x"})
			}
		}, MemoryCodeLimit},
		{"long statement", func(e *MemoryEnvelope) { e.Assertions[0].Statement = strings.Repeat("x", 2001) }, MemoryCodeBound},
		{"long title", func(e *MemoryEnvelope) { e.Title = strings.Repeat("x", 201) }, MemoryCodeBound},
		{"long summary", func(e *MemoryEnvelope) { e.Summary = strings.Repeat("x", 2001) }, MemoryCodeBound},
		{"confidence high", func(e *MemoryEnvelope) { e.Assertions[0].Confidence = 0.81 }, MemoryCodeBound},
		{"importance high", func(e *MemoryEnvelope) { e.Assertions[0].Importance = 5 }, MemoryCodeBound},
		{"bad kind", func(e *MemoryEnvelope) { e.Kind = "guess" }, MemoryCodeEnum},
		{"bad assertion type", func(e *MemoryEnvelope) { e.Assertions[0].AssertionType = "opinion" }, MemoryCodeEnum},
		{"bad predicate pair", func(e *MemoryEnvelope) { e.Assertions[0].Predicate = "requires" }, MemoryCodeEnum},
		{"bad subject", func(e *MemoryEnvelope) { e.Assertions[0].Subject.ContextEntity = "planet" }, MemoryCodeEnum},
		{"bad literal type", func(e *MemoryEnvelope) { e.Assertions[0].Object.Literal.Type = "date" }, MemoryCodeEnum},
		{"bad qualifier", func(e *MemoryEnvelope) { e.Assertions[0].Object.Qualifier = "Bad Qualifier" }, MemoryCodeEnum},
		{"missing ev ref", func(e *MemoryEnvelope) { e.Assertions[0].EvidenceRefs = []string{"E-999"} }, MemoryCodeEvidenceRef},
		{"duplicate id", func(e *MemoryEnvelope) { e.Evidence = append(e.Evidence, e.Evidence[0]) }, MemoryCodeBound},
		{"future valid_from", func(e *MemoryEnvelope) { e.ValidFrom = time.Now().UTC().Add(time.Hour).Format(time.RFC3339) }, MemoryCodeBound},
		{"short idempotency", func(e *MemoryEnvelope) { e.IdempotencyKey = "short" }, MemoryCodeBound},
		{"self relation", func(e *MemoryEnvelope) {
			e.MemoryID = "mem_self"
			e.Relations = []MemoryRelation{{Type: MemoryRelationRelatedTo, TargetMemoryID: "mem_self"}}
		}, MemoryCodeRelationCycle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testMemoryEnvelope("p", "title", "summary")
			tc.mutate(env)
			diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{})
			if len(diags) == 0 {
				t.Fatal("expected diagnostics")
			}
			found := false
			for _, d := range diags {
				if d.Code == tc.code {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing code %s in %v", tc.code, diags)
			}
		})
	}
}

func TestMemoryEnumsAndAuthority(t *testing.T) {
	descriptive := []string{"has_property", "causes", "supersedes"}
	normative := []string{"requires", "forbids", "recommends"}
	for _, p := range descriptive {
		env := testMemoryEnvelope("p", "t", "s")
		env.Assertions[0].AssertionType = MemoryAssertionDescriptive
		env.Assertions[0].Predicate = p
		if diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{}); len(diags) > 0 {
			t.Errorf("descriptive/%s rejected: %v", p, diags)
		}
	}
	for _, p := range normative {
		env := testMemoryEnvelope("p", "t", "s")
		env.Assertions[0].AssertionType = MemoryAssertionNormative
		env.Assertions[0].Predicate = p
		if diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{}); len(diags) > 0 {
			t.Errorf("normative/%s rejected: %v", p, diags)
		}
	}
	for _, kind := range MemoryKinds() {
		env := testMemoryEnvelope("p", "t", "s")
		env.Kind = kind
		if diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{}); len(diags) > 0 {
			t.Errorf("kind %s rejected: %v", kind, diags)
		}
	}
	for _, kind := range MemoryEvidenceKinds() {
		env := testMemoryEnvelope("p", "t", "s")
		env.Evidence[0].Kind = kind
		env.Evidence[0].Locator = MemoryLocator{}
		switch kind {
		case MemoryEvidenceFile:
			env.Evidence[0].Locator.Path = "ok.go"
		case MemoryEvidenceTest:
			env.Evidence[0].Locator.Path = "ok.go"
		case MemoryEvidenceCommit:
			env.Evidence[0].Locator.Commit = "abc123"
		}
		if diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{}); len(diags) > 0 {
			t.Errorf("evidence kind %s rejected: %v", kind, diags)
		}
	}
	for _, rel := range MemoryRelationTypes() {
		env := testMemoryEnvelope("p", "t", "s")
		env.Relations = []MemoryRelation{{Type: rel, TargetMemoryID: "mem_target"}}
		diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{})
		for _, d := range diags {
			if d.Code == MemoryCodeEnum {
				t.Errorf("relation %s rejected: %v", rel, diags)
			}
		}
	}
	userAttested := testMemoryEnvelope("p", "t", "s")
	userAttested.Evidence[0].Kind = MemoryEvidenceUserAttestation
	userAttested.Assertions[0].Confidence = 0.95
	userAttested.Assertions[0].Importance = 5
	if diags := ValidateMemoryEnvelope(userAttested, MemoryValidateOptions{}); len(diags) > 0 {
		t.Fatalf("user attestation caps rejected: %v", diags)
	}
	if MemoryAuthority(userAttested) != "user_attested" {
		t.Fatalf("authority = %q", MemoryAuthority(userAttested))
	}
	if MemoryAuthority(testMemoryEnvelope("p", "t", "s")) != "agent_save" {
		t.Fatal("agent save label missing")
	}
}

func TestMemoryEvidencePathSafety(t *testing.T) {
	projectDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectDir, "ok.go"), []byte("package ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(projectDir, "nested", "escape")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	cases := []struct {
		name string
		ev   MemoryEvidence
		code string
	}{
		{"valid", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "ok.go"}}, ""},
		{"absolute", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "/etc/passwd"}}, MemoryCodeEvidencePath},
		{"traversal", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "../secret"}}, MemoryCodeEvidencePath},
		{"home", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "~/.ssh/id_rsa"}}, MemoryCodeEvidencePath},
		{"secret", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: ".env"}}, MemoryCodeEvidencePath},
		{"temp", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "/tmp/x"}}, MemoryCodeEvidencePath},
		{"symlink escape", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "nested/escape"}}, MemoryCodeEvidencePath},
		{"missing", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "missing.go"}}, MemoryCodeEvidencePath},
		{"missing allowed", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "missing.go"}, MissingAtWrite: true, MissingReason: "removed upstream"}, ""},
		{"bad sha", MemoryEvidence{Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "ok.go"}, SHA256: "ABC"}, MemoryCodeEvidenceDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testMemoryEnvelope("p", "title", "summary")
			env.Evidence[0] = tc.ev
			env.Evidence[0].ID = "E-001"
			env.Evidence[0].Summary = "evidence"
			diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{ProjectDir: projectDir})
			got := ""
			for _, d := range diags {
				if d.Code == MemoryCodeEvidencePath || d.Code == MemoryCodeEvidenceDigest {
					got = d.Code
				}
			}
			if got != tc.code {
				t.Fatalf("code = %q, want %q (%v)", got, tc.code, diags)
			}
		})
	}
}

func TestMemoryEvidenceDigest(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, "data.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex("hello")
	env := testMemoryEnvelope("p", "title", "summary")
	env.Evidence[0] = MemoryEvidence{ID: "E-001", Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "data.txt"}, Summary: "data", SHA256: sum}
	if diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{ProjectDir: projectDir}); len(diags) > 0 {
		t.Fatalf("matching digest rejected: %v", diags)
	}
	env.Evidence[0].SHA256 = strings.Repeat("0", 64)
	diags := ValidateMemoryEnvelope(env, MemoryValidateOptions{ProjectDir: projectDir})
	if !hasMemoryCode(diags, MemoryCodeEvidenceDigest) {
		t.Fatalf("mismatched digest accepted: %v", diags)
	}
}

func TestMemoryDigestStable(t *testing.T) {
	a, err := DecodeMemoryEnvelope([]byte(memoryFullFixture("p")))
	if err != nil {
		t.Fatal(err)
	}
	b, err := DecodeMemoryEnvelope([]byte(memoryFullFixture("p")))
	if err != nil {
		t.Fatal(err)
	}
	b.MemoryID = "mem_different"
	b.CreatedAt = "2030-01-01T00:00:00Z"
	b.UpdatedAt = b.CreatedAt
	b.Assertions[0], b.Assertions[1] = b.Assertions[1], b.Assertions[0]
	b.Evidence[0], b.Evidence[1] = b.Evidence[1], b.Evidence[0]
	da, err := MemoryDigest(a)
	if err != nil {
		t.Fatal(err)
	}
	db, err := MemoryDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if da != db {
		t.Fatalf("digest changed with order/timestamps: %s != %s", da, db)
	}
	b.Title = "a different title"
	dc, err := MemoryDigest(b)
	if err != nil {
		t.Fatal(err)
	}
	if dc == da {
		t.Fatal("digest ignored a normative field change")
	}
	if strings.ToLower(da) != da || len(da) != 64 {
		t.Fatalf("digest is not lowercase sha256: %q", da)
	}
}

func TestSaveMemoryEnvelopeBridgeFact(t *testing.T) {
	st := openMemoryStore(t)
	env := testMemoryEnvelope("proj", "Bridge title", "Bridge summary text")
	rec, created, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{ToolName: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
	if rec.ID == "" || rec.Digest == "" || rec.FactID == "" {
		t.Fatalf("incomplete record: %+v", rec)
	}
	fact, err := st.FactByID(rec.FactID)
	if err != nil {
		t.Fatal(err)
	}
	if fact.Claim != env.Summary {
		t.Fatalf("bridge claim = %q", fact.Claim)
	}
	if fact.Kind != "root_cause" {
		t.Fatalf("bridge kind = %q", fact.Kind)
	}
	audit, err := st.VerifyChain("")
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Intact {
		t.Fatalf("hash chain broken: %+v", audit)
	}
	if !rec.Grounded {
		t.Fatal("agent-initiated evidence should count as grounded")
	}
}

func TestMemoryIdempotency(t *testing.T) {
	st := openMemoryStore(t)
	env := testMemoryEnvelope("proj", "Idempotent", "Same summary")
	env.IdempotencyKey = "idem-key-0000000001"
	first, created, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{})
	if err != nil || !created {
		t.Fatalf("first save: created=%v err=%v", created, err)
	}
	replay := testMemoryEnvelope("proj", "Idempotent", "Same summary")
	replay.IdempotencyKey = "idem-key-0000000001"
	second, created, err := st.SaveMemoryEnvelope(context.Background(), "proj", replay, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("replay: created=%v id=%s want=%s", created, second.ID, first.ID)
	}
	conflict := testMemoryEnvelope("proj", "Different title", "Different summary")
	conflict.IdempotencyKey = "idem-key-0000000001"
	_, _, err = st.SaveMemoryEnvelope(context.Background(), "proj", conflict, Provenance{})
	if MemoryCode(err) != MemoryCodeIdempotency {
		t.Fatalf("conflict code = %q (%v)", MemoryCode(err), err)
	}
	if _, err := st.MemoryRecordByID(conflict.MemoryID); err == nil {
		t.Fatal("conflicting replay was stored")
	}
}

func TestMemoryDuplicateDetection(t *testing.T) {
	st := openMemoryStore(t)
	env := testMemoryEnvelope("proj", "Duplicate", "Identical summary")
	first, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	dup := testMemoryEnvelope("proj", "Duplicate", "Identical summary")
	dup.IdempotencyKey = "different-key-000001"
	_, _, err = st.SaveMemoryEnvelope(context.Background(), "proj", dup, Provenance{})
	if MemoryCode(err) != MemoryCodeDuplicate {
		t.Fatalf("duplicate code = %q (%v)", MemoryCode(err), err)
	}
	if !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("duplicate error does not name the existing record: %v", err)
	}
	similar := testMemoryEnvelope("proj", "Duplicate", "Identical summary note")
	similar.IdempotencyKey = "similar-key-000000001"
	rec, created, err := st.SaveMemoryEnvelope(context.Background(), "proj", similar, Provenance{})
	if err != nil || !created {
		t.Fatalf("similar write: created=%v err=%v", created, err)
	}
	if !hasMemoryDiagnostic(rec.Diagnostics, MemoryCodeSimilar) {
		t.Fatalf("similar diagnostic missing: %v", rec.Diagnostics)
	}
}

func TestMemorySupersedesValidation(t *testing.T) {
	st := openMemoryStore(t)
	old, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", testMemoryEnvelope("proj", "Old", "Old summary"), Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	replacement := testMemoryEnvelope("proj", "New", "New summary")
	replacement.Relations = []MemoryRelation{{Type: MemoryRelationSupersedes, TargetMemoryID: old.ID}}
	newRec, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", replacement, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	loadedOld, err := st.MemoryRecordByID(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loadedOld.Superseded {
		t.Fatal("old record is not marked superseded")
	}
	list, err := st.ListMemoryRecords(MemoryListOptions{ProjectID: "proj"})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range list {
		if rec.ID == old.ID {
			t.Fatal("superseded record appears in default list")
		}
	}
	all, err := st.ListMemoryRecords(MemoryListOptions{ProjectID: "proj", IncludeSuperseded: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("include-superseded list = %d", len(all))
	}
	missing := testMemoryEnvelope("proj", "Broken target", "Broken target summary")
	missing.Relations = []MemoryRelation{{Type: MemoryRelationSupersedes, TargetMemoryID: "mem_missing"}}
	if _, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", missing, Provenance{}); MemoryCode(err) != MemoryCodeRelationTarget {
		t.Fatalf("missing target code = %q (%v)", MemoryCode(err), err)
	}
	other, _, err := st.SaveMemoryEnvelope(context.Background(), "other", testMemoryEnvelope("other", "Other", "Other summary"), Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	cross := testMemoryEnvelope("proj", "Cross", "Cross summary")
	cross.Relations = []MemoryRelation{{Type: MemoryRelationRelatedTo, TargetMemoryID: other.ID}}
	if _, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", cross, Provenance{}); MemoryCode(err) != MemoryCodeRelationTarget {
		t.Fatalf("cross-project code = %q (%v)", MemoryCode(err), err)
	}
	if loadedOld.FactID == "" || newRec.FactID == "" {
		t.Fatal("bridge fact ids missing")
	}
}

func TestMemoryFTSAndPrivacy(t *testing.T) {
	st := openMemoryStore(t)
	env := testMemoryEnvelope("proj", "WAL checkpoint starvation", "The checkpoint starves under long readers.")
	env.Evidence[0] = MemoryEvidence{ID: "E-001", Kind: MemoryEvidenceAgentInitiated, Summary: "observed"}
	if _, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{}); err != nil {
		t.Fatal(err)
	}
	hits, err := st.SearchMemoryRecords("checkpoint", "proj", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("fts hits=%d err=%v", len(hits), err)
	}
	secret := testMemoryEnvelope("proj", "Unrelated title", "Unrelated summary")
	secret.Evidence[0] = MemoryEvidence{ID: "E-001", Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "internal/needle_locator.go"}, Summary: "path only"}
	if _, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", secret, Provenance{}); err != nil {
		t.Fatal(err)
	}
	hits, err = st.SearchMemoryRecords("needle_locator", "proj", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("evidence locator was indexed: %+v", hits)
	}
	hits, err = st.SearchMemoryRecords("starv", "proj", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("prefix search hits=%d err=%v", len(hits), err)
	}
}

func TestLegacyWritesCreateTypedMemory(t *testing.T) {
	st := openMemoryStore(t)
	memo, err := st.InsertMemo("proj", "Use WAL for concurrent readers", "codex", "", []string{"internal/store/store.go"})
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := st.SaveLesson("proj", "race detector hangs", "CGO sqlite", "use pure Go driver", "sqlite,race", "codex", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	arch, err := st.SaveCase("proj", "typed memory bridge", "sidecar tables", "facts remain the read surface", "memory", "codex", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []*Fact{memo, lesson, arch} {
		if fact.Claim == "" {
			t.Fatal("legacy claim was not preserved")
		}
	}
	if symptom, cause, fix, tags := ParseLesson(lesson.Claim); symptom != "race detector hangs" || cause != "CGO sqlite" || fix != "use pure Go driver" || tags != "sqlite,race" {
		t.Fatalf("ParseLesson changed: %q %q %q %q", symptom, cause, fix, tags)
	}
	if intent, approach, invariant, tags := ParseCase(arch.Claim); intent != "typed memory bridge" || approach != "sidecar tables" || invariant != "facts remain the read surface" || tags != "memory" {
		t.Fatalf("ParseCase changed: %q %q %q %q", intent, approach, invariant, tags)
	}
	list, err := st.ListMemoryRecords(MemoryListOptions{ProjectID: "proj", IncludeSuperseded: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("typed records = %d, want 3", len(list))
	}
	kinds := map[string]bool{}
	for _, rec := range list {
		kinds[rec.Kind] = true
		if rec.Authority != "agent_save" {
			t.Errorf("legacy record authority = %q", rec.Authority)
		}
	}
	for _, want := range []string{MemoryKindArchitectureDecision, MemoryKindFix, MemoryKindProcedure} {
		if !kinds[want] {
			t.Errorf("missing typed kind %s in %v", want, kinds)
		}
	}
	audit, err := st.VerifyChain("")
	if err != nil || !audit.Intact {
		t.Fatalf("hash chain after legacy writes: audit=%+v err=%v", audit, err)
	}
}

func TestMemorySaveRollback(t *testing.T) {
	st := openMemoryStore(t)
	first := testMemoryEnvelope("proj", "First", "First summary")
	existing, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", first, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	collision := testMemoryEnvelope("proj", "Second", "Second summary")
	collision.MemoryID = existing.ID
	_, _, err = st.SaveMemoryEnvelope(context.Background(), "proj", collision, Provenance{})
	if err == nil {
		t.Fatal("expected a primary-key collision")
	}
	if fact, _ := st.FactByID(FactID("proj", "Second summary")); fact != nil {
		t.Fatal("rollback left the bridge fact behind")
	}
}

func TestMemoryStatsAndDiagnostics(t *testing.T) {
	st := openMemoryStore(t)
	env := testMemoryEnvelope("proj", "Grounded", "Grounded summary")
	if _, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", env, Provenance{}); err != nil {
		t.Fatal(err)
	}
	missing := testMemoryEnvelope("proj", "Ungrounded", "Ungrounded summary")
	missing.Evidence[0] = MemoryEvidence{ID: "E-001", Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "gone.go"}, Summary: "gone", MissingAtWrite: true, MissingReason: "removed"}
	if _, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", missing, Provenance{}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListMemoryRecords(MemoryListOptions{ProjectID: "proj", IncludeSuperseded: true})
	if err != nil {
		t.Fatal(err)
	}
	stats := ComputeMemoryStats(list)
	if stats.Total != 2 || stats.Grounded != 1 || stats.Ungrounded != 1 || stats.MissingAtWrite != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	report, err := st.MemoryDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != CurrentSchemaVersion() || report.Records != 2 || !report.FTSHealthy {
		t.Fatalf("diagnostics = %+v", report)
	}
}

func hasMemoryCode(diags []MemoryDiagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func hasMemoryDiagnostic(diags []MemoryDiagnostic, code string) bool {
	return hasMemoryCode(diags, code)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestTypedMemoryReadPathParity(t *testing.T) {
	st := openMemoryStore(t)
	lesson, err := st.SaveLesson("proj", "race detector hangs", "CGO sqlite", "use the pure Go driver", "sqlite,race", "codex", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	lessons, err := st.LessonFacts("proj", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range lessons {
		if f.ID == lesson.ID {
			found = true
			if symptom, _, _, _ := ParseLesson(f.Claim); symptom != "race detector hangs" {
				t.Fatalf("lesson read path changed: %q", f.Claim)
			}
		}
	}
	if !found {
		t.Fatal("lesson read path lost the legacy fact")
	}
	hits, err := st.SearchFactsFTS("race", "proj", false, 10)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, f := range hits {
		if f.ID == lesson.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("fact FTS lost the legacy lesson")
	}
	records, err := st.ListMemoryRecords(MemoryListOptions{ProjectID: "proj", IncludeSuperseded: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Kind != MemoryKindFix || records[0].FactID != lesson.ID {
		t.Fatalf("typed parity = %+v", records)
	}
	audit, err := st.VerifyChain("proj")
	if err != nil || !audit.Intact {
		t.Fatalf("hash chain parity: %+v err=%v", audit, err)
	}
}

func TestTypedMemoryAuthorityLabels(t *testing.T) {
	st := openMemoryStore(t)
	attested := testMemoryEnvelope("proj", "User attested", "User attested summary")
	attested.Evidence[0].Kind = MemoryEvidenceUserAttestation
	attested.Assertions[0].Confidence = 0.95
	attested.Assertions[0].Importance = 5
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", attested, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Authority != "user_attested" {
		t.Fatalf("authority = %q", rec.Authority)
	}
	missing := testMemoryEnvelope("proj", "Missing evidence", "Missing evidence summary")
	missing.Evidence[0] = MemoryEvidence{ID: "E-001", Kind: MemoryEvidenceFile, Locator: MemoryLocator{Path: "missing.go"}, Summary: "gone", MissingAtWrite: true, MissingReason: "deleted"}
	rec, _, err = st.SaveMemoryEnvelope(context.Background(), "proj", missing, Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Grounded || !rec.MissingAtWrite {
		t.Fatalf("missing-at-write reporting: %+v", rec)
	}
}
