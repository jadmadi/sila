// Package man embeds the Sila manual pages and serves topic lookups.
package man

import (
	"embed"
	"regexp"
	"sort"
	"strings"
)

// Revision identifies the embedded manual revision.
const Revision = "2026-11-06"

//go:embed pages/*.md
var pageFS embed.FS

// Page is one manual topic.
type Page struct {
	Topic   string `json:"topic"`
	Section string `json:"section"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

var (
	pages   []Page
	byTopic = map[string]*Page{}
)

var (
	sectionRE = regexp.MustCompile(`(?m)^<!--\s*section:\s*([0-9]+)\s*-->`)
	titleRE   = regexp.MustCompile(`(?m)^#\s+(.+)$`)
)

func init() {
	entries, err := pageFS.ReadDir("pages")
	if err != nil {
		panic("man: read embedded pages: " + err.Error())
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		topic := strings.TrimSuffix(entry.Name(), ".md")
		data, err := pageFS.ReadFile("pages/" + entry.Name())
		if err != nil {
			panic("man: read page " + topic + ": " + err.Error())
		}
		if _, exists := byTopic[topic]; exists {
			panic("man: duplicate topic " + topic)
		}
		page := Page{Topic: topic, Body: string(data)}
		if match := sectionRE.FindStringSubmatch(page.Body); len(match) == 2 {
			page.Section = match[1]
		}
		if match := titleRE.FindStringSubmatch(page.Body); len(match) == 2 {
			page.Title = strings.TrimSpace(match[1])
		}
		pages = append(pages, page)
		byTopic[topic] = &pages[len(pages)-1]
	}
	sort.Slice(pages, func(i, j int) bool {
		if pages[i].Section != pages[j].Section {
			return sectionOrder(pages[i].Section) < sectionOrder(pages[j].Section)
		}
		return pages[i].Topic < pages[j].Topic
	})
}

func sectionOrder(section string) int {
	switch section {
	case "1":
		return 1
	case "3":
		return 3
	case "7":
		return 7
	default:
		return 99
	}
}

// Lookup returns the page for a topic.
func Lookup(topic string) (Page, bool) {
	page, ok := byTopic[strings.TrimSpace(strings.ToLower(topic))]
	if !ok {
		return Page{}, false
	}
	return *page, true
}

// List returns all pages ordered by section then topic.
func List() []Page {
	return append([]Page{}, pages...)
}

// Search returns pages whose title or body contains term, case-insensitively.
func Search(term string) []Page {
	needle := strings.ToLower(strings.TrimSpace(term))
	if needle == "" {
		return nil
	}
	var hits []Page
	for _, page := range pages {
		if strings.Contains(strings.ToLower(page.Title), needle) || strings.Contains(strings.ToLower(page.Body), needle) {
			hits = append(hits, page)
		}
	}
	return hits
}

// Topics returns every embedded topic.
func Topics() []string {
	topics := make([]string, 0, len(pages))
	for _, page := range pages {
		topics = append(topics, page.Topic)
	}
	return topics
}
