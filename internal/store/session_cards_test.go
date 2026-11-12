package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openCardStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "sila.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func testCard(project, session string, at time.Time) SessionCard {
	return SessionCard{
		Schema:    SessionCardSchema,
		ProjectID: project,
		SessionID: session,
		Agent:     "dsh",
		CreatedAt: at,
		Intent:    "ship the connect command",
		Outcome:   "certified",
		NextAction: CardNextAction{
			Text:  "claim session-card-capture",
			Owner: "agent",
		},
		Source: CardSource{Generator: "deterministic", Messages: 4},
	}
}

func TestSaveSessionCardRoundTrip(t *testing.T) {
	st := openCardStore(t)
	ctx := context.Background()
	card := testCard("sila-cli", "sess-1", time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
	id, err := st.SaveSessionCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	if id != SessionCardID("sila-cli", "sess-1") {
		t.Fatalf("id = %q", id)
	}
	got, ok, err := st.GetSessionCard(ctx, id)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Intent != card.Intent || got.Agent != "dsh" || got.NextAction.Text != card.NextAction.Text {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestSaveSessionCardUpserts(t *testing.T) {
	st := openCardStore(t)
	ctx := context.Background()
	card := testCard("sila-cli", "sess-1", time.Now().UTC())
	if _, err := st.SaveSessionCard(ctx, card); err != nil {
		t.Fatal(err)
	}
	card.Outcome = "rewritten"
	if _, err := st.SaveSessionCard(ctx, card); err != nil {
		t.Fatal(err)
	}
	cards, err := st.LatestSessionCards(ctx, "sila-cli", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	if cards[0].Outcome != "rewritten" {
		t.Fatalf("outcome = %q", cards[0].Outcome)
	}
}

func TestLatestSessionCardsCapAndOrder(t *testing.T) {
	st := openCardStore(t)
	ctx := context.Background()
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 25; i++ {
		card := testCard("p", fmt.Sprintf("sess-%02d", i), base.Add(time.Duration(i)*time.Minute))
		if _, err := st.SaveSessionCard(ctx, card); err != nil {
			t.Fatal(err)
		}
	}
	cards, err := st.LatestSessionCards(ctx, "p", 99)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != SessionCardMaxList {
		t.Fatalf("cards = %d, want %d", len(cards), SessionCardMaxList)
	}
	if cards[0].SessionID != "sess-24" {
		t.Fatalf("newest = %q", cards[0].SessionID)
	}
	if cards[len(cards)-1].SessionID != "sess-05" {
		t.Fatalf("oldest kept = %q", cards[len(cards)-1].SessionID)
	}
}

func TestSaveSessionCardRejectsOversize(t *testing.T) {
	st := openCardStore(t)
	card := testCard("p", "s", time.Now().UTC())
	card.Intent = strings.Repeat("x", SessionCardMaxBytes)
	if _, err := st.SaveSessionCard(context.Background(), card); err == nil {
		t.Fatal("expected an oversize error")
	}
}

func TestSaveSessionCardRequiresCreatedAt(t *testing.T) {
	st := openCardStore(t)
	card := testCard("p", "s", time.Time{})
	if _, err := st.SaveSessionCard(context.Background(), card); err == nil {
		t.Fatal("expected a validation error")
	}
}

func TestSessionCardMigrationVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sila.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := st.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version < 45 {
		t.Fatalf("schema version = %d, want at least 45", version)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	cards, err := reopened.LatestSessionCards(context.Background(), "p", 5)
	if err != nil {
		t.Fatalf("latest after reopen: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("fresh store has %d cards", len(cards))
	}
}
