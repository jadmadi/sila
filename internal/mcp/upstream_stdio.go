package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type stdioState int

const (
	stdioStateClosed stdioState = iota
	stdioStateStarting
	stdioStateReady
)

// StdioUpstreamClient manages a child MCP server communicating over standard I/O with JSON-RPC 2.0.
type StdioUpstreamClient struct {
	name       string
	prefix     string
	command    string
	args       []string
	env        map[string]string
	autoIngest bool

	mu       sync.Mutex
	state    stdioState
	startCh  chan struct{}
	startErr error

	cmd      *exec.Cmd
	stdin    io.WriteCloser
	waitDone chan struct{}
	waitErr  error
	reqID    atomic.Int64
	pending  map[int64]chan JSONRPCResponse
}

// NewStdioUpstreamClient creates a new StdioUpstreamClient instance.
func NewStdioUpstreamClient(name, prefix, command string, args []string, env map[string]string, autoIngest bool) *StdioUpstreamClient {
	return &StdioUpstreamClient{
		name:       name,
		prefix:     prefix,
		command:    command,
		args:       args,
		env:        env,
		autoIngest: autoIngest,
		pending:    make(map[int64]chan JSONRPCResponse),
		state:      stdioStateClosed,
	}
}

func (s *StdioUpstreamClient) Name() string   { return s.name }
func (s *StdioUpstreamClient) Prefix() string { return s.prefix }

func (s *StdioUpstreamClient) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == stdioStateReady
}

// ProcessState returns the underlying child process state, if executed.
func (s *StdioUpstreamClient) ProcessState() *os.ProcessState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil {
		return s.cmd.ProcessState
	}
	return nil
}

// WaitError returns any error returned by cmd.Wait() when the child process exited.
func (s *StdioUpstreamClient) WaitError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitErr
}

func (s *StdioUpstreamClient) ensureStarted(ctx context.Context) error {
	for {
		s.mu.Lock()
		switch s.state {
		case stdioStateReady:
			s.mu.Unlock()
			return nil

		case stdioStateStarting:
			ch := s.startCh
			s.mu.Unlock()
			select {
			case <-ch:
				s.mu.Lock()
				if s.state == stdioStateReady {
					s.mu.Unlock()
					return nil
				}
				err := s.startErr
				if err == nil {
					err = errors.New("upstream server failed to start")
				}
				s.mu.Unlock()
				return err
			case <-ctx.Done():
				return ctx.Err()
			}

		case stdioStateClosed:
			s.state = stdioStateStarting
			startCh := make(chan struct{})
			s.startCh = startCh
			s.startErr = nil
			s.mu.Unlock()

			err := s.startAndInitialize(ctx)
			s.mu.Lock()
			if err != nil {
				s.state = stdioStateClosed
				s.startErr = err
				close(startCh)
				s.mu.Unlock()
				return err
			}
			if s.state == stdioStateStarting {
				s.state = stdioStateReady
				close(startCh)
				s.mu.Unlock()
				return nil
			}
			s.startErr = errors.New("upstream server closed during initialization")
			close(startCh)
			s.mu.Unlock()
			_ = s.Close()
			return s.startErr
		}
	}
}

func (s *StdioUpstreamClient) startAndInitialize(ctx context.Context) error {
	s.mu.Lock()
	if s.command == "" {
		s.mu.Unlock()
		return fmt.Errorf("upstream server %q has no command specified", s.name)
	}

	if err := ValidateStdioArgs(s.args); err != nil {
		s.mu.Unlock()
		return err
	}

	cmd := exec.CommandContext(context.Background(), s.command, s.args...)
	if len(s.env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range s.env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to open stdin pipe for %s: %w", s.name, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		s.mu.Unlock()
		return fmt.Errorf("failed to open stdout pipe for %s: %w", s.name, err)
	}

	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		s.mu.Unlock()
		return fmt.Errorf("failed to start upstream MCP server %q (%s): %w", s.name, s.command, err)
	}

	s.cmd = cmd
	s.stdin = stdin
	waitDone := make(chan struct{})
	s.waitDone = waitDone
	s.mu.Unlock()

	// Spawn single reaper goroutine for this child process
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.waitErr = err
		s.state = stdioStateClosed
		if s.stdin != nil {
			_ = s.stdin.Close()
			s.stdin = nil
		}
		for id, ch := range s.pending {
			close(ch)
			delete(s.pending, id)
		}
		s.mu.Unlock()
		close(waitDone)
	}()

	// Read stdout responses in background
	go s.readLoop(stdout)

	// Perform initialize handshake with a 30s budget (accommodates npx and remote proxy cold starts)
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "sila-gateway",
			"version": "v0.9.0",
		},
	}
	if _, err := s.sendRequest(initCtx, "initialize", initParams); err != nil {
		_ = s.Close()
		return fmt.Errorf("initialize handshake with %s failed: %w", s.name, err)
	}

	// Send notifications/initialized notification (no response expected)
	_ = s.sendNotification("notifications/initialized", map[string]any{})

	return nil
}

func (s *StdioUpstreamClient) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var res JSONRPCResponse
		if err := jsonv2.Unmarshal([]byte(line), &res); err != nil {
			continue
		}

		var idInt int64
		switch v := res.ID.(type) {
		case float64:
			idInt = int64(v)
		case int64:
			idInt = v
		case int:
			idInt = int64(v)
		case string:
			_, _ = fmt.Sscanf(v, "%d", &idInt)
		}

		s.mu.Lock()
		ch, ok := s.pending[idInt]
		s.mu.Unlock()

		if ok && ch != nil {
			select {
			case ch <- res:
			default:
			}
		}
	}
	if err := scanner.Err(); err != nil {
		_ = s.Close()
	}
}

func (s *StdioUpstreamClient) sendNotification(method string, params any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if (s.state != stdioStateReady && s.state != stdioStateStarting) || s.stdin == nil {
		return errors.New("upstream server is not running")
	}
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = s.stdin.Write(data)
	return err
}

func (s *StdioUpstreamClient) sendRequest(ctx context.Context, method string, params any) (*JSONRPCResponse, error) {
	id := s.reqID.Add(1)
	ch := make(chan JSONRPCResponse, 1)

	s.mu.Lock()
	if (s.state != stdioStateReady && s.state != stdioStateStarting) || s.stdin == nil {
		s.mu.Unlock()
		return nil, errors.New("upstream server is not running")
	}
	s.pending[id] = ch
	stdin := s.stdin
	s.mu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	data = append(data, '\n')

	s.mu.Lock()
	if s.stdin != stdin || s.stdin == nil {
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, errors.New("upstream server connection closed")
	}
	if _, err := s.stdin.Write(data); err != nil {
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	select {
	case res, ok := <-ch:
		if !ok {
			return nil, errors.New("upstream connection closed unexpectedly")
		}
		if res.Error != nil {
			return nil, &UpstreamToolError{Prefix: "upstream error", Code: res.Error.Code, Message: RedactEgressString(res.Error.Message)}
		}
		return &res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallTool invokes an upstream tool by name over stdio.
func (s *StdioUpstreamClient) CallTool(ctx context.Context, name string, args map[string]any) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	sanitizedArgs, err := SanitizeEgressArgs(name, args)
	if err != nil {
		return nil, err
	}

	if err := s.ensureStarted(ctx); err != nil {
		return nil, RedactEgressError(err)
	}

	callParams := map[string]any{
		"name":      name,
		"arguments": sanitizedArgs,
	}

	res, err := s.sendRequest(ctx, "tools/call", callParams)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			_ = s.Close()
		}
		return nil, RedactEgressError(err)
	}
	return res.Result, nil
}

// DiscoverTools queries the child process for its exposed tools via tools/list.
func (s *StdioUpstreamClient) DiscoverTools(ctx context.Context) ([]Tool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	if err := s.ensureStarted(ctx); err != nil {
		return nil, RedactEgressError(err)
	}
	res, err := s.sendRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			_ = s.Close()
		}
		return nil, RedactEgressError(err)
	}
	return parseDiscoveredTools(res.Result)
}

// Close terminates the child process gracefully and reaps it exactly once.
func (s *StdioUpstreamClient) Close() error {
	s.mu.Lock()
	cmd := s.cmd
	stdin := s.stdin
	waitDone := s.waitDone
	s.state = stdioStateClosed
	s.stdin = nil
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}

	if cmd != nil && cmd.Process != nil && waitDone != nil {
		select {
		case <-waitDone:
			// Already reaped
		case <-time.After(1500 * time.Millisecond):
			_ = cmd.Process.Kill()
			<-waitDone
		}
	}
	return nil
}
