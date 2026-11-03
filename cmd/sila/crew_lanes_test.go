package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/crew"
	"github.com/jadmadi/sila/internal/render"
)

func TestCrewPlanLanes_CLI(t *testing.T) {
	tempDir := t.TempDir()

	goMod := `module example.com/testmod

go 1.27
`
	_ = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644)
	_ = os.MkdirAll(filepath.Join(tempDir, "internal", "store"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "internal", "store", "store.go"), []byte(`package store
type S struct{}
`), 0644)
	_ = os.MkdirAll(filepath.Join(tempDir, "cmd", "app"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "cmd", "app", "main.go"), []byte(`package main
import "example.com/testmod/internal/store"
var s store.S
`), 0644)

	briefPath := filepath.Join(tempDir, "tasks.txt")
	brief := `task_store: internal/store/store.go
task_app: cmd/app/main.go
`
	_ = os.WriteFile(briefPath, []byte(brief), 0644)

	ui := render.New(os.Stdout, true)

	// Test JSON output
	// capture stdout
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runCrewPlanLanes(ui, tempDir, true, false, []string{"2", "--brief=" + briefPath})

	_ = w.Close()
	os.Stdout = origStdout

	var outBuf bytes.Buffer
	_, _ = outBuf.ReadFrom(r)

	var res crew.LanePlanResult
	if err := json.Unmarshal(outBuf.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse JSON lane plan: %v\noutput: %s", err, outBuf.String())
	}

	if res.Status != "planned" {
		t.Errorf("expected status 'planned', got %q", res.Status)
	}
	if res.PlannedLanes != 2 {
		t.Errorf("expected 2 planned lanes, got %d", res.PlannedLanes)
	}
	if len(res.ContractTouches) == 0 {
		t.Errorf("expected contract touch between store and app")
	}

	// Test Human output
	r2, w2, _ := os.Pipe()
	os.Stdout = w2

	runCrewPlanLanes(ui, tempDir, false, false, []string{"2", "--brief=" + briefPath})

	_ = w2.Close()
	os.Stdout = origStdout

	var humanBuf bytes.Buffer
	_, _ = humanBuf.ReadFrom(r2)
	humanOut := humanBuf.String()

	if !strings.Contains(humanOut, "crew plan-lanes") {
		t.Errorf("expected 'crew plan-lanes' in human output, got: %s", humanOut)
	}
	if !strings.Contains(humanOut, "Contract touches") {
		t.Errorf("expected 'Contract touches' in human output, got: %s", humanOut)
	}
}
