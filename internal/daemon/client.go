package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Client talks to a daemon over its Unix socket.
type Client struct {
	conn net.Conn
	rd   *bufio.Reader
	mu   sync.Mutex
	next atomic.Int64
}

type rawResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Dial connects and validates the daemon contract.
func Dial(socketPath string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, err
	}
	client := &Client{conn: conn, rd: bufio.NewReader(conn)}
	if _, err := client.Call(context.Background(), "hello", map[string]string{"contract": Contract}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

// Call sends one JSON-RPC request and returns its result.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.next.Add(1)
	request := Request{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		request.Params = encoded
	}
	line, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
	}
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	responseLine, err := c.rd.ReadString('\n')
	if err != nil {
		return nil, err
	}
	var response rawResponse
	if err := json.Unmarshal([]byte(responseLine), &response); err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s", response.Error.Message)
	}
	return response.Result, nil
}

// MCPFrame forwards one MCP stdio line and returns the daemon's reply.
func (c *Client) MCPFrame(ctx context.Context, line []byte) ([]byte, error) {
	raw, err := c.Call(ctx, "mcp.frame", map[string]string{"line": string(line)})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Line string `json:"line"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return []byte(payload.Line), nil
}

// ProxyStream forwards NDJSON MCP frames from in to out through the daemon.
func ProxyStream(ctx context.Context, client *Client, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		reply, err := client.MCPFrame(ctx, line)
		if err != nil {
			return err
		}
		if _, err := out.Write(append(reply, '\n')); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// Close stops the client connection.
func (c *Client) Close() error { return c.conn.Close() }

// DefaultSocketPath returns the user-scoped daemon socket path.
func DefaultSocketPath() string {
	if configured := os.Getenv("SILA_DAEMON_SOCKET"); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "sila", "daemon.sock")
}

// Subscribe streams events until the context is cancelled.
func (c *Client) Subscribe(ctx context.Context, kinds []string, handler func(Event) error) error {
	params, err := json.Marshal(map[string]any{"kinds": kinds})
	if err != nil {
		return err
	}
	request := Request{JSONRPC: "2.0", ID: "subscribe", Method: "subscribe", Params: params}
	line, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return err
	}
	for {
		if deadline, ok := ctx.Deadline(); ok {
			_ = c.conn.SetReadDeadline(deadline)
		}
		responseLine, err := c.rd.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		var frame struct {
			Event Event `json:"event"`
		}
		if err := json.Unmarshal([]byte(responseLine), &frame); err != nil {
			return err
		}
		if err := handler(frame.Event); err != nil {
			return err
		}
	}
}

// DialWithRetry retries connection with 50 ms doubling backoff up to 2 s,
// bounded to ten attempts and the given budget.
func DialWithRetry(socketPath string, budget time.Duration) (*Client, error) {
	deadline := time.Now().Add(budget)
	backoff := 50 * time.Millisecond
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		client, err := Dial(socketPath, 200*time.Millisecond)
		if err == nil {
			return client, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			break
		}
		wait := backoff
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		time.Sleep(wait)
		backoff *= 2
		if backoff > 2*time.Second {
			backoff = 2 * time.Second
		}
	}
	return nil, fmt.Errorf("daemon unavailable: %w", lastErr)
}
