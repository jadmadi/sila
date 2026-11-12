package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func openHookStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sila.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSaveSessionCardFromHandoff(t *testing.T) {
	st := openHookStore(t)
	ctx := context.Background()
	h := store.Handoff{
		ID:        "hoff_card_1",
		ProjectID: "sila-cli",
		Agent:     "dsh",
		Branch:    "main",
		Summary:   "shipped the connect command",
		OpenItems: []string{"claim session-card-capture"},
		RepoFiles: []string{"cmd/sila/connect.go"},
		Status:    "pending",
	}
	if err := st.InsertHandoff(h); err != nil {
		t.Fatal(err)
	}
	if err := saveSessionCardFromHandoff(st, "sila-cli", "/work/sila", "sess-1", "dsh", h.ID); err != nil {
		t.Fatalf("save card: %v", err)
	}
	cards, err := st.LatestSessionCards(ctx, "sila-cli", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	card := cards[0]
	if card.NextAction.Text != "claim session-card-capture" {
		t.Fatalf("next action = %q", card.NextAction.Text)
	}
	if card.Agent != "dsh" || card.SessionID != "sess-1" {
		t.Fatalf("identity = %+v", card)
	}
	if err := saveSessionCardFromHandoff(st, "sila-cli", "/work/sila", "sess-1", "dsh", h.ID); err != nil {
		t.Fatal(err)
	}
	cards, _ = st.LatestSessionCards(ctx, "sila-cli", 5)
	if len(cards) != 1 {
		t.Fatalf("replay created %d cards", len(cards))
	}
}

func TestSaveSessionCardModeOff(t *testing.T) {
	st := openHookStore(t)
	t.Setenv("SILA_CARD_MODE", "off")
	if err := saveSessionCardFromHandoff(st, "p", "/w", "s", "dsh", ""); err != nil {
		t.Fatal(err)
	}
	cards, err := st.LatestSessionCards(context.Background(), "p", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 0 {
		t.Fatalf("cards = %d, want 0", len(cards))
	}
}

func TestSaveSessionCardPartialWithoutHandoff(t *testing.T) {
	st := openHookStore(t)
	if err := saveSessionCardFromHandoff(st, "p", "/w", "s", "dsh", "missing"); err != nil {
		t.Fatal(err)
	}
	cards, err := st.LatestSessionCards(context.Background(), "p", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || !cards[0].Partial {
		t.Fatalf("cards = %+v", cards)
	}
}

func TestSaveSessionCardRequiresStore(t *testing.T) {
	if err := saveSessionCardFromHandoff(nil, "p", "/w", "s", "dsh", ""); err == nil {
		t.Fatal("expected an error for a nil store")
	}
}
