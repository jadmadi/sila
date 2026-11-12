package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/git"
)

// gateLogBound caps the verbose pipeline log returned over MCP.
const gateLogBound = 256 << 10

type gatePipelineArgs struct {
	Branch        string `json:"branch"`
	ForwardTarget string `json:"forward_target"`
	AutoForward   bool   `json:"auto_forward"`
	Project       string `json:"project"`
	Verbose       bool   `json:"verbose"`
}

// decodeGatePipelineArgs rejects unknown keys instead of silently ignoring them.
func decodeGatePipelineArgs(raw []byte) (gatePipelineArgs, error) {
	var args gatePipelineArgs
	if len(bytes.TrimSpace(raw)) == 0 {
		return args, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return args, err
	}
	return args, nil
}

// gatePipelineResponse is the compact MCP shape: stage lines and the receipt
// path by default, the bounded log only when verbose is true.
type gatePipelineResponse struct {
	Branch      string   `json:"branch"`
	TargetRef   string   `json:"target_ref,omitempty"`
	Forwarded   bool     `json:"forwarded"`
	Clean       bool     `json:"clean"`
	DurationMS  int64    `json:"duration_ms"`
	StageLines  []string `json:"stage_lines"`
	ReceiptPath string   `json:"receipt_path"`
	Output      string   `json:"output,omitempty"`
}

func buildGatePipelineResponse(res git.GatePipelineResult, verbose bool) gatePipelineResponse {
	stages := res.Checks
	if stages == nil {
		stages = []string{}
	}
	out := gatePipelineResponse{
		Branch:      res.Branch,
		TargetRef:   res.TargetRef,
		Forwarded:   res.Forwarded,
		Clean:       res.Clean,
		DurationMS:  res.Duration.Milliseconds(),
		StageLines:  stages,
		ReceiptPath: findGateReceipt(res.Worktree),
	}
	if verbose {
		out.Output = boundGateOutput(res.Output, gateLogBound)
	}
	return out
}

// findGateReceipt returns the newest .sila/gate/run-*.log under worktree, or ""
// when the worktree or receipts are absent. A missing receipt is never an error.
func findGateReceipt(worktree string) string {
	if strings.TrimSpace(worktree) == "" {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(worktree, ".sila", "gate", "run-*.log"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Slice(matches, func(i, j int) bool {
		left, lerr := os.Stat(matches[i])
		right, rerr := os.Stat(matches[j])
		if lerr != nil || rerr != nil {
			return matches[i] > matches[j]
		}
		if left.ModTime().Equal(right.ModTime()) {
			return matches[i] > matches[j]
		}
		return left.ModTime().After(right.ModTime())
	})
	return matches[0]
}

func boundGateOutput(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "\n[truncated]\n"
}

var _ = time.Second
