package store

import (
	"path/filepath"
	"testing"
)

func TestTokenizeMemoryText(t *testing.T) {
	got := TokenizeMemoryText("The the AUTH token")
	if len(got) != 2 || got[0] != "auth" || got[1] != "token" {
		t.Fatalf("tokens = %v", got)
	}
	if tokens := TokenizeMemoryText("!!! ??? ..."); len(tokens) != 0 {
		t.Fatalf("punctuation tokens = %v", tokens)
	}
	if tokens := TokenizeMemoryText("A an of"); len(tokens) != 0 {
		t.Fatalf("short tokens = %v", tokens)
	}
}

func TestTokenJaccard(t *testing.T) {
	if got := TokenJaccard([]string{"a", "b"}, []string{"a", "b"}); got != 1 {
		t.Fatalf("identical = %v", got)
	}
	if got := TokenJaccard([]string{"a"}, []string{"b"}); got != 0 {
		t.Fatalf("disjoint = %v", got)
	}
	if got := TokenJaccard(nil, nil); got != 0 {
		t.Fatalf("empty = %v", got)
	}
}

func TestDocumentFrequency(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "df.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ResetDocumentFrequencyCache()
	for i := 0; i < 10; i++ {
		claim := "gamma delta epsilon number" + string(rune('a'+i))
		if i < 3 {
			claim = "alpha beta gamma number" + string(rune('a'+i))
		}
		if err := st.UpsertFact(Fact{ID: "f" + string(rune('a'+i)), ProjectID: "p", Claim: claim, Kind: "fact", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}); err != nil {
			t.Fatal(err)
		}
	}
	count, err := st.DocumentFrequency("p", "alpha")
	if err != nil || count != 3 {
		t.Fatalf("df = %d err=%v", count, err)
	}
	st.ReadOnly = true
	readOnly, err := st.DocumentFrequency("p", "gamma")
	if err != nil || readOnly == 0 {
		t.Fatalf("read-only df = %d err=%v", readOnly, err)
	}
}
