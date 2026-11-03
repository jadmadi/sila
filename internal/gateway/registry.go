package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jadmadi/sila/internal/config"
)

// Registry manages the set of registered external gateway adapters.
type Registry struct {
	mu       sync.RWMutex
	gateways map[Surface]Gateway
}

// NewRegistry initializes an empty or pre-populated gateway registry.
func NewRegistry(adapters ...Gateway) *Registry {
	r := &Registry{
		gateways: make(map[Surface]Gateway),
	}
	for _, a := range adapters {
		if a != nil {
			r.gateways[a.Surface()] = a
		}
	}
	return r
}

// NewFromConfig builds a Registry populated from persistent user configuration.
func NewFromConfig(cfg *config.Config) *Registry {
	r := NewRegistry()
	if cfg == nil {
		return r
	}

	masterEnabled := cfg.GatewayEnabled

	// 1. Discord
	discordEnabled := masterEnabled && cfg.GatewayDiscord.Enabled && cfg.GatewayDiscord.WebhookURL != ""
	r.Register(NewDiscordAdapter(
		cfg.GatewayDiscord.WebhookURL,
		cfg.GatewayDiscord.Username,
		cfg.GatewayDiscord.AvatarURL,
		discordEnabled,
	))

	// 2. Telegram
	telegramEnabled := masterEnabled && cfg.GatewayTelegram.Enabled && cfg.GatewayTelegram.BotToken != "" && cfg.GatewayTelegram.ChatID != ""
	r.Register(NewTelegramAdapter(
		cfg.GatewayTelegram.BotToken,
		cfg.GatewayTelegram.ChatID,
		telegramEnabled,
	))

	// 3. Gemini Bridge
	geminiEnabled := masterEnabled && cfg.GatewayGemini.Enabled && cfg.GatewayGemini.Endpoint != ""
	r.Register(NewGeminiBridgeAdapter(
		cfg.GatewayGemini.Endpoint,
		cfg.GatewayGemini.AuthToken,
		geminiEnabled,
	))

	// 4. Remote MCP
	mcpEnabled := masterEnabled && cfg.GatewayMCP.Enabled && cfg.GatewayMCP.Endpoint != ""
	r.Register(NewMCPGatewayAdapter(
		cfg.GatewayMCP.Endpoint,
		cfg.GatewayMCP.AuthToken,
		mcpEnabled,
	))

	return r
}

// Register adds or updates a gateway adapter in the registry.
func (r *Registry) Register(g Gateway) {
	if g == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gateways[g.Surface()] = g
}

// Get returns the adapter for the requested surface.
func (r *Registry) Get(surface Surface) (Gateway, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.gateways[surface]
	return g, ok
}

// All returns all registered gateway adapters in stable order.
func (r *Registry) All() []Gateway {
	r.mu.RLock()
	defer r.mu.RUnlock()
	surfaces := []Surface{SurfaceDiscord, SurfaceTelegram, SurfaceGemini, SurfaceMCP}
	var res []Gateway
	for _, s := range surfaces {
		if g, ok := r.gateways[s]; ok {
			res = append(res, g)
		}
	}
	return res
}

// Enabled returns all actively enabled gateway adapters.
func (r *Registry) Enabled() []Gateway {
	var res []Gateway
	for _, g := range r.All() {
		if g.Enabled() {
			res = append(res, g)
		}
	}
	return res
}

// SendNotification dispatches a notification across all enabled gateway adapters.
func (r *Registry) SendNotification(ctx context.Context, n Notification) []DeliveryResult {
	enabled := r.Enabled()
	if len(enabled) == 0 {
		return nil
	}

	results := make([]DeliveryResult, len(enabled))
	var wg sync.WaitGroup

	for i, g := range enabled {
		wg.Add(1)
		go func(idx int, gw Gateway) {
			defer wg.Done()
			err := gw.SendNotification(ctx, n)
			res := DeliveryResult{
				Surface:   gw.Surface(),
				Timestamp: time.Now(),
			}
			if err != nil {
				res.Success = false
				res.Message = err.Error()
			} else {
				res.Success = true
				res.Message = "delivered"
			}
			results[idx] = res
		}(i, g)
	}

	wg.Wait()
	return results
}

// Broadcast is an alias for SendNotification to dispatch notifications across all enabled gateway adapters.
func (r *Registry) Broadcast(ctx context.Context, n Notification) []DeliveryResult {
	return r.SendNotification(ctx, n)
}

// TestAll runs connectivity probes on all registered gateways.
func (r *Registry) TestAll(ctx context.Context) []TestResult {
	all := r.All()
	results := make([]TestResult, len(all))
	var wg sync.WaitGroup

	for i, g := range all {
		wg.Add(1)
		go func(idx int, gw Gateway) {
			defer wg.Done()
			res, err := gw.Test(ctx)
			if res != nil {
				results[idx] = *res
			} else if err != nil {
				results[idx] = TestResult{
					Surface: gw.Surface(),
					Success: false,
					Message: err.Error(),
				}
			} else {
				results[idx] = TestResult{
					Surface: gw.Surface(),
					Success: false,
					Message: "unknown status",
				}
			}
		}(i, g)
	}

	wg.Wait()
	return results
}

// TestSurface tests a single named gateway surface.
func (r *Registry) TestSurface(ctx context.Context, surface string) (*TestResult, error) {
	norm := Surface(strings.ToLower(strings.TrimSpace(surface)))
	g, ok := r.Get(norm)
	if !ok {
		return nil, fmt.Errorf("unknown gateway surface: %q (supported: discord, telegram, gemini, mcp)", surface)
	}
	return g.Test(ctx)
}
