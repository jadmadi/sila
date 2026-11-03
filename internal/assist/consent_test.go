package assist_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/config"
	"github.com/jadmadi/sila/internal/store"
)

type countingTransport struct {
	calls atomic.Int64
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(rec).Encode(map[string]any{
		"model":   "jev-test",
		"answers": map[string]any{},
		"usage":   map[string]any{"input_tokens": 10},
	})
	return rec.Result(), nil
}

func TestCountingTransportZeroCallsWhenDisabled(t *testing.T) {
	// Set up temporary environment with explicit opt-out
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	t.Setenv("SILA_ASSIST_PROVIDER", "typesafe")
	t.Setenv("SILA_ASSIST_ENABLED", "false")

	cfgPath := config.DefaultConfigPath()

	// Store an API key in the config file as well
	cfg := &config.Config{
		TypeSafeAPIKey: "file-key",
		AssistProvider: "typesafe",
		AssistEnabled:  false,
		AssistScopes:   []string{"recap"},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	counting := &countingTransport{}
	_ = counting

	// 1. All caller surfaces must return nil provider when assist is disabled
	surfaces := []struct {
		scope   string
		project string
	}{
		{"recap", "test-proj"},
		{"gate", "test-proj"},
		{"doctor", ""},
		{"live", "test-proj"},
		{"search", "test-proj"},
		{"verify", "test-proj"},
	}

	for _, s := range surfaces {
		provider := assist.ActiveProviderFor(s.scope, s.project)
		if provider != nil {
			t.Errorf("scope %q project %q: expected nil provider when AssistEnabled=false, got %s", s.scope, s.project, provider.Name())
		}
	}
}

func TestScopeIsolationWithCountingTransport(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("SILA_ASSIST_PROVIDER", "")
	t.Setenv("SILA_ASSIST_ENABLED", "")
	t.Setenv("SILA_ASSIST_SCOPES", "")
	t.Setenv("SILA_ASSIST_PROJECTS", "")

	cfgPath := config.DefaultConfigPath()

	// Only recap is consented for project "allowed-proj"
	cfg := &config.Config{
		TypeSafeAPIKey: "valid-test-key",
		AssistProvider: "typesafe",
		AssistEnabled:  true,
		AssistScopes:   []string{"recap"},
		AssistProjects: []string{"allowed-proj"},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Unconsented scopes must return nil
	unconsentedScopes := []string{"gate", "doctor", "live", "search", "verify"}
	for _, scope := range unconsentedScopes {
		p := assist.ActiveProviderFor(scope, "allowed-proj")
		if p != nil {
			t.Errorf("scope %q: expected nil provider because only 'recap' is consented, got %s", scope, p.Name())
		}
	}

	// Unconsented project must return nil even for consented scope
	pWrongProj := assist.ActiveProviderFor("recap", "other-proj")
	if pWrongProj != nil {
		t.Errorf("expected nil provider for project 'other-proj', got %s", pWrongProj.Name())
	}

	// Global/empty selector must return nil when projects are restricted
	pGlobal := assist.ActiveProviderFor("recap", "")
	if pGlobal != nil {
		t.Errorf("expected nil provider for empty project selector when allowlist is restricted, got %s", pGlobal.Name())
	}

	// Consented scope and project must return active provider
	pAllowed := assist.ActiveProviderFor("recap", "allowed-proj")
	if pAllowed == nil {
		t.Fatalf("expected non-nil provider for consented scope 'recap' on 'allowed-proj'")
	}

	// Attach counting transport to an adapter and verify callers making 0 calls on nil provider
	counter := &countingTransport{}
	httpClient := &http.Client{Transport: counter}
	adapter := assist.NewTypeSafeAdapter("test-key", httpClient)

	ctx := context.Background()

	// Calling sentry / verify on nil provider must produce 0 calls
	var nilProvider assist.Provider
	sentry, err := assist.AuditDiff(ctx, nilProvider, "+ diff")
	if err != nil || sentry != nil {
		t.Errorf("AuditDiff with nil provider returned non-nil sentry")
	}
	vRes, err := assist.VerifyFact(ctx, nilProvider, "claim", "file", "text")
	if err != nil || vRes != nil {
		t.Errorf("VerifyFact with nil provider returned non-nil result")
	}
	rRes, err := assist.Rerank(ctx, nilProvider, "query", []assist.RerankCandidate{{Key: "1", Excerpt: "text"}})
	if err != nil || rRes != nil {
		t.Errorf("Rerank with nil provider returned non-nil result")
	}
	acts := assist.InterpretActivity(ctx, nilProvider, []store.StoredMessage{{Content: "hello"}})
	if len(acts) != 0 {
		t.Errorf("InterpretActivity with nil provider returned non-empty insights")
	}

	// Assert exactly 0 calls made to transport
	if calls := counter.calls.Load(); calls != 0 {
		t.Errorf("expected 0 HTTP calls on disabled/nil surfaces, got %d", calls)
	}

	// When explicitly invoked through consented adapter, exactly 1 call occurs
	_, _ = adapter.Evaluate(ctx, "test", map[string]any{}, map[string]assist.Question{"q": {Type: assist.TypeNoul, Instructions: "hi"}})
	if calls := counter.calls.Load(); calls != 1 {
		t.Errorf("expected 1 HTTP call after explicit evaluation, got %d", calls)
	}
}

func TestEnvironmentOverrideDisablesAssistance(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)

	cfgPath := config.DefaultConfigPath()

	// Config file has assist_enabled: true
	cfg := &config.Config{
		TypeSafeAPIKey: "key",
		AssistProvider: "typesafe",
		AssistEnabled:  true,
		AssistScopes:   []string{"recap", "gate"},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Override via environment variable
	t.Setenv("SILA_ASSIST_ENABLED", "false")

	p := assist.ActiveProviderFor("recap", "")
	if p != nil {
		t.Fatalf("expected nil provider when SILA_ASSIST_ENABLED=false overrides config, got %s", p.Name())
	}
}
