package main

import (
	"context"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestMergeSlotAcquireP95(t *testing.T) {
	if raceEnabled {
		t.Skip("latency budget excludes race instrumentation")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "perf.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	const runs = 100
	durations := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		slot, err := st.AcquireMergeSlot(ctx, "p", "agent", time.Minute, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ReleaseMergeSlot(ctx, "p", slot.Token, false); err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(runs)*0.95)-1]
	t.Logf("merge slot acquire p95 = %s", p95)
	if p95 > 20*time.Millisecond {
		t.Fatalf("acquire p95 %s exceeds 20ms", p95)
	}
}
