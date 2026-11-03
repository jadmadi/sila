package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// HTTPUpstreamClient connects to a remote MCP server via HTTP POST JSON-RPC 2.0 with session negotiation and SSE support.
type HTTPUpstreamClient struct {
	mu          sync.RWMutex
	name        string
	prefix      string
	serverURL   string
	headers     map[string]string
	authToken   string
	autoIngest  bool
	client      *http.Client
	maxBodySize int64

	initMu       sync.Mutex
	initialized  bool
	initEpoch    uint64
	sessionID    string
	protoVersion string
	serverCaps   map[string]any
	reqID        atomic.Int64
}

// NewHTTPUpstreamClient creates a new HTTPUpstreamClient instance.
func NewHTTPUpstreamClient(name, prefix, serverURL string, headers map[string]string, authToken string, autoIngest bool) *HTTPUpstreamClient {
	var copiedHeaders map[string]string
	if headers != nil {
		copiedHeaders = make(map[string]string, len(headers))
		for k, v := range headers {
			copiedHeaders[k] = v
		}
	}
	return &HTTPUpstreamClient{
		name:        name,
		prefix:      prefix,
		serverURL:   serverURL,
		headers:     copiedHeaders,
		authToken:   authToken,
		autoIngest:  autoIngest,
		client:      &http.Client{Timeout: 30 * time.Second},
		maxBodySize: 10 * 1024 * 1024,
	}
}

func (h *HTTPUpstreamClient) Name() string    { return h.name }
func (h *HTTPUpstreamClient) Prefix() string  { return h.prefix }
func (h *HTTPUpstreamClient) IsRunning() bool { return true }

// Close resets negotiated session state and marks the client uninitialized.
func (h *HTTPUpstreamClient) Close() error {
	h.initMu.Lock()
	defer h.initMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.initialized = false
	h.sessionID = ""
	return nil
}

// UpdateCredentials updates authentication tokens and transport headers on an active HTTP client,
// invalidating any existing session to ensure re-initialization with new credentials.
func (h *HTTPUpstreamClient) UpdateCredentials(authToken string, headers map[string]string) {
	h.initMu.Lock()
	defer h.initMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.authToken = authToken
	if headers != nil {
		h.headers = make(map[string]string, len(headers))
		for k, v := range headers {
			h.headers[k] = v
		}
	} else {
		h.headers = nil
	}
	h.initialized = false
	h.sessionID = ""
	h.initEpoch++
}

// GetCredentials returns the current auth token and a copy of the transport headers.
func (h *HTTPUpstreamClient) GetCredentials() (string, map[string]string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	var headersCopy map[string]string
	if h.headers != nil {
		headersCopy = make(map[string]string, len(h.headers))
		for k, v := range h.headers {
			headersCopy[k] = v
		}
	}
	return h.authToken, headersCopy
}

// SetHTTPClient sets the underlying http.Client (useful for recording test fixtures).
func (h *HTTPUpstreamClient) SetHTTPClient(client *http.Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.client = client
}

// SessionID returns the current negotiated session ID, if any.
func (h *HTTPUpstreamClient) SessionID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessionID
}

// ProtocolVersion returns the negotiated MCP protocol version.
func (h *HTTPUpstreamClient) ProtocolVersion() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.protoVersion
}

// ServerCapabilities returns a copy of the server capabilities reported during initialize.
func (h *HTTPUpstreamClient) ServerCapabilities() map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.serverCaps == nil {
		return nil
	}
	res := make(map[string]any, len(h.serverCaps))
	for k, v := range h.serverCaps {
		res[k] = v
	}
	return res
}

// IsInitialized reports whether the initialize handshake has successfully completed.
func (h *HTTPUpstreamClient) IsInitialized() bool {
	h.initMu.Lock()
	defer h.initMu.Unlock()
	return h.initialized
}

// SetMaxBodySize configures the maximum response body/frame limit in bytes.
func (h *HTTPUpstreamClient) SetMaxBodySize(size int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.maxBodySize = size
}

type sessionExpiredError struct {
	msg string
}

func (e *sessionExpiredError) Error() string {
	return e.msg
}

func (h *HTTPUpstreamClient) isSessionExpiry(statusCode int, body string, rpcErr *JSONRPCError) bool {
	h.mu.RLock()
	hasSession := h.sessionID != ""
	h.mu.RUnlock()

	if statusCode == http.StatusNotFound && hasSession {
		return true
	}
	bodyLower := strings.ToLower(body)
	if strings.Contains(bodyLower, "session expired") ||
		strings.Contains(bodyLower, "session not found") ||
		strings.Contains(bodyLower, "invalid session") ||
		strings.Contains(bodyLower, "unknown session") ||
		strings.Contains(bodyLower, "missing session") {
		return true
	}
	if rpcErr != nil {
		msgLower := strings.ToLower(rpcErr.Message)
		if strings.Contains(msgLower, "session expired") ||
			strings.Contains(msgLower, "session not found") ||
			strings.Contains(msgLower, "invalid session") ||
			strings.Contains(msgLower, "unknown session") ||
			strings.Contains(msgLower, "missing session") {
			return true
		}
	}
	return false
}

func (h *HTTPUpstreamClient) ensureInitialized(ctx context.Context) error {
	h.initMu.Lock()
	defer h.initMu.Unlock()
	if h.initialized {
		return nil
	}
	return h.initializeLocked(ctx)
}

func (h *HTTPUpstreamClient) reinitialize(ctx context.Context, failedEpoch uint64) error {
	h.initMu.Lock()
	defer h.initMu.Unlock()
	if h.initEpoch != failedEpoch {
		// Another goroutine has already re-initialized
		return nil
	}
	h.initialized = false
	h.mu.Lock()
	h.sessionID = ""
	h.mu.Unlock()
	h.initEpoch++
	return h.initializeLocked(ctx)
}

func (h *HTTPUpstreamClient) initializeLocked(ctx context.Context) error {
	reqID := h.reqID.Add(1)

	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "sila-gateway",
			"version": "v0.9.0",
		},
	}

	reqPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "initialize",
		"params":  initParams,
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return RedactEgressError(fmt.Errorf("failed to marshal initialize request: %w", err))
	}

	h.mu.RLock()
	serverURL := h.serverURL
	authToken := h.authToken
	sessID := h.sessionID
	headers := make(map[string]string, len(h.headers))
	for k, v := range h.headers {
		headers[k] = v
	}
	httpClient := h.client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	h.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return RedactEgressError(fmt.Errorf("failed to create initialize request: %w", err))
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if sessID != "" {
		req.Header.Set("Mcp-Session-Id", sessID)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return RedactEgressError(fmt.Errorf("initialize request to %s failed: %w", RedactEgressString(serverURL), err))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("remote MCP server %q returned 401 Unauthorized during initialize. Set API token or credentials for %s", h.name, RedactEgressString(serverURL))
	}

	if sessHeader := resp.Header.Get("Mcp-Session-Id"); sessHeader != "" {
		h.mu.Lock()
		h.sessionID = sessHeader
		h.mu.Unlock()
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		cleanBody := RedactEgressString(string(respBody))
		return fmt.Errorf("remote MCP server returned HTTP %d during initialize: %s", resp.StatusCode, cleanBody)
	}

	result, err := h.decodeResponse(ctx, resp, reqID)
	if err != nil {
		return RedactEgressError(fmt.Errorf("initialize response error: %w", err))
	}

	resMap, ok := result.(map[string]any)
	if !ok {
		return errors.New("remote MCP server initialize response result must be a JSON object")
	}

	proto, ok := resMap["protocolVersion"].(string)
	if !ok || strings.TrimSpace(proto) == "" {
		return errors.New("remote MCP server initialize response missing required protocolVersion")
	}

	h.mu.Lock()
	h.protoVersion = proto
	if caps, ok := resMap["capabilities"].(map[string]any); ok {
		h.serverCaps = caps
	}
	if h.sessionID == "" {
		if sID, ok := resMap["sessionId"].(string); ok && sID != "" {
			h.sessionID = sID
		}
	}
	currentSessionID := h.sessionID
	h.mu.Unlock()

	// Send notifications/initialized notification
	notifPayload := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	}
	notifBytes, err := json.Marshal(notifPayload)
	if err != nil {
		return RedactEgressError(fmt.Errorf("failed to marshal initialized notification: %w", err))
	}

	notifReq, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(notifBytes))
	if err != nil {
		return RedactEgressError(fmt.Errorf("failed to create initialized notification request: %w", err))
	}
	notifReq.Header.Set("Content-Type", "application/json")
	notifReq.Header.Set("Accept", "application/json, text/event-stream")
	if authToken != "" {
		notifReq.Header.Set("Authorization", "Bearer "+authToken)
	}
	for k, v := range headers {
		notifReq.Header.Set(k, v)
	}
	if currentSessionID != "" {
		notifReq.Header.Set("Mcp-Session-Id", currentSessionID)
	}

	notifResp, err := httpClient.Do(notifReq)
	if err != nil {
		return RedactEgressError(fmt.Errorf("initialized notification to %s failed: %w", RedactEgressString(serverURL), err))
	}
	if notifSess := notifResp.Header.Get("Mcp-Session-Id"); notifSess != "" {
		h.mu.Lock()
		h.sessionID = notifSess
		h.mu.Unlock()
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(notifResp.Body, 1024))
	notifResp.Body.Close()

	if notifResp.StatusCode < 200 || notifResp.StatusCode >= 300 {
		return fmt.Errorf("remote MCP server returned HTTP %d for initialized notification", notifResp.StatusCode)
	}

	h.initialized = true
	return nil
}

func (h *HTTPUpstreamClient) doRequest(ctx context.Context, method string, params any) (any, error) {
	if err := h.ensureInitialized(ctx); err != nil {
		return nil, err
	}

	h.initMu.Lock()
	currentEpoch := h.initEpoch
	h.initMu.Unlock()

	result, err := h.sendSingleRequest(ctx, method, params)
	if err != nil {
		var sessErr *sessionExpiredError
		if errors.As(err, &sessErr) {
			if reinitErr := h.reinitialize(ctx, currentEpoch); reinitErr != nil {
				return nil, fmt.Errorf("session re-initialization failed after expiry: %w", reinitErr)
			}
			return h.sendSingleRequest(ctx, method, params)
		}
		return nil, err
	}
	return result, nil
}

func (h *HTTPUpstreamClient) sendSingleRequest(ctx context.Context, method string, params any) (any, error) {
	reqID := h.reqID.Add(1)

	reqPayload := map[string]any{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  method,
		"params":  params,
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, RedactEgressError(fmt.Errorf("failed to marshal request: %w", err))
	}

	h.mu.RLock()
	serverURL := h.serverURL
	authToken := h.authToken
	sessID := h.sessionID
	headers := make(map[string]string, len(h.headers))
	for k, v := range h.headers {
		headers[k] = v
	}
	httpClient := h.client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	h.mu.RUnlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, RedactEgressError(fmt.Errorf("failed to create http request: %w", err))
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if sessID != "" {
		req.Header.Set("Mcp-Session-Id", sessID)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, RedactEgressError(fmt.Errorf("http request to %s failed: %w", RedactEgressString(serverURL), err))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("remote MCP server %q returned 401 Unauthorized. Set API token or credentials for %s", h.name, RedactEgressString(serverURL))
	}

	if newSess := resp.Header.Get("Mcp-Session-Id"); newSess != "" {
		h.mu.Lock()
		h.sessionID = newSess
		h.mu.Unlock()
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		cleanBody := RedactEgressString(string(respBody))
		if h.isSessionExpiry(resp.StatusCode, cleanBody, nil) {
			return nil, &sessionExpiredError{msg: fmt.Sprintf("session expired (HTTP %d): %s", resp.StatusCode, cleanBody)}
		}
		return nil, fmt.Errorf("remote MCP server returned HTTP %d: %s", resp.StatusCode, cleanBody)
	}

	return h.decodeResponse(ctx, resp, reqID)
}

func (h *HTTPUpstreamClient) decodeResponse(ctx context.Context, resp *http.Response, expectedID any) (any, error) {
	h.mu.RLock()
	limit := h.maxBodySize
	h.mu.RUnlock()
	if limit <= 0 {
		limit = 10 * 1024 * 1024
	}
	limitedReader := io.LimitReader(resp.Body, limit+1)

	ctype := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ctype)

	switch mediaType {
	case "text/event-stream":
		return h.decodeSSEResponse(ctx, limitedReader, expectedID, limit)
	case "application/json", "":
		return h.decodeJSONResponse(limitedReader, expectedID, limit)
	default:
		body, err := io.ReadAll(limitedReader)
		if err != nil {
			return nil, RedactEgressError(fmt.Errorf("failed to read response body: %w", err))
		}
		if int64(len(body)) > limit {
			return nil, errors.New("response body exceeded limit of 10MB")
		}
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			var jsonRpcResp JSONRPCResponse
			if err := json.Unmarshal(trimmed, &jsonRpcResp); err == nil {
				if expectedID != nil && (jsonRpcResp.ID == nil || !matchResponseID(expectedID, jsonRpcResp.ID)) {
					return nil, fmt.Errorf("mismatched response ID: expected %v, got %v", expectedID, jsonRpcResp.ID)
				}
				if jsonRpcResp.Error != nil {
					if h.isSessionExpiry(0, "", jsonRpcResp.Error) {
						return nil, &sessionExpiredError{msg: fmt.Sprintf("session expired (%d): %s", jsonRpcResp.Error.Code, jsonRpcResp.Error.Message)}
					}
					return nil, fmt.Errorf("remote error (%d): %s", jsonRpcResp.Error.Code, RedactEgressString(jsonRpcResp.Error.Message))
				}
				return jsonRpcResp.Result, nil
			}
		}
		return nil, fmt.Errorf("unsupported content-type from remote MCP server: %q", ctype)
	}
}

func (h *HTTPUpstreamClient) decodeJSONResponse(r io.Reader, expectedID any, limit int64) (any, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, RedactEgressError(fmt.Errorf("failed to read response body: %w", err))
	}
	if int64(len(body)) > limit {
		return nil, errors.New("response body exceeded limit of 10MB")
	}

	var jsonRpcResp JSONRPCResponse
	if err := json.Unmarshal(body, &jsonRpcResp); err != nil {
		return nil, RedactEgressError(fmt.Errorf("failed to decode json-rpc response: %w", err))
	}

	if expectedID != nil {
		if jsonRpcResp.ID == nil || !matchResponseID(expectedID, jsonRpcResp.ID) {
			return nil, fmt.Errorf("mismatched response ID: expected %v, got %v", expectedID, jsonRpcResp.ID)
		}
	}

	if jsonRpcResp.Error != nil {
		if h.isSessionExpiry(0, "", jsonRpcResp.Error) {
			return nil, &sessionExpiredError{msg: fmt.Sprintf("session expired (%d): %s", jsonRpcResp.Error.Code, jsonRpcResp.Error.Message)}
		}
		cleanMsg := RedactEgressString(jsonRpcResp.Error.Message)
		return nil, &UpstreamToolError{Prefix: "remote error", Code: jsonRpcResp.Error.Code, Message: cleanMsg}
	}

	return jsonRpcResp.Result, nil
}

func (h *HTTPUpstreamClient) decodeSSEResponse(ctx context.Context, r io.Reader, expectedID any, limit int64) (any, error) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, int(limit))

	var currentEvent string
	var dataBuilder strings.Builder
	var receivedResponse any
	var foundResponse bool

	processBlock := func() (bool, error) {
		if currentEvent == "error" {
			errMsg := dataBuilder.String()
			if errMsg == "" {
				errMsg = "unknown sse error"
			}
			return false, fmt.Errorf("sse error event received: %s", RedactEgressString(errMsg))
		}

		if dataBuilder.Len() > 0 {
			dataBytes := []byte(dataBuilder.String())
			var jsonRpcResp JSONRPCResponse
			if err := json.Unmarshal(dataBytes, &jsonRpcResp); err != nil {
				return false, fmt.Errorf("malformed sse data frame: %w", err)
			}

			if jsonRpcResp.ID != nil {
				if expectedID != nil && !matchResponseID(expectedID, jsonRpcResp.ID) {
					return false, fmt.Errorf("mismatched response ID: expected %v, got %v", expectedID, jsonRpcResp.ID)
				}

				if jsonRpcResp.Error != nil {
					if h.isSessionExpiry(0, "", jsonRpcResp.Error) {
						return false, &sessionExpiredError{msg: fmt.Sprintf("session expired (%d): %s", jsonRpcResp.Error.Code, jsonRpcResp.Error.Message)}
					}
					cleanMsg := RedactEgressString(jsonRpcResp.Error.Message)
					return false, &UpstreamToolError{Prefix: "remote error", Code: jsonRpcResp.Error.Code, Message: cleanMsg}
				}

				receivedResponse = jsonRpcResp.Result
				foundResponse = true
				return true, nil
			}
		}
		return false, nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimRight(line, "\r")

		if line == "" {
			done, err := processBlock()
			if err != nil {
				return nil, err
			}
			if done {
				break
			}
			currentEvent = ""
			dataBuilder.Reset()
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataVal := strings.TrimPrefix(line, "data:")
			dataVal = strings.TrimPrefix(dataVal, " ")
			if dataBuilder.Len() > 0 {
				dataBuilder.WriteByte('\n')
			}
			dataBuilder.WriteString(dataVal)
		} else if strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
			continue
		} else {
			return nil, fmt.Errorf("malformed sse line: %q", line)
		}
	}

	if !foundResponse && dataBuilder.Len() > 0 {
		_, err := processBlock()
		if err != nil {
			return nil, err
		}
	}

	if scanner.Err() != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(scanner.Err(), bufio.ErrTooLong) {
			return nil, errors.New("sse frame exceeded maximum allowed limit")
		}
		return nil, RedactEgressError(fmt.Errorf("sse stream read error: %w", scanner.Err()))
	}

	if !foundResponse {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("sse stream ended without matching json-rpc response")
	}

	return receivedResponse, nil
}

// CallTool invokes a tool on the remote HTTP MCP server.
func (h *HTTPUpstreamClient) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	sanitizedArgs, err := SanitizeEgressArgs(name, args)
	if err != nil {
		return nil, err
	}

	callParams := map[string]any{
		"name":      name,
		"arguments": sanitizedArgs,
	}

	return h.doRequest(ctx, "tools/call", callParams)
}

// DiscoverTools queries the remote HTTP MCP server for its exposed tools via tools/list.
func (h *HTTPUpstreamClient) DiscoverTools(ctx context.Context) ([]Tool, error) {
	res, err := h.doRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return parseDiscoveredTools(res)
}

func matchResponseID(expected, actual any) bool {
	if expected == nil && actual == nil {
		return true
	}
	if expected == nil || actual == nil {
		return false
	}
	if expected == actual {
		return true
	}
	switch e := expected.(type) {
	case int64:
		if a, ok := actual.(float64); ok {
			return float64(e) == a
		}
		if a, ok := actual.(int64); ok {
			return e == a
		}
		if a, ok := actual.(int); ok {
			return e == int64(a)
		}
	case float64:
		if a, ok := actual.(int64); ok {
			return e == float64(a)
		}
		if a, ok := actual.(float64); ok {
			return e == a
		}
	}
	return fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
}
