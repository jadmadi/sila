package testutil

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// CaptureStdout runs fn while capturing everything written to os.Stdout and
// returns it. It redirects os.Stdout to a temporary file in t.TempDir() to
// eliminate 64KB Linux pipe buffer deadlocks and avoid reader goroutine leaks.
func CaptureStdout(t testing.TB, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout-*.txt")
	if err != nil {
		t.Fatalf("testutil: create stdout capture file: %v", err)
	}
	orig := os.Stdout
	os.Stdout = f
	defer func() { os.Stdout = orig }()

	fn()

	if err := f.Close(); err != nil {
		t.Fatalf("testutil: close stdout capture file: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("testutil: read stdout capture file: %v", err)
	}
	return string(data)
}

// CaptureStderr runs fn while capturing everything written to os.Stderr and
// returns it.
func CaptureStderr(t testing.TB, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr-*.txt")
	if err != nil {
		t.Fatalf("testutil: create stderr capture file: %v", err)
	}
	orig := os.Stderr
	os.Stderr = f
	defer func() { os.Stderr = orig }()

	fn()

	if err := f.Close(); err != nil {
		t.Fatalf("testutil: close stderr capture file: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("testutil: read stderr capture file: %v", err)
	}
	return string(data)
}

// CaptureBoth runs fn while capturing both os.Stdout and os.Stderr.
func CaptureBoth(t testing.TB, fn func()) (string, string) {
	t.Helper()
	dir := t.TempDir()
	fOut, err := os.CreateTemp(dir, "stdout-*.txt")
	if err != nil {
		t.Fatalf("testutil: create stdout capture file: %v", err)
	}
	fErr, err := os.CreateTemp(dir, "stderr-*.txt")
	if err != nil {
		t.Fatalf("testutil: create stderr capture file: %v", err)
	}
	origOut := os.Stdout
	origErr := os.Stderr
	os.Stdout = fOut
	os.Stderr = fErr
	defer func() {
		os.Stdout = origOut
		os.Stderr = origErr
	}()

	fn()

	_ = fOut.Close()
	_ = fErr.Close()

	outData, _ := os.ReadFile(fOut.Name())
	errData, _ := os.ReadFile(fErr.Name())
	return string(outData), string(errData)
}

// CapturePipeStdout runs fn while redirecting os.Stdout through an os.Pipe
// drained concurrently in a background goroutine.
func CapturePipeStdout(t testing.TB, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("testutil: pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&buf, r)
	}()

	fn()

	_ = w.Close()
	<-done
	_ = r.Close()
	os.Stdout = orig
	return buf.String()
}
