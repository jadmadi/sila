package assess

import (
	"bytes"
	"runtime/pprof"
	"testing"
	"time"
)

// AssertNoGoroutineLeaks ensures no dangling goroutines outlive the test.
// It inspects runtime/pprof goroutine profiles before and after test teardown.
func AssertNoGoroutineLeaks(t *testing.T) {
	t.Helper()
	initialCount := getGoroutineCount()

	t.Cleanup(func() {
		// Allow a brief grace window for deferred background cleanups
		var leaked int
		for i := 0; i < 5; i++ {
			time.Sleep(20 * time.Millisecond)
			currentCount := getGoroutineCount()
			if currentCount <= initialCount {
				return
			}
			leaked = currentCount - initialCount
		}

		var buf bytes.Buffer
		prof := pprof.Lookup("goroutineleak")
		if prof == nil {
			prof = pprof.Lookup("goroutine")
		}
		if prof != nil {
			_ = prof.WriteTo(&buf, 1)
		}

		t.Errorf("goroutine leak detected: %d excess goroutines after test completion\nActive profile:\n%s", leaked, buf.String())
	})
}

func getGoroutineCount() int {
	prof := pprof.Lookup("goroutineleak")
	if prof != nil {
		return prof.Count()
	}
	prof = pprof.Lookup("goroutine")
	if prof != nil {
		return prof.Count()
	}
	return 0
}
