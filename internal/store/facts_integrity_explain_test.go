package store

import (
	"path/filepath"
	"testing"
)

func TestVerifyChainExplainedClassifiesAndCounts(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "explain.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i, claim := range []string{"first directive", "second directive", "third directive"} {
		if _, err := st.InsertMemo("explain", claim, "test", "", nil); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if _, err := st.DB.Exec(`UPDATE facts SET claim='tampered' WHERE rowid=2`); err != nil {
		t.Fatal(err)
	}
	report, err := st.VerifyChainExplained("")
	if err != nil {
		t.Fatal(err)
	}
	if report.Intact || report.Summary == nil || len(report.ViolationDetails) == 0 {
		t.Fatalf("expected structured violations: %+v", report)
	}
	if report.Summary.Roots+report.Summary.Cascades != len(report.ViolationDetails) {
		t.Fatalf("roots+cascades != details: %+v", report.Summary)
	}
	if report.FirstDivergence == nil || report.FirstDivergence.Block != 2 {
		t.Fatalf("first divergence: %+v", report.FirstDivergence)
	}
	found := false
	for _, v := range report.ViolationDetails {
		if v.Block == 2 && v.Type == "digest_mismatch" && !v.Cascade {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing root digest_mismatch: %+v", report.ViolationDetails)
	}
	if len(report.Violations) != len(report.ViolationDetails) {
		t.Fatalf("legacy/string counts differ: %d vs %d", len(report.Violations), len(report.ViolationDetails))
	}
}
