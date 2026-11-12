package assess

import "testing"

func TestExtractVerificationCommandsRejectsMarkdownReference(t *testing.T) {
	md := "## Verification\n\n- Changelog entry under `[Unreleased]` naming the table.\n- `[ -f coverage.out ]`\n"
	cmds := ExtractVerificationCommands(md)
	for _, c := range cmds {
		if c == "[Unreleased]" {
			t.Fatalf("markdown reference extracted as a command: %v", cmds)
		}
	}
	found := false
	for _, c := range cmds {
		if c == "[ -f coverage.out ]" {
			found = true
		}
	}
	if !found {
		t.Fatalf("shell test operator missing: %v", cmds)
	}
}
