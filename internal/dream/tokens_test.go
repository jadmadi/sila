package dream

import (
	"reflect"
	"slices"
	"testing"
)

func TestExtractKeepTokens_Basic(t *testing.T) {
	text := `
During testing of internal/store/handoffs.go, we encountered SQLITE_BUSY and exit code 127.
The agent ran 'go test -race ./internal/store/...' which failed on commit a1b2c3d4e5f.
See documentation at https://sqlite.org/c3ref/busy_handler.html for details.
Check env var SILA_ACTIVE_GOAL and task id task_987654321.
Invoked PopEligibleHandoff with store.Store struct.
`
	tokens := ExtractKeepTokens(text)

	// Verify FilePaths
	if !slices.Contains(tokens.FilePaths, "internal/store/handoffs.go") {
		t.Errorf("expected FilePaths to contain 'internal/store/handoffs.go', got %v", tokens.FilePaths)
	}

	// Verify ErrorCodes
	if !slices.Contains(tokens.ErrorCodes, "SQLITE_BUSY") {
		t.Errorf("expected ErrorCodes to contain 'SQLITE_BUSY', got %v", tokens.ErrorCodes)
	}
	if !slices.Contains(tokens.ErrorCodes, "exit code 127") {
		t.Errorf("expected ErrorCodes to contain 'exit code 127', got %v", tokens.ErrorCodes)
	}

	// Verify URLs
	if !slices.Contains(tokens.URLs, "https://sqlite.org/c3ref/busy_handler.html") {
		t.Errorf("expected URLs to contain 'https://sqlite.org/c3ref/busy_handler.html', got %v", tokens.URLs)
	}

	// Verify Commands
	hasCmd := false
	for _, cmd := range tokens.Commands {
		if cmd == "go test -race ./internal/store/..." || cmd == "go test" {
			hasCmd = true
			break
		}
	}
	if !hasCmd {
		t.Errorf("expected Commands to contain go test invocation, got %v", tokens.Commands)
	}

	// Verify Symbols
	if !slices.Contains(tokens.Symbols, "PopEligibleHandoff") {
		t.Errorf("expected Symbols to contain 'PopEligibleHandoff', got %v", tokens.Symbols)
	}
	if !slices.Contains(tokens.Symbols, "store.Store") {
		t.Errorf("expected Symbols to contain 'store.Store', got %v", tokens.Symbols)
	}

	// Verify Identifiers
	if !slices.Contains(tokens.Identifiers, "SILA_ACTIVE_GOAL") {
		t.Errorf("expected Identifiers to contain 'SILA_ACTIVE_GOAL', got %v", tokens.Identifiers)
	}
	if !slices.Contains(tokens.Identifiers, "task_987654321") {
		t.Errorf("expected Identifiers to contain 'task_987654321', got %v", tokens.Identifiers)
	}
}

func TestExtractKeepTokens_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		validate func(t *testing.T, k ExtractedKeepTokens)
	}{
		{
			name:  "empty string",
			input: "",
			validate: func(t *testing.T, k ExtractedKeepTokens) {
				if len(k.FilePaths) != 0 || len(k.Symbols) != 0 || len(k.ErrorCodes) != 0 {
					t.Errorf("expected empty tokens, got %+v", k)
				}
			},
		},
		{
			name:  "windows paths and panic",
			input: "C:\\Users\\dev\\project\\main.go threw panic: nil pointer dereference at runtime.",
			validate: func(t *testing.T, k ExtractedKeepTokens) {
				if !slices.Contains(k.FilePaths, "C:\\Users\\dev\\project\\main.go") {
					t.Errorf("expected windows path, got %v", k.FilePaths)
				}
				if !slices.Contains(k.ErrorCodes, "panic: nil pointer dereference at runtime") {
					t.Errorf("expected panic error code, got %v", k.ErrorCodes)
				}
			},
		},
		{
			name:  "http status and posix errors",
			input: "Gateway returned HTTP 502 with ENOSPC on disk drive.",
			validate: func(t *testing.T, k ExtractedKeepTokens) {
				if !slices.Contains(k.ErrorCodes, "HTTP 502") {
					t.Errorf("expected HTTP 502, got %v", k.ErrorCodes)
				}
				if !slices.Contains(k.ErrorCodes, "ENOSPC") {
					t.Errorf("expected ENOSPC, got %v", k.ErrorCodes)
				}
			},
		},
		{
			name:  "uuid and sha",
			input: "Session b6953258-00a8-4c81-8176-79177ea336f0 committed 78e412a32190bb4.",
			validate: func(t *testing.T, k ExtractedKeepTokens) {
				if !slices.Contains(k.Identifiers, "b6953258-00a8-4c81-8176-79177ea336f0") {
					t.Errorf("expected uuid identifier, got %v", k.Identifiers)
				}
				if !slices.Contains(k.Identifiers, "78e412a32190bb4") {
					t.Errorf("expected sha identifier, got %v", k.Identifiers)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			k := ExtractKeepTokens(tc.input)
			tc.validate(t, k)
		})
	}
}

func TestExtractKeepTokens_DeterministicSorting(t *testing.T) {
	input := "apple.go zebra.go banana.go cherry.go"
	tokens1 := ExtractKeepTokens(input)
	tokens2 := ExtractKeepTokens(input)

	if !reflect.DeepEqual(tokens1, tokens2) {
		t.Fatalf("expected deterministic output across runs")
	}

	expectedOrder := []string{"apple.go", "banana.go", "cherry.go", "zebra.go"}
	if !reflect.DeepEqual(tokens1.FilePaths, expectedOrder) {
		t.Errorf("expected sorted paths %v, got %v", expectedOrder, tokens1.FilePaths)
	}
}
