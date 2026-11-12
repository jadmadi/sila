package main

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestCommitResolverPerformance(t *testing.T) {
	repoDir, dbPath, chain := commitFixture(t)
	projectID := store.ProjectID(repoDir)
	insertSession(t, dbPath, projectID, "s-perf", chain[1])
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 100; i++ {
		receipt := store.CommitReceipt{
			CommitSHA: "fixture" + time.Duration(i).String(), ProjectID: projectID,
			SessionID: "filler", Source: "ancestry", CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		if err := st.SaveCommitReceipt(receipt); err != nil {
			t.Fatal(err)
		}
	}
	const runs = 100
	durations := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		receipt, found, err := resolveCommitReceipt(st, projectID, repoDir)
		durations = append(durations, time.Since(start))
		if err != nil || !found || receipt.SessionID != "s-perf" {
			t.Fatalf("run %d: found=%v err=%v", i, found, err)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(runs)*0.95)-1]
	t.Logf("resolver p95 = %s over %d runs on a 100-receipt fixture", p95, runs)
	if p95 > 15*time.Millisecond {
		t.Fatalf("resolver p95 %s exceeds 15ms", p95)
	}
	_ = filepath.Base(repoDir)
}
