package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jadmadi/sila/internal/man"
	"github.com/jadmadi/sila/internal/render"
)

// runMan implements `sila man`.
func runMan(ui render.UI, jsonOut bool, args []string) {
	term := ""
	topic := ""
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--search="):
			term = strings.TrimSpace(strings.TrimPrefix(arg, "--search="))
		case arg == "--json":
			jsonOut = true
		case strings.HasPrefix(arg, "-"):
			continue
		default:
			if topic == "" {
				topic = strings.TrimSpace(arg)
			}
		}
	}
	if term != "" {
		hits := man.Search(term)
		printManSearch(ui, hits, term, jsonOut)
		return
	}
	if topic == "" {
		pages := man.List()
		if jsonOut {
			type entry struct {
				Topic   string `json:"topic"`
				Section string `json:"section"`
				Title   string `json:"title"`
			}
			entries := make([]entry, 0, len(pages))
			for _, page := range pages {
				entries = append(entries, entry{Topic: page.Topic, Section: page.Section, Title: page.Title})
			}
			emitJSON(entries)
			return
		}
		rows := make([][]string, 0, len(pages))
		for _, page := range pages {
			rows = append(rows, []string{page.Section, page.Topic, page.Title})
		}
		fmt.Fprintln(os.Stdout, ui.Cyan(ui.Bold("sila · manual")))
		fmt.Fprint(os.Stdout, ui.Table([]string{"section", "topic", "title"}, rows, []string{"left", "left", "left"}))
		return
	}
	page, ok := man.Lookup(topic)
	if !ok {
		suggestions := suggestTopics(topic, 3)
		fmt.Fprintf(os.Stderr, "no manual page for %q", topic)
		if len(suggestions) > 0 {
			fmt.Fprintf(os.Stderr, "; did you mean: %s", strings.Join(suggestions, ", "))
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	if jsonOut {
		emitJSON(page)
		return
	}
	fmt.Fprint(os.Stdout, page.Body)
}

func printManSearch(ui render.UI, hits []man.Page, term string, jsonOut bool) {
	if jsonOut {
		emitJSON(hits)
		return
	}
	if len(hits) == 0 {
		fmt.Fprintln(os.Stdout, "no matching manual pages")
		return
	}
	fmt.Fprintf(os.Stdout, "manual pages matching %q\n", term)
	for _, page := range hits {
		fmt.Fprintf(os.Stdout, "  %-12s %s — %s\n", page.Topic, page.Title, manExcerpt(page.Body))
	}
}

func manExcerpt(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "<!--") || strings.HasPrefix(line, "```") {
			continue
		}
		if len(line) > 70 {
			line = line[:70] + "..."
		}
		return line
	}
	return ""
}

// suggestTopics returns up to limit topics close to the input.
func suggestTopics(input string, limit int) []string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return nil
	}
	type scored struct {
		topic string
		score int
	}
	var candidates []scored
	for _, topic := range man.Topics() {
		lower := strings.ToLower(topic)
		score := 0
		switch {
		case strings.HasPrefix(lower, input) || strings.HasPrefix(input, lower):
			score = 100 - absInt(len(lower)-len(input))
		case strings.Contains(lower, input):
			score = 50
		}
		if score > 0 {
			candidates = append(candidates, scored{topic: topic, score: score})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].topic < candidates[j].topic
		}
		return candidates[i].score > candidates[j].score
	})
	out := make([]string, 0, limit)
	for _, candidate := range candidates {
		if len(out) == limit {
			break
		}
		out = append(out, candidate.topic)
	}
	return out
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
