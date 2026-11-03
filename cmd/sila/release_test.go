package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/render"
)

func TestReleaseCommands(t *testing.T) {
	ui := render.New(os.Stdout, true)
	tempDir := t.TempDir()

	// 1. Snapshot command
	snapPath := filepath.Join(tempDir, "contracts", "mcp-v1.json")
	runRelease(ui, false, false, snapPath, false, false, false, []string{"snapshot"})
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("expected snapshot to exist: %v", err)
	}

	// 2. Metadata command
	metaPath := filepath.Join(tempDir, "release-metadata.json")
	runRelease(ui, false, false, metaPath, false, false, false, []string{"metadata"})
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("expected metadata file to exist: %v", err)
	}

	// 3. Preflight command (Terminal output, offline and allow-dirty)
	termOut := captureStdout(t, func() {
		runRelease(ui, false, false, "", true, true, false, []string{"preflight"})
	})
	if !strings.Contains(termOut, "sila · release preflight") {
		t.Errorf("expected preflight header, got: %s", termOut)
	}
	if !strings.Contains(termOut, "Software Quality Scorecard") {
		t.Errorf("expected scorecard in output, got: %s", termOut)
	}

	// 4. Preflight command (JSON output)
	jsonOut := captureStdout(t, func() {
		runRelease(ui, true, false, "", true, true, false, []string{"preflight"})
	})
	if !strings.Contains(jsonOut, `"grade":`) || !strings.Contains(jsonOut, `"score":`) {
		t.Errorf("expected valid JSON scorecard output, got: %s", jsonOut)
	}

	// 5. Preflight command (TOON output)
	toonOut := captureStdout(t, func() {
		runRelease(ui, false, true, "", true, true, false, []string{"preflight"})
	})
	if !strings.Contains(toonOut, "release_scorecard:") {
		t.Errorf("expected valid TOON scorecard output, got: %s", toonOut)
	}
}

func TestBuildReleasePreflightFailurePropagation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build.sh integration test in short mode")
	}

	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("failed to create bin dir: %v", err)
	}

	mockGoSrc := filepath.Join(tempDir, "mock_go.go")
	mockGoCode := `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
	mode := os.Getenv("MOCK_GO_MODE")
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(0)
	}

	switch args[0] {
	case "env":
		if len(args) > 1 {
			switch args[1] {
			case "GOOS":
				fmt.Println(runtime.GOOS)
				return
			case "GOARCH":
				fmt.Println(runtime.GOARCH)
				return
			case "GOPATH":
				fmt.Println(os.Getenv("GOPATH"))
				return
			}
		}
		os.Exit(0)

	case "run":
		cmdStr := strings.Join(args, " ")
		if strings.Contains(cmdStr, "report-card") {
			if mode == "fail_report_card" {
				fmt.Fprintln(os.Stderr, "report-card failed")
				os.Exit(1)
			}
			fmt.Println("report-card ok")
			return
		}
		if strings.Contains(cmdStr, "coverage") {
			if mode == "coverage_rejected" {
				fmt.Fprintln(os.Stderr, "sila coverage: total coverage 10.0% is below required threshold 80.0%")
				os.Exit(1)
			}
			fmt.Println("✔ Coverage threshold passed")
			return
		}
		return

	case "test":
		var coverFile string
		for _, a := range args[1:] {
			if strings.HasPrefix(a, "-coverprofile=") {
				coverFile = strings.TrimPrefix(a, "-coverprofile=")
			}
		}

		dummyCoverage := "mode: set\ngithub.com/jadmadi/sila/cmd/sila/main.go:10.1,20.2 1 1\n"

		switch mode {
		case "fail_with_partial_coverage":
			if coverFile != "" {
				_ = os.WriteFile(coverFile, []byte(dummyCoverage), 0644)
			}
			fmt.Fprintln(os.Stderr, "--- FAIL: TestDisposableFailureFixture (0.02s)")
			fmt.Fprintln(os.Stderr, "    failure_fixture_test.go:42: simulated test failure with partial profile")
			fmt.Fprintln(os.Stderr, "FAIL")
			os.Exit(7)

		case "fail_without_coverage":
			fmt.Fprintln(os.Stderr, "--- FAIL: TestDisposableFailureNoCoverage (0.01s)")
			fmt.Fprintln(os.Stderr, "    failure_fixture_test.go:19: simulated test failure without profile")
			fmt.Fprintln(os.Stderr, "FAIL")
			os.Exit(7)

		case "empty_coverage":
			if coverFile != "" {
				_ = os.WriteFile(coverFile, []byte(""), 0644)
			}
			fmt.Println("PASS")
			os.Exit(0)

		case "coverage_rejected":
			if coverFile != "" {
				_ = os.WriteFile(coverFile, []byte(dummyCoverage), 0644)
			}
			fmt.Println("PASS")
			os.Exit(0)

		case "success":
			if coverFile != "" {
				_ = os.WriteFile(coverFile, []byte(dummyCoverage), 0644)
			}
			fmt.Println("PASS")
			os.Exit(0)

		default:
			fmt.Fprintf(os.Stderr, "unknown mock go mode: %s\n", mode)
			os.Exit(1)
		}

	case "build":
		var outPath string
		for i := 0; i < len(args); i++ {
			if args[i] == "-o" && i+1 < len(args) {
				outPath = args[i+1]
				break
			}
		}
		if outPath != "" {
			_ = os.MkdirAll(filepath.Dir(outPath), 0755)
			_ = os.WriteFile(outPath, []byte("#!/bin/sh\necho sila-mock-fixture\n"), 0755)
		}
		return
	}
}
`
	if err := os.WriteFile(mockGoSrc, []byte(mockGoCode), 0644); err != nil {
		t.Fatalf("failed to write mock go source: %v", err)
	}

	mockGoBin := filepath.Join(binDir, "go")
	buildCmd := exec.Command("go", "build", "-o", mockGoBin, mockGoSrc)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build mock go: %v: %s", err, out)
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := cwd
	for {
		if _, err := os.Stat(filepath.Join(repoRoot, "build.sh")); err == nil {
			break
		}
		parent := filepath.Dir(repoRoot)
		if parent == repoRoot {
			t.Fatalf("could not find build.sh in any parent directory of %s", cwd)
		}
		repoRoot = parent
	}
	buildSh := filepath.Join(repoRoot, "build.sh")

	cases := []struct {
		name             string
		mode             string
		expectSuccess    bool
		wantOutputSubstr []string
		wantAbsentSubstr []string
		expectArtifact   bool
	}{
		{
			name:          "failing test with partial coverage stops preflight and reveals failure",
			mode:          "fail_with_partial_coverage",
			expectSuccess: false,
			wantOutputSubstr: []string{
				"Error: Test suite failed during release quality preflight.",
				"--- FAIL: TestDisposableFailureFixture",
			},
			wantAbsentSubstr: []string{
				"Quality preflight passed",
				"Built successfully",
			},
			expectArtifact: false,
		},
		{
			name:          "failing test without coverage stops preflight",
			mode:          "fail_without_coverage",
			expectSuccess: false,
			wantOutputSubstr: []string{
				"Error: Test suite failed during release quality preflight.",
				"--- FAIL: TestDisposableFailureNoCoverage",
			},
			wantAbsentSubstr: []string{
				"Quality preflight passed",
				"Built successfully",
			},
			expectArtifact: false,
		},
		{
			name:          "missing or empty coverage profile stops preflight",
			mode:          "empty_coverage",
			expectSuccess: false,
			wantOutputSubstr: []string{
				"Error: Coverage profile is missing or empty.",
			},
			wantAbsentSubstr: []string{
				"Quality preflight passed",
				"Built successfully",
			},
			expectArtifact: false,
		},
		{
			name:          "coverage rejection stops preflight",
			mode:          "coverage_rejected",
			expectSuccess: false,
			wantOutputSubstr: []string{
				"Error: Test coverage requirements rejected.",
			},
			wantAbsentSubstr: []string{
				"Quality preflight passed",
				"Built successfully",
			},
			expectArtifact: false,
		},
		{
			name:          "clean test and valid coverage completes preflight and builds artifact",
			mode:          "success",
			expectSuccess: true,
			wantOutputSubstr: []string{
				"Quality preflight passed",
				"Built successfully",
			},
			wantAbsentSubstr: []string{
				"Error:",
			},
			expectArtifact: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			artifactPath := filepath.Join(tempDir, "artifact-"+tc.mode)
			cmd := exec.Command("bash", buildSh, "--check", "--no-install", "--output", artifactPath)
			cmd.Dir = repoRoot

			newPath := "PATH=" + binDir + string(filepath.ListSeparator) + os.Getenv("PATH")
			customEnv := append(os.Environ(), newPath, "MOCK_GO_MODE="+tc.mode)
			cmd.Env = customEnv

			outBytes, err := cmd.CombinedOutput()
			outStr := string(outBytes)

			if tc.expectSuccess {
				if err != nil {
					t.Fatalf("expected success, got error: %v\nOutput: %s", err, outStr)
				}
			} else {
				if err == nil {
					t.Fatalf("expected command failure, but it succeeded!\nOutput: %s", outStr)
				}
			}

			for _, sub := range tc.wantOutputSubstr {
				if !strings.Contains(outStr, sub) {
					t.Errorf("expected output to contain %q, but got:\n%s", sub, outStr)
				}
			}

			for _, sub := range tc.wantAbsentSubstr {
				if strings.Contains(outStr, sub) {
					t.Errorf("output must NOT contain %q, but got:\n%s", sub, outStr)
				}
			}

			_, statErr := os.Stat(artifactPath)
			hasArtifact := (statErr == nil)
			if tc.expectArtifact && !hasArtifact {
				t.Errorf("expected artifact to exist at %s, but it was not created", artifactPath)
			}
			if !tc.expectArtifact && hasArtifact {
				t.Errorf("artifact must NOT exist at %s on failure, but found file", artifactPath)
			}
		})
	}
}
