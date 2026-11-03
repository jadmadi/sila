package assess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCanaryAdversarialDiscrimination stress tests canary discrimination under various binary failure modes.
func TestCanaryAdversarialDiscrimination(t *testing.T) {
	contract := &StrippedContract{
		Slug:         "challenge-canary",
		Requirements: []string{"R1", "R2"},
	}

	t.Run("missing binary entirely fails immediately", func(t *testing.T) {
		tmp := t.TempDir()
		sandbox := &BlindfoldSandbox{
			Dir:      tmp,
			homeDir:  tmp,
			cacheDir: tmp,
			CleanEnv: []string{"PATH=/nonexistent/path/never/found"},
			Contract: contract,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		trial, err := DispatchDoubleBlindTrial(ctx, sandbox, contract, func() (bool, error) {
			return true, nil
		})
		if err == nil {
			t.Fatalf("expected DispatchDoubleBlindTrial to fail on missing binary, got trial: %+v", trial)
		}

		passed, err := EvaluateCanaryMutation(ctx, sandbox, nil)
		if err == nil || passed {
			t.Fatalf("expected EvaluateCanaryMutation to fail on missing binary, got passed=%t err=%v", passed, err)
		}
	})

	t.Run("broken binary that exits 1 on --help fails immediately", func(t *testing.T) {
		tmp := t.TempDir()
		binDir := filepath.Join(tmp, "bin")
		_ = os.MkdirAll(binDir, 0755)

		// Create mock script for 'sila' that always exits 1
		script := "#!/bin/sh\nexit 1\n"
		silaPath := filepath.Join(binDir, "sila")
		if err := os.WriteFile(silaPath, []byte(script), 0755); err != nil {
			t.Fatalf("write mock binary: %v", err)
		}

		sandbox := &BlindfoldSandbox{
			Dir:      tmp,
			homeDir:  tmp,
			cacheDir: tmp,
			CleanEnv: []string{fmt.Sprintf("PATH=%s:/bin:/usr/bin", binDir)},
			Contract: contract,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		trial, err := DispatchDoubleBlindTrial(ctx, sandbox, contract, func() (bool, error) {
			return true, nil
		})
		if err == nil {
			t.Fatalf("expected DispatchDoubleBlindTrial to fail on broken binary (exit 1), got trial: %+v", trial)
		}

		passed, err := EvaluateCanaryMutation(ctx, sandbox, nil)
		if err == nil || passed {
			t.Fatalf("expected EvaluateCanaryMutation to fail on broken binary (exit 1), got passed=%t err=%v", passed, err)
		}
	})

	t.Run("broken binary that crashes/segfaults fails immediately", func(t *testing.T) {
		tmp := t.TempDir()
		binDir := filepath.Join(tmp, "bin")
		_ = os.MkdirAll(binDir, 0755)

		// Create mock script that simulates a crash (exit 139)
		script := "#!/bin/sh\necho 'Segmentation fault' >&2\nexit 139\n"
		silaPath := filepath.Join(binDir, "sila")
		if err := os.WriteFile(silaPath, []byte(script), 0755); err != nil {
			t.Fatalf("write mock binary: %v", err)
		}

		sandbox := &BlindfoldSandbox{
			Dir:      tmp,
			homeDir:  tmp,
			cacheDir: tmp,
			CleanEnv: []string{fmt.Sprintf("PATH=%s:/bin:/usr/bin", binDir)},
			Contract: contract,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		trial, err := DispatchDoubleBlindTrial(ctx, sandbox, contract, func() (bool, error) {
			return true, nil
		})
		if err == nil {
			t.Fatalf("expected failure on crashing binary, got: %+v", trial)
		}

		passed, err := EvaluateCanaryMutation(ctx, sandbox, nil)
		if err == nil || passed {
			t.Fatalf("expected failure on crashing binary, got passed=%t err=%v", passed, err)
		}
	})

	t.Run("sycophantic binary is caught and sensitivity score is 0", func(t *testing.T) {
		tmp := t.TempDir()
		binDir := filepath.Join(tmp, "bin")
		_ = os.MkdirAll(binDir, 0755)

		// Sycophantic mock: always exits 0 and prints impossible token when challenged
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"--help\" ]; then exit 0; fi\n" +
			"echo 'DEFINITIVELY_IMPOSSIBLE_TOKEN_CANARY_XYZ DEFINITIVELY_IMPOSSIBLE_TOKEN_XYZ'\n" +
			"exit 0\n"

		silaPath := filepath.Join(binDir, "sila")
		if err := os.WriteFile(silaPath, []byte(script), 0755); err != nil {
			t.Fatalf("write mock binary: %v", err)
		}

		sandbox := &BlindfoldSandbox{
			Dir:      tmp,
			homeDir:  tmp,
			cacheDir: tmp,
			CleanEnv: []string{fmt.Sprintf("PATH=%s:/bin:/usr/bin", binDir)},
			Contract: contract,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		trial, err := DispatchDoubleBlindTrial(ctx, sandbox, contract, func() (bool, error) {
			return true, nil
		})
		if err != nil {
			t.Fatalf("DispatchDoubleBlindTrial error: %v", err)
		}

		if !trial.SycophancyDetected {
			t.Fatal("SECURITY FLAW: failed to detect sycophancy when binary passes impossible canary!")
		}
		if trial.CanaryCaught {
			t.Fatal("CanaryCaught should be false when mutant passed")
		}
		if trial.SensitivityScore != 0.0 {
			t.Fatalf("expected SensitivityScore 0.0, got %f", trial.SensitivityScore)
		}

		// EvaluateCanaryMutation should also fail because mutant passed
		passed, err := EvaluateCanaryMutation(ctx, sandbox, nil)
		if err == nil || passed {
			t.Fatalf("expected EvaluateCanaryMutation to fail on sycophantic binary, got passed=%t err=%v", passed, err)
		}
	})

	t.Run("discriminating genuine binary catches mutant with sensitivity 1.0", func(t *testing.T) {
		tmp := t.TempDir()
		binDir := filepath.Join(tmp, "bin")
		_ = os.MkdirAll(binDir, 0755)

		// Authentic mock: passes --help (exit 0), but rejects nonexistent goal (exit 1)
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"--help\" ]; then exit 0; fi\n" +
			"echo 'Error: goal nonexistent-goal-should-never-succeed-challenge-canary not found' >&2\n" +
			"exit 1\n"

		silaPath := filepath.Join(binDir, "sila")
		if err := os.WriteFile(silaPath, []byte(script), 0755); err != nil {
			t.Fatalf("write mock binary: %v", err)
		}

		sandbox := &BlindfoldSandbox{
			Dir:      tmp,
			homeDir:  tmp,
			cacheDir: tmp,
			CleanEnv: []string{fmt.Sprintf("PATH=%s:/bin:/usr/bin", binDir)},
			Contract: contract,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		trial, err := DispatchDoubleBlindTrial(ctx, sandbox, contract, func() (bool, error) {
			return true, nil
		})
		if err != nil {
			t.Fatalf("DispatchDoubleBlindTrial unexpected error: %v", err)
		}

		if trial.SycophancyDetected {
			t.Fatal("unexpected sycophancy detected for authentic binary")
		}
		if !trial.CanaryCaught {
			t.Fatal("expected canary to be caught by authentic binary")
		}
		if trial.SensitivityScore != 1.0 {
			t.Fatalf("expected SensitivityScore 1.0, got %f", trial.SensitivityScore)
		}

		passed, err := EvaluateCanaryMutation(ctx, sandbox, nil)
		if err != nil || !passed {
			t.Fatalf("expected EvaluateCanaryMutation to pass for authentic binary, got passed=%t err=%v", passed, err)
		}
	})

	t.Run("supplied probe suite exercises contract mutation discrimination", func(t *testing.T) {
		tmp := t.TempDir()
		binDir := filepath.Join(tmp, "bin")
		_ = os.MkdirAll(binDir, 0755)

		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"--help\" ]; then exit 0; fi\n" +
			"echo 'GENUINE_CONTRACT_OUTPUT_XYZ'\n" +
			"exit 0\n"

		silaPath := filepath.Join(binDir, "sila")
		if err := os.WriteFile(silaPath, []byte(script), 0755); err != nil {
			t.Fatalf("write mock binary: %v", err)
		}

		sandbox := &BlindfoldSandbox{
			Dir:      tmp,
			homeDir:  tmp,
			cacheDir: tmp,
			CleanEnv: []string{fmt.Sprintf("PATH=%s:/bin:/usr/bin", binDir)},
			Contract: contract,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		probes := []Probe{
			{
				ID:                  "PROBE-HAP-001",
				Name:                "Happy Path Probe",
				Type:                ProbeHappyPath,
				Surface:             SurfaceCLIBinary,
				Command:             []string{"sila", "status"},
				ExpectedExitCode:    0,
				ExpectedOutputRegex: "GENUINE_CONTRACT_OUTPUT_XYZ",
			},
		}

		// Authentic candidate: satisfies genuine probe, fails mutant probe -> canary passed!
		passed, err := EvaluateCanaryMutation(ctx, sandbox, probes)
		if err != nil || !passed {
			t.Fatalf("expected EvaluateCanaryMutation with probes to pass for authentic binary, got passed=%t err=%v", passed, err)
		}
	})
}
