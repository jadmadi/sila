package agents

import (
	"strings"
	"testing"
)

func TestComposeStateBlockIdempotent(t *testing.T) {
	body := "### Continuity state\n- Next: ship it"
	out, changed := ComposeStateBlock("", body)
	if !changed {
		t.Fatal("first compose reported unchanged")
	}
	for _, want := range []string{StateBlockBegin, StateBlockEnd, "scope=state", "- Next: ship it"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	again, changed := ComposeStateBlock(out, body)
	if changed || again != out {
		t.Fatalf("second compose changed the file:\n%s", again)
	}
}

func TestComposeStateBlockPreservesUserAndCore(t *testing.T) {
	existing := "# Rules\n\nAlways run tests.\n\n" + OwnedBlockBegin + "\n<!-- sila:meta rev=1 scope=core hash=0123456789abcdef -->\n# Sila\ncore body\n" + OwnedBlockEnd + "\n"
	out, changed := ComposeStateBlock(existing, "### Continuity state\n- Next: ship")
	if !changed {
		t.Fatal("expected a change")
	}
	if !strings.Contains(out, "# Rules") || !strings.Contains(out, "Always run tests.") {
		t.Fatal("user content lost")
	}
	if !strings.Contains(out, OwnedBlockBegin) || !strings.Contains(out, "core body") {
		t.Fatal("core block lost")
	}
	if strings.Count(out, StateBlockBegin) != 1 {
		t.Fatalf("state block duplicated:\n%s", out)
	}
	if strings.Index(out, StateBlockBegin) > strings.Index(out, OwnedBlockBegin) {
		t.Fatalf("state block must precede the core block:\n%s", out)
	}
}

func TestComposeStateBlockUpdatesBody(t *testing.T) {
	out, _ := ComposeStateBlock("", "### Continuity state\n- Next: one")
	updated, changed := ComposeStateBlock(out, "### Continuity state\n- Next: two")
	if !changed {
		t.Fatal("changed body reported unchanged")
	}
	if strings.Contains(updated, "Next: one") || !strings.Contains(updated, "Next: two") {
		t.Fatalf("body not replaced:\n%s", updated)
	}
	if strings.Count(updated, StateBlockBegin) != 1 {
		t.Fatal("state block duplicated")
	}
}

func TestComposeStateBlockRemovesEmptyBody(t *testing.T) {
	out, _ := ComposeStateBlock("# Rules\n\nkeep me\n", "### Continuity state\n- Next: ship")
	removed, changed := ComposeStateBlock(out, "")
	if !changed {
		t.Fatal("removal reported unchanged")
	}
	if strings.Contains(removed, StateBlockBegin) || strings.Contains(removed, "Continuity state") {
		t.Fatalf("state block still present:\n%s", removed)
	}
	if !strings.Contains(removed, "keep me") {
		t.Fatalf("user content lost:\n%s", removed)
	}
}

func TestExtractStateBlock(t *testing.T) {
	out, _ := ComposeStateBlock("", "### Continuity state\n- Next: ship")
	body, hash, ok := ExtractStateBlock(out)
	if !ok || !strings.Contains(body, "Next: ship") {
		t.Fatalf("extract: ok=%v body=%q", ok, body)
	}
	if hash != HashManagedBody(body) {
		t.Fatalf("hash mismatch: %q vs %q", hash, HashManagedBody(body))
	}
	if _, _, ok := ExtractStateBlock("no block here"); ok {
		t.Fatal("extract found a block where none exists")
	}
}

func TestStateMetaRejectsWrongScope(t *testing.T) {
	if _, ok := parseStateMeta("<!-- sila:state-meta rev=1 scope=core hash=0123456789abcdef -->"); ok {
		t.Fatal("core scope accepted for a state block")
	}
	if _, ok := parseStateMeta("<!-- sila:state-meta rev=1 scope=state hash=XYZ -->"); ok {
		t.Fatal("invalid hash accepted")
	}
}
