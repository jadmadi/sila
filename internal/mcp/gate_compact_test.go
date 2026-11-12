package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/git"
)

func TestGatePipelineResponseDefaultIsCompact(t *testing.T) {
	worktree := t.TempDir()
	gateDir := filepath.Join(worktree, ".sila", "gate")
	if err := os.MkdirAll(gateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(gateDir, "run-1.log")
	if err := os.WriteFile(receipt, []byte("log"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := git.GatePipelineResult{
		Branch: "main", Clean: true, Duration: 1200 * time.Millisecond,
		Worktree: worktree, Checks: []string{"stage 1: pass", "stage 2: pass"},
		Output: strings.Repeat("verbose log line\n", 100),
	}
	view := buildGatePipelineResponse(res, false)
	if view.Output != "" {
		t.Fatal("default response embeds the log")
	}
	if len(view.StageLines) != 2 || !view.Clean || view.DurationMS != 1200 {
		t.Fatalf("view = %+v", view)
	}
	if view.ReceiptPath != receipt {
		t.Fatalf("receipt = %q", view.ReceiptPath)
	}
	if tokens := len(view.StageLines); tokens > 400 {
		t.Fatalf("too many stage lines: %d", tokens)
	}
}

func TestGatePipelineResponseVerbose(t *testing.T) {
	res := git.GatePipelineResult{Clean: false, Output: strings.Repeat("x", gateLogBound+10)}
	view := buildGatePipelineResponse(res, true)
	if !strings.Contains(view.Output, "[truncated]") {
		t.Fatal("verbose output is not bounded")
	}
	if view.Clean {
		t.Fatal("clean flag changed")
	}
}

func TestGatePipelineMissingReceiptIsEmpty(t *testing.T) {
	view := buildGatePipelineResponse(git.GatePipelineResult{Worktree: t.TempDir()}, false)
	if view.ReceiptPath != "" {
		t.Fatalf("receipt = %q", view.ReceiptPath)
	}
	if view.StageLines == nil {
		t.Fatal("stage lines must be an empty slice, not nil")
	}
}

func TestDecodeGatePipelineArgs(t *testing.T) {
	args, err := decodeGatePipelineArgs([]byte(`{"branch":"main","verbose":true}`))
	if err != nil || !args.Verbose || args.Branch != "main" {
		t.Fatalf("args = %+v err=%v", args, err)
	}
	if _, err := decodeGatePipelineArgs([]byte(`{"unknown":1}`)); err == nil {
		t.Fatal("unknown key was accepted")
	}
	if _, err := decodeGatePipelineArgs(nil); err != nil {
		t.Fatal(err)
	}
}

func TestGatePipelineSchemaDocumentsVerbose(t *testing.T) {
	for _, tool := range ToolDefinitions() {
		if tool.Name != "run_gate_pipeline" {
			continue
		}
		if _, ok := tool.InputSchema.Properties["verbose"]; !ok {
			t.Fatal("schema lacks verbose")
		}
		if !strings.Contains(tool.Description, "receipt_path") {
			t.Fatal("description lacks receipt_path")
		}
		return
	}
	t.Fatal("run_gate_pipeline not defined")
}

func TestGatePipelineResponseJSONHasReceiptPath(t *testing.T) {
	data, err := json.Marshal(gatePipelineResponse{Clean: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"receipt_path":""`) {
		t.Fatalf("json = %s", data)
	}
}
