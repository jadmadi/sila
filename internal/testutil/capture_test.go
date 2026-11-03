package testutil

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestCaptureStdout(t *testing.T) {
	out := CaptureStdout(t, func() {
		fmt.Print("hello stdout")
	})
	if out != "hello stdout" {
		t.Fatalf("expected 'hello stdout', got %q", out)
	}
}

func TestCaptureStderr(t *testing.T) {
	errOut := CaptureStderr(t, func() {
		fmt.Fprint(os.Stderr, "hello stderr")
	})
	if errOut != "hello stderr" {
		t.Fatalf("expected 'hello stderr', got %q", errOut)
	}
}

func TestCaptureBoth(t *testing.T) {
	stdout, stderr := CaptureBoth(t, func() {
		fmt.Print("stdout text")
		fmt.Fprint(os.Stderr, "stderr text")
	})
	if stdout != "stdout text" {
		t.Fatalf("expected 'stdout text', got %q", stdout)
	}
	if stderr != "stderr text" {
		t.Fatalf("expected 'stderr text', got %q", stderr)
	}
}

func TestCaptureStdout_LargeVolumeNoDeadlock(t *testing.T) {
	// Write 256KB to stdout (exceeds Linux 64KB pipe buffer limit)
	chunk := strings.Repeat("A", 1024)
	out := CaptureStdout(t, func() {
		for i := 0; i < 256; i++ {
			fmt.Print(chunk)
		}
	})
	if len(out) != 256*1024 {
		t.Fatalf("expected %d bytes, got %d", 256*1024, len(out))
	}
}

func TestCapturePipeStdout_LargeVolumeNoDeadlock(t *testing.T) {
	// Write 256KB through pipe with concurrent background reader
	chunk := strings.Repeat("B", 1024)
	out := CapturePipeStdout(t, func() {
		for i := 0; i < 256; i++ {
			fmt.Print(chunk)
		}
	})
	if len(out) != 256*1024 {
		t.Fatalf("expected %d bytes, got %d", 256*1024, len(out))
	}
}
