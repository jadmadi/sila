package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestMemoryReportEmptyStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "memory-report.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	out := captureStdout(t, func() {
		runMemoryReport(render.New(os.Stdout, true), st, "", "", true, false)
	})
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("report JSON: %v\n%s", err, out)
	}
	if report["schema"] != "sila/memory-adoption/v1" {
		t.Fatalf("schema: %v", report["schema"])
	}
}
