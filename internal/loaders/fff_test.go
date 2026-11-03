package loaders

import (
	"reflect"
	"testing"
)

func TestIsFFFToolRecognizesVariants(t *testing.T) {
	for _, name := range []string{"fffind", "ffgrep", "fff_multi_grep", "fff-multi-grep", "fff_search", "fff-grep", "fff", "fff-mcp"} {
		if !IsFFFTool(name) {
			t.Errorf("IsFFFTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"grep", "ripgrep", "read", "bash", "fffx"} {
		if IsFFFTool(name) {
			t.Errorf("IsFFFTool(%q) = true, want false", name)
		}
	}
	if got := NormalizeFFFTool("ffgrep"); got != "fff" {
		t.Errorf("NormalizeFFFTool(ffgrep) = %q, want fff", got)
	}
	if got := NormalizeFFFTool("read"); got != "read" {
		t.Errorf("NormalizeFFFTool(read) = %q, want read", got)
	}
}

func TestExtractFFFInvocation(t *testing.T) {
	content := "I searched the tree.\n- ffgrep \"token refresh\" internal/auth/token.go src/session.ts\n> fffind \"ExportPanel\"\nthen I read the file."
	query, paths := ExtractFFFInvocation(content)
	if query != "token refresh" {
		t.Fatalf("query = %q, want token refresh", query)
	}
	want := []string{"internal/auth/token.go", "src/session.ts"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if only := ExtractFFFToolPaths(content); !reflect.DeepEqual(only, want) {
		t.Fatalf("ExtractFFFToolPaths = %v, want %v", only, want)
	}

	// Non-fff lines and bare words are ignored.
	query, paths = ExtractFFFInvocation("grep -r token .\nffgrep ExportPanel")
	if query != "ExportPanel" || len(paths) != 0 {
		t.Fatalf("bare-word invocation = (%q, %v), want (ExportPanel, none)", query, paths)
	}
}
