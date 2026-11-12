package man

import (
	"sort"
	"strings"
	"testing"
)

func TestLookupAndRevision(t *testing.T) {
	page, ok := Lookup("gate")
	if !ok || page.Topic != "gate" || page.Section != "1" || page.Title == "" {
		t.Fatalf("page = %+v ok=%v", page, ok)
	}
	if _, ok := Lookup("does-not-exist"); ok {
		t.Fatal("unknown topic found")
	}
	if Revision == "" {
		t.Fatal("empty revision")
	}
}

func TestListOrdered(t *testing.T) {
	pages := List()
	if len(pages) < 15 {
		t.Fatalf("pages = %d", len(pages))
	}
	order := map[string]int{"1": 1, "3": 3, "7": 7}
	previousSection, previousTopic := 0, ""
	for _, page := range pages {
		rank := order[page.Section]
		if rank < previousSection || (rank == previousSection && page.Topic < previousTopic) {
			t.Fatalf("out of order at %+v", page)
		}
		previousSection, previousTopic = rank, page.Topic
	}
}

func TestSearch(t *testing.T) {
	hits := Search("handoff")
	if len(hits) == 0 {
		t.Fatal("no handoff page")
	}
	if len(Search("")) != 0 {
		t.Fatal("empty term matched")
	}
	topics := make([]string, 0, len(hits))
	for _, page := range hits {
		topics = append(topics, page.Topic)
	}
	sort.Strings(topics)
	if !strings.Contains(strings.Join(topics, ","), "handoff") {
		t.Fatalf("topics = %v", topics)
	}
}
