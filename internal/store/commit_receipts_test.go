package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCommitReceiptUpsertAndLookup(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().UTC()
	if err := st.SaveCommitReceipt(CommitReceipt{CommitSHA: "abc", ProjectID: "p", SessionID: "s1", Agent: "a", Harness: "h", Branch: "main", TreeHash: "tree1", Source: "ancestry", AncestorPID: 42, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveCommitReceipt(CommitReceipt{CommitSHA: "abc", ProjectID: "p", SessionID: "s2", Agent: "b", Harness: "h", Branch: "main", TreeHash: "tree2", Source: "session-env", AncestorPID: 43, CreatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.CommitReceipt("p", "abc")
	if err != nil || !ok || got.SessionID != "s2" || got.TreeHash != "tree2" {
		t.Fatalf("got = %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, err := st.CommitReceipt("p", "missing"); err != nil || ok {
		t.Fatalf("missing ok=%v err=%v", ok, err)
	}
	if err := st.SaveCommitReceipt(CommitReceipt{CommitSHA: "", ProjectID: "p"}); err == nil {
		t.Fatal("empty sha accepted")
	}
}

func TestListCommitReceiptsOrderAndCap(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "c2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := st.SaveCommitReceipt(CommitReceipt{CommitSHA: "sha" + string(rune('0'+i)), ProjectID: "p", Source: "ancestry", CreatedAt: base.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	receipts, err := st.ListCommitReceipts("p", 2)
	if err != nil || len(receipts) != 2 {
		t.Fatalf("len = %d err=%v", len(receipts), err)
	}
	if receipts[0].CommitSHA != "sha4" || receipts[1].CommitSHA != "sha3" {
		t.Fatalf("order = %+v", receipts)
	}
}
