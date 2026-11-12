package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	socket := filepath.Join(dir, "sila.sock")
	srv := NewServer(socket, "test-version")
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-done
	})
	return srv, socket
}

type testClient struct {
	conn net.Conn
	rd   *bufio.Reader
}

func dialTest(t *testing.T, socket string) *testClient {
	t.Helper()
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &testClient{conn: conn, rd: bufio.NewReader(conn)}
}

func (c *testClient) roundTrip(t *testing.T, request string) Response {
	t.Helper()
	if _, err := c.conn.Write([]byte(request + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := c.rd.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestProtocolHelloAndModes(t *testing.T) {
	srv, socket := startTestServer(t)
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(socket))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", dirInfo.Mode().Perm())
	}
	client := dialTest(t, socket)
	response := client.roundTrip(t, `{"jsonrpc":"2.0","id":1,"method":"hello","params":{"contract":"sila/daemon/v1"}}`)
	result, ok := response.Result.(map[string]any)
	if !ok || result["contract"] != Contract || result["pid"] == nil {
		t.Fatalf("hello = %+v", response)
	}
	mismatch := client.roundTrip(t, `{"jsonrpc":"2.0","id":2,"method":"hello","params":{"contract":"wrong"}}`)
	if mismatch.Error == nil || mismatch.Error.Message != "contract_mismatch" {
		t.Fatalf("mismatch = %+v", mismatch)
	}
	_ = srv
}

func TestProtocolMalformedKeepsConnection(t *testing.T) {
	_, socket := startTestServer(t)
	client := dialTest(t, socket)
	parseErr := client.roundTrip(t, `{not json`)
	if parseErr.Error == nil || parseErr.Error.Code != -32700 {
		t.Fatalf("parse error = %+v", parseErr)
	}
	ok := client.roundTrip(t, `{"jsonrpc":"2.0","id":3,"method":"hello","params":{"contract":"sila/daemon/v1"}}`)
	if ok.Error != nil {
		t.Fatalf("connection did not survive: %+v", ok)
	}
}

func TestProtocolConnectionLimit(t *testing.T) {
	_, socket := startTestServer(t)
	var conns []net.Conn
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()
	for i := 0; i < maxConnections; i++ {
		conn, err := net.Dial("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, conn)
	}
	extra := dialTest(t, socket)
	extra.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := extra.rd.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "too_many_connections") {
		t.Fatalf("65th response = %s", line)
	}
}

func TestListenRejectsNonSocketFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sila.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(path, "v")
	if err := srv.Listen(); err == nil {
		t.Fatal("non-socket file accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep" {
		t.Fatalf("file changed: %q err=%v", data, err)
	}
}
