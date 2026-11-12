package mcp

import (
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestCaptureGuardBlocksWrites(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.PauseCapture("p", "paused", "test", false); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: st}
	if !server.capturePausedForProject("p") {
		t.Fatal("paused project not detected")
	}
	if server.capturePausedForProject("other") {
		t.Fatal("unpaused project reported paused")
	}
	for _, name := range []string{"save_memo", "save_lesson", "save_case", "save_handoff", "save_memory"} {
		if !isCaptureWriteTool(name) {
			t.Fatalf("%s is not guarded", name)
		}
	}
	if isCaptureWriteTool("recall") {
		t.Fatal("recall must stay readable")
	}
}
