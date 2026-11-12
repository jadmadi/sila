package dream

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
	"github.com/jadmadi/sila/internal/store"
)

func blockInput(now time.Time) StateBlockInput {
	return StateBlockInput{
		ProjectID: "sila-cli",
		Now:       now,
		Cards: []store.SessionCard{{
			Agent:      "dsh",
			CreatedAt:  now.Add(-2 * time.Hour),
			Outcome:    "shipped session cards",
			NextAction: store.CardNextAction{Text: "wire the state block"},
		}},
		Facts: []store.Fact{
			{ID: "f1", Kind: "memo", Claim: "keep the ten-tool surface", FirstSeen: now.Add(-time.Hour).Format(time.RFC3339)},
			{ID: "f2", Kind: "lesson", Claim: "run build.sh before committing a migration", FirstSeen: now.Add(-2 * time.Hour).Format(time.RFC3339)},
		},
		Goals: []goals.Goal{{Slug: "pushed-continuity-state-block", Status: "pending"}},
	}
}

func TestBuildStateBlockDeterministic(t *testing.T) {
	in := blockInput(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
	a := BuildStateBlock(in, 1200)
	b := BuildStateBlock(in, 1200)
	if a == "" || a != b {
		t.Fatalf("not deterministic or empty:\n%q\n%q", a, b)
	}
}

func TestBuildStateBlockEmpty(t *testing.T) {
	if got := BuildStateBlock(StateBlockInput{}, 1200); got != "" {
		t.Fatalf("empty input rendered %q", got)
	}
}

func TestBuildStateBlockSectionsAndStale(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	in := blockInput(now)
	got := BuildStateBlock(in, 1200)
	for _, want := range []string{"### Continuity state", "Last session (dsh, 2h ago)", "shipped session cards", "Next: wire the state block", "Decisions:", "Pitfalls:", "Goals: 1 in flight"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	old := blockInput(now)
	old.Cards[0].CreatedAt = now.Add(-30 * 24 * time.Hour)
	if out := BuildStateBlock(old, 1200); !strings.Contains(out, "(stale)") {
		t.Fatalf("30-day card missing stale marker:\n%s", out)
	}
}

func TestBuildStateBlockDeltaLine(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	in := blockInput(now)
	in.Delta = &WelcomeBackDelta{Gap: 48 * time.Hour, Decisions: 3, Lessons: 1, Handoffs: 1}
	got := BuildStateBlock(in, 1200)
	want := "- Since you left (2d): 3 decisions, 1 lesson, 1 handoff"
	if !strings.Contains(got, want) {
		t.Fatalf("missing delta line %q in:\n%s", want, got)
	}
}

func TestBuildStateBlockBudgetTrims(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	in := blockInput(now)
	in.Delta = &WelcomeBackDelta{Gap: 48 * time.Hour, Decisions: 99, Lessons: 99, Handoffs: 99, Capped: true}
	for i := 0; i < 20; i++ {
		in.Facts = append(in.Facts,
			store.Fact{ID: "memo" + string(rune('a'+i)), Kind: "memo", Claim: strings.Repeat("decision ", 20), FirstSeen: now.Format(time.RFC3339)},
			store.Fact{ID: "lesson" + string(rune('a'+i)), Kind: "lesson", Claim: strings.Repeat("pitfall ", 20), FirstSeen: now.Format(time.RFC3339)},
		)
	}
	got := BuildStateBlock(in, 400)
	if len(got) > 400 {
		t.Fatalf("block is %d bytes, budget 400", len(got))
	}
	if !strings.HasPrefix(got, "### Continuity state") {
		t.Fatalf("header lost: %q", got)
	}
	if strings.Contains(got, "- Goals:") {
		t.Fatal("goals should be trimmed first")
	}
}

func TestBuildStateBlockRedactsSecrets(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	in := blockInput(now)
	in.Facts = []store.Fact{{ID: "f", Kind: "memo", Claim: "deploy with password=hunter2", FirstSeen: now.Format(time.RFC3339)}}
	got := BuildStateBlock(in, 1200)
	if strings.Contains(got, "hunter2") {
		t.Fatalf("secret leaked:\n%s", got)
	}
	if !strings.Contains(got, "redacted") {
		t.Fatalf("missing redaction marker:\n%s", got)
	}
}

func openStateStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sila.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestBuildWelcomeBackDeltaCounts(t *testing.T) {
	st := openStateStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		if _, err := st.InsertMemoWithKind("p", "decision "+string(rune('a'+i)), "dsh", "", nil, "memo"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.InsertMemoWithKind("p", "lesson one", "dsh", "", nil, "lesson"); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(store.Handoff{ID: "h1", ProjectID: "p", Agent: "dsh", Summary: "s", Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	delta, ok := BuildWelcomeBackDelta(st, "p", now.Add(-48*time.Hour), now, 6*time.Hour)
	if !ok {
		t.Fatal("expected a delta")
	}
	if delta.Decisions != 3 || delta.Lessons != 1 || delta.Handoffs != 1 {
		t.Fatalf("delta = %+v", delta)
	}
	_ = ctx
}

func TestBuildWelcomeBackDeltaSkipsShortOrDisabled(t *testing.T) {
	st := openStateStore(t)
	now := time.Now().UTC()
	if _, ok := BuildWelcomeBackDelta(st, "p", now.Add(-2*time.Hour), now, 6*time.Hour); ok {
		t.Fatal("short gap produced a delta")
	}
	if _, ok := BuildWelcomeBackDelta(st, "p", now.Add(-48*time.Hour), now, 0); ok {
		t.Fatal("zero window produced a delta")
	}
	if _, ok := BuildWelcomeBackDelta(st, "p", now.Add(48*time.Hour), now, 6*time.Hour); ok {
		t.Fatal("future since produced a delta")
	}
}
