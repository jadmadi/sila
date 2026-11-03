package dream

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestBuildSessionStartBriefBudgetAndContract(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "brief.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: p.ID,
		Agent:     "test",
		Summary:   "Implemented the previous slice",
		OpenItems: []string{"write the follow-up test", "- [x] already done"},
	}); err != nil {
		t.Fatal(err)
	}

	brief, err := BuildSessionStartBrief(st, p.ID, tmp, "sess-1", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Schema != MemoryContractVersion || brief.MemoryContractVersion != MemoryContractVersion {
		t.Fatalf("wrong contract: %+v", brief)
	}
	if brief.Bytes > 1024 || len(brief.Markdown) > 1024 {
		t.Fatalf("brief exceeded budget: %d bytes\n%s", brief.Bytes, brief.Markdown)
	}
	capped, err := BuildSessionStartBrief(st, p.ID, tmp, "sess-1", 512)
	if err != nil {
		t.Fatal(err)
	}
	if capped.Bytes > 512 {
		t.Fatalf("512-byte cap exceeded: %d", capped.Bytes)
	}
	for _, want := range []string{"Memory required", "sila recall", "sila memo", "sila handoff", "sila/memory-contract/v1"} {
		if !strings.Contains(brief.Markdown, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief.Markdown)
		}
	}
	if !strings.Contains(brief.Markdown, "Implemented the previous slice") {
		t.Fatalf("handoff summary missing:\n%s", brief.Markdown)
	}

	// A larger budget may include the optional next/open items.
	large, err := BuildSessionStartBrief(st, p.ID, tmp, "sess-1", 8192)
	if err != nil {
		t.Fatal(err)
	}
	if large.Bytes < brief.Bytes {
		t.Fatalf("larger budget produced a smaller brief: %d < %d", large.Bytes, brief.Bytes)
	}
	if !strings.Contains(large.Markdown, "Open items:") {
		t.Fatalf("large brief missing open items:\n%s", large.Markdown)
	}

	// Minimum clamp.
	small, err := BuildSessionStartBrief(st, p.ID, tmp, "sess-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if small.Bytes > MinSessionBriefBudget {
		t.Fatalf("minimum budget not clamped: %d", small.Bytes)
	}

	// JSON contract.
	data, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"memory_contract_version":"`+MemoryContractVersion+`"`) {
		t.Fatalf("JSON contract missing: %s", data)
	}
}

func TestBuildSessionStartBriefDegradesWithoutStore(t *testing.T) {
	tmp := t.TempDir()
	brief, err := BuildSessionStartBrief(nil, "", tmp, "", 512)
	if err != nil {
		t.Fatal(err)
	}
	if !brief.MemoryUnavailable {
		t.Fatal("expected memory_unavailable without a store")
	}
	if brief.Project == "" || brief.Markdown == "" {
		t.Fatalf("expected static brief: %+v", brief)
	}
}

func TestSessionStartBriefPrefersStoredContext(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "brief-context.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.EnsureProject(tmp)
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	if err := st.UpsertAgentSession(store.AgentSession{
		SessionID: "sess-ctx", ProjectID: p.ID, Harness: "codex", Agent: "codex",
		Source: "flag", Branch: "main", Head: head,
	}); err != nil {
		t.Fatal(err)
	}
	brief, err := BuildSessionStartBrief(st, p.ID, tmp, "sess-ctx", 2048)
	if err != nil {
		t.Fatal(err)
	}
	if brief.Branch != "main" || brief.Head != head || brief.ContextSource != "session" {
		t.Fatalf("stored context not preferred: %+v", brief)
	}

	if err := st.UpsertAgentSession(store.AgentSession{
		SessionID: "sess-legacy", ProjectID: p.ID, Harness: "codex", Agent: "codex", Source: "flag",
	}); err != nil {
		t.Fatal(err)
	}
	legacy, err := BuildSessionStartBrief(st, p.ID, tmp, "sess-legacy", 2048)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ContextSource != "unknown" {
		t.Fatalf("legacy session should be marked unknown: %+v", legacy)
	}
}
