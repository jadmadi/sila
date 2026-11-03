package render

import (
	"os"
	"strings"
	"testing"
)

func TestRenderCard_Basic(t *testing.T) {
	ui := New(os.Stdout, true) // noColor = true
	card := RenderBox(CardOptions{
		Title:       "v0.11.0",
		RightHeader: "2026-10-04",
		Lines: []string{
			" Features:",
			"  • Added conversational phrase router",
			"  • Added TOON output",
		},
		Width: 60,
		UI:    ui,
	})

	if !strings.Contains(card, "v0.11.0") {
		t.Errorf("expected card to contain title, got: %s", card)
	}
	if !strings.Contains(card, "2026-10-04") {
		t.Errorf("expected card to contain right header, got: %s", card)
	}
	if !strings.Contains(card, "╭─") || !strings.Contains(card, "─╮") {
		t.Errorf("expected rounded top border, got: %s", card)
	}
	if !strings.Contains(card, "╰") || !strings.Contains(card, "╯") {
		t.Errorf("expected rounded bottom border, got: %s", card)
	}
}

func TestRenderCard_WrapBullet(t *testing.T) {
	lines := WrapBullet("  • ", "This is a long line that should definitely wrap across multiple lines because the limit is small.", 30)
	if len(lines) < 2 {
		t.Fatalf("expected wrapped lines >= 2, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "  • ") {
		t.Errorf("first line should start with bullet prefix, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("second line should have hanging indent matching prefix width (4 spaces), got %q", lines[1])
	}
}
