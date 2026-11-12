package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

func TestDialWithRetrySucceedsWhenDaemonAppears(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "late.sock")
	st, err := store.Open(filepath.Join(dir, "late.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := NewServerWithStore(socket, "late", st)
	go func() {
		time.Sleep(250 * time.Millisecond)
		if err := srv.Listen(); err != nil {
			return
		}
		_ = srv.Serve(context.Background())
	}()
	client, err := DialWithRetry(socket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer srv.Close()
	if _, err := client.Call(context.Background(), "status", nil); err != nil {
		t.Fatal(err)
	}
}

func TestDialWithRetryGivesUp(t *testing.T) {
	start := time.Now()
	if _, err := DialWithRetry(filepath.Join(t.TempDir(), "never.sock"), 300*time.Millisecond); err == nil {
		t.Fatal("missing daemon connected")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("gave up after %s", elapsed)
	}
}

func TestInflightCallReturnsOnContextTimeout(t *testing.T) {
	_, socket, _ := startStoreServer(t)
	client, err := Dial(socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := client.Call(ctx, "status", nil); err != nil {
		t.Fatalf("status with timeout: %v", err)
	}
}
