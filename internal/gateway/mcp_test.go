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

func TestMCPGatewayAdapter_SendNotification(t *testing.T) {
	var received jsonrpcNotification
	var authHeader string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected application/json, got %s", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode mcp payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"ok"}`))
	}))
	defer srv.Close()

	adapter := NewMCPGatewayAdapter(srv.URL, "mcp-token-xyz", true)
	if !adapter.Enabled() {
		t.Fatalf("expected adapter to be enabled")
	}

	n := Notification{
		Title:     "Goal Completed in /home/alice/sila",
		Body:      "Goal was completed with " + "sk" + "-1234567890abcdef1234567890abcdef",
		Level:     "success",
		Project:   "sila",
		Category:  "goal",
		Timestamp: time.Now(),
	}

	err := adapter.SendNotification(context.Background(), n)
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}

	if authHeader != "Bearer mcp-token-xyz" {
		t.Errorf("expected Bearer mcp-token-xyz, got %s", authHeader)
	}
	if received.Method != "notifications/message" {
		t.Errorf("expected method notifications/message, got %s", received.Method)
	}

	paramsBytes, _ := json.Marshal(received.Params)
	paramsStr := string(paramsBytes)
	if strings.Contains(paramsStr, "/home/alice") {
		t.Errorf("params contains unredacted path: %s", paramsStr)
	}
	if strings.Contains(paramsStr, "sk"+"-1234567890") {
		t.Errorf("params contains unredacted api key: %s", paramsStr)
	}
	if !strings.Contains(paramsStr, "[REDACTED_API_KEY]") {
		t.Errorf("expected params to contain [REDACTED_API_KEY], got: %s", paramsStr)
	}
}

func TestMCPGatewayAdapter_Test(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":"pong","id":1}`))
	}))
	defer srv.Close()

	adapter := NewMCPGatewayAdapter(srv.URL, "token", true)
	res, err := adapter.Test(context.Background())
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if !res.Success {
		t.Errorf("expected test success, got: %s", res.Message)
	}
}
