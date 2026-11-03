package feedback

import (
	"os"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestNormalizeCategory(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"suggestion", "idea"},
		{"bug", "issue"},
		{"best-practice", "tip"},
		{"friction", "challenge"},
		{"thankyou", "praise"},
		{"custom", "custom"},
		{"", "idea"},
	}

	for _, c := range cases {
		got := NormalizeCategory(c.in)
		if got != c.want {
			t.Errorf("NormalizeCategory(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCategoryBadge(t *testing.T) {
	ui := render.New(os.Stdout, true)
	badge := CategoryBadge(ui, "idea")
	if badge == "" {
		t.Fatal("empty badge")
	}
}

func TestPrintFeedbackLedger(t *testing.T) {
	ui := render.New(os.Stdout, true)
	items := []store.Feedback{
		{
			ID:        "fb_1",
			ProjectID: "sila",
			Agent:     "antigravity",
			Category:  "idea",
			Content:   "Add feedback option",
			CreatedAt: "2026-08-29T07:45:00Z",
		},
	}
	stats := map[string]int{"idea": 1}
	statusStats := map[string]int{"open": 1}
	PrintFeedbackLedger(ui, items, stats, statusStats)
}
