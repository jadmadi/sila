package store

import (
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestNearDuplicateGuardP95Under20ms(t *testing.T) {
	if raceEnabled {
		t.Skip("latency budget excludes race instrumentation")
	}
	st, err := Open(filepath.Join(t.TempDir(), "perf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < 100; i++ {
		claim := "deployment pipeline configuration rollout alpha beta gamma delta number" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := st.UpsertFact(Fact{ProjectID: "perf", Claim: claim, Kind: "fact", FirstSeen: "2026-11-06T10:00:00Z", ReviewStatus: "approved"}); err != nil {
			t.Fatal(err)
		}
	}
	const runs = 100
	durations := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		claim := "deployment pipeline configuration rollout alpha beta gamma delta epsilon zeta eta theta"
		_ = st.UpsertFact(Fact{ProjectID: "perf", Claim: claim, Kind: "memo", FirstSeen: "2026-11-06T11:00:00Z", ReviewStatus: "approved"})
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(runs)*0.95)-1]
	t.Logf("write guard p95 = %s over %d runs", p95, runs)
	if p95 > 20*time.Millisecond {
		t.Fatalf("write guard p95 %s exceeds 20ms", p95)
	}
}
