package dream

import (
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func mkFact(id, claim string, conf string, verified bool, firstSeen string) store.Fact {
	return store.Fact{ID: id, Claim: claim, Confidence: conf, Verified: verified, FirstSeen: firstSeen}
}

func TestRankDropsZeroMatchWhenTermsGiven(t *testing.T) {
	facts := []store.Fact{
		mkFact("a", "the auth flow lives in middleware", "high", true, "2026-08-01T00:00:00Z"),
		mkFact("b", "the auth middleware was added", "high", false, "2026-08-02T00:00:00Z"),
		mkFact("c", "unrelated note about caching", "mid", true, "2026-08-03T00:00:00Z"),
	}
	// 'auth' matches a and b; c matches nothing and is dropped from a search.
	ranked := Rank(facts, []string{"auth"}, false)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 relevant facts, got %d: %+v", len(ranked), rankedIDs(ranked))
	}
	if ranked[0].ID != "a" || ranked[1].ID != "b" {
		t.Fatalf("unexpected order: %s > %s (verified should win the tie)", ranked[0].ID, ranked[1].ID)
	}
}

func TestRankDistinctPerTerm(t *testing.T) {
	facts := []store.Fact{
		mkFact("auth", "authentication handled in middleware.ts", "mid", true, "t"),
		mkFact("cache", "caching via redis layer", "mid", true, "t"),
	}
	// searching 'auth' surfaces the auth fact first; 'cache' surfaces the cache fact.
	if got := Rank(facts, []string{"auth"}, false); got[0].ID != "auth" {
		t.Fatalf("term 'auth' should rank auth fact first, got %s", got[0].ID)
	}
	if got := Rank(facts, []string{"caching"}, false); got[0].ID != "cache" {
		t.Fatalf("term 'caching' should rank cache fact first, got %s", got[0].ID)
	}
}

func rankedIDs(facts []store.Fact) []string {
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		out = append(out, f.ID)
	}
	return out
}

func TestRankTieBreaksByRecency(t *testing.T) {
	facts := []store.Fact{
		mkFact("old", "auth middleware", "mid", false, "2026-08-01T00:00:00Z"),
		mkFact("new", "auth middleware", "mid", false, "2026-08-05T00:00:00Z"),
	}
	ranked := Rank(facts, []string{"auth"}, false)
	if ranked[0].ID != "new" {
		t.Fatalf("tie should break by recency, got %s", ranked[0].ID)
	}
}

func TestRankVerifiedOnly(t *testing.T) {
	facts := []store.Fact{
		mkFact("v", "x middleware", "mid", true, "t"),
		mkFact("u", "x middleware", "mid", false, "t"),
	}
	ranked := Rank(facts, []string{"middleware"}, true)
	if len(ranked) != 1 || ranked[0].ID != "v" {
		t.Fatalf("verified-only filter wrong: %+v", ranked)
	}
}

func TestQueryStoreFactsCaseInsensitiveAndPartialProject(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(tmp + "/k.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/home/user/github/example-app"); err != nil {
		t.Fatal(err)
	}
	st.UpsertFact(store.Fact{
		ID: "fact_1", ProjectID: "example-app", Claim: "Auth route uses refresh tokens with 7d expiry",
		Confidence: "high", Verified: true, RepoFiles: []string{"auth.go"}, Tools: []string{"devin"},
		SessionRef: "s1", FirstSeen: "2026-08-01T00:00:00Z", PromotedAt: "2026-08-01T00:00:00Z",
	})

	selectors := []string{"example-app", "example-app", "/home/user/github/example-app"}
	for _, sel := range selectors {
		facts, err := QueryStoreFacts(st, []string{"tokens"}, sel, "", false, false, false, 0)
		if err != nil {
			t.Fatalf("selector %q failed: %v", sel, err)
		}
		if len(facts) != 1 || facts[0].ID != "fact_1" {
			t.Fatalf("QueryStoreFacts selector %q expected 1 fact, got %v", sel, facts)
		}
	}

	// Test staleOnly filters out verified facts
	staleFacts, err := QueryStoreFacts(st, []string{"tokens"}, "example-app", "", false, true, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(staleFacts) != 0 {
		t.Fatalf("expected 0 stale facts, got %d", len(staleFacts))
	}

	// Test factsOnly filters out un-distilled claims
	onlyFacts, err := QueryStoreFacts(st, []string{"tokens"}, "example-app", "", false, false, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyFacts) != 0 {
		t.Fatalf("expected 0 facts when distill_status=pending, got %d", len(onlyFacts))
	}
}
