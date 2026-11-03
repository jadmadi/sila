package store

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHLCTickBasicAndMonotonicity(t *testing.T) {
	c := NewHLC("agent_test")
	if c.NodeID() != "agent_test" {
		t.Fatalf("expected nodeID agent_test, got %s", c.NodeID())
	}

	baseTime := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	hlc1 := c.Tick(baseTime)
	hlc2 := c.Tick(baseTime)
	hlc3 := c.Tick(baseTime.Add(1 * time.Millisecond))

	if CompareHLC(hlc1, hlc2) >= 0 {
		t.Fatalf("expected hlc1 < hlc2, got %s >= %s", hlc1, hlc2)
	}
	if CompareHLC(hlc2, hlc3) >= 0 {
		t.Fatalf("expected hlc2 < hlc3, got %s >= %s", hlc2, hlc3)
	}

	ms1, count1, node1, err := ParseHLC(hlc1)
	if err != nil {
		t.Fatalf("parse hlc1: %v", err)
	}
	ms2, count2, node2, err := ParseHLC(hlc2)
	if err != nil {
		t.Fatalf("parse hlc2: %v", err)
	}
	ms3, count3, node3, err := ParseHLC(hlc3)
	if err != nil {
		t.Fatalf("parse hlc3: %v", err)
	}

	if node1 != "agent_test" || node2 != "agent_test" || node3 != "agent_test" {
		t.Fatalf("unexpected nodeIDs in parsed HLCs")
	}
	if ms1 != baseTime.UnixMilli() || ms2 != baseTime.UnixMilli() {
		t.Fatalf("expected ms to match baseTime, got %d and %d", ms1, ms2)
	}
	if count1 != 0 || count2 != 1 {
		t.Fatalf("expected counters 0 and 1, got %d and %d", count1, count2)
	}
	if ms3 != baseTime.UnixMilli()+1 || count3 != 0 {
		t.Fatalf("expected ms3 to advance and counter to reset to 0, got ms=%d count=%d", ms3, count3)
	}
}

func TestHLCClockRegressionNeverDecreases(t *testing.T) {
	c := NewHLC("agent_regress")
	baseTime := time.Date(2026, 9, 30, 15, 0, 0, 0, time.UTC)

	last := c.Tick(baseTime)

	// Artificially jump backward by 10 minutes
	regressedTime := baseTime.Add(-10 * time.Minute)
	for i := 0; i < 100; i++ {
		next := c.Tick(regressedTime)
		if CompareHLC(last, next) >= 0 {
			t.Fatalf("monotonicity violated during clock regression: last=%s, next=%s", last, next)
		}
		last = next
	}

	// Verify physical time is still locked at baseTime or beyond
	ms, count, _, err := ParseHLC(last)
	if err != nil {
		t.Fatal(err)
	}
	if ms < baseTime.UnixMilli() {
		t.Fatalf("ms regressed to %d, expected at least %d", ms, baseTime.UnixMilli())
	}
	if count != 100 {
		t.Fatalf("expected counter 100 after 100 ticks at regressed time, got %d", count)
	}
}

func TestHLCObserveCrossNodeCausality(t *testing.T) {
	nodeA := NewHLC("node_a")
	nodeB := NewHLC("node_b")

	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)

	// Node A generates event 1
	hlcA1 := nodeA.Tick(t0)

	// Node B is lagging by 50ms in physical time
	tB := t0.Add(-50 * time.Millisecond)

	// Node B receives event 1 from Node A
	if err := nodeB.Observe(hlcA1, tB); err != nil {
		t.Fatalf("nodeB failed to observe: %v", err)
	}

	// Node B generates its reply
	hlcB1 := nodeB.Tick(tB)

	// hlcB1 must sort strictly after hlcA1 despite Node B's clock lag!
	if CompareHLC(hlcA1, hlcB1) >= 0 {
		t.Fatalf("causal order violated! expected hlcA1 < hlcB1, got %s >= %s", hlcA1, hlcB1)
	}

	// Node A receives reply from Node B
	tA2 := t0.Add(5 * time.Millisecond)
	if err := nodeA.Observe(hlcB1, tA2); err != nil {
		t.Fatalf("nodeA failed to observe: %v", err)
	}

	hlcA2 := nodeA.Tick(tA2)
	if CompareHLC(hlcB1, hlcA2) >= 0 {
		t.Fatalf("causal order violated! expected hlcB1 < hlcA2, got %s >= %s", hlcB1, hlcA2)
	}
}

func TestHLCDriftClamping(t *testing.T) {
	c := NewHLC("node_drift")
	now := time.Now()

	// Remote clock is 2 hours in the future
	futureTime := now.Add(2 * time.Hour)
	remoteFutureHLC := NewHLC("rogue_future").Tick(futureTime)

	err := c.Observe(remoteFutureHLC, now)
	if err == nil {
		t.Fatalf("expected error for excessive future clock drift, got nil")
	}
	if !strings.Contains(err.Error(), "drift exceeded") {
		t.Fatalf("expected drift exceeded error, got: %v", err)
	}
}

func TestHLCParseValidation(t *testing.T) {
	invalidCases := []string{
		"",
		"not-a-valid-hlc",
		"178303884912-000001-node",        // 12 digits (needs 13)
		"000017830388491234-000001-node",  // 18 digits (max 16)
		"001783038849123-0001-node",       // 4 hex chars (needs 6)
		"001783038849123-00000z-node",     // invalid hex
		"001783038849123-000001-",         // empty node
		"001783038849123-000001-node$bad", // invalid node char
	}

	for _, tc := range invalidCases {
		_, _, _, err := ParseHLC(tc)
		if err == nil {
			t.Errorf("expected error parsing %q, got nil", tc)
		}
	}

	valid := "001783038849123-00002a-worker_1"
	ms, counter, node, err := ParseHLC(valid)
	if err != nil {
		t.Fatalf("unexpected error parsing %q: %v", valid, err)
	}
	if ms != 1783038849123 {
		t.Errorf("expected ms 1783038849123, got %d", ms)
	}
	if counter != 0x2a {
		t.Errorf("expected counter 42, got %d", counter)
	}
	if node != "worker_1" {
		t.Errorf("expected node worker_1, got %s", node)
	}
}

func TestHLCConcurrencyNoCollisions(t *testing.T) {
	c := NewHLC("agent_concurrent")
	const numGoroutines = 10
	const itemsPerGoroutine = 5000
	const totalItems = numGoroutines * itemsPerGoroutine

	results := make([][]string, numGoroutines)
	var wg sync.WaitGroup

	now := time.Now()
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gIdx int) {
			defer wg.Done()
			localList := make([]string, itemsPerGoroutine)
			for i := 0; i < itemsPerGoroutine; i++ {
				localList[i] = c.Tick(now)
			}
			results[gIdx] = localList
		}(g)
	}
	wg.Wait()

	seen := make(map[string]bool, totalItems)
	all := make([]string, 0, totalItems)
	for _, list := range results {
		for _, id := range list {
			if seen[id] {
				t.Fatalf("duplicate HLC generated: %s", id)
			}
			seen[id] = true
			all = append(all, id)
		}
	}

	// Verify all items can be sorted deterministically
	sort.Strings(all)
	for i := 1; i < len(all); i++ {
		if all[i-1] >= all[i] {
			t.Fatalf("sort order invariant violated at %d: %s >= %s", i, all[i-1], all[i])
		}
	}
}

func TestHLCSQLiteTextOrdering(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "hlc_order.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	c1 := NewHLC("node_alpha")
	c2 := NewHLC("node_beta")

	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)

	// Simulate cross-node interleaved causal sequence
	id1 := c1.Tick(base)
	_ = c2.Observe(id1, base)
	id2 := c2.Tick(base)
	_ = c1.Observe(id2, base)
	id3 := c1.Tick(base.Add(2 * time.Millisecond))
	id4 := c2.Tick(base.Add(3 * time.Millisecond))

	expectedOrder := []string{id1, id2, id3, id4}

	// Create test table
	_, err = st.DB.Exec(`CREATE TABLE causal_events (hlc TEXT PRIMARY KEY, payload TEXT);`)
	if err != nil {
		t.Fatal(err)
	}

	// Insert in scrambled order
	insertOrder := []int{2, 0, 3, 1}
	for _, idx := range insertOrder {
		hlc := expectedOrder[idx]
		_, err := st.DB.Exec(`INSERT INTO causal_events (hlc, payload) VALUES (?, ?)`, hlc, "event_"+hlc)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Query ORDER BY hlc ASC
	rows, err := st.DB.Query(`SELECT hlc FROM causal_events ORDER BY hlc ASC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var actualOrder []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		actualOrder = append(actualOrder, h)
	}

	if len(actualOrder) != len(expectedOrder) {
		t.Fatalf("expected %d rows, got %d", len(expectedOrder), len(actualOrder))
	}
	for i := range expectedOrder {
		if actualOrder[i] != expectedOrder[i] {
			t.Errorf("mismatch at index %d: expected %s, got %s", i, expectedOrder[i], actualOrder[i])
		}
	}
}

func TestHLCParallelStress500k(t *testing.T) {
	c := NewHLC("node_stress_500k")
	numGoroutines := 10
	itemsPerGoroutine := 50000
	if testing.Short() {
		itemsPerGoroutine = 5000
	}
	totalItems := numGoroutines * itemsPerGoroutine

	all := make([]string, totalItems)
	var wg sync.WaitGroup

	now := time.Now()
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gIdx int) {
			defer wg.Done()
			offset := gIdx * itemsPerGoroutine
			for i := 0; i < itemsPerGoroutine; i++ {
				all[offset+i] = c.Tick(now)
			}
		}(g)
	}
	wg.Wait()

	seen := make(map[string]struct{}, totalItems)
	for _, id := range all {
		if _, exists := seen[id]; exists {
			t.Fatalf("collision detected in 500k stress test: %s", id)
		}
		seen[id] = struct{}{}
	}
}
