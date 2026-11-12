package dream

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func oodStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ood.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	store.ResetDocumentFrequencyCache()
	facts := []string{
		"alpha beta gamma delta deployment pipeline",
		"alpha beta gamma rollout staging",
		"alpha beta configuration service",
	}
	for i := 0; i < 40; i++ {
		filler := "filler" + string(rune('a'+i%26)) + " word" + string(rune('a'+i%26)) + " token" + string(rune('a'+i%26))
		if err := st.UpsertFact(store.Fact{ID: "z" + string(rune('a'+i)), ProjectID: "oodp", Claim: filler, Kind: "fact", FirstSeen: "2026-11-06T09:00:00Z", ReviewStatus: "approved"}); err != nil {
			t.Fatal(err)
		}
	}
	for i, claim := range facts {
		if err := st.UpsertFact(store.Fact{ID: "f" + string(rune('a'+i)), ProjectID: "oodp", Claim: claim, Kind: "fact", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func TestEvaluateOODNonsense(t *testing.T) {
	st := oodStore(t)
	verdict := EvaluateOOD(st, "oodp", "quantum flux capacitor", 0.60)
	if !verdict.OutOfDomain || verdict.Reason != "no_term_match" {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict.Hint == "" || verdict.Missed == 0 {
		t.Fatalf("verdict missing hint or misses: %+v", verdict)
	}
}

func TestEvaluateOODInDomain(t *testing.T) {
	st := oodStore(t)
	verdict := EvaluateOOD(st, "oodp", "alpha beta gamma", 0.60)
	if verdict.OutOfDomain {
		t.Fatalf("in-domain query rejected: %+v", verdict)
	}
	if len(verdict.Terms) == 0 || len(verdict.Terms) > 8 {
		t.Fatalf("terms = %v", verdict.Terms)
	}
}

func TestEvaluateOODThetaZero(t *testing.T) {
	st := oodStore(t)
	verdict := EvaluateOOD(st, "oodp", "alpha", 0)
	if verdict.OutOfDomain {
		t.Fatalf("theta zero rejected a matched term: %+v", verdict)
	}
}

func BenchmarkEvaluateOOD(b *testing.B) {
	st := oodStore(&testing.T{})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = EvaluateOOD(st, "oodp", "alpha beta gamma", 0.60)
	}
}
