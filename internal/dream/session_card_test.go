package dream

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func cardInput() CardInput {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	return CardInput{
		Now:        now,
		ProjectID:  "sila-cli",
		SessionID:  "sess-1",
		Agent:      "dsh",
		ProjectDir: "/work/sila",
		HomeDir:    "/home/user",
		Messages: []CardMessage{
			{Role: "user", Content: "Ship the connect command and verify it"},
			{Role: "assistant", Content: "Implemented list, add, verify."},
			{Role: "user", Content: "decision: keep the ten-tool surface"},
			{Role: "assistant", Content: "Done."},
		},
		FilesTouched: []string{"/work/sila/cmd/sila/connect.go", "README.md", "/etc/passwd"},
		OpenItems:    []string{"claim session-card-capture"},
	}
}

func TestBuildSessionCardDeterministic(t *testing.T) {
	a, err := BuildSessionCard(cardInput())
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildSessionCard(cardInput())
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) {
		t.Fatal("builder is not deterministic")
	}
}

func TestBuildSessionCardFields(t *testing.T) {
	card, err := BuildSessionCard(cardInput())
	if err != nil {
		t.Fatal(err)
	}
	if card.Intent != "Ship the connect command and verify it" {
		t.Fatalf("intent = %q", card.Intent)
	}
	if card.Outcome != "Done." {
		t.Fatalf("outcome = %q", card.Outcome)
	}
	if len(card.Decisions) != 1 || !strings.Contains(card.Decisions[0].Text, "ten-tool") {
		t.Fatalf("decisions = %+v", card.Decisions)
	}
	if card.NextAction.Text != "claim session-card-capture" || card.NextAction.Owner != "agent" {
		t.Fatalf("next action = %+v", card.NextAction)
	}
	if card.ID != store.SessionCardID("sila-cli", "sess-1") {
		t.Fatalf("id = %q", card.ID)
	}
	if card.Source.Generator != "deterministic" || card.Source.Messages != 4 {
		t.Fatalf("source = %+v", card.Source)
	}
}

func TestBuildSessionCardFiles(t *testing.T) {
	in := cardInput()
	in.FilesTouched = nil
	for i := 0; i < 1000; i++ {
		in.FilesTouched = append(in.FilesTouched, "/work/sila/internal/file"+string(rune('a'+i%26))+".go")
	}
	in.FilesTouched = append(in.FilesTouched, "/etc/passwd", "/home/user/.ssh/id_rsa")
	card, err := BuildSessionCard(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(card.FilesTouched) != 20 {
		t.Fatalf("files = %d, want 20", len(card.FilesTouched))
	}
	for _, f := range card.FilesTouched {
		if strings.HasPrefix(f, "/") || strings.HasPrefix(f, "~") {
			t.Fatalf("unsafe path kept: %q", f)
		}
	}
	for i := 1; i < len(card.FilesTouched); i++ {
		if card.FilesTouched[i-1] > card.FilesTouched[i] {
			t.Fatalf("files not sorted at %d", i)
		}
	}
}

func TestBuildSessionCardRedactsSecrets(t *testing.T) {
	in := cardInput()
	in.Messages = []CardMessage{{Role: "user", Content: "deploy with password=hunter2 and token: abc123"}}
	card, err := BuildSessionCard(in)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(card)
	text := string(payload)
	if strings.Contains(text, "hunter2") || strings.Contains(text, "abc123") {
		t.Fatalf("secret leaked: %s", text)
	}
	if !strings.Contains(text, "[redacted]") {
		t.Fatalf("expected redaction marker: %s", text)
	}
}

func TestBuildSessionCardTruncatesUnderCap(t *testing.T) {
	in := cardInput()
	in.Messages = nil
	for i := 0; i < 60; i++ {
		in.Messages = append(in.Messages, CardMessage{Role: "user", Content: "decision: " + strings.Repeat("x", 150+i)})
	}
	card, err := BuildSessionCard(in)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(card)
	if len(payload) > store.SessionCardMaxBytes {
		t.Fatalf("payload = %d bytes", len(payload))
	}
	if !card.Source.Truncated {
		t.Fatal("expected the truncated flag")
	}
}

func TestBuildSessionCardPartialWithoutMessages(t *testing.T) {
	in := cardInput()
	in.Messages = nil
	card, err := BuildSessionCard(in)
	if err != nil {
		t.Fatal(err)
	}
	if !card.Partial {
		t.Fatal("expected a partial card")
	}
	if card.NextAction.Text != "claim session-card-capture" {
		t.Fatalf("next action = %q", card.NextAction.Text)
	}
}

func TestFormatSessionCards(t *testing.T) {
	if got := FormatSessionCards(nil, 3); got != "" {
		t.Fatalf("empty render = %q", got)
	}
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	cards := []store.SessionCard{
		{Agent: "dsh", CreatedAt: now, Outcome: "first", NextAction: store.CardNextAction{Text: "next one"}},
		{Agent: "codex", CreatedAt: now.Add(time.Hour), Outcome: "second"},
		{Agent: "zed", CreatedAt: now.Add(2 * time.Hour), Intent: "third"},
		{Agent: "extra", CreatedAt: now.Add(3 * time.Hour), Outcome: "fourth"},
	}
	got := FormatSessionCards(cards, 3)
	if !strings.Contains(got, "Recent session cards") {
		t.Fatalf("missing heading: %q", got)
	}
	if strings.Contains(got, "fourth") {
		t.Fatalf("rendered more than three cards: %q", got)
	}
	if !strings.Contains(got, "Next: next one") {
		t.Fatalf("missing next action: %q", got)
	}
	if len(got) > 1600 {
		t.Fatalf("section is %d bytes", len(got))
	}
}
