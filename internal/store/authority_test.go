package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// seedPreV13Facts inserts facts the way pre-v13 writes left them: no
// review_status, origin_role, or quoted columns existed, and Distill marked
// inferred invariants as verified with corroboration "invariant".
func seedPreV13Facts(t *testing.T, s *Store) {
	t.Helper()
	stmt := `INSERT INTO facts(id,project_id,claim,confidence,verified,file_refs_exist,repo_files,tools,session_ref,first_seen,promoted_at,refs_checked_at,distilled,distill_status,kind)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	rows := []struct {
		id, claim, conf, sessRef, kind string
		verified                       int
	}{
		{"f_inferred", "Rule: Prefer purple teapots in every build pipeline", "invariant", "sess-old", "invariant", 1},
		{"f_memo", "always use pure-Go SQLite", "corroborated", "manual_memo", "", 1},
		{"f_nongoal", "Non-goal: no background daemons", "corroborated", "manual_memo", "nongoal", 1},
		{"f_claim", "payment worker uses a pool of 4 goroutines", "corroborated", "sess-old", "code_claim", 1},
	}
	for _, r := range rows {
		if _, err := s.DB.Exec(stmt, r.id, "p", r.claim, r.conf, r.verified, r.verified, "[]", `["codex"]`, r.sessRef, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", r.claim, "distilled", r.kind); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationV13AuthorityDemotion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "authority.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedPreV13Facts(t, s)
	// Rewind to v12 so the next open re-runs migration 13 over the seeded rows.
	if _, err := s.DB.Exec("PRAGMA user_version=12"); err != nil {
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

	byID := map[string]Fact{}
	for _, f := range mustFacts(t, s) {
		byID[f.ID] = f
	}

	inferred := byID["f_inferred"]
	if inferred.ReviewStatus != ReviewCandidate {
		t.Fatalf("inferred invariant review_status = %q, want candidate", inferred.ReviewStatus)
	}
	if inferred.IsFact() || inferred.IsInvariant() {
		t.Fatal("demoted invariant still qualifies as fact/invariant")
	}
	if inferred.Verified || inferred.FileRefsExist {
		t.Fatal("demoted invariant kept verified/file_refs_exist")
	}

	if m := byID["f_memo"]; m.ReviewStatus != ReviewApproved || !m.IsFact() {
		t.Fatalf("manual memo not approved: %+v", m)
	}
	if ng := byID["f_nongoal"]; ng.ReviewStatus != ReviewApproved {
		t.Fatalf("nongoal not grandfathered: %+v", ng)
	}
	if c := byID["f_claim"]; c.ReviewStatus != ReviewApproved || !c.IsFact() {
		t.Fatalf("grounded claim not grandfathered: %+v", c)
	}
}

func TestCandidatePromotionViaMemo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "promotion.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	cand := Fact{
		ProjectID: "p", Claim: "Rule: always checkpoint the WAL",
		Kind: "invariant", Confidence: "invariant", Corroboration: "invariant",
		SessionRef: "sess-9", OriginRole: "assistant", Quoted: true,
		DistillStatus: "pending", ReviewStatus: ReviewCandidate,
	}
	if err := s.UpsertFact(cand); err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchFactsFTS("checkpoint", "p", false, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("candidate lookup failed: %v %+v", err, got)
	}
	if got[0].ReviewStatus != ReviewCandidate || got[0].IsFact() {
		t.Fatalf("candidate stored wrongly: %+v", got[0])
	}
	if got[0].OriginRole != "assistant" || !got[0].Quoted {
		t.Fatalf("candidate context lost: %+v", got[0])
	}

	// Promotion path: saving an explicit memo with the same claim approves it.
	if _, err := s.InsertMemo("p", "Rule: always checkpoint the WAL", "user", "", nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.SearchFactsFTS("checkpoint", "p", false, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("promotion lookup failed: %v %+v", err, got)
	}
	if got[0].ReviewStatus != ReviewApproved || !got[0].IsFact() {
		t.Fatalf("memo promotion did not approve candidate: %+v", got[0])
	}

	// Downgrade protection: re-distilling the same claim must not demote it.
	again := cand
	again.ReviewStatus = ReviewCandidate
	if err := s.UpsertFact(again); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SearchFactsFTS("checkpoint", "p", false, 10)
	if got[0].ReviewStatus != ReviewApproved {
		t.Fatalf("approved fact was demoted by re-distill: %+v", got[0])
	}
}

func mustFacts(t *testing.T, s *Store) []Fact {
	t.Helper()
	facts, err := s.FactsAll()
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

// TestStaleExcludesCandidates pins the distinction between an unapproved
// observation and filesystem drift. Migration v13 demotes inferred invariants
// to candidates with file_refs_exist=0; without this rule, "stale claims"
// would jump by the number of demoted facts (observed on real data: 90 -> 304)
// and report review status as missing files.
func TestStaleExcludesCandidates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	facts := []Fact{
		{
			ID: "f_candidate", ProjectID: "p",
			Claim: "Rule: prefer purple teapots", Kind: "invariant",
			Confidence: "candidate", Corroboration: "candidate",
			ReviewStatus: ReviewCandidate,
		},
		{
			ID: "f_stale_claim", ProjectID: "p",
			Claim: "Refunds run through src/legacy.go", Kind: "code_claim",
			Confidence: "grounded", Corroboration: "grounded",
			ReviewStatus: ReviewApproved, FileRefsExist: false,
			RepoFiles: []string{"src/legacy.go"},
		},
		{
			ID: "f_grounded", ProjectID: "p",
			Claim: "Auth uses bearer tokens in src/auth.ts", Kind: "code_claim",
			Confidence: "grounded", Corroboration: "grounded",
			ReviewStatus: ReviewApproved, FileRefsExist: true,
			RepoFiles: []string{"src/auth.ts"},
		},
		{
			ID: "f_invariant", ProjectID: "p",
			Claim: "Decision: pure-Go SQLite only", Kind: "invariant",
			Confidence: "invariant", Corroboration: "invariant",
			ReviewStatus: ReviewApproved, SessionRef: "manual_memo",
		},
	}

	for _, f := range facts {
		if err := s.UpsertFact(f); err != nil {
			t.Fatal(err)
		}
	}

	stale, err := s.StaleFacts("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].ID != "f_stale_claim" {
		t.Fatalf("stale facts = %+v, want only f_stale_claim", stale)
	}

	all, err := s.FactsAll()
	if err != nil {
		t.Fatal(err)
	}
	if got := FilterStale(all); len(got) != 1 {
		t.Fatalf("FilterStale = %d facts, want 1", len(got))
	}
	// The candidate must not be stale; it is merely unreviewed.
	for _, f := range all {
		if f.ID == "f_candidate" && f.IsStale() {
			t.Fatal("candidate reported as stale")
		}
	}
}

// TestSortableTimeOrdersChronologically guards the source_time comparison:
// RFC3339Nano's trimmed trailing zeros made same-second, differently-precise
// occurrences sort in the wrong order in TEXT columns.
func TestSortableTimeOrdersChronologically(t *testing.T) {
	base := time.Date(2026, 9, 15, 10, 0, 48, 0, time.UTC)
	half := base.Add(500 * time.Millisecond)
	early := SortableTime(base)
	late := SortableTime(half)
	if !(early < late) {
		t.Fatalf("sortable times not ordered: %q !< %q", early, late)
	}
	if len(early) != len(late) {
		t.Fatalf("sortable time is not fixed width: %q (%d) vs %q (%d)", early, len(early), late, len(late))
	}
	if !strings.HasSuffix(early, "Z") {
		t.Fatalf("sortable time lost its UTC marker: %q", early)
	}
}

// TestPruneFactsKeepsAuthoritativeRecords pins the boundary pruning must not
// cross: reconciling derived claims must never delete an explicit memo or a
// vision record. Distillation that yields nothing used to wipe a project's
// facts wholesale, including the memos that are the authorized write path.
func TestPruneFactsKeepsAuthoritativeRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prune.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	memo, err := s.InsertMemo("p", "always use pure-Go SQLite", "user", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	mcpMemo, err := s.InsertMemoWithSession("p", "mcp directive survives", "agent", "", nil, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	lesson, err := s.InsertLesson("p", FormatLesson("sync pruned a lesson", "narrow clause", "protect authority", ""), "agent", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	procedural, err := s.InsertCase("p", "Case: prune boundary", "agent", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	nonGoal, err := s.InsertNonGoal("p", "no background daemons", "user")
	if err != nil {
		t.Fatal(err)
	}
	milestone, err := s.InsertMilestone("p", "ship the prune fix", "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertFact(Fact{
		ID: "invariant-1", ProjectID: "p", Claim: "rule: authority is never pruned",
		Kind: "invariant", Confidence: "invariant", ReviewStatus: ReviewApproved,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertFact(Fact{
		ID: "derived-1", ProjectID: "p", Claim: "auth uses bearer tokens",
		Kind: "code_claim", Confidence: "grounded", ReviewStatus: ReviewApproved,
	}); err != nil {
		t.Fatal(err)
	}

	// Pruning with nothing to keep represents "distillation produced no claims".
	if err := s.PruneFacts("p", nil); err != nil {
		t.Fatal(err)
	}

	facts, err := s.FactsByProject("p")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Fact{}
	for _, f := range facts {
		byID[f.ID] = f
	}
	for name, id := range map[string]string{
		"CLI memo": memo.ID, "MCP memo": mcpMemo.ID, "lesson": lesson.ID,
		"case": procedural.ID, "non-goal": nonGoal.ID, "milestone": milestone.ID,
		"invariant": "invariant-1",
	} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("%s was pruned: %+v", name, facts)
		}
	}
	if _, ok := byID["derived-1"]; ok {
		t.Fatalf("derived claim should have been pruned: %+v", facts)
	}
	if audit, err := s.VerifyChain("p"); err != nil || !audit.Intact {
		t.Fatalf("chain broken after prune: err=%v audit=%+v", err, audit)
	}

	// A keep set may retain derived facts but must still preserve authority.
	if err := s.UpsertFact(Fact{
		ID: "derived-2", ProjectID: "p", Claim: "kept derived claim",
		Kind: "code_claim", Confidence: "grounded", ReviewStatus: ReviewApproved,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneFacts("p", []string{"derived-2"}); err != nil {
		t.Fatal(err)
	}
	facts, err = s.FactsByProject("p")
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, f := range facts {
		kept[f.ID] = true
	}
	if !kept["derived-2"] {
		t.Fatal("derived fact in keep was pruned")
	}
	if !kept[memo.ID] || !kept[mcpMemo.ID] || !kept[lesson.ID] || !kept[procedural.ID] {
		t.Fatalf("authority lost with keep set: %+v", facts)
	}
}
