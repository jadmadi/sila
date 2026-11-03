package loaders

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHookAuditRoundTrip(t *testing.T) {
	dir := t.TempDir()
	entries := []HookAuditEntry{
		{Event: "session-start", DurationMS: 3, ExitCode: 0, SessionID: "s1", Agent: "command-code", Summary: "prime"},
		{Event: "stop", DurationMS: 5, ExitCode: 0, SessionID: "s1", Agent: "command-code", Summary: "handoff"},
	}
	for _, entry := range entries {
		if err := AppendHookAudit(dir, entry); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadHookAudit(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Event != "session-start" || got[1].Event != "stop" {
		t.Fatalf("audit entries: %+v", got)
	}
	fi, err := os.Stat(filepath.Join(dir, ".sila", "state", "hooks", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("audit mode = %o, want 600", fi.Mode().Perm())
	}
}
