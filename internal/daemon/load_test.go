package daemon

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestHundredConcurrentStatus(t *testing.T) {
	_, socket, _ := startStoreServer(t)
	const workers = 100
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var client *Client
			var err error
			for attempt := 0; attempt < 50; attempt++ {
				client, err = Dial(socket, time.Second)
				if err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if err != nil {
				errs <- err
				return
			}
			defer client.Close()
			if _, err := client.Call(context.Background(), "status", nil); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestNoGoroutineLeakAfterConnections(t *testing.T) {
	_, socket, _ := startStoreServer(t)
	baseline := runtime.NumGoroutine()
	for i := 0; i < 100; i++ {
		client, err := Dial(socket, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Call(context.Background(), "status", nil); err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+5 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines grew: baseline=%d now=%d", baseline, runtime.NumGoroutine())
}
