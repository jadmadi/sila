package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestAdvanceMaturityBands(t *testing.T) {
	cases := []struct {
		current string
		score   float64
		want    string
	}{
		{"draft", 64, "draft"},
		{"draft", 65, "validated"},
		{"validated", 36, "validated"},
		{"validated", 34, "draft"},
		{"validated", 85, "core"},
		{"core", 59, "validated"},
		{"core", 60, "core"},
		{"draft", -10, "draft"},
		{"validated", 120, "core"},
	}
	for _, tc := range cases {
		if got := AdvanceMaturity(tc.current, tc.score); got != tc.want {
			t.Fatalf("%s at %.0f = %s, want %s", tc.current, tc.score, got, tc.want)
		}
	}
	if got := AdvanceMaturity("validated", math.NaN()); got != "validated" {
		t.Fatalf("NaN changed tier to %s", got)
	}
}

func TestAdvanceMaturityHysteresis(t *testing.T) {
	// A score bouncing around the 65 edge cannot flip the tier twice.
	tier := AdvanceMaturity("draft", 65)
	for _, score := range []float64{64, 65, 64, 65} {
		tier = AdvanceMaturity(tier, score)
	}
	if tier != MaturityValidated {
		t.Fatalf("oscillation changed tier to %s", tier)
	}
	// A validated row at 34 falls once and stays draft at 35.
	tier = AdvanceMaturity("validated", 34)
	if tier != MaturityDraft {
		t.Fatalf("fall = %s", tier)
	}
	if Advamat := AdvanceMaturity(tier, 35); Advamat != MaturityDraft {
		t.Fatalf("rose at 35: %s", Advamat)
	}
	// Core at 59 falls to validated and stays there at 60.
	tier = AdvanceMaturity("core", 59)
	if tier != MaturityValidated {
		t.Fatalf("core fall = %s", tier)
	}
	if again := AdvanceMaturity(tier, 60); again != MaturityValidated {
		t.Fatalf("core re-rose at 60: %s", again)
	}
}

func TestDecayMaturityScore(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	got := DecayMaturityScore(100, now.Add(-30*24*time.Hour), now)
	want := 100 * math.Pow(0.995, 30)
	if math.Abs(got-want) > 0.01 {
		t.Fatalf("decay = %.4f, want %.4f", got, want)
	}
	if DecayMaturityScore(-5, now, now) != 0 || DecayMaturityScore(150, now, now) != 100 {
		t.Fatal("decay did not clamp")
	}
}

func TestBumpFactAccessAdvancesMaturity(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, file_refs_exist, repo_files, tools, session_ref,
		first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, maturity_score, maturity_tier)
		VALUES ('f1','p','claim','grounded',1,'["a.go"]','[]','s',?,?,?,'h',?,62,'draft')`,
		now, now, now, GenesisHash); err != nil {
		t.Fatal(err)
	}
	if err := st.BumpFactAccessSync(context.Background(), "f1", "claude"); err != nil {
		t.Fatal(err)
	}
	var score float64
	var tier string
	_ = st.DB.QueryRow(`SELECT maturity_score, maturity_tier FROM facts WHERE id='f1'`).Scan(&score, &tier)
	if score != 65 || tier != "validated" {
		t.Fatalf("score=%.0f tier=%s", score, tier)
	}
}

func TestTopFactsOrdersMaturityFirst(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	insert := func(id, tier string) {
		t.Helper()
		if _, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, verified, file_refs_exist, repo_files, tools,
			session_ref, first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash, kind, maturity_tier, review_status)
			VALUES (?, 'p', ?, 'invariant', 1, 1, '["a.go"]', '[]', 's', ?, ?, ?, ?, ?, 'memo', ?, 'approved')`,
			id, "claim-"+id, now, now, now, "hash-"+id, GenesisHash, tier); err != nil {
			t.Fatal(err)
		}
	}
	insert("d1", "draft")
	insert("v1", "validated")
	insert("c1", "core")
	facts, err := st.TopFactsForProject("p", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 3 || facts[0].ID != "c1" || facts[1].ID != "v1" || facts[2].ID != "d1" {
		t.Fatalf("order = %+v", facts)
	}
}
