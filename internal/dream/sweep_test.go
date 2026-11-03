package dream

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestRunCompactionSweep(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sweep.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ctx := context.Background()

	// 1. Seed store with 1 invariant, 1 cold episodic fact, and 1 cold lesson
	inv := store.Fact{
		ID:        "fact_inv",
		ProjectID: "proj_x",
		Claim:     "decision: Pure Go only, zero external CGO dependencies",
		Kind:      "invariant",
		FirstSeen: time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339),
	}
	if err := st.SaveFact(inv); err != nil {
		t.Fatal(err)
	}

	rawEpisodicClaim := `
# Fix memory leak in background workers
The worker process was leaking goroutines because the context timeout was not cancelled.
Let me investigate internal/dream/spool.go and internal/store/handoffs.go.
During load tests, we encountered exit code 127 and SQLITE_BUSY when 50 concurrent agents spawned.
To reproduce this, run:
$ go test -race ./internal/dream/...
See issue report at https://github.com/jadmadi/sila/issues/42.
Task task_999 failed with commit 1a2b3c4d.
Thank you for reporting this issue!
`
	coldFact := store.Fact{
		ID:        "fact_cold_1",
		ProjectID: "proj_x",
		Claim:     rawEpisodicClaim,
		Kind:      "code_claim",
		FirstSeen: time.Now().Add(-45 * 24 * time.Hour).Format(time.RFC3339),
		RepoFiles: []string{"internal/dream/spool.go"},
	}
	if err := st.SaveFact(coldFact); err != nil {
		t.Fatal(err)
	}

	// 2. Run Dry-Run sweep
	dryRunOpts := CompactionSweepOptions{
		ProjectID:     "proj_x",
		ColdThreshold: 0.35,
		DryRun:        true,
	}
	dryTelem, err := RunCompactionSweep(ctx, st, dryRunOpts)
	if err != nil {
		t.Fatal(err)
	}

	if dryTelem.CompactedCount != 1 {
		t.Fatalf("expected 1 compacted candidate in dry-run, got %d", dryTelem.CompactedCount)
	}
	if dryTelem.SavedTokens <= 0 || dryTelem.SavingsRatio <= 0 {
		t.Errorf("expected positive savings in dry-run, got %d saved (ratio: %f)", dryTelem.SavedTokens, dryTelem.SavingsRatio)
	}
	if dryTelem.TotalPathsMined == 0 {
		t.Errorf("expected paths mined in dry-run, got 0")
	}

	// Verify database was NOT modified in dry-run
	fUnchanged, err := st.FactsByProject("proj_x")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fUnchanged {
		if f.ID == "fact_cold_1" {
			if f.IsCompacted() {
				t.Fatalf("dry run mutated fact_cold_1 compacted_at")
			}
			if f.Claim != rawEpisodicClaim {
				t.Fatalf("dry run mutated fact_cold_1 claim text")
			}
		}
	}

	// 3. Run Live sweep
	liveOpts := CompactionSweepOptions{
		ProjectID:     "proj_x",
		ColdThreshold: 0.35,
		DryRun:        false,
	}
	liveTelem, err := RunCompactionSweep(ctx, st, liveOpts)
	if err != nil {
		t.Fatal(err)
	}

	if liveTelem.CompactedCount != 1 {
		t.Fatalf("expected 1 compacted candidate in live run, got %d", liveTelem.CompactedCount)
	}

	// Verify database WAS modified in live run
	fUpdated, err := st.FactsByProject("proj_x")
	if err != nil {
		t.Fatal(err)
	}
	var targetFact *store.Fact
	for _, f := range fUpdated {
		if f.ID == "fact_cold_1" {
			fCopy := f
			targetFact = &fCopy
			break
		}
	}
	if targetFact == nil {
		t.Fatalf("fact_cold_1 not found after live sweep")
	}
	if !targetFact.IsCompacted() {
		t.Errorf("expected fact_cold_1 to report IsCompacted() == true")
	}
	if !IsAlreadyCompacted(targetFact.Claim) {
		t.Errorf("expected claim to have compacted header, got:\n%s", targetFact.Claim)
	}

	// 4. Run second Live sweep (should be idempotent / database query filters out compacted facts)
	secondTelem, err := RunCompactionSweep(ctx, st, liveOpts)
	if err != nil {
		t.Fatal(err)
	}
	if secondTelem.CompactedCount != 0 {
		t.Errorf("expected 0 compactions on second sweep, got %d", secondTelem.CompactedCount)
	}
	if secondTelem.CandidatesFound != 0 {
		t.Errorf("expected 0 candidates on second sweep, got %d", secondTelem.CandidatesFound)
	}
}
