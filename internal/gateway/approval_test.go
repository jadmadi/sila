package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/gateway"
	"github.com/jadmadi/sila/internal/store"
)

func setupTestStore(t *testing.T) *store.Store {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_approvals.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestApprovalCoordinator_Approve(t *testing.T) {
	st := setupTestStore(t)
	coord := gateway.NewApprovalCoordinator(st, nil)

	ctx := context.Background()
	req := gateway.ApprovalRequest{
		Project:        "project-x",
		Agent:          "antigravity",
		Prompt:         "Drop legacy table user_sessions",
		RiskLevel:      "high",
		TimeoutSeconds: 5,
	}

	errCh := make(chan error, 1)
	resCh := make(chan *gateway.ApprovalResponse, 1)

	go func() {
		res, err := coord.RequestApproval(ctx, req)
		if err != nil {
			errCh <- err
			return
		}
		resCh <- res
	}()

	// Wait briefly for request to register in store
	var pending []store.Approval
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		pending, _ = coord.ListPending("project-x")
		if len(pending) > 0 {
			break
		}
	}
	if len(pending) == 0 {
		t.Fatalf("expected pending approval in store")
	}

	apprID := pending[0].ID

	// Resolve: approve
	decRes, err := coord.Resolve(apprID, true, "cli:jad", "Verified schema migration")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !decRes.Approved || decRes.Status != "approved" {
		t.Errorf("expected approved status, got %+v", decRes)
	}

	select {
	case err := <-errCh:
		t.Fatalf("RequestApproval returned error: %v", err)
	case res := <-resCh:
		if !res.Approved {
			t.Errorf("expected Approved=true, got false")
		}
		if res.Status != "approved" {
			t.Errorf("expected Status=approved, got %q", res.Status)
		}
		if res.DecidedBy != "cli:jad" {
			t.Errorf("expected DecidedBy=cli:jad, got %q", res.DecidedBy)
		}
		if res.Reason != "Verified schema migration" {
			t.Errorf("expected Reason='Verified schema migration', got %q", res.Reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RequestApproval to return")
	}
}

func TestApprovalCoordinator_Reject(t *testing.T) {
	st := setupTestStore(t)
	coord := gateway.NewApprovalCoordinator(st, nil)

	ctx := context.Background()
	req := gateway.ApprovalRequest{
		Project:        "project-x",
		Agent:          "devin",
		Prompt:         "Deploy untested changes to production",
		RiskLevel:      "critical",
		TimeoutSeconds: 5,
	}

	resCh := make(chan *gateway.ApprovalResponse, 1)
	go func() {
		res, _ := coord.RequestApproval(ctx, req)
		resCh <- res
	}()

	var pending []store.Approval
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		pending, _ = coord.ListPending("project-x")
		if len(pending) > 0 {
			break
		}
	}
	if len(pending) == 0 {
		t.Fatalf("expected pending approval")
	}

	apprID := pending[0].ID

	// Resolve: reject
	_, err := coord.Resolve(apprID, false, "telegram:@jadmadi", "Tests must pass first")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	select {
	case res := <-resCh:
		if res.Approved {
			t.Errorf("expected Approved=false, got true")
		}
		if res.Status != "rejected" {
			t.Errorf("expected Status=rejected, got %q", res.Status)
		}
		if res.DecidedBy != "telegram:@jadmadi" {
			t.Errorf("expected DecidedBy=telegram:@jadmadi, got %q", res.DecidedBy)
		}
		if res.Reason != "Tests must pass first" {
			t.Errorf("expected Reason='Tests must pass first', got %q", res.Reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RequestApproval to return")
	}
}

func TestApprovalCoordinator_Timeout(t *testing.T) {
	st := setupTestStore(t)
	coord := gateway.NewApprovalCoordinator(st, nil)

	ctx := context.Background()
	req := gateway.ApprovalRequest{
		Project:        "project-x",
		Agent:          "antigravity",
		Prompt:         "Quick operation",
		TimeoutSeconds: 1, // 1 second timeout
	}

	start := time.Now()
	res, err := coord.RequestApproval(ctx, req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Approved {
		t.Errorf("expected Approved=false on timeout, got true")
	}
	if res.Status != "timed_out" {
		t.Errorf("expected Status=timed_out, got %q", res.Status)
	}
	if elapsed < 900*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Errorf("unexpected elapsed duration: %v", elapsed)
	}
}

func TestApprovalCoordinator_ContextCancel(t *testing.T) {
	st := setupTestStore(t)
	coord := gateway.NewApprovalCoordinator(st, nil)

	ctx, cancel := context.WithCancel(context.Background())
	req := gateway.ApprovalRequest{
		Project:        "project-x",
		Agent:          "antigravity",
		Prompt:         "Cancelable operation",
		TimeoutSeconds: 10,
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	res, err := coord.RequestApproval(ctx, req)
	if err == nil {
		t.Errorf("expected context cancellation error, got nil")
	}
	if res.Approved {
		t.Errorf("expected Approved=false on cancellation")
	}
	if res.Status != "cancelled" {
		t.Errorf("expected Status=cancelled, got %q", res.Status)
	}
}

func TestApprovalCoordinator_SecretRedaction(t *testing.T) {
	st := setupTestStore(t)
	coord := gateway.NewApprovalCoordinator(st, nil)

	ctx := context.Background()
	req := gateway.ApprovalRequest{
		Project: "project-x",
		Agent:   "antigravity",
		Prompt:  "Update config with token sk-abcdef1234567890abcdef12 and secret ghp_1234567890abcdef1234567890abcdef1234",
		Metadata: map[string]string{
			"raw_key": "sk-1234567890abcdef1234567890abcdef",
		},
		TimeoutSeconds: 1,
	}

	res, _ := coord.RequestApproval(ctx, req)
	saved, err := st.GetApproval(res.ID)
	if err != nil {
		t.Fatalf("failed to get saved approval: %v", err)
	}

	if strings.Contains(saved.Prompt, "sk-abcdef") || strings.Contains(saved.Prompt, "ghp_12345") {
		t.Errorf("SECURITY INVARIANT VIOLATION: raw credentials present in saved prompt: %s", saved.Prompt)
	}
	if !strings.Contains(saved.Prompt, "[REDACTED_") {
		t.Errorf("expected [REDACTED_...] in prompt: %s", saved.Prompt)
	}
	if strings.Contains(saved.Metadata["raw_key"], "sk-12345") {
		t.Errorf("SECURITY INVARIANT VIOLATION: raw secret in metadata: %s", saved.Metadata["raw_key"])
	}
}

func TestTelegram_ApprovalCallbackQuery(t *testing.T) {
	st := setupTestStore(t)
	coord := gateway.NewApprovalCoordinator(st, nil)
	gateway.SetDefaultCoordinator(coord)

	var answeredCallback atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "answerCallbackQuery") {
			answeredCallback.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	tg := gateway.NewTelegramAdapter("123:TOKEN", "chat123", true)
	tg.BaseURL = server.URL

	ctx := context.Background()
	req := gateway.ApprovalRequest{
		Project:        "project-tg",
		Agent:          "antigravity",
		Prompt:         "Deploy v2.0",
		TimeoutSeconds: 5,
	}

	resCh := make(chan *gateway.ApprovalResponse, 1)
	go func() {
		res, _ := coord.RequestApproval(ctx, req)
		resCh <- res
	}()

	var pending []store.Approval
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		pending, _ = coord.ListPending("project-tg")
		if len(pending) > 0 {
			break
		}
	}
	if len(pending) == 0 {
		t.Fatalf("expected pending approval")
	}

	apprID := pending[0].ID

	// Simulate Telegram incoming update with callback query
	updateJSON, _ := json.Marshal(gateway.TelegramUpdate{
		UpdateID: 101,
		CallbackQuery: &gateway.TelegramCallbackQuery{
			ID:   "query_123",
			From: gateway.TelegramUser{ID: 999, Username: "jadmadi"},
			Data: "appr:" + apprID + ":approve",
		},
	})

	inMsg := gateway.InboundMessage{
		Surface: gateway.SurfaceTelegram,
		Content: string(updateJSON),
	}

	if err := tg.HandleInbound(ctx, inMsg); err != nil {
		t.Fatalf("HandleInbound failed: %v", err)
	}

	select {
	case res := <-resCh:
		if !res.Approved {
			t.Errorf("expected Approved=true, got false")
		}
		if res.DecidedBy != "telegram:@jadmadi" {
			t.Errorf("expected DecidedBy='telegram:@jadmadi', got %q", res.DecidedBy)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RequestApproval to return after callback query")
	}

	if !answeredCallback.Load() {
		t.Errorf("expected answerCallbackQuery to be called")
	}
}

func TestDiscord_ApprovalCommand(t *testing.T) {
	st := setupTestStore(t)
	coord := gateway.NewApprovalCoordinator(st, nil)
	gateway.SetDefaultCoordinator(coord)

	discord := gateway.NewDiscordAdapter("https://discord.com/api/webhooks/test", "SilaBot", "", true)

	ctx := context.Background()
	req := gateway.ApprovalRequest{
		Project:        "project-dc",
		Agent:          "antigravity",
		Prompt:         "Purge cache",
		TimeoutSeconds: 5,
	}

	resCh := make(chan *gateway.ApprovalResponse, 1)
	go func() {
		res, _ := coord.RequestApproval(ctx, req)
		resCh <- res
	}()

	var pending []store.Approval
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		pending, _ = coord.ListPending("project-dc")
		if len(pending) > 0 {
			break
		}
	}
	if len(pending) == 0 {
		t.Fatalf("expected pending approval")
	}

	apprID := pending[0].ID

	// Simulate Discord bot command message
	inMsg := gateway.InboundMessage{
		Surface: gateway.SurfaceDiscord,
		Sender:  "discord:@admin",
		Content: "/approve " + apprID + " Approved by ops",
	}

	if err := discord.HandleInbound(ctx, inMsg); err != nil {
		t.Fatalf("HandleInbound failed: %v", err)
	}

	select {
	case res := <-resCh:
		if !res.Approved {
			t.Errorf("expected Approved=true, got false")
		}
		if res.DecidedBy != "discord:@admin" {
			t.Errorf("expected DecidedBy='discord:@admin', got %q", res.DecidedBy)
		}
		if res.Reason != "Approved by ops" {
			t.Errorf("expected Reason='Approved by ops', got %q", res.Reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for RequestApproval to return after Discord command")
	}
}
