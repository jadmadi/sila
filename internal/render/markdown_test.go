package render

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderMarkdownColorAndPlain(t *testing.T) {
	sample := "# Document Title\n\n" +
		"This is a **bold statement** with some *italic* and ~~struck~~ text, plus `code_symbol` and [Sila Website](https://sila.sh).\n\n" +
		"---\n\n" +
		"## Architecture Principles\n\n" +
		"- Zero external dependencies\n" +
		"- Pure Go 1.26+\n" +
		"- Modern SQLite storage\n\n" +
		"1. Fast startup\n" +
		"2. Offline first\n\n" +
		"> [!NOTE]\n" +
		"> All session transcripts remain local and sovereign.\n\n" +
		"```go\n" +
		"func main() {\n" +
		"    println(\"hello world\")\n" +
		"}\n" +
		"```\n"

	// 1. Test Plain (Color disabled - piped / --no-color)
	uiPlain := UI{noColor: true, enabled: false}
	var bufPlain bytes.Buffer
	err := RenderMarkdown(&bufPlain, sample, uiPlain)
	if err != nil {
		t.Fatalf("RenderMarkdown failed: %v", err)
	}
	plainOut := bufPlain.String()

	// Must NOT contain any ANSI escape codes
	if strings.Contains(plainOut, "\x1b[") {
		t.Errorf("plain output must not contain ANSI escape codes, got:\n%s", plainOut)
	}

	// Must contain rendered structural elements
	if !strings.Contains(plainOut, "DOCUMENT TITLE") {
		t.Errorf("expected uppercase DOCUMENT TITLE in plain output")
	}
	if !strings.Contains(plainOut, "• Zero external dependencies") {
		t.Errorf("expected bullet symbol in plain output")
	}
	if !strings.Contains(plainOut, "1. Fast startup") {
		t.Errorf("expected ordered list in plain output")
	}
	if !strings.Contains(plainOut, "NOTE:") {
		t.Errorf("expected alert badge NOTE: in plain output")
	}
	if !strings.Contains(plainOut, "┌─ go ─") {
		t.Errorf("expected code block border in plain output")
	}
	if !strings.Contains(plainOut, "hello world") {
		t.Errorf("expected code content in plain output")
	}
	// Markdown asterisks should be stripped for clean text
	if strings.Contains(plainOut, "**bold statement**") {
		t.Errorf("expected ** to be stripped in plain output")
	}

	// 2. Test Color (Color enabled)
	uiColor := UI{noColor: false, enabled: true}
	var bufColor bytes.Buffer
	err = RenderMarkdown(&bufColor, sample, uiColor)
	if err != nil {
		t.Fatalf("RenderMarkdown with color failed: %v", err)
	}
	colorOut := bufColor.String()

	// Must contain ANSI escape codes
	if !strings.Contains(colorOut, "\x1b[") {
		t.Errorf("color output must contain ANSI escape codes")
	}
	if !strings.Contains(colorOut, "DOCUMENT TITLE") {
		t.Errorf("expected uppercase heading in color output")
	}
	if !strings.Contains(colorOut, "hello world") {
		t.Errorf("expected code block content in color output")
	}
}

func TestRenderMarkdownEdgeCases(t *testing.T) {
	ui := UI{noColor: false, enabled: true}

	// Empty string
	if out := RenderMarkdownString("", ui); out != "" {
		t.Errorf("expected empty output for empty input, got %q", out)
	}
	if out := RenderMarkdownString("   \n\n  ", ui); out != "" {
		t.Errorf("expected empty output for whitespace input, got %q", out)
	}

	// Unclosed code block
	unclosed := "```go\nfmt.Println(1)\n"
	out := RenderMarkdownString(unclosed, ui)
	if !strings.Contains(out, "fmt.Println(1)") {
		t.Errorf("expected unclosed code block content rendered")
	}

	// All alert types
	alerts := `> [!NOTE] note text
> [!TIP] tip text
> [!IMPORTANT] important text
> [!WARNING] warning text
> [!CAUTION] caution text
`
	outAlerts := RenderMarkdownString(alerts, ui)
	for _, expected := range []string{"NOTE:", "TIP:", "IMPORTANT:", "WARNING:", "CAUTION:"} {
		if !strings.Contains(outAlerts, expected) {
			t.Errorf("expected alert %s in output", expected)
		}
	}
}
