package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestManPageAndIndex(t *testing.T) {
	out := captureStdout(t, func() {
		runMan(render.New(nil, true), false, []string{"gate"})
	})
	if !strings.Contains(out, "## Synopsis") || !strings.Contains(out, "# Quality gate") {
		t.Fatalf("page = %s", out)
	}
	index := captureStdout(t, func() {
		runMan(render.New(nil, true), true, nil)
	})
	var entries []map[string]any
	if err := json.Unmarshal([]byte(index), &entries); err != nil || len(entries) < 8 {
		t.Fatalf("index: %v %s", err, index)
	}
	pageJSON := captureStdout(t, func() {
		runMan(render.New(nil, true), true, []string{"revision"})
	})
	var page map[string]any
	if err := json.Unmarshal([]byte(pageJSON), &page); err != nil || page["topic"] != "revision" {
		t.Fatalf("page json: %v %s", err, pageJSON)
	}
}

func TestManSearchAndSuggestions(t *testing.T) {
	out := captureStdout(t, func() {
		runMan(render.New(nil, true), false, []string{"--search=revision"})
	})
	if !strings.Contains(out, "revision") {
		t.Fatalf("search = %s", out)
	}
	suggestions := suggestTopics("gates", 3)
	found := false
	for _, suggestion := range suggestions {
		if suggestion == "gate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("suggestions = %v", suggestions)
	}
	if manExcerpt("# T\n\n<!-- c -->\n\nSynopsis line here") == "" {
		t.Fatal("empty excerpt")
	}
}
