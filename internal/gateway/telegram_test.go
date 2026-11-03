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

func TestTelegramAdapter_SendNotification(t *testing.T) {
	var received telegramSendMessagePayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/botmytoken/sendMessage") {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode telegram payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	adapter := NewTelegramAdapter("mytoken", "12345678", true)
	adapter.BaseURL = srv.URL

	if !adapter.Enabled() {
		t.Fatalf("expected adapter to be enabled")
	}

	n := Notification{
		Title:     "Alert in /home/user/app",
		Body:      "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.t-IDc was leaked",
		Level:     "error",
		Project:   "sila",
		Category:  "security",
		Timestamp: time.Now(),
	}

	err := adapter.SendNotification(context.Background(), n)
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	if received.ChatID != "12345678" {
		t.Errorf("expected chat_id 12345678, got %s", received.ChatID)
	}
	if strings.Contains(received.Text, "/home/user") {
		t.Errorf("text contains unredacted home path: %s", received.Text)
	}
	if strings.Contains(received.Text, "eyJhbGciOi") {
		t.Errorf("text contains unredacted token: %s", received.Text)
	}
	if !strings.Contains(received.Text, "Bearer [REDACTED]") {
		t.Errorf("expected Bearer [REDACTED], got: %s", received.Text)
	}
}

func TestTelegramAdapter_Test(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/botmytoken/getMe") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":123,"is_bot":true,"first_name":"Sila"}}`))
	}))
	defer srv.Close()

	adapter := NewTelegramAdapter("mytoken", "12345", true)
	adapter.BaseURL = srv.URL

	res, err := adapter.Test(context.Background())
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected test success, got: %s", res.Message)
	}
}
