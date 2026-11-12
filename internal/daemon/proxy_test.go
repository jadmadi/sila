package daemon

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func startStoreServer(t *testing.T) (*Server, string, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	socket := filepath.Join(t.TempDir(), "sila.sock")
	srv := NewServerWithStore(socket, "test", st)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-done
	})
	return srv, socket, st
}

func TestProxySharesOneStore(t *testing.T) {
	srv, socket, _ := startStoreServer(t)
	client1, err := Dial(socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client1.Close()
	client2, err := Dial(socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close()
	initReply, err := client1.MCPFrame(context.Background(), []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(initReply) == 0 {
		t.Fatal("empty initialize reply")
	}
	list1, err := client1.MCPFrame(context.Background(), []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	list2, err := client2.MCPFrame(context.Background(), []byte(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	var parsed1, parsed2 map[string]any
	if err := json.Unmarshal(list1, &parsed1); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(list2, &parsed2); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed1["result"]; !ok {
		t.Fatalf("list1 = %s", list1)
	}
	if srv.StoreHandleCount() != 1 {
		t.Fatalf("handles = %d", srv.StoreHandleCount())
	}
}

func TestDialMissingSocket(t *testing.T) {
	if _, err := Dial(filepath.Join(t.TempDir(), "absent.sock"), 100*time.Millisecond); err == nil {
		t.Fatal("missing socket dialed")
	}
}

func TestDialContractMismatch(t *testing.T) {
	_, socket, _ := startStoreServer(t)
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"hello","params":{"contract":"wrong"}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(buf[:n]) || !containsText(string(buf[:n]), "contract_mismatch") {
		t.Fatalf("reply = %s", buf[:n])
	}
}

func containsText(text, needle string) bool {
	for i := 0; i+len(needle) <= len(text); i++ {
		if text[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
