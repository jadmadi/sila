package session

import (
	"testing"
)

func TestResolveIdentityRecordsStartTicks(t *testing.T) {
	id, err := ResolveIdentity("sess-ticks", "p", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if id.PID == 0 {
		t.Fatal("missing pid")
	}
	if id.PIDStartTicks == 0 {
		t.Fatal("missing start ticks")
	}
}
