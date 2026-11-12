package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jadmadi/sila/internal/mcp"
	"github.com/jadmadi/sila/internal/store"
)

const (
	maxConnections = 64
	maxFrameBytes  = 8 << 20
)

// Server is the local Unix-socket daemon.
type Server struct {
	socketPath string
	version    string
	started    time.Time

	ln           net.Listener
	closed       atomic.Bool
	active       atomic.Int64
	wg           sync.WaitGroup
	handlers     map[string]func(context.Context, json.RawMessage) (any, *RPCError)
	mcpServer    *mcp.Server
	store        *store.Store
	frameMu      sync.Mutex
	connMu       sync.Mutex
	conns        map[net.Conn]bool
	statusMu     sync.Mutex
	statusAt     time.Time
	statusCache  StatusSnapshot
	syncRuns     atomic.Int64
	lastSyncUnix atomic.Int64
	bus          *Bus
}

// NewServer prepares a daemon server for one socket path.
func NewServer(socketPath, version string) *Server {
	server := &Server{
		socketPath: socketPath,
		version:    version,
		started:    time.Now().UTC(),
		handlers:   map[string]func(context.Context, json.RawMessage) (any, *RPCError){},
		bus:        NewBus(),
		conns:      map[net.Conn]bool{},
	}
	server.handlers["hello"] = server.handleHello
	return server
}

// Listen creates the socket directory and listener with restrictive modes.
func (s *Server) Listen() error {
	if len(s.socketPath) > 100 {
		return fmt.Errorf("daemon socket path too long (%d bytes)", len(s.socketPath))
	}
	dir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("daemon socket directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("daemon socket directory mode: %w", err)
	}
	if info, err := os.Stat(s.socketPath); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("daemon socket path exists and is not a socket")
		}
		_ = os.Remove(s.socketPath)
	}
	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("daemon listen: %w", err)
	}
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		ln.Close()
		return fmt.Errorf("daemon socket mode: %w", err)
	}
	s.ln = ln
	return nil
}

// Serve accepts connections until the context is cancelled or Close is called.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return errors.New("daemon not listening")
	}
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if s.closed.Load() {
				return nil
			}
			return err
		}
		if !peerAllowed(conn) {
			_ = conn.Close()
			continue
		}
		if s.active.Add(1) > maxConnections {
			_ = writeResponse(conn, NewError(nil, -32000, "too_many_connections"))
			s.active.Add(-1)
			_ = conn.Close()
			continue
		}
		s.connMu.Lock()
		s.conns[conn] = true
		s.connMu.Unlock()
		s.wg.Add(1)
		go func(conn net.Conn) {
			defer s.wg.Done()
			defer func() {
				s.connMu.Lock()
				delete(s.conns, conn)
				s.connMu.Unlock()
			}()
			defer s.active.Add(-1)
			defer conn.Close()
			s.serveConn(ctx, conn)
		}(conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
	writer := bufio.NewWriter(conn)
	for scanner.Scan() {
		var request Request
		line := scanner.Bytes()
		if err := json.Unmarshal(line, &request); err != nil {
			_ = writeResponseBuffered(writer, NewError(nil, -32700, "parse error"))
			continue
		}
		if request.Method == "subscribe" {
			s.serveSubscription(ctx, writer, request)
			return
		}
		response := s.dispatch(ctx, request)
		if err := writeResponseBuffered(writer, response); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, request Request) Response {
	handler, ok := s.handlers[request.Method]
	if !ok {
		return NewError(request.ID, -32601, "method not found")
	}
	result, rpcErr := handler(ctx, request.Params)
	if rpcErr != nil {
		return Response{JSONRPC: "2.0", ID: request.ID, Error: rpcErr}
	}
	return NewResult(request.ID, result)
}

func (s *Server) handleHello(ctx context.Context, params json.RawMessage) (any, *RPCError) {
	var payload struct {
		Contract string `json:"contract"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &payload); err != nil {
			return nil, &RPCError{Code: -32602, Message: "invalid params"}
		}
	}
	if payload.Contract != Contract {
		return nil, &RPCError{Code: -32001, Message: "contract_mismatch"}
	}
	return map[string]any{
		"contract":   Contract,
		"version":    s.version,
		"pid":        os.Getpid(),
		"started_at": s.started.Format(time.RFC3339),
	}, nil
}

// AddHandler registers a method handler.
func (s *Server) AddHandler(method string, handler func(context.Context, json.RawMessage) (any, *RPCError)) {
	s.handlers[method] = handler
}

// Close stops the listener and waits for active connections.
func (s *Server) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	s.connMu.Lock()
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.connMu.Unlock()
	s.wg.Wait()
	if s.socketPath != "" {
		_ = os.Remove(s.socketPath)
	}
	return err
}

// SocketPath returns the configured path.
func (s *Server) SocketPath() string { return s.socketPath }

// Started returns the server start time.
func (s *Server) Started() time.Time { return s.started }

func writeResponse(conn net.Conn, response Response) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	_, err = conn.Write(encoded)
	return err
}

func writeResponseBuffered(writer *bufio.Writer, response Response) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := writer.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return writer.Flush()
}

// NewServerWithStore makes a daemon that owns one store and forwards MCP frames.
func NewServerWithStore(socketPath, version string, st *store.Store) *Server {
	server := NewServer(socketPath, version)
	server.store = st
	server.mcpServer = mcp.NewServer(st, nil, &bytes.Buffer{})
	server.AddLifecycleHandlers()
	server.handlers["mcp.frame"] = func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		var payload struct {
			Line string `json:"line"`
		}
		if len(params) > 0 {
			if err := json.Unmarshal(params, &payload); err != nil {
				return nil, &RPCError{Code: -32602, Message: "invalid params"}
			}
		}
		server.frameMu.Lock()
		out := server.mcpServer.ProcessFrame([]byte(payload.Line))
		server.frameMu.Unlock()
		server.invalidateStatus()
		server.bus.Publish("write", nil)
		return map[string]any{"line": string(out)}, nil
	}
	return server
}

// StoreHandleCount reports how many stores the daemon owns.
func (s *Server) StoreHandleCount() int {
	if s.store == nil {
		return 0
	}
	return 1
}

// NotifySync records a completed background sync tick and refreshes status cache when changes arrived.
func (s *Server) NotifySync(newRecords int64, distilled int) {
	s.syncRuns.Add(1)
	s.lastSyncUnix.Store(time.Now().UTC().Unix())
	if newRecords > 0 || distilled > 0 {
		s.invalidateStatus()
		s.bus.Publish("sync.completed", map[string]any{
			"new_records":     newRecords,
			"facts_distilled": distilled,
		})
	}
}

// AddLifecycleHandlers registers status and shutdown.
func (s *Server) AddLifecycleHandlers() {
	s.handlers["status"] = func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		snap := s.snapshot(time.Now().UTC())
		lastSync := ""
		if ts := s.lastSyncUnix.Load(); ts > 0 {
			lastSync = time.Unix(ts, 0).UTC().Format(time.RFC3339)
		}
		return map[string]any{
			"pid":             os.Getpid(),
			"version":         s.version,
			"uptime_seconds":  int(time.Since(s.started).Seconds()),
			"clients":         int(s.active.Load()),
			"socket":          s.socketPath,
			"messages":        snap.Messages,
			"facts":           snap.Facts,
			"projects":        snap.Projects,
			"verified_facts":  snap.VerifiedFacts,
			"goals_ready":     snap.GoalsReady,
			"goals_in_flight": snap.GoalsInFlight,
			"claims":          snap.Claims,
			"sync_runs":       s.syncRuns.Load(),
			"last_sync_at":    lastSync,
		}, nil
	}
	s.handlers["shutdown"] = func(ctx context.Context, params json.RawMessage) (any, *RPCError) {
		go func() {
			time.Sleep(50 * time.Millisecond)
			_ = s.Close()
		}()
		return map[string]any{"ok": true}, nil
	}
}
