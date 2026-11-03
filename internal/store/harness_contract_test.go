package store

import "testing"

func TestCountMemoryWritesIsSessionScoped(t *testing.T) {
	st, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := t.TempDir()
	if _, err := st.InsertMemoWithProvenance("proj", "memo", "tester", root, nil, "memo", Provenance{SessionRef: "sess-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertMemoWithProvenance("proj", "case", "tester", root, nil, "case", Provenance{SessionRef: "sess-b"}); err != nil {
		t.Fatal(err)
	}
	if got, err := st.CountMemoryWrites("proj", "sess-a"); err != nil || got != 1 {
		t.Fatalf("sess-a: %d, %v", got, err)
	}
	if got, err := st.CountMemoryWrites("proj", "sess-b"); err != nil || got != 1 {
		t.Fatalf("sess-b: %d, %v", got, err)
	}
	if got, err := st.CountMemoryWrites("proj", "sess-c"); err != nil || got != 0 {
		t.Fatalf("sess-c: %d, %v", got, err)
	}
}
