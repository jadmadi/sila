package store

import (
	"path/filepath"
	"testing"
	"time"
)

func factAt(day time.Time, age time.Duration) string {
	return day.Add(-age).UTC().Format(time.RFC3339)
}

func TestClassifyFact(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	checked29 := factAt(now, 29*24*time.Hour)
	checked31 := factAt(now, 31*24*time.Hour)
	cases := []struct {
		name string
		fact Fact
		ttl  time.Duration
		want VerificationState
	}{
		{"verified", Fact{FileRefsExist: true, RefsCheckedAt: checked29, PrevHash: GenesisHash, FactHash: "h"}, 30 * 24 * time.Hour, VerificationVerified},
		{"ttl stale", Fact{FileRefsExist: true, RefsCheckedAt: checked31, PrevHash: GenesisHash, FactHash: "h"}, 30 * 24 * time.Hour, VerificationStale},
		{"ttl zero", Fact{FileRefsExist: true, RefsCheckedAt: factAt(now, 5*365*24*time.Hour), PrevHash: GenesisHash, FactHash: "h"}, 0, VerificationVerified},
		{"stale flag", Fact{FileRefsExist: true, IsStaleFlag: true, RefsCheckedAt: checked29}, 30 * 24 * time.Hour, VerificationStale},
		{"disputed beats stale", Fact{DisputedBy: "agent-b", IsStaleFlag: true}, 30 * 24 * time.Hour, VerificationDisputed},
		{"unverified no refs", Fact{FileRefsExist: false, RefsCheckedAt: checked29}, 30 * 24 * time.Hour, VerificationUnverified},
		{"never checked", Fact{FileRefsExist: true}, 30 * 24 * time.Hour, VerificationUnverified},
		{"broken chain", Fact{FileRefsExist: true, RefsCheckedAt: checked29, FactHash: "h"}, 30 * 24 * time.Hour, VerificationStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyFact(tc.fact, now, tc.ttl); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestFactCountsFixture(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	checked := now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)
	staleOld := now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)
	insert := func(id string, refs, stale int, disputed string, checkedAt, prev string) {
		t.Helper()
		_, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, verified, repo_files, tools, session_ref,
			first_seen, promoted_at, file_refs_exist, is_stale, disputed_by, refs_checked_at, fact_hash, prev_hash)
			VALUES (?, 'p', ?, 'grounded', 0, '["a.go"]', '[]', 's', ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, "claim-"+id, checked, checked, refs, stale, disputed, checkedAt, "hash-"+id, prev)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		insert("v"+string(rune('a'+i)), 1, 0, "", checked, GenesisHash)
	}
	insert("s1", 1, 1, "", checked, GenesisHash)
	insert("s2", 1, 0, "", staleOld, GenesisHash)
	insert("s3", 1, 0, "", checked, "")
	for i := 0; i < 3; i++ {
		insert("u"+string(rune('a'+i)), 0, 0, "", checked, GenesisHash)
	}
	insert("d1", 1, 0, "agent-b", checked, GenesisHash)
	counts, err := st.FactCounts("p", now, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Verified != 3 || counts.Stale != 3 || counts.Unverified != 3 || counts.Disputed != 1 {
		t.Fatalf("counts = %+v", counts)
	}
	if counts.BrokenChain != 1 {
		t.Fatalf("broken = %d", counts.BrokenChain)
	}
}

func BenchmarkClassifyFact(b *testing.B) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	f := Fact{FileRefsExist: true, RefsCheckedAt: now.Add(-2 * 24 * time.Hour).Format(time.RFC3339), PrevHash: GenesisHash, FactHash: "h"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ClassifyFact(f, now, 30*24*time.Hour)
	}
}

func BenchmarkFactCounts(b *testing.B) {
	st, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	recent := now.Format(time.RFC3339)
	for i := 0; i < 100; i++ {
		if _, err := st.DB.Exec(`INSERT INTO facts (id, project_id, claim, confidence, file_refs_exist, repo_files, tools, session_ref,
			first_seen, promoted_at, refs_checked_at, fact_hash, prev_hash)
			VALUES (?, 'p', ?, 'grounded', 1, '["a.go"]', '[]', 's', ?, ?, ?, ?, ?)`,
			"f"+string(rune('A'+i%26))+string(rune('a'+i/26)), "claim-"+string(rune('A'+i%26))+string(rune('a'+i/26)), recent, recent, recent, "hash-"+string(rune('A'+i%26)), GenesisHash); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.FactCounts("p", now, 30*24*time.Hour); err != nil {
			b.Fatal(err)
		}
	}
}
