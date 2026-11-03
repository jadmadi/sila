package dream

import (
	"strings"
	"testing"
)

func TestParseBudget(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"8k", 8000},
		{"16k", 16000},
		{"32K", 32000},
		{"1m", 1000000},
		{"4000", 4000},
		{"", 0},
		{"invalid", 0},
	}

	for _, tt := range tests {
		got := ParseBudget(tt.input)
		if got != tt.expected {
			t.Errorf("ParseBudget(%q) = %d; want %d", tt.input, got, tt.expected)
		}
	}
}

func TestStripBoilerplateHeaders(t *testing.T) {
	input := `// Copyright 2026 The Example Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
// SPDX-License-Identifier: BSD-3-Clause

package main

func main() {}
`
	res, count := StripBoilerplateHeaders(input)
	if count != 1 {
		t.Errorf("expected 1 stripped header, got %d", count)
	}
	if strings.Contains(res, "SPDX-License-Identifier") {
		t.Errorf("expected SPDX header to be stripped, got:\n%s", res)
	}
	if !strings.Contains(res, "package main") {
		t.Errorf("expected code to be retained, got:\n%s", res)
	}
}

func TestFoldTestFixtures(t *testing.T) {
	var lines []string
	lines = append(lines, "var TestCases = []string{")
	for i := 1; i <= 30; i++ {
		lines = append(lines, `  "test case line `+string(rune('A'+i))+`",`)
	}
	lines = append(lines, "}")
	input := strings.Join(lines, "\n")

	res, count := FoldTestFixtures(input, 15)
	if count != 1 {
		t.Errorf("expected 1 folded fixture, got %d (result: %s)", count, res)
	}
	if !strings.Contains(res, "folded for context budget") {
		t.Errorf("expected folded marker in result:\n%s", res)
	}
	// Verify first and last items are preserved
	if !strings.Contains(res, "test case line B") {
		t.Errorf("expected first items to be preserved")
	}
	if !strings.Contains(res, "test case line _") {
		// Verify closing
		if !strings.HasSuffix(strings.TrimSpace(res), "}") {
			t.Errorf("expected closing bracket to be preserved")
		}
	}
}

func TestCondenseStackTraces(t *testing.T) {
	input := `panic: runtime error: invalid memory address or nil pointer dereference
goroutine 1 [running]:
main.doStep(0x1, 0x2)
	/workspace/cmd/sila/main.go:42 +0x3a
main.stepIntermediate1(...)
	/workspace/cmd/sila/main.go:88 +0x12
main.stepIntermediate2(...)
	/workspace/cmd/sila/main.go:95 +0x15
main.stepIntermediate3(...)
	/workspace/cmd/sila/main.go:102 +0x18
main.stepIntermediate4(...)
	/workspace/cmd/sila/main.go:110 +0x20
main.runApp(0x0)
	/workspace/cmd/sila/main.go:200 +0x50
main.main()
	/workspace/cmd/sila/main.go:250 +0x80
`
	res, count := CondenseStackTraces(input)
	if count != 1 {
		t.Errorf("expected 1 condensed stack trace, got %d", count)
	}
	if !strings.Contains(res, "intermediate stack frames condensed") {
		t.Errorf("expected condensed marker in result:\n%s", res)
	}
	if !strings.Contains(res, "main.doStep") {
		t.Errorf("expected root frame to be preserved")
	}
	if !strings.Contains(res, "main.main()") {
		t.Errorf("expected bottom frame to be preserved")
	}
}

func TestCompressContext(t *testing.T) {
	boilerplate := `// Copyright 2026 Example Corp.
// All rights reserved.
// SPDX-License-Identifier: MIT

`
	var fixtures []string
	fixtures = append(fixtures, "const mockJSON = `[")
	for i := 0; i < 25; i++ {
		fixtures = append(fixtures, `  {"id": `+string(rune('1'+i))+`, "data": "sample payload data lorem ipsum"},`)
	}
	fixtures = append(fixtures, "]`")

	fullText := boilerplate + strings.Join(fixtures, "\n") + "\n\n**File-Referenced Claims**:\n• claim 1 (`file1.go`)\n• claim 2 (`file2.go`)\n• claim 3 (`file3.go`)\n• claim 4 (`file4.go`)\n• claim 5 (`file5.go`)\n"

	opts := DefaultCompressionOptions()
	opts.TargetBudget = 50 // Low budget to force claim pruning
	res := CompressContext(fullText, opts)

	if res.OriginalTokens <= res.CompressedTokens {
		t.Errorf("expected compression savings, got orig=%d, compressed=%d", res.OriginalTokens, res.CompressedTokens)
	}
	if res.PrunedTokens <= 0 {
		t.Errorf("expected pruned tokens > 0, got %d", res.PrunedTokens)
	}
	if res.SavingsRatio <= 0.0 {
		t.Errorf("expected savings ratio > 0, got %f", res.SavingsRatio)
	}
	if !strings.Contains(res.CompressedText, "License boilerplate stripped") {
		t.Errorf("expected header stripped marker in result")
	}
}
