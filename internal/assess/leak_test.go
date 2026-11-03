package assess

import (
	"testing"
	"time"
)

func TestAssertNoGoroutineLeaks(t *testing.T) {
	AssertNoGoroutineLeaks(t)

	// Launch a transient goroutine that completes quickly
	done := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Millisecond)
		close(done)
	}()
	<-done
}
