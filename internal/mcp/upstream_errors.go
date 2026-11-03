package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"syscall"
)

// Upstream error codes returned in JSON-RPC data.code. They are stable so a
// client can branch without parsing the human message.
const (
	UpstreamDisabled      = "upstream_disabled"
	UpstreamUnconfigured  = "upstream_unconfigured"
	UpstreamUnreachable   = "upstream_unreachable"
	UpstreamTimeout       = "upstream_timeout"
	UpstreamToolErrorCode = "upstream_tool_error"
	UpstreamProtocolError = "upstream_protocol_error"
)

// UpstreamToolError is a JSON-RPC error returned by the upstream server
// itself. The transport prefix is preserved so existing messages do not change.
type UpstreamToolError struct {
	Prefix  string
	Code    int
	Message string
}

func (e *UpstreamToolError) Error() string {
	prefix := e.Prefix
	if prefix == "" {
		prefix = "upstream error"
	}
	return fmt.Sprintf("%s (%d): %s", prefix, e.Code, e.Message)
}

// UpstreamErrorState is the runtime snapshot attached to an upstream failure.
type UpstreamErrorState struct {
	Manager       string `json:"manager"`
	Upstream      string `json:"upstream,omitempty"`
	Configured    bool   `json:"configured"`
	ServerEnabled bool   `json:"server_enabled"`
	OnDemand      bool   `json:"on_demand"`
	Toggled       bool   `json:"toggled"`
	Running       bool   `json:"running"`
}

// Upstream is the configured server name, when one is known.
type UpstreamErrorData struct {
	Code         string             `json:"code"`
	Upstream     string             `json:"upstream,omitempty"`
	State        UpstreamErrorState `json:"state"`
	Message      string             `json:"message"`
	Remediation  string             `json:"remediation"`
	UpstreamCode int                `json:"upstream_code,omitempty"`
}

// Runnable reports whether dispatch can proceed for this snapshot.
func (s UpstreamErrorState) Runnable() bool {
	return s.Manager == "enabled" && s.Configured && s.ServerEnabled && (!s.OnDemand || s.Toggled)
}

// ToolRuntimeState returns the configured upstream and runtime state for a
// tool, including when the manager is disabled. Built-in management tools have
// an empty Upstream.
func (u *UpstreamManager) ToolRuntimeState(fullName string) (UpstreamErrorState, bool) {
	state := UpstreamErrorState{Manager: "disabled"}
	if u == nil {
		return state, false
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.enabled {
		state.Manager = "enabled"
	}
	reg, ok := u.tools[fullName]
	if !ok {
		return state, false
	}
	if reg.serverName != "" {
		state.Upstream = reg.serverName
		if srv, found := u.servers[reg.serverName]; found {
			state.Configured = true
			state.ServerEnabled = !srv.Disabled
			state.OnDemand = srv.OnDemand
			state.Toggled = u.toggled[reg.serverName]
		}
		if client, found := u.clients[reg.serverName]; found && client != nil {
			state.Running = client.IsRunning()
		}
	}
	return state, true
}

var upstreamURLQueryRE = regexp.MustCompile(`(?i)\b(https?://[^\s?#]+)\?[^\s]*`)

// sanitizeUpstreamMessage keeps the cause on one line and removes secrets,
// home paths, and URL query strings before it reaches a client.
func sanitizeUpstreamMessage(msg string) string {
	msg = RedactEgressString(msg)
	msg = upstreamURLQueryRE.ReplaceAllString(msg, "$1?[REDACTED]")
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return msg
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isUnreachableError(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection refused", "no such host", "network is unreachable",
		"connection reset", "broken pipe", "unexpected eof", "upstream connection closed",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ClassifyUpstreamFailure turns a transport/tool error plus the manager
// snapshot into an attributable failure. State problems win over the error text.
func ClassifyUpstreamFailure(tool string, err error, state UpstreamErrorState) UpstreamErrorData {
	if state.Manager == "" {
		state.Manager = "disabled"
	}
	data := UpstreamErrorData{Upstream: state.Upstream, State: state}
	switch {
	case state.Manager != "enabled":
		data.Code = UpstreamDisabled
		data.Message = "upstream manager is disabled"
		data.Remediation = "start the MCP server with `sila mcp --upstreams`"
		return data
	case !state.Configured:
		data.Code = UpstreamUnconfigured
		data.Message = "no configured upstream serves this tool"
		data.Remediation = "configure the upstream under `mcp_upstreams` and restart the MCP server"
		return data
	case !state.ServerEnabled:
		data.Code = UpstreamDisabled
		data.Message = fmt.Sprintf("upstream %q is disabled", state.Upstream)
		data.Remediation = fmt.Sprintf("remove `disabled` from %q or run `mcp_upstream_toggle` with server=%s enabled=true", state.Upstream, state.Upstream)
		return data
	case state.OnDemand && !state.Toggled:
		data.Code = UpstreamDisabled
		data.Message = fmt.Sprintf("upstream %q is on-demand and not toggled on", state.Upstream)
		data.Remediation = fmt.Sprintf("run `mcp_upstream_toggle` with server=%s enabled=true", state.Upstream)
		return data
	}

	var toolErr *UpstreamToolError
	switch {
	case errors.As(err, &toolErr):
		data.Code = UpstreamToolErrorCode
		data.UpstreamCode = toolErr.Code
		data.Message = sanitizeUpstreamMessage(toolErr.Message)
		data.Remediation = fmt.Sprintf("check the arguments for %s and inspect `mcp_upstream_status` for %q", tool, state.Upstream)
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || isTimeoutError(err)):
		data.Code = UpstreamTimeout
		data.Message = "upstream did not answer before the deadline"
		data.Remediation = fmt.Sprintf("retry with a narrower request and inspect `mcp_upstream_status` for %q", state.Upstream)
	case err != nil && isUnreachableError(err):
		data.Code = UpstreamUnreachable
		data.Message = sanitizeUpstreamMessage(err.Error())
		data.Remediation = fmt.Sprintf("start or restart %q, then run `mcp_upstream_status`", state.Upstream)
	default:
		data.Code = UpstreamProtocolError
		if err != nil {
			data.Message = sanitizeUpstreamMessage(err.Error())
		} else {
			data.Message = "upstream failed without a classified cause"
		}
		data.Remediation = fmt.Sprintf("run `mcp_upstream_status` and restart %q if the failure persists", state.Upstream)
	}
	return data
}

// FormatUpstreamErrorMessage is the one-line human message; the structured
// data carries the same fields for machine clients.
func FormatUpstreamErrorMessage(tool string, data UpstreamErrorData) string {
	target := tool
	if data.Upstream != "" {
		target = fmt.Sprintf("%s via %s", tool, data.Upstream)
	}
	return fmt.Sprintf("Upstream error (%s): %s — %s", target, data.Message, data.Remediation)
}
