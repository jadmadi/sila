package daemon

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func seedFacts(t *testing.T, st *store.Store, count int) {
	t.Helper()
	tx, err := st.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO facts(id, project_id, claim, confidence, repo_files, tools, session_ref, first_seen, promoted_at, review_status)
		VALUES (?, 'statusp', ?, 'grounded', '[]', '[]', '', '2026-11-06T10:00:00Z', '2026-11-06T10:00:00Z', 'approved')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		id := "seed-" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + string(rune('a'+i/676))
		if _, err := stmt.Exec(id, "seed claim "+id); err != nil {
			t.Fatal(err)
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestStatusCacheLatency(t *testing.T) {
	srv, socket, st := startStoreServer(t)
	seedFacts(t, st, 10000)
	client, err := Dial(socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Call(context.Background(), "status", nil); err != nil {
		t.Fatal(err)
	}
	const calls = 100
	durations := make([]time.Duration, 0, calls)
	for i := 0; i < calls; i++ {
		start := time.Now()
		if _, err := client.Call(context.Background(), "status", nil); err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[int(float64(calls)*0.95)-1]
	t.Logf("status p95 = %s over %d calls", p95, calls)
	if p95 > 5*time.Millisecond {
		t.Fatalf("status p95 = %s", p95)
	}
	if srv.StoreHandleCount() != 1 {
		t.Fatalf("handles = %d", srv.StoreHandleCount())
	}
}

func TestStatusInvalidatedByWrite(t *testing.T) {
	t.Setenv("SILA_AGENT", "status-test")
	_, socket, _ := startStoreServer(t)
	client, err := Dial(socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	raw, err := client.Call(context.Background(), "status", nil)
	if err != nil {
		t.Fatal(err)
	}
	var before map[string]any
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}
	beforeFacts := before["facts"].(float64)
	frame := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"save_lesson","arguments":{"symptom":"status cache test","cause":"cache","fix":"invalidate"}}}`)
	reply, err := client.MCPFrame(context.Background(), frame)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("frame reply: %s", reply)
	raw, err = client.Call(context.Background(), "status", nil)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if after["facts"].(float64) <= beforeFacts {
		t.Fatalf("facts did not grow: %v -> %v", beforeFacts, after["facts"])
	}
}
