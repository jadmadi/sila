package main

import (
	"os"
	"testing"
)

// captureStdout runs fn while capturing everything written to os.Stdout and
// returns it. It uses a temp file rather than a pipe: the older pipe-plus-
// goroutine pattern could stall when the reader raced the writer, and it left
// a blocked goroutine behind on failure.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout-*.txt")
	if err != nil {
		t.Fatalf("create stdout capture file: %v", err)
	}
	orig := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = orig }()

	fn()

	if err := f.Close(); err != nil {
		t.Fatalf("close stdout capture file: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read stdout capture file: %v", err)
	}
	return string(data)
}

// captureStderr runs fn while capturing everything written to os.Stderr and
// returns it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr-*.txt")
	if err != nil {
		t.Fatalf("create stderr capture file: %v", err)
	}
	orig := os.Stderr
	os.Stderr = f
	defer func() { os.Stderr = orig }()

	fn()

	if err := f.Close(); err != nil {
		t.Fatalf("close stderr capture file: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read stderr capture file: %v", err)
	}
	return string(data)
}
