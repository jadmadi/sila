package main

import "testing"

func TestPlural(t *testing.T) {
	cases := map[string]string{
		"match": "matches",
		"fix":   "fixes",
		"entry": "entries",
		"goal":  "goals",
		"day":   "days",
		"class": "classes",
		"box":   "boxes",
	}
	for word, want := range cases {
		if got := plural(1, word); got != word {
			t.Errorf("plural(1, %q) = %q, want %q", word, got, word)
		}
		if got := plural(2, word); got != want {
			t.Errorf("plural(2, %q) = %q, want %q", word, got, want)
		}
		if got := plural(0, word); got != want {
			t.Errorf("plural(0, %q) = %q, want %q", word, got, want)
		}
	}
}
