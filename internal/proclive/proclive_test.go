package proclive

import (
	"errors"
	"os"
	"testing"
)

func TestAncestryIncludesCurrentProcess(t *testing.T) {
	chain, err := Ancestry(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) == 0 || chain[0].PID != os.Getpid() {
		t.Fatalf("chain = %+v", chain)
	}
	if chain[0].Name == "" {
		t.Fatal("missing process name")
	}
	if len(chain) > 1 && chain[1].PID != chain[0].PPID {
		t.Fatalf("parent mismatch: %+v", chain[:2])
	}
}

func TestAncestryNonexistentPID(t *testing.T) {
	_, err := Ancestry(4194303, 4)
	if !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestAncestryMaxDepth(t *testing.T) {
	chain, err := Ancestry(os.Getpid(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 1 {
		t.Fatalf("depth 1 returned %d entries", len(chain))
	}
}

func TestAncestryStartTicksStable(t *testing.T) {
	first, err := Ancestry(os.Getpid(), 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Ancestry(os.Getpid(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].StartTicks == 0 {
		t.Fatal("start ticks are zero")
	}
	if first[0].StartTicks != second[0].StartTicks {
		t.Fatalf("ticks %d != %d", first[0].StartTicks, second[0].StartTicks)
	}
}

func TestAncestryInvalidPID(t *testing.T) {
	if _, err := Ancestry(0, 4); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("err = %v", err)
	}
}
