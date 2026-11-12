package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestLoadCardBySessionAndID(t *testing.T) {
	st := openHookStore(t)
	ctx := context.Background()
	card := store.SessionCard{
		Schema:     store.SessionCardSchema,
		ProjectID:  "p",
		SessionID:  "s1",
		Agent:      "dsh",
		CreatedAt:  time.Now().UTC(),
		Intent:     "test",
		NextAction: store.CardNextAction{Text: "next"},
	}
	id, err := st.SaveSessionCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}
	byID, ok, err := loadCard(ctx, st, "p", id)
	if err != nil || !ok || byID.SessionID != "s1" {
		t.Fatalf("by id: ok=%v err=%v card=%+v", ok, err, byID)
	}
	bySession, ok, err := loadCard(ctx, st, "p", "s1")
	if err != nil || !ok || bySession.ID != id {
		t.Fatalf("by session: ok=%v err=%v card=%+v", ok, err, bySession)
	}
	if _, ok, _ := loadCard(ctx, st, "p", "missing"); ok {
		t.Fatal("missing session resolved a card")
	}
}

func TestShortCardID(t *testing.T) {
	if got := shortCardID("sha256:0123456789abcdef0123"); got != "0123456789abcdef" {
		t.Fatalf("short = %q", got)
	}
	if got := shortCardID("sha256:abc"); got != "abc" {
		t.Fatalf("short = %q", got)
	}
}

func TestClipCardText(t *testing.T) {
	if got := clipCardText(strings.Repeat("x", 30), 10); !strings.HasSuffix(got, "...") || len(got) != 13 {
		t.Fatalf("clip = %q", got)
	}
}
