package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func setupCrewCLIRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmds := [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	}
	for _, args := range cmds {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return dir
}

func captureCrewOutput(t *testing.T, fn func()) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "crew-output-*.txt")
	if err != nil {
		t.Fatalf("create capture file: %v", err)
	}
	origStdout := os.Stdout
	origStderr := os.Stderr
	os.Stdout = f
	os.Stderr = f
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	fn()

	if err := f.Close(); err != nil {
		t.Fatalf("close capture file: %v", err)
	}
	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read capture file: %v", err)
	}
	return string(data)
}

func TestRunCrewListEmpty(t *testing.T) {
	dir := setupCrewCLIRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureCrewOutput(t, func() {
		runCrew(ui, false, false, []string{"list"})
	})
	if !strings.Contains(out, "No active crewmates") {
		t.Errorf("expected empty state message, got %q", out)
	}
}

func TestRunCrewSuperviseEmpty(t *testing.T) {
	dir := setupCrewCLIRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	out := captureCrewOutput(t, func() {
		runCrew(ui, false, false, []string{"supervise"})
	})
	if !strings.Contains(out, "No crewmates to supervise") {
		t.Errorf("expected empty supervise message, got %q", out)
	}
}

func TestRunCrewNoArgs(t *testing.T) {
	dir := setupCrewCLIRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	if os.Getenv("TEST_CREW_NOARGS") == "1" {
		ui := render.New(os.Stdout, true)
		runCrew(ui, false, false, []string{})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunCrewNoArgs")
	cmd.Env = append(os.Environ(), "TEST_CREW_NOARGS=1")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit code for no args")
	}
}

func TestRunCrewStatusNotFound(t *testing.T) {
	dir := setupCrewCLIRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	if os.Getenv("TEST_CREW_STATUS_NOTFOUND") == "1" {
		ui := render.New(os.Stdout, true)
		runCrew(ui, false, false, []string{"status", "nonexistent-id"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunCrewStatusNotFound")
	cmd.Env = append(os.Environ(), "TEST_CREW_STATUS_NOTFOUND=1")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit code for nonexistent crewmate")
	}
}

func TestRunCrewSpawnMissingArgs(t *testing.T) {
	dir := setupCrewCLIRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	if os.Getenv("TEST_CREW_SPAWN_MISSING") == "1" {
		ui := render.New(os.Stdout, true)
		runCrew(ui, false, false, []string{"spawn", "devin"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunCrewSpawnMissingArgs")
	cmd.Env = append(os.Environ(), "TEST_CREW_SPAWN_MISSING=1")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		t.Error("expected non-zero exit code for missing goal slug")
	}
}
