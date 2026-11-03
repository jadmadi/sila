package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultServerCard(t *testing.T) {
	card := DefaultServerCard("2.4.0")
	if card.Name != "sila" {
		t.Errorf("card.Name = %q, want sila", card.Name)
	}
	if card.Version != "2.4.0" {
		t.Errorf("card.Version = %q, want 2.4.0", card.Version)
	}
	if !card.Capabilities.Tools {
		t.Error("expected Tools capability to be true")
	}
	if !card.Capabilities.Prompts {
		t.Error("expected Prompts capability to be true")
	}

	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("failed to marshal server card: %v", err)
	}
	var unmarshaled ServerCard
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Fatalf("failed to unmarshal server card: %v", err)
	}
	if unmarshaled.Name != "sila" {
		t.Errorf("unmarshaled name = %s, want sila", unmarshaled.Name)
	}
	if !unmarshaled.Capabilities.Prompts {
		t.Error("expected unmarshaled Prompts capability to be true")
	}
}

func TestDefaultLinkset(t *testing.T) {
	linkset := DefaultLinkset("https://api.sila.dev")
	if len(linkset.Linkset) != 1 {
		t.Fatalf("expected 1 linkset entry, got %d", len(linkset.Linkset))
	}
	entry := linkset.Linkset[0]
	if entry.Anchor != "https://api.sila.dev/" {
		t.Errorf("entry.Anchor = %q, want https://api.sila.dev/", entry.Anchor)
	}
	if len(entry.MCPServerCard) != 1 {
		t.Fatalf("expected 1 mcp-server-card link, got %d", len(entry.MCPServerCard))
	}
	if entry.MCPServerCard[0].Href != "https://api.sila.dev/.well-known/mcp/server-card.json" {
		t.Errorf("server card href = %q", entry.MCPServerCard[0].Href)
	}

	data, err := json.Marshal(linkset)
	if err != nil {
		t.Fatalf("failed to marshal linkset: %v", err)
	}
	if !strings.Contains(string(data), "mcp-server-card") {
		t.Errorf("expected mcp-server-card in linkset JSON, got:\n%s", string(data))
	}
}

func TestDefaultLinkHeaders(t *testing.T) {
	header := DefaultLinkHeaders("https://api.sila.dev")
	if !strings.Contains(header, "rel=\"mcp-server-card\"") {
		t.Errorf("expected rel=\"mcp-server-card\" in header, got: %s", header)
	}
	if !strings.Contains(header, "rel=\"linkset\"") {
		t.Errorf("expected rel=\"linkset\" in header, got: %s", header)
	}
	if !strings.Contains(header, "rel=\"service-doc\"") {
		t.Errorf("expected rel=\"service-doc\" in header, got: %s", header)
	}
}

func TestDefaultLLMsTxt(t *testing.T) {
	llms := DefaultLLMsTxt("https://api.sila.dev", "1.0.0")
	if !strings.Contains(llms, "# Sila Continuity Engine") {
		t.Errorf("expected title in llms.txt, got:\n%s", llms)
	}
	if !strings.Contains(llms, "https://api.sila.dev/.well-known/mcp/server-card.json") {
		t.Errorf("expected server-card URL in llms.txt, got:\n%s", llms)
	}
}
