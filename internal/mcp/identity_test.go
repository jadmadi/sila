package mcp

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalizeAgentName(t *testing.T) {
	cases := map[string]string{
		"DeepSeek Harness (TUI)": "deepseekharnesstui",
		"  Claude Code ":         "claude-code"[:0] + "claudecode",
		"---":                    "",
		"":                       "",
	}
	for input, want := range cases {
		if got := NormalizeAgentName(input); got != want {
			t.Fatalf("%q -> %q, want %q", input, got, want)
		}
	}
	long := strings.Repeat("a", 40)
	if got := NormalizeAgentName(long); len([]rune(got)) != 32 {
		t.Fatalf("long name = %d runes", len([]rune(got)))
	}
}

func TestResolveAgentPrecedence(t *testing.T) {
	if got := ResolveAgent("env-agent", "config-agent", "client-agent"); got != "envagent" {
		t.Fatalf("env precedence = %q", got)
	}
	if got := ResolveAgent("", "config-agent", "client-agent"); got != "configagent" {
		t.Fatalf("config precedence = %q", got)
	}
	if got := ResolveAgent("", "", "client-agent"); got != "clientagent" {
		t.Fatalf("client precedence = %q", got)
	}
	if got := ResolveAgent("--", "", ""); got != "" {
		t.Fatalf("punctuation = %q", got)
	}
}

func TestInitializeReportsAgent(t *testing.T) {
	t.Setenv("SILA_AGENT", "")
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"DeepSeek Harness (TUI)"}}}`)
	var out bytes.Buffer
	srv := NewServer(nil, in, &out)
	if err := srv.Run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"agent":"deepseekharnesstui"`) {
		t.Fatalf("initialize = %s", out.String())
	}
}
