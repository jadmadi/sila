package agents

import (
	"strings"
	"testing"
)

func TestMemoryProtocolChecklistBudgetAndCoverage(t *testing.T) {
	if len(MemoryProtocolChecklist) > 12 {
		t.Fatalf("checklist has %d lines, want <=12", len(MemoryProtocolChecklist))
	}
	body := strings.Join(MemoryProtocolChecklist, "\n")
	if len(body) > 900 {
		t.Fatalf("checklist is %d bytes, want <=900", len(body))
	}
	for _, verb := range []string{"prime", "recall", "memo", "lesson", "case", "facts verify", "gate", "handoff", "sync"} {
		if !strings.Contains(body, verb) {
			t.Errorf("checklist missing verb %q", verb)
		}
	}
	if !strings.Contains(MemoryProtocolChecklist[0], "goal contract") || !strings.Contains(MemoryProtocolChecklist[0], "gate") {
		t.Errorf("first line must name the goal contract and gate: %q", MemoryProtocolChecklist[0])
	}
	last := MemoryProtocolChecklist[len(MemoryProtocolChecklist)-1]
	for _, ref := range []string{"sila salam", "sila guide", "docs/reference.md"} {
		if !strings.Contains(last, ref) {
			t.Errorf("last line missing %q: %q", ref, last)
		}
	}
	if !strings.Contains(TriggerContract, "Sila Trigger Contract") {
		t.Fatal("TriggerContract heading missing")
	}
	if len(MemoryProtocolExpanded()) != 9 {
		t.Fatalf("expanded checklist has %d verbs, want 9", len(MemoryProtocolExpanded()))
	}
}
