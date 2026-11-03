package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestUIBoxRendering(t *testing.T) {
	uiPlain := UI{noColor: true, enabled: false}
	boxPlain := uiPlain.Box("Alert", "First line of notice\nSecond line of notice")

	if strings.Contains(boxPlain, "\x1b[") {
		t.Errorf("plain box must not contain escape sequences")
	}
	if !strings.Contains(boxPlain, "Alert") {
		t.Errorf("expected title in box output")
	}
	if !strings.Contains(boxPlain, "First line of notice") {
		t.Errorf("expected body line in box output")
	}
	if !strings.Contains(boxPlain, "┌─") || !strings.Contains(boxPlain, "└─") {
		t.Errorf("expected box borders")
	}

	uiColor := UI{noColor: false, enabled: true}
	boxColor := uiColor.Box("Title", "Colored box body")
	if !strings.Contains(boxColor, "\x1b[") {
		t.Errorf("color box expected escape sequences for title")
	}
}

func TestUISpinnerNonInteractive(t *testing.T) {
	var buf bytes.Buffer
	ui := UI{noColor: true, enabled: false}
	sp := &Spinner{
		ui:  ui,
		out: &buf,
		msg: "Processing task",
	}

	sp.Start()
	sp.Update("Still processing")
	time.Sleep(20 * time.Millisecond)
	sp.Success("Completed successfully")

	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("non-interactive spinner output must not contain ANSI escape codes, got: %q", out)
	}
	if !strings.Contains(out, "Completed successfully") {
		t.Errorf("expected success message in output, got: %q", out)
	}

	buf.Reset()
	sp2 := &Spinner{
		ui:  ui,
		out: &buf,
		msg: "Failing task",
	}
	sp2.Start()
	sp2.Failure("Task failed")
	out2 := buf.String()
	if strings.Contains(out2, "\x1b[") {
		t.Errorf("non-interactive spinner failure must not contain ANSI codes, got: %q", out2)
	}
	if !strings.Contains(out2, "Task failed") {
		t.Errorf("expected failure message in output, got: %q", out2)
	}
}

func TestUIMarkdownMethods(t *testing.T) {
	ui := UI{noColor: true, enabled: false}
	md := "# Hello\n\n**World**"

	str := ui.Markdown(md)
	if !strings.Contains(str, "HELLO") || !strings.Contains(str, "World") {
		t.Errorf("unexpected ui.Markdown output: %s", str)
	}

	var buf bytes.Buffer
	err := ui.RenderMarkdown(&buf, md)
	if err != nil {
		t.Fatalf("ui.RenderMarkdown failed: %v", err)
	}
	if buf.String() != str {
		t.Errorf("expected RenderMarkdown to match Markdown string")
	}
}
