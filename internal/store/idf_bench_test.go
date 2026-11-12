package store

import (
	"path/filepath"
	"testing"
)

func BenchmarkNearDuplicateGuard(b *testing.B) {
	st, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 100; i++ {
		claim := "deployment pipeline configuration rollout alpha beta gamma delta number" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := st.UpsertFact(Fact{ProjectID: "bench", Claim: claim, Kind: "fact", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		claim := "deployment pipeline configuration rollout alpha beta gamma delta epsilon zeta eta theta"
		_ = st.UpsertFact(Fact{ProjectID: "bench", Claim: claim, Kind: "memo", FirstSeen: "2026-11-06T11:00:00Z", ReviewStatus: "approved"})
	}
}
