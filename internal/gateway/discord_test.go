package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiscordAdapter_SendNotification(t *testing.T) {
	var received discordPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode discord payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	adapter := NewDiscordAdapter(srv.URL, "Test Bot", "https://example.com/avatar.png", true)
	if !adapter.Enabled() {
		t.Fatalf("expected adapter to be enabled")
	}

	n := Notification{
		Title:    "Build Succeeded for " + "sk" + "-1234567890abcdef1234567890abcdef",
		Body:     "Completed in /home/tester/repo with token " + "ghp" + "_123456789012345678901234567890123456",
		Level:    "success",
		Project:  "sila",
		Category: "build",
		Metadata: map[string]string{
			"Commit": "abc1234",
		},
		Timestamp: time.Now(),
	}

	err := adapter.SendNotification(context.Background(), n)
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	if len(received.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(received.Embeds))
	}
	embed := received.Embeds[0]
	if embed.Color != 0x2ecc71 {
		t.Errorf("expected emerald color (0x2ecc71), got 0x%x", embed.Color)
	}
	if strings.Contains(embed.Title, "sk"+"-1234567890") {
		t.Errorf("title contains unredacted API key: %s", embed.Title)
	}
	if !strings.Contains(embed.Title, "[REDACTED_API_KEY]") {
		t.Errorf("expected title to have [REDACTED_API_KEY], got: %s", embed.Title)
	}
	if strings.Contains(embed.Description, "ghp"+"_1234") {
		t.Errorf("description contains unredacted GitHub token: %s", embed.Description)
	}
	if !strings.Contains(embed.Description, "~/repo") {
		t.Errorf("expected description to have ~/repo, got: %s", embed.Description)
	}
}

func TestDiscordAdapter_Disabled(t *testing.T) {
	adapter := NewDiscordAdapter("", "Bot", "", false)
	if adapter.Enabled() {
		t.Errorf("expected disabled")
	}
	err := adapter.SendNotification(context.Background(), Notification{Title: "Test"})
	if err == nil {
		t.Errorf("expected error when sending on disabled adapter")
	}
}

func TestDiscordAdapter_Test(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := NewDiscordAdapter(srv.URL, "Bot", "", true)
	res, err := adapter.Test(context.Background())
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected test success, got: %s", res.Message)
	}
}
