package main

import (
	"testing"
	"time"
)

func TestRebaseRetestTimeout(t *testing.T) {
	t.Setenv("SILA_REBASE_RETEST_TIMEOUT", "")
	if got := rebaseRetestTimeout(); got != 60*time.Second {
		t.Fatalf("default timeout = %s, want 60s", got)
	}

	t.Setenv("SILA_REBASE_RETEST_TIMEOUT", "5m")
	if got := rebaseRetestTimeout(); got != 5*time.Minute {
		t.Fatalf("override = %s, want 5m", got)
	}

	t.Setenv("SILA_REBASE_RETEST_TIMEOUT", "not-a-duration")
	if got := rebaseRetestTimeout(); got != 60*time.Second {
		t.Fatalf("invalid override = %s, want 60s", got)
	}
}
