package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/jadmadi/sila/internal/config"
)

func TestClassifyUpstreamFailure(t *testing.T) {
	base := UpstreamErrorState{
		Manager: "enabled", Upstream: "cf", Configured: true, ServerEnabled: true,
	}
	cases := []struct {
		name     string
		err      error
		state    UpstreamErrorState
		wantCode string
	}{
		{"manager disabled", nil, UpstreamErrorState{Upstream: "cf"}, UpstreamDisabled},
		{"unconfigured", nil, UpstreamErrorState{Manager: "enabled", Upstream: "cf"}, UpstreamUnconfigured},
		{"server disabled", nil, UpstreamErrorState{Manager: "enabled", Upstream: "cf", Configured: true}, UpstreamDisabled},
		{"on demand", nil, UpstreamErrorState{Manager: "enabled", Upstream: "cf", Configured: true, ServerEnabled: true, OnDemand: true}, UpstreamDisabled},
		{"timeout", context.DeadlineExceeded, base, UpstreamTimeout},
		{"unreachable", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), base, UpstreamUnreachable},
		{"tool error", &UpstreamToolError{Code: -32602, Message: "bad arguments"}, base, UpstreamToolErrorCode},
		{"protocol", errors.New("unexpected frame"), base, UpstreamProtocolError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := ClassifyUpstreamFailure("cf_search", tc.err, tc.state)
			if data.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", data.Code, tc.wantCode)
			}
			if data.Remediation == "" {
				t.Fatal("missing remediation")
			}
			if data.State.Manager == "" {
				t.Fatal("missing state snapshot")
			}
			if tc.wantCode == UpstreamToolErrorCode && data.UpstreamCode != -32602 {
				t.Fatalf("upstream code = %d, want -32602", data.UpstreamCode)
			}
		})
	}
}

func TestUpstreamErrorMessageRedactsSecrets(t *testing.T) {
	state := UpstreamErrorState{Manager: "enabled", Upstream: "cf", Configured: true, ServerEnabled: true}
	err := fmt.Errorf("request to https://api.example.com/v1?token=supersecret failed for /home/alice/secret with api_key=abc123")
	data := ClassifyUpstreamFailure("cf_search", err, state)
	msg := FormatUpstreamErrorMessage("cf_search", data)
	for _, leak := range []string{"supersecret", "abc123", "/home/alice"} {
		if strings.Contains(msg, leak) || strings.Contains(data.Message, leak) {
			t.Fatalf("secret %q leaked: msg=%q data=%q", leak, msg, data.Message)
		}
	}
	if strings.ContainsAny(data.Message, "\n\r\t") {
		t.Fatalf("message is not one line: %q", data.Message)
	}
	if !strings.Contains(msg, "cf_search via cf") {
		t.Fatalf("message does not attribute the upstream: %q", msg)
	}
}

func TestToolRuntimeStateDisabledAndOnDemand(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: false,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"test": {Command: "true", Prefix: "t"},
		},
	}
	mgr := NewUpstreamManager(nil, cfg)
	mgr.RegisterServerTools("test", []Tool{{Name: "t_ping"}})

	state, ok := mgr.ToolRuntimeState("t_ping")
	if !ok || state.Upstream != "test" || state.Runnable() {
		t.Fatalf("disabled state = %+v ok=%v", state, ok)
	}
	if data := ClassifyUpstreamFailure("t_ping", nil, state); data.Code != UpstreamDisabled {
		t.Fatalf("disabled code = %q", data.Code)
	}

	mgr.SetEnabled(true)
	state, _ = mgr.ToolRuntimeState("t_ping")
	if !state.Runnable() || state.Manager != "enabled" {
		t.Fatalf("enabled state = %+v", state)
	}

	cfg.MCPUpstreams["test"] = config.UpstreamMCPServer{Command: "true", Prefix: "t", OnDemand: true}
	mgr = NewUpstreamManager(nil, cfg)
	mgr.SetEnabled(true)
	mgr.RegisterServerTools("test", []Tool{{Name: "t_ping"}})
	state, _ = mgr.ToolRuntimeState("t_ping")
	if state.Runnable() {
		t.Fatalf("on-demand state should not be runnable: %+v", state)
	}
	mgr.mu.Lock()
	mgr.toggled["test"] = true
	mgr.mu.Unlock()
	state, _ = mgr.ToolRuntimeState("t_ping")
	if !state.Runnable() {
		t.Fatalf("toggled state should be runnable: %+v", state)
	}
}

func TestUpstreamErrorDataJSONShape(t *testing.T) {
	state := UpstreamErrorState{Manager: "enabled", Upstream: "cf", Configured: true, ServerEnabled: true}
	data := ClassifyUpstreamFailure("cf_search", &UpstreamToolError{Code: -32602, Message: "bad arguments"}, state)
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"code":"upstream_tool_error"`, `"upstream":"cf"`, `"state":`, `"remediation":`, `"upstream_code":-32602`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("missing %s in %s", key, raw)
		}
	}
}

func TestUpstreamToolErrorPreservesTransportMessages(t *testing.T) {
	if got := (&UpstreamToolError{Prefix: "remote error", Code: -32602, Message: "bad"}).Error(); got != "remote error (-32602): bad" {
		t.Fatalf("remote message = %q", got)
	}
	if got := (&UpstreamToolError{Code: -32603, Message: "boom"}).Error(); got != "upstream error (-32603): boom" {
		t.Fatalf("stdio message = %q", got)
	}
}

type fakeUpstreamClient struct {
	err error
}

func (f *fakeUpstreamClient) Name() string   { return "test" }
func (f *fakeUpstreamClient) Prefix() string { return "t" }
func (f *fakeUpstreamClient) CallTool(context.Context, string, map[string]any) (any, error) {
	return nil, f.err
}
func (f *fakeUpstreamClient) Close() error    { return nil }
func (f *fakeUpstreamClient) IsRunning() bool { return true }

func TestFakeUpstreamClassification(t *testing.T) {
	cfg := &config.Config{
		MCPUpstreamsEnabled: true,
		MCPUpstreams: map[string]config.UpstreamMCPServer{
			"test": {Command: "true", Prefix: "t"},
		},
	}
	mgr := NewUpstreamManager(nil, cfg)
	mgr.RegisterServerTools("test", []Tool{{Name: "t_ping"}})

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"timeout", context.DeadlineExceeded, UpstreamTimeout},
		{"refused", fmt.Errorf("dial tcp: %w", syscall.ECONNREFUSED), UpstreamUnreachable},
		{"tool error", &UpstreamToolError{Code: -32602, Message: "bad arguments"}, UpstreamToolErrorCode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr.SetClient("test", &fakeUpstreamClient{err: tc.err})
			_, err := mgr.CallTool(context.Background(), "t_ping", nil, "")
			if err == nil {
				t.Fatal("expected an upstream error")
			}
			state, _ := mgr.ToolRuntimeState("t_ping")
			if !state.Running {
				t.Fatalf("state should be running: %+v", state)
			}
			data := ClassifyUpstreamFailure("t_ping", err, state)
			if data.Code != tc.want {
				t.Fatalf("code = %q, want %q (err=%v)", data.Code, tc.want, err)
			}
		})
	}
}
