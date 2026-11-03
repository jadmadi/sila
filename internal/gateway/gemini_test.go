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

func TestGeminiBridgeAdapter_SendNotification(t *testing.T) {
	var received geminiBridgePayload
	var authHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode gemini payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	adapter := NewGeminiBridgeAdapter(srv.URL, "secret-edge-token", true)
	if !adapter.Enabled() {
		t.Fatalf("expected adapter to be enabled")
	}

	n := Notification{
		Title:    "Spark Task for /home/dev/sila",
		Body:     "Task details with token=secret12345",
		Level:    "info",
		Project:  "sila",
		Category: "planning",
		Metadata: map[string]string{
			"Author": "gemini",
		},
		Timestamp: time.Now(),
	}

	err := adapter.SendNotification(context.Background(), n)
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	if authHeader != "Bearer secret-edge-token" {
		t.Errorf("expected Bearer secret-edge-token, got %s", authHeader)
	}
	if strings.Contains(received.Title, "/home/dev") {
		t.Errorf("title contains unredacted path: %s", received.Title)
	}
	if !strings.Contains(received.Title, "~/sila") {
		t.Errorf("expected title to contain ~/sila, got: %s", received.Title)
	}
	if strings.Contains(received.Body, "secret12345") {
		t.Errorf("body contains unredacted token: %s", received.Body)
	}
	if !strings.Contains(received.Body, "token=[REDACTED]") {
		t.Errorf("expected token=[REDACTED], got: %s", received.Body)
	}
}

func TestGeminiBridgeAdapter_Test(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"healthy"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	adapter := NewGeminiBridgeAdapter(srv.URL, "token", true)
	res, err := adapter.Test(context.Background())
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected test success, got: %s", res.Message)
	}
}
