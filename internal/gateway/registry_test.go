package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/config"
)

func TestRegistry_FromConfig(t *testing.T) {
	cfg := &config.Config{
		GatewayEnabled: true,
		GatewayDiscord: config.DiscordConfig{
			Enabled:    true,
			WebhookURL: "https://discord.com/api/webhooks/123/abc",
		},
		GatewayTelegram: config.TelegramConfig{
			Enabled:  true,
			BotToken: "tok123",
			ChatID:   "chat456",
		},
	}

	reg := NewFromConfig(cfg)
	all := reg.All()
	if len(all) != 4 {
		t.Errorf("expected 4 registered gateways, got %d", len(all))
	}

	enabled := reg.Enabled()
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled gateways, got %d", len(enabled))
	}

	disc, ok := reg.Get(SurfaceDiscord)
	if !ok || !disc.Enabled() {
		t.Errorf("expected enabled discord gateway")
	}

	tele, ok := reg.Get(SurfaceTelegram)
	if !ok || !tele.Enabled() {
		t.Errorf("expected enabled telegram gateway")
	}

	gem, ok := reg.Get(SurfaceGemini)
	if !ok || gem.Enabled() {
		t.Errorf("expected disabled gemini gateway")
	}
}

func TestRegistry_SendNotificationAndTest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	d := NewDiscordAdapter(srv.URL, "Bot", "", true)
	m := NewMCPGatewayAdapter(srv.URL, "tok", true)

	reg := NewRegistry(d, m)
	results := reg.SendNotification(context.Background(), Notification{
		Title:     "Deployment",
		Body:      "Dispatched across surfaces",
		Level:     "success",
		Timestamp: time.Now(),
	})

	if len(results) != 2 {
		t.Fatalf("expected 2 delivery results, got %d", len(results))
	}
	for _, res := range results {
		if !res.Success {
			t.Errorf("expected delivery success for %s, got: %s", res.Surface, res.Message)
		}
	}

	testResults := reg.TestAll(context.Background())
	if len(testResults) != 2 {
		t.Fatalf("expected 2 test results, got %d", len(testResults))
	}
	for _, tr := range testResults {
		if !tr.Success {
			t.Errorf("expected test success for %s, got: %s", tr.Surface, tr.Message)
		}
	}
}
