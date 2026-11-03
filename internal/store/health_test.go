package store

import (
	"path/filepath"
	"testing"
)

func TestHealthReportIncludesSizeWALAndContention(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.InsertMemo("health", "health probe directive", "test", "", nil); err != nil {
		t.Fatal(err)
	}
	h, err := st.Health()
	if err != nil {
		t.Fatal(err)
	}
	if h.FileSize <= 0 || h.BusyTimeoutMS != BusyTimeoutMS {
		t.Fatalf("health report: %+v", h)
	}
	if h.QuickCheck != "ok" {
		t.Fatalf("quick check: %s", h.QuickCheck)
	}
	if h.ActiveWriters < 0 || h.LastLockWaitMS < 0 || h.RetryableErrors < 0 {
		t.Fatalf("negative contention metric: %+v", h)
	}
}
