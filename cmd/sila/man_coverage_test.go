package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/cli"
	"github.com/jadmadi/sila/internal/man"
	"github.com/jadmadi/sila/internal/mcp"
)

func manualExpectedTopics(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, spec := range cli.All() {
		seen[spec.Name] = true
	}
	tools, err := mcp.ContinuityTools(mcp.ToolDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		seen[tool.Name] = true
	}
	topics := make([]string, 0, len(seen))
	for topic := range seen {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics
}

func TestManCoverage(t *testing.T) {
	pages := man.List()
	sections := map[string]int{}
	for _, page := range pages {
		sections[page.Section]++
		for _, heading := range []string{"## Synopsis", "## Usage", "## Examples", "## See Also"} {
			if !strings.Contains(page.Body, heading) {
				t.Fatalf("%s missing %s", page.Topic, heading)
			}
		}
		if strings.Count(page.Body, "\n") > 200 {
			t.Fatalf("%s exceeds 200 lines", page.Topic)
		}
		if strings.Contains(page.Body, "/home/") || strings.Contains(page.Body, "password=") || strings.Contains(page.Body, "sk-") || strings.Contains(page.Body, "AKIA") {
			t.Fatalf("%s contains a path or secret pattern", page.Topic)
		}
	}
	if sections["1"] < 8 || sections["3"] < 4 || sections["7"] < 3 {
		t.Fatalf("sections = %v", sections)
	}

	waiverPath := filepath.Join("..", "..", "internal", "man", "pages", "waivers.txt")
	waivers := map[string]bool{}
	if data, err := os.ReadFile(waiverPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				waivers[line] = true
			}
		}
	}
	var missing []string
	for _, topic := range manualExpectedTopics(t) {
		if _, ok := man.Lookup(topic); ok || waivers[topic] {
			continue
		}
		missing = append(missing, topic)
	}
	if update := os.Getenv("SILA_UPDATE_WAIVERS"); update == "1" {
		for _, topic := range missing {
			waivers[topic] = true
		}
		lines := []string{"# Waived topics: command or tool surface without a dedicated page yet."}
		for topic := range waivers {
			lines = append(lines, topic)
		}
		sort.Strings(lines[1:])
		if err := os.WriteFile(waiverPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d waivers", len(waivers))
		return
	}
	if len(missing) > 0 {
		t.Fatalf("missing manual topics: %s", strings.Join(missing, ", "))
	}
	_ = fmt.Sprint
}
