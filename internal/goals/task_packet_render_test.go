package goals

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleRenderPacket() *TaskPacket {
	return &TaskPacket{
		SchemaVersion: TaskPacketSchemaVersion,
		GoalSlug:      "sample-engine-delivery",
		TaskID:        "T-001",
		Purpose:       "Deliver high-performance execution packets",
		Task: TaskContract{
			ID:        "T-001",
			Title:     "Assemble Engine Kernel",
			Context:   "Initialize engine data structures with UTF-8 support (صِلَة).",
			Why:       "Foundational kernel initialization layer.",
			DependsOn: []string{},
			Files: TaskFiles{
				Read:   []string{"docs/spec.md"},
				Edit:   []string{"internal/kernel/engine.go"},
				Create: []string{"internal/kernel/output.go"},
				Forbid: []string{"research/*", ".git/*"},
			},
			References: []Reference{
				{
					Path:    "docs/spec.md",
					Symbol:  "EngineSpec",
					Excerpt: "```go\ntype KernelConfig struct {\n\tTickRate int // `json:\"tick_rate\"`\n}\n```",
					SHA256:  "a1b2c3d4e5f67890123456789012345678901234567890123456789012345678",
				},
			},
			Decisions: []Decision{
				{
					ID:        "D-001",
					Chosen:    "Use 64-bit integer ticks",
					Rationale: "Avoid integer overflow after 24 hours of operation",
					Rejected:  []string{"32-bit ticks", "floating point time"},
				},
			},
			Steps: []Step{
				{
					ID:        "S-001",
					Action:    "Instantiate kernel configuration",
					Expected:  "TickRate configured to 60 Hz",
					OnFailure: "Abort initialization",
				},
			},
			Interfaces: []string{
				"type EngineKernel interface { Start() error }",
			},
			Constraints: []string{
				"CGO_ENABLED=0",
				"Zero heap allocation in Start loop",
			},
			Examples: []Example{
				{
					Input:       "Start()",
					Output:      "nil",
					Explanation: "Clean kernel startup without errors",
				},
			},
			AcceptanceIDs: []string{"AC-001", "AC-002"},
			Checks: []Check{
				{
					ID:               "C-001",
					Executable:       "go",
					Args:             []string{"test", "-v", "./internal/kernel"},
					Cwd:              "cli",
					TimeoutSeconds:   60,
					ExpectedExitCode: 0,
					Scope:            ScopeUnit,
					Assertions: []Assertion{
						{
							Kind:     AssertionGoTestPass,
							Target:   "github.com/jadmadi/sila/internal/kernel/TestKernelStart",
							Expected: true,
						},
					},
					Capability:  CapabilityLocal,
					Environment: map[string]string{"CGO_ENABLED": "0"},
				},
				{
					ID:               "C-002",
					Executable:       "go",
					Args:             []string{"test", "-run", "TestFaultInjection"},
					Cwd:              "cli",
					TimeoutSeconds:   30,
					ExpectedExitCode: 2,
					Scope:            ScopeIntegration,
					Assertions: []Assertion{
						{
							Kind:     AssertionStdoutExact,
							Target:   "stdout",
							Expected: "FATAL_FAULT_TRIGGERED",
						},
					},
					Capability: CapabilityLocal,
				},
			},
			StopConditions: []string{
				"Two consecutive test failures with identical error",
				"Deadlock detected during kernel initialization",
			},
			ReturnFormat: "sila/task-result/v1",
		},
		Acceptance: []AcceptanceCase{
			{
				ID:            "AC-001",
				Requirement:   "Kernel initializes within 10ms",
				Given:         "Clean operating environment",
				Action:        "Invoke Start()",
				Expected:      "Startup completed in <10ms",
				RequiredScope: ScopeUnit,
				Mandatory:     true,
				CheckIDs:      []string{"C-001"},
			},
			{
				ID:            "AC-002",
				Requirement:   "Fault injection triggers expected exit code 2",
				Given:         "Corrupted configuration input",
				Action:        "Invoke StartWithCorruptedConfig()",
				Expected:      "Process halts with exit code 2",
				RequiredScope: ScopeIntegration,
				Mandatory:     true,
				CheckIDs:      []string{"C-002"},
			},
		},
		GlobalConstraints: []string{"pure-Go stdlib", "zero external dependencies"},
		NonGoals:          []string{"no GUI dashboard", "no telemetry collection"},
		DependencyOutputs: []DependencyOutput{},
		SpecDigest:        "f8d3c1b4e2a56789012345678901234567890123456789012345678901234567",
		SourcePins: []SourcePin{
			{
				Path:         "docs/spec.md",
				SHA256:       "a1b2c3d4e5f67890123456789012345678901234567890123456789012345678",
				MustBeAbsent: false,
			},
			{
				Path:         "internal/kernel/engine.go",
				SHA256:       "b2c3d4e5f6789012345678901234567890123456789012345678901234567890",
				MustBeAbsent: false,
			},
			{
				Path:         "internal/kernel/output.go",
				SHA256:       "",
				MustBeAbsent: true,
			},
		},
		CandidateCommit: "0123456789abcdef0123456789abcdef01234567",
		PacketDigest:    "e5f6789012345678901234567890123456789012345678901234567890123456",
		Executable:      true,
		Diagnostics:     []Diagnostic{},
		MaxBytes:        0,
		Resume:          nil,
	}
}

func TestTaskPacketRender(t *testing.T) {
	packet := sampleRenderPacket()

	md, err := RenderTaskPacketMarkdown(packet)
	if err != nil {
		t.Fatalf("RenderTaskPacketMarkdown failed: %v", err)
	}

	goldenPath := filepath.Join("testdata", "task-packet.golden.md")
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		// If golden doesn't exist yet, we write it and verify
		if os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0755); err != nil {
				t.Fatalf("mkdir testdata: %v", err)
			}
			if err := os.WriteFile(goldenPath, []byte(md), 0644); err != nil {
				t.Fatalf("write golden: %v", err)
			}
			goldenBytes = []byte(md)
		} else {
			t.Fatalf("read golden file: %v", err)
		}
	}

	if md != string(goldenBytes) {
		t.Fatalf("rendered Markdown does not match golden file %s:\nGOT:\n%s\nWANT:\n%s", goldenPath, md, string(goldenBytes))
	}

	// Verify required sections are present
	requiredHeadings := []string{
		"# Task: T-001 Assemble Engine Kernel",
		"## Context",
		"## Files",
		"## Requirements",
		"## Interfaces",
		"## Constraints",
		"## Acceptance criteria",
		"## If blocked",
		"## Return",
	}
	for _, h := range requiredHeadings {
		if !strings.Contains(md, h) {
			t.Errorf("missing required heading in Markdown: %q", h)
		}
	}

	// Verify exact exit code 2 is preserved
	if !strings.Contains(md, "expected exit: 2") {
		t.Errorf("expected exit code 2 to be explicitly preserved, got:\n%s", md)
	}

	// Verify UTF-8 / Arabic content preservation
	if !strings.Contains(md, "صِلَة") {
		t.Errorf("Unicode content (صِلَة) was corrupted in Markdown")
	}

	// Verify lossless JSON encoding and decoding
	jsonData, err := EncodeTaskPacketJSON(packet)
	if err != nil {
		t.Fatalf("EncodeTaskPacketJSON failed: %v", err)
	}
	decoded, err := DecodeTaskPacket(jsonData)
	if err != nil {
		t.Fatalf("DecodeTaskPacket failed on encoded JSON: %v", err)
	}
	if decoded.TaskID != packet.TaskID || decoded.PacketDigest != packet.PacketDigest {
		t.Errorf("JSON roundtrip mismatch: %+v vs %+v", decoded, packet)
	}
	if !strings.Contains(string(jsonData), "صِلَة") {
		t.Errorf("Unicode corrupted in JSON output")
	}
}

func TestTaskPacketNoTruncation(t *testing.T) {
	packet := sampleRenderPacket()

	// 1. One-byte limit must fail explicitly with PACKET_TOO_LARGE diagnostic code
	packet.MaxBytes = 1

	_, err := RenderTaskPacketMarkdown(packet)
	if err == nil {
		t.Fatalf("expected RenderTaskPacketMarkdown to fail with 1-byte limit, got nil error")
	}
	if !strings.Contains(err.Error(), DiagPacketTooLarge) {
		t.Errorf("expected error to cite %s, got: %v", DiagPacketTooLarge, err)
	}

	_, err = EncodeTaskPacketJSON(packet)
	if err == nil {
		t.Fatalf("expected EncodeTaskPacketJSON to fail with 1-byte limit, got nil error")
	}
	if !strings.Contains(err.Error(), DiagPacketTooLarge) {
		t.Errorf("expected error to cite %s, got: %v", DiagPacketTooLarge, err)
	}

	// 2. Default max_bytes=0 means no limit, complete rendering with zero truncation
	packet.MaxBytes = 0
	md, err := RenderTaskPacketMarkdown(packet)
	if err != nil {
		t.Fatalf("expected RenderTaskPacketMarkdown to succeed with max_bytes=0: %v", err)
	}

	// Ensure stop conditions, acceptance cases, and steps are NOT dropped
	if !strings.Contains(md, "Deadlock detected during kernel initialization") {
		t.Errorf("stop condition was unexpectedly dropped from Markdown")
	}
	if !strings.Contains(md, "AC-002") {
		t.Errorf("acceptance case AC-002 was unexpectedly dropped from Markdown")
	}
	if !strings.Contains(md, "S-001") {
		t.Errorf("step S-001 was unexpectedly dropped from Markdown")
	}
}
