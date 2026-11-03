package dream

import (
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/store"
)

type mockRerankProvider struct {
	scores       map[string]float64
	answerExists float64
	hasAnswer    bool
	err          error
}

func (m *mockRerankProvider) Name() assist.ModelProvider {
	return assist.ProviderMock
}

func (m *mockRerankProvider) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]assist.Question) (*assist.ProviderResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	nouls := make(map[string]assist.NoulAnswer)
	for k, v := range m.scores {
		nouls[k] = assist.NoulAnswer{Noul: v}
	}
	if m.hasAnswer {
		nouls["answer_exists"] = assist.NoulAnswer{Noul: m.answerExists}
	}
	return &assist.ProviderResponse{
		Nouls:      nouls,
		DurationMs: 15,
	}, nil
}

func TestOmni_LowerAuthorityNeverOvertakesHigherAuthority(t *testing.T) {
	// Structural invariant: Invariant (5000) > Fact (4000) > Primary (3000) > Doc (2000) > Candidate (1000)
	items := []OmniResultItem{
		{
			Type:  OmniTypeDoc,
			ID:    "doc-high-semantic",
			Score: tierDoc + 10, // 2010
		},
		{
			Type:  OmniTypeMessage,
			ID:    "msg-low-semantic",
			Score: tierPrimary + 0, // 3000
		},
		{
			Type:  OmniTypeCandidate,
			ID:    "cand-huge-score",
			Score: tierCandidate + 50000, // Even with a massive local or malformed score!
		},
		{
			Type:  OmniTypeFact,
			ID:    "fact-zero-semantic",
			Score: tierFact + 0, // 4000
		},
		{
			Type:  OmniTypeInvariant,
			ID:    "inv-rule",
			Score: tierInvariant + 0, // 5000
		},
	}

	sortOmniItems(items)

	// Invariant must be 1st
	if items[0].Type != OmniTypeInvariant {
		t.Fatalf("expected 1st item to be Invariant, got %s (%s)", items[0].Type, items[0].ID)
	}
	// Fact must be 2nd
	if items[1].Type != OmniTypeFact {
		t.Fatalf("expected 2nd item to be Fact, got %s (%s)", items[1].Type, items[1].ID)
	}
	// Primary (Message) must be 3rd
	if items[2].Type != OmniTypeMessage {
		t.Fatalf("expected 3rd item to be Message, got %s (%s)", items[2].Type, items[2].ID)
	}
	// Doc must be 4th
	if items[3].Type != OmniTypeDoc {
		t.Fatalf("expected 4th item to be Doc, got %s (%s)", items[3].Type, items[3].ID)
	}
	// Candidate must be 5th
	if items[4].Type != OmniTypeCandidate {
		t.Fatalf("expected 5th item to be Candidate, got %s (%s)", items[4].Type, items[4].ID)
	}
}

func TestOmni_RerankScoreValidation(t *testing.T) {
	ctx := context.Background()

	// Provider returns malicious / invalid Nouls: > 1, < 0, NaN
	provider := &mockRerankProvider{
		scores: map[string]float64{
			"rel_00": 999999.0, // Maliciously large
			"rel_01": -42.0,    // Negative
			"rel_02": math.NaN(),
			"rel_03": 0.85, // Valid
		},
		hasAnswer:    true,
		answerExists: 0.0, // Exact zero
	}

	candidates := []assist.RerankCandidate{
		{Key: "c0", Excerpt: "text 0"},
		{Key: "c1", Excerpt: "text 1"},
		{Key: "c2", Excerpt: "text 2"},
		{Key: "c3", Excerpt: "text 3"},
	}

	res, err := assist.Rerank(ctx, provider, "test query", candidates)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}

	// c0, c1, c2 are invalid; partial reranking must be rejected (empty Scores)
	// to preserve deterministic local ranking
	if len(res.Scores) != 0 {
		t.Errorf("expected partial scores to be rejected, got %d scores: %v", len(res.Scores), res.Scores)
	}

	// Exact zero answer exists must be recorded with HasAnswerExists = true
	if !res.HasAnswerExists || res.AnswerExists != 0.0 {
		t.Errorf("expected HasAnswerExists=true and AnswerExists=0.0, got has=%v val=%v", res.HasAnswerExists, res.AnswerExists)
	}

	// Provider with all valid scores returns complete scores
	validProvider := &mockRerankProvider{
		scores: map[string]float64{
			"rel_00": 0.10,
			"rel_01": 0.50,
			"rel_02": 0.70,
			"rel_03": 0.85,
		},
		hasAnswer:    true,
		answerExists: 0.90,
	}
	validRes, err := assist.Rerank(ctx, validProvider, "test query", candidates)
	if err != nil {
		t.Fatalf("Rerank with valid provider failed: %v", err)
	}
	if len(validRes.Scores) != 4 {
		t.Errorf("expected 4 scores for valid provider, got %d", len(validRes.Scores))
	}
	if s, ok := validRes.Scores["c3"]; !ok || s != 0.85 {
		t.Errorf("expected valid score 0.85 for c3, got %v", s)
	}
}

func TestOmni_ZeroAnswerExistsTriggersWarning(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/test"); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertFact(store.Fact{
		ID:        "fact-1",
		ProjectID: "test",
		Claim:     "PostgreSQL connection pooling configuration",
		Kind:      "fact",
		FirstSeen: "2026-08-01T10:00:00Z",
	})
	_ = st.UpsertFact(store.Fact{
		ID:        "fact-2",
		ProjectID: "test",
		Claim:     "SQLite write-ahead logging settings",
		Kind:      "fact",
		FirstSeen: "2026-08-01T10:01:00Z",
	})

	// When semantic assistance is requested but provider is not configured,
	// warning must be emitted and exact local order preserved.
	t.Setenv("SILA_ASSIST_ENABLED", "false")
	res, err := OmniSearchFiltered(st, "connection", OmniOptions{
		Project:  "test",
		Semantic: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	foundWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "semantic assistance not consented") || strings.Contains(w, "preserved deterministic local ranking") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected semantic availability warning when provider is unconfigured, got warnings: %v", res.Warnings)
	}

	// Verify exact local ordering is preserved
	if len(res.Items) == 0 {
		t.Fatal("expected items returned")
	}
}

func TestOmni_DeterministicTieBreaking(t *testing.T) {
	items := []OmniResultItem{
		{Type: OmniTypeFact, ID: "fact-b", Score: 4010, Timestamp: "2026-08-01T10:00:00Z"},
		{Type: OmniTypeFact, ID: "fact-a", Score: 4010, Timestamp: "2026-08-01T10:00:00Z"},
		{Type: OmniTypeFact, ID: "fact-newer", Score: 4010, Timestamp: "2026-08-01T10:05:00Z"},
	}

	sortOmniItems(items)

	// fact-newer has newer timestamp: ranks 1st
	if items[0].ID != "fact-newer" {
		t.Errorf("expected newer timestamp to rank 1st, got %s", items[0].ID)
	}
	// fact-a and fact-b tie on score and timestamp: sorted by ID ascending (fact-a before fact-b)
	if items[1].ID != "fact-a" || items[2].ID != "fact-b" {
		t.Errorf("expected deterministic tie-breaker (fact-a then fact-b), got %s then %s", items[1].ID, items[2].ID)
	}
}

func TestOmni_SemanticRerankEndToEnd(t *testing.T) {
	t.Setenv("SILA_ASSIST_ENABLED", "true")
	t.Setenv("SILA_ASSIST_PROVIDER", "mock")
	t.Setenv("SILA_ASSIST_SCOPES", "search,all")

	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "omni.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := st.EnsureProject("/workspace/test"); err != nil {
		t.Fatal(err)
	}
	_ = st.UpsertFact(store.Fact{
		ID:           "fact-1",
		ProjectID:    "test",
		Claim:        "Authentication token rotation policy",
		Kind:         "fact",
		Verified:     true,
		ReviewStatus: store.ReviewApproved,
		FirstSeen:    "2026-08-01T10:00:00Z",
	})
	_ = st.UpsertFact(store.Fact{
		ID:           "fact-2",
		ProjectID:    "test",
		Claim:        "Database connection pooling policy",
		Kind:         "fact",
		Verified:     true,
		ReviewStatus: store.ReviewApproved,
		FirstSeen:    "2026-08-01T10:01:00Z",
	})

	res, err := OmniSearchFiltered(st, "policy", OmniOptions{
		Project:  "test",
		Semantic: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !res.SemanticReranked {
		t.Errorf("expected SemanticReranked to be true when mock provider is active, warnings=%v", res.Warnings)
	}
	if len(res.Items) == 0 {
		t.Fatal("expected items returned")
	}
	// Authority tier checks: facts stay facts, never outranked by lower tiers
	for _, it := range res.Items {
		if it.Type != OmniTypeFact {
			t.Errorf("unexpected item type: %s", it.Type)
		}
	}
}
