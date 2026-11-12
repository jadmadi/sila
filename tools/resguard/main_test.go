package main

import (
	"strings"
	"testing"
)

func TestParseMemInfo(t *testing.T) {
	sample := `MemTotal:       32000000 kB
MemFree:         1000000 kB
MemAvailable:   16000000 kB
SwapTotal:       8000000 kB
SwapFree:        2000000 kB
`
	m, err := parseMemInfo(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalKB != 32000000 || m.AvailableKB != 16000000 {
		t.Fatalf("unexpected memory values: %+v", m)
	}
	if got := m.SwapUsedPct(); got < 74.9 || got > 75.1 {
		t.Fatalf("swap used = %.2f, want 75", got)
	}
	if got := m.AvailableGB(); got < 15.2 || got > 15.3 {
		t.Fatalf("available GB = %.2f", got)
	}
}

func TestParseMemInfoRejectsEmpty(t *testing.T) {
	if _, err := parseMemInfo(strings.NewReader("")); err == nil {
		t.Fatal("expected an error for empty meminfo")
	}
}

func TestGoFlagsAddsParallelism(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	if got := goFlags(2); got != "-p=2" {
		t.Fatalf("goFlags = %q", got)
	}
	t.Setenv("GOFLAGS", "-mod=mod")
	if got := goFlags(2); got != "-mod=mod -p=2" {
		t.Fatalf("goFlags = %q", got)
	}
	t.Setenv("GOFLAGS", "-p=1")
	if got := goFlags(2); got != "-p=1" {
		t.Fatalf("existing -p must win, got %q", got)
	}
}

func TestBreachReportsDiskAndMemory(t *testing.T) {
	m := memInfo{TotalKB: 32_000_000, AvailableKB: 1_000_000, SwapTotalKB: 8_000_000, SwapFreeKB: 100_000}
	reason := breach(t.TempDir(), limits{MinDiskGB: 0.0001, MinMemGB: 2}, m)
	if !strings.Contains(reason, "memory available") {
		t.Fatalf("reason = %q", reason)
	}
	m = memInfo{TotalKB: 32_000_000, AvailableKB: 16_000_000, SwapTotalKB: 8_000_000, SwapFreeKB: 100_000}
	reason = breach(t.TempDir(), limits{MinDiskGB: 0.0001, MinMemGB: 2}, m)
	if !strings.Contains(reason, "swap") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestDiskFreeIsPositive(t *testing.T) {
	got, err := diskFreeGB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got <= 0 {
		t.Fatalf("free GB = %.2f", got)
	}
}
