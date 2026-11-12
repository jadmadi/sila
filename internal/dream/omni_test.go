package dream

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestOmniSearch(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "omni.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	st.EnsureProject("/workspace/my-app")

	// 1. Approved invariant, plus an unapproved candidate from the same tier.
	_ = st.UpsertFact(store.Fact{
		ID:           "fact-1",
		ProjectID:    "my-app",
		Claim:        "Authentication uses JWT tokens signed with Ed25519",
		Kind:         "invariant",
		Confidence:   "invariant",
		Verified:     true,
		FirstSeen:    "2026-08-01T10:00:00Z",
		ReviewStatus: store.ReviewApproved,
	})
	_ = st.UpsertFact(store.Fact{
		ID:         "cand-1",
		ProjectID:  "my-app",
		Claim:      "Quoted rule: authentication should also use teapot protocols",
		Kind:       "invariant",
		Confidence: "candidate",
		Quoted:     true,
		FirstSeen:  "2026-08-01T10:00:00Z",
	})

	// 4. A handoff, which omni previously never searched.
	_ = st.InsertHandoff(store.Handoff{
		ProjectID: "my-app",
		Agent:     "devin",
		Summary:   "authentication rewrite baton pass",
		OpenItems: []string{"rotate the authentication keys"},
		CreatedAt: "2026-08-01T11:00:00Z",
		Status:    "open",
	})

	// 2. Message
	_, _, _ = st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "my-app", ContentHash: "h1", TS: "2026-08-01T10:00:00Z", Role: "user", Content: "How does authentication work?", Tool: "devin"},
		{ProjectID: "my-app", ContentHash: "h2", TS: "2026-08-01T10:01:00Z", Role: "commit", Content: "Git Commit abc1234: Add authentication middleware", Tool: "git"},
	})

	// 3. Doc
	_ = st.InsertDoc(store.Doc{
		ID:        "doc-1",
		ProjectID: "my-app",
		Title:     "Auth Security Architecture",
		Content:   "Detailed authentication and authorization manual",
		CreatedAt: "2026-08-01T10:00:00Z",
	})

	res, err := OmniSearch(st, "authentication", "my-app", 10)
	if err != nil {
		t.Fatalf("OmniSearch failed: %v", err)
	}

	if len(res.Items) == 0 {
		t.Fatalf("expected search hits, got 0")
	}

	// Verify we got items from multiple tiers
	hasInvariant := false
	hasCommit := false
	hasDoc := false
	for _, it := range res.Items {
		if it.Type == OmniTypeInvariant || it.Type == OmniTypeFact {
			hasInvariant = true
		}
		if it.Type == OmniTypeCommit {
			hasCommit = true
		}
		if it.Type == OmniTypeDoc {
			hasDoc = true
		}
	}

	if !hasInvariant || !hasCommit || !hasDoc {
		t.Fatalf("expected multi-tier hits, got invariant=%v, commit=%v, doc=%v (items: %+v)", hasInvariant, hasCommit, hasDoc, res.Items)
	}

	// The handoff tier is reachable — a handoff-only term used to return zero.
	handoffHits, err := OmniSearch(st, "baton pass", "my-app", 10)
	if err != nil {
		t.Fatal(err)
	}
	foundHandoff := false
	for _, it := range handoffHits.Items {
		if it.Type == OmniTypeHandoff {
			foundHandoff = true
		}
	}
	if !foundHandoff {
		t.Fatalf("handoff evidence invisible to omni: %+v", handoffHits.Items)
	}

	// Authority survives presentation: the quoted candidate is labeled a
	// candidate, keeps its review status and quotation context, and ranks below
	// primary evidence instead of above it.
	var candidate, invariant, message, doc int
	for i, it := range res.Items {
		switch it.Type {
		case OmniTypeCandidate:
			candidate = i
			if it.ReviewStatus != store.ReviewCandidate || !it.Quoted {
				t.Fatalf("candidate lost its authority context: %+v", it)
			}
		case OmniTypeInvariant:
			invariant = i
		case OmniTypeMessage, OmniTypeCommit:
			if message == 0 {
				message = i
			}
		case OmniTypeDoc:
			doc = i
		}
	}
	if invariant != 0 {
		t.Fatalf("approved invariant should rank first, got index %d: %+v", invariant, res.Items)
	}
	if candidate == 0 {
		t.Fatal("candidate fragment missing from results")
	}
	if message == 0 && doc == 0 {
		t.Fatal("expected primary evidence or docs in the result set")
	}
	if candidate < message || candidate < doc {
		t.Fatalf("unapproved candidate outranked primary evidence (candidate=%d message=%d doc=%d)", candidate, message, doc)
	}

	// Counts are honest: returned vs matched.
	if res.Returned != len(res.Items) {
		t.Fatalf("returned=%d but %d items", res.Returned, len(res.Items))
	}
	if res.Matched < res.Returned {
		t.Fatalf("matched=%d cannot be less than returned=%d", res.Matched, res.Returned)
	}
	if res.TotalHits != res.Returned {
		t.Fatalf("legacy total_hits must stay the returned count: %d vs %d", res.TotalHits, res.Returned)
	}
}

// TestOmniReportsBrokenTier is the re-eval F07 probe: with the transcript FTS
// table dropped, omni used to return zero hits and a nil error, which reads
// exactly like "no evidence exists".
func TestOmniReportsBrokenTier(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject("/workspace/broken"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "broken", ContentHash: "h1", TS: "2026-08-01T10:00:00Z", Role: "user", Content: "unique marker phrase", Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE messages_fts`); err != nil {
		t.Fatal(err)
	}

	res, err := OmniSearch(st, "unique marker phrase", "broken", 10)
	if err != nil {
		t.Fatalf("omni must not fail the whole query for one broken tier: %v", err)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "transcript tier failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a broken tier must be reported, got warnings=%v items=%d", res.Warnings, len(res.Items))
	}
}

// TestOmniFiltersReportedNotIgnored pins the filter contract: filters reach the
// tiers that can honor them, and an inapplicable filter is stated rather than
// silently changing nothing.
func TestOmniFiltersReportedNotIgnored(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.EnsureProject("/workspace/filters"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("", "devin", "1", []store.LoadMessage{
		{ProjectID: "filters", ContentHash: "f1", TS: "2026-08-01T10:00:00Z", Role: "user", Content: "filtered marker by devin", Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.WriteRun("", "codex", "1", []store.LoadMessage{
		{ProjectID: "filters", ContentHash: "f2", TS: "2026-08-01T10:00:00Z", Role: "assistant", Content: "filtered marker by codex", Tool: "codex"},
	}); err != nil {
		t.Fatal(err)
	}

	res, err := OmniSearchFiltered(st, "filtered marker", OmniOptions{Project: "filters", Limit: 10, Tool: "devin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) == 0 {
		t.Fatal("tool filter dropped every hit")
	}
	for _, it := range res.Items {
		if it.Tools != nil && !containsFold(it.Tools, "devin") {
			t.Fatalf("--tool filter ignored for tier %s: %+v", it.Type, it.Tools)
		}
	}

	// A role filter is meaningful for transcripts only; saying so beats
	// silently returning the same set.
	roleRes, err := OmniSearchFiltered(st, "filtered marker", OmniOptions{Project: "filters", Limit: 10, Role: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	var noted bool
	for _, w := range roleRes.Warnings {
		if strings.Contains(w, "role filter") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("role filter scope must be stated, got warnings=%v", roleRes.Warnings)
	}
	for _, it := range roleRes.Items {
		if it.Type == OmniTypeMessage && it.Content == "filtered marker by devin" {
			t.Fatalf("role filter did not restrict transcripts: %+v", it)
		}
	}
}

func TestOmniSearchTemporalProximityBoost(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "omni_temporal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	st.EnsureProject("/workspace/temporal-app")

	now := time.Now().UTC()
	tLastWeek := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	t50DaysAgo := now.Add(-50 * 24 * time.Hour).Format(time.RFC3339)

	// Older session (50 days ago) with identical keywords
	_, _, err = st.WriteRun("", "claude-code", "sess-old", []store.LoadMessage{
		{
			ProjectID:   "temporal-app",
			ContentHash: "hash-old-msg",
			TS:          t50DaysAgo,
			Role:        "assistant",
			Content:     "resolved intermittent websocket connection timeouts during peak load",
			Tool:        "claude-code",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Last week's session (7 days ago) with identical keywords
	_, _, err = st.WriteRun("", "claude-code", "sess-last-week", []store.LoadMessage{
		{
			ProjectID:   "temporal-app",
			ContentHash: "hash-last-week-msg",
			TS:          tLastWeek,
			Role:        "assistant",
			Content:     "resolved intermittent websocket connection timeouts during peak load",
			Tool:        "claude-code",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Omni-search with temporal anchor: "websocket connection timeouts last week"
	res, err := OmniSearchFiltered(st, "websocket connection timeouts last week", OmniOptions{Project: "temporal-app", Limit: 10})
	if err != nil {
		t.Fatalf("OmniSearchFiltered failed: %v", err)
	}
	if len(res.Items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(res.Items))
	}

	// The session from last week must decisively rank first!
	if res.Items[0].Timestamp != tLastWeek {
		t.Errorf("expected last week's session to rank first, got timestamp %s (wanted %s)", res.Items[0].Timestamp, tLastWeek)
	}
	if res.Items[0].Score <= res.Items[1].Score {
		t.Errorf("expected last week's item score (%d) to exceed older item score (%d)", res.Items[0].Score, res.Items[1].Score)
	}
}

// TestOmniSearchACLScoped proves that an ACL-scoped read keeps same-project
// records and cross-project global records, while foreign project-scoped facts,
// transcripts, and commits never leak.
func TestOmniSearchACLScoped(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureProject("/workspace/beta"); err != nil {
		t.Fatal(err)
	}

	alphaMemo, err := st.InsertMemoWithProvenanceAndVisibility("alpha", "alpha sqlite private fee schedule", "agent", "", nil, "", store.VisibilityProject, store.Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	globalLesson, err := st.InsertMemoWithProvenanceAndVisibility("alpha", store.FormatLesson("sqlite lock contention", "shared writer", "busy timeout retry", "sqlite"), "agent", "", nil, "lesson", store.VisibilityGlobal, store.Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	betaMemo, err := st.InsertMemoWithProvenanceAndVisibility("beta", "beta sqlite deployment pipeline", "agent", "", nil, "", store.VisibilityProject, store.Provenance{})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := st.WriteRun("", "agent", "acl-session", []store.LoadMessage{
		{ProjectID: "alpha", ContentHash: "acl-alpha-msg", TS: "2026-11-01T10:00:00Z", Role: "user", Content: "alpha sqlite transcript detail", Tool: "devin"},
		{ProjectID: "alpha", ContentHash: "acl-alpha-commit", TS: "2026-11-01T10:01:00Z", Role: "commit", Content: "Git Commit abc1234: alpha sqlite commit", Tool: "git"},
		{ProjectID: "beta", ContentHash: "acl-beta-msg", TS: "2026-11-01T10:02:00Z", Role: "user", Content: "beta sqlite transcript detail", Tool: "devin"},
	}); err != nil {
		t.Fatal(err)
	}

	hasID := func(items []OmniResultItem, id string) bool {
		for _, item := range items {
			if item.ID == id {
				return true
			}
		}
		return false
	}
	hasContent := func(items []OmniResultItem, needle string) bool {
		for _, item := range items {
			if strings.Contains(item.Content, needle) {
				return true
			}
		}
		return false
	}

	scoped, err := OmniSearchFiltered(st, "sqlite", OmniOptions{ACLScoped: true, RequesterProject: "beta", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !hasID(scoped.Items, betaMemo.ID) {
		t.Errorf("same-project beta memo missing from ACL-scoped results: %+v", scoped.Items)
	}
	if !hasID(scoped.Items, globalLesson.ID) {
		t.Errorf("global lesson missing from ACL-scoped results: %+v", scoped.Items)
	}
	if hasID(scoped.Items, alphaMemo.ID) {
		t.Errorf("foreign project-scoped memo leaked into ACL-scoped results")
	}
	if hasContent(scoped.Items, "alpha sqlite transcript detail") {
		t.Errorf("foreign transcript leaked into ACL-scoped results")
	}
	if hasContent(scoped.Items, "alpha sqlite commit") {
		t.Errorf("foreign commit leaked into ACL-scoped results")
	}
	if !hasContent(scoped.Items, "beta sqlite transcript detail") {
		t.Errorf("same-project transcript missing from ACL-scoped results")
	}
	for _, item := range scoped.Items {
		if item.ID == globalLesson.ID && item.Visibility != store.VisibilityGlobal {
			t.Errorf("global lesson visibility = %q, want %q", item.Visibility, store.VisibilityGlobal)
		}
	}

	unscoped, err := OmniSearchFiltered(st, "sqlite", OmniOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !hasID(unscoped.Items, alphaMemo.ID) {
		t.Errorf("unscoped search should still see the alpha memo")
	}
	if !hasContent(unscoped.Items, "alpha sqlite transcript detail") {
		t.Errorf("unscoped search should still see the alpha transcript")
	}
}
