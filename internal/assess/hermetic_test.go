package assess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
)

func TestBwrapCommandConstruction(t *testing.T) {
	sandbox := "/tmp/test-sandbox"
	home := "/tmp/test-home"
	cache := "/tmp/test-cache"
	cleanEnv := []string{"PATH=/bin:/usr/bin", "FOO=bar"}
	cmd := []string{"echo", "hello"}

	args := BuildBwrapArgs(sandbox, home, cache, cleanEnv, cmd)

	expectedArgs := []string{
		"--unshare-all",
		"--unshare-net",
		"--clearenv",
		"--proc", "/proc",
		"--dev", "/dev",
		"--dir", "/tmp",
		"--ro-bind", "/usr", "/usr",
		"--ro-bind-try", "/bin", "/bin",
		"--ro-bind-try", "/lib", "/lib",
		"--ro-bind-try", "/lib64", "/lib64",
		"--ro-bind-try", "/etc", "/etc",
		"--bind", sandbox, sandbox,
		"--chdir", sandbox,
		"--bind", home, home,
		"--bind", cache, cache,
		"--setenv", "PATH", "/bin:/usr/bin",
		"--setenv", "FOO", "bar",
		"--",
		"echo", "hello",
	}

	if len(args) != len(expectedArgs) {
		t.Fatalf("args length = %d, want %d: %v", len(args), len(expectedArgs), args)
	}
	for i, arg := range args {
		if arg != expectedArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, arg, expectedArgs[i])
		}
	}
}

func TestExecuteInNamespaceDirect(t *testing.T) {
	t.Setenv("SILA_DISABLE_BWRAP", "1")
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cache := filepath.Join(tmp, "cache")
	sandboxDir := filepath.Join(tmp, "sandbox")
	_ = os.MkdirAll(home, 0755)
	_ = os.MkdirAll(cache, 0755)
	_ = os.MkdirAll(sandboxDir, 0755)

	sandbox := &BlindfoldSandbox{
		Dir:      sandboxDir,
		homeDir:  home,
		cacheDir: cache,
		CleanEnv: []string{"PATH=" + os.Getenv("PATH"), "TEST_VAR=isolated"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := ExecuteInNamespace(ctx, sandbox, []string{"echo", "hermetic-probe"}, nil, "")
	if err != nil {
		t.Fatalf("ExecuteInNamespace failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0, stderr=%s", res.ExitCode, res.Stderr)
	}
	if res.Stdout == "" {
		t.Error("expected non-empty stdout from namespace command")
	}
}

func TestBwrapHostNamespaceIsolation(t *testing.T) {
	if !IsBubblewrapAvailable() {
		t.Skip("bubblewrap (bwrap) not installed, skipping namespace isolation test")
	}

	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cache := filepath.Join(tmp, "cache")
	sandboxDir := filepath.Join(tmp, "sandbox")
	_ = os.MkdirAll(home, 0755)
	_ = os.MkdirAll(cache, 0755)
	_ = os.MkdirAll(sandboxDir, 0755)

	sandbox := &BlindfoldSandbox{
		Dir:      sandboxDir,
		homeDir:  home,
		cacheDir: cache,
		CleanEnv: []string{"PATH=/bin:/usr/bin"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Verify host paths outside sandbox such as /mnt cannot be accessed
	res, err := ExecuteInNamespace(ctx, sandbox, []string{"ls", "/mnt"}, nil, "")
	if err == nil && res.ExitCode == 0 {
		t.Errorf("expected failure accessing host /mnt inside isolated bwrap, got exit code %d: %s", res.ExitCode, res.Stdout)
	}
}

func TestScrubEightChannels(t *testing.T) {
	tmp := t.TempDir()
	sandboxDir := filepath.Join(tmp, "sandbox")
	homeDir := filepath.Join(tmp, "home")
	cacheDir := filepath.Join(tmp, "cache")
	_ = os.MkdirAll(sandboxDir, 0755)
	_ = os.MkdirAll(homeDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	// Create artifacts representing the 8 leakage channels
	scratchDir := filepath.Join(sandboxDir, "scratch")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.WriteFile(filepath.Join(scratchDir, "note.txt"), []byte("scratch note"), 0644)

	transcriptDir := filepath.Join(sandboxDir, ".system_generated")
	_ = os.MkdirAll(transcriptDir, 0755)
	_ = os.WriteFile(filepath.Join(transcriptDir, "transcript.jsonl"), []byte("{}"), 0644)

	historyFile := filepath.Join(homeDir, ".bash_history")
	_ = os.WriteFile(historyFile, []byte("ls -la"), 0644)

	defectFile := filepath.Join(sandboxDir, "defect-dossier.json")
	_ = os.WriteFile(defectFile, []byte("{}"), 0644)

	targetFile := filepath.Join(sandboxDir, "code.go")
	_ = os.WriteFile(targetFile, []byte("package main"), 0644)

	fixedEpoch := time.Unix(1700000000, 0).UTC()
	err := ScrubEightChannels(sandboxDir, homeDir, cacheDir, fixedEpoch)
	if err != nil {
		t.Fatalf("ScrubEightChannels failed: %v", err)
	}

	// Verify channel neutralization
	if _, err := os.Stat(scratchDir); !os.IsNotExist(err) {
		t.Error("scratch directory was not scrubbed")
	}
	if _, err := os.Stat(transcriptDir); !os.IsNotExist(err) {
		t.Error("transcripts were not scrubbed")
	}
	if _, err := os.Stat(historyFile); !os.IsNotExist(err) {
		t.Error("history file was not scrubbed")
	}
	if _, err := os.Stat(defectFile); !os.IsNotExist(err) {
		t.Error("defect file was not scrubbed")
	}

	// Verify timestamp normalization
	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("stat target file: %v", err)
	}
	if !info.ModTime().Equal(fixedEpoch) {
		t.Errorf("file modtime = %v, want %v", info.ModTime(), fixedEpoch)
	}
}

func TestCanaryMutantGeneratorAndTrial(t *testing.T) {
	if _, err := exec.LookPath("sila"); err != nil {
		t.Skip("sila binary not on PATH; canary trial requires a candidate binary")
	}
	contract := &StrippedContract{
		Slug:         "test-goal",
		Requirements: []string{"R1", "R2"},
	}

	mutant := GenerateCanaryMutant(contract)
	if mutant.ID != "CANARY-MUTANT-001" {
		t.Errorf("mutant ID = %q, want CANARY-MUTANT-001", mutant.ID)
	}
	if mutant.ExpectedExitCode != 0 {
		t.Errorf("mutant ExpectedExitCode = %d, want 0", mutant.ExpectedExitCode)
	}

	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	_ = os.MkdirAll(binDir, 0755)
	if hostSila, err := exec.LookPath("sila"); err == nil {
		if data, err := os.ReadFile(hostSila); err == nil {
			_ = os.WriteFile(filepath.Join(binDir, "sila"), data, 0755)
		}
	}

	sandbox := &BlindfoldSandbox{
		Dir:      tmp,
		homeDir:  tmp,
		cacheDir: tmp,
		CleanEnv: []string{"PATH=" + binDir + ":/bin:/usr/bin"},
	}

	ctx := context.Background()
	trial, err := DispatchDoubleBlindTrial(ctx, sandbox, contract, func() (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("DispatchDoubleBlindTrial failed: %v", err)
	}

	if !trial.GenuineCandidatePassed {
		t.Error("expected genuine candidate to pass")
	}
	if !trial.CanaryCaught {
		t.Error("expected canary mutant to be caught")
	}
	if trial.SycophancyDetected {
		t.Error("sycophancy falsely detected")
	}
	if trial.SensitivityScore != 1.0 {
		t.Errorf("sensitivity score = %f, want 1.0", trial.SensitivityScore)
	}
}

func TestCanaryFailsWhenBinaryMissingOrBroken(t *testing.T) {
	contract := &StrippedContract{
		Slug:         "test-goal-broken",
		Requirements: []string{"R1"},
	}

	tmp := t.TempDir()
	sandbox := &BlindfoldSandbox{
		Dir:      tmp,
		homeDir:  tmp,
		cacheDir: tmp,
		CleanEnv: []string{"PATH=/nonexistent/path/never/found"},
	}

	ctx := context.Background()

	// 1. DispatchDoubleBlindTrial must fail when binary is missing
	_, err := DispatchDoubleBlindTrial(ctx, sandbox, contract, func() (bool, error) {
		return true, nil
	})
	if err == nil {
		t.Errorf("expected DispatchDoubleBlindTrial to fail when binary is missing")
	}

	// 2. EvaluateCanaryMutation must fail when binary is missing
	passed, err := EvaluateCanaryMutation(ctx, sandbox, nil)
	if err == nil || passed {
		t.Errorf("expected EvaluateCanaryMutation to fail when binary is missing; got passed=%t, err=%v", passed, err)
	}
}

func TestCCDSIScoring(t *testing.T) {
	probes := []ProbeResult{
		{ProbeID: "P1", RequirementID: "R1", Type: ProbeContractNegative, Passed: true},
		{ProbeID: "P2", RequirementID: "R1", Type: ProbeContractNegative, Passed: true},
		{ProbeID: "P3", RequirementID: "R2", Type: ProbeContractBoundary, Passed: true},
		{ProbeID: "P4", RequirementID: "R2", Type: ProbeContractBoundary, Passed: true},
		{ProbeID: "P5", RequirementID: "R3", Type: ProbeContractConcurrency, Passed: true},
		{ProbeID: "P6", RequirementID: "R3", Type: ProbeHappyPath, Passed: true},
	}

	reqs := []string{"R1", "R2", "R3"}
	rep := CalculateCCDSI(probes, reqs)

	if rep.TotalProbes != 6 {
		t.Errorf("TotalProbes = %d, want 6", rep.TotalProbes)
	}
	if rep.PassedProbes != 6 {
		t.Errorf("PassedProbes = %d, want 6", rep.PassedProbes)
	}
	if rep.CCDSIScore < 0.40 {
		t.Errorf("CCDSIScore = %f, want >= 0.40", rep.CCDSIScore)
	}
	if rep.ReqCoverage < 0.50 {
		t.Errorf("ReqCoverage = %f, want >= 0.50", rep.ReqCoverage)
	}
	if !rep.GatingPassed {
		t.Errorf("GatingPassed = false, reason: %s", rep.FailureReason)
	}
}

func TestReceiptCryptographicVerification(t *testing.T) {
	pub, priv, err := GenerateAuditorKeypair()
	if err != nil {
		t.Fatalf("GenerateAuditorKeypair failed: %v", err)
	}

	receipt := &goals.AuditReceipt{
		Version:            "sila/audit-receipt/v1",
		GoalSlug:           "hermetic-blindfold-namespace-audits",
		CommitSHA:          "abc1234567890",
		SpecHash:           "fedcba0987654",
		SandboxNonce:       "nonce-123456",
		CandidateSelection: "A",
		BlindfoldVerified:  true,
		CanaryPassed:       true,
		CCDSIScore:         0.83,
		ReqCoverage:        1.0,
		ProbesExecuted:     6,
		ProbesPassed:       6,
		ProbeManifestHash:  "manifest-hash-1234",
		CertifiedAt:        time.Now().UTC(),
	}

	if err := SignAuditReceipt(receipt, priv); err != nil {
		t.Fatalf("SignAuditReceipt failed: %v", err)
	}

	valid, err := VerifyAuditReceipt(receipt)
	if err != nil || !valid {
		t.Fatalf("VerifyAuditReceipt failed: %v", err)
	}

	// 1. Verify tampering detection on CCDSIScore (value 0.85 is above floor 0.40, testing cryptographic signature failure)
	tamperedScore := *receipt
	tamperedScore.CCDSIScore = 0.85
	if _, err := VerifyAuditReceipt(&tamperedScore); err == nil {
		t.Error("expected verification failure on tampered CCDSI score")
	}

	// 2. Verify tampering detection on GoalSlug
	tamperedSlug := *receipt
	tamperedSlug.GoalSlug = "tampered-goal-slug"
	if _, err := VerifyAuditReceipt(&tamperedSlug); err == nil {
		t.Error("expected verification failure on tampered goal slug")
	}

	// 3. Verify tampering detection on CommitSHA
	tamperedCommit := *receipt
	tamperedCommit.CommitSHA = "0000000000000000"
	if _, err := VerifyAuditReceipt(&tamperedCommit); err == nil {
		t.Error("expected verification failure on tampered commit SHA")
	}

	// 4. Verify tampering detection on ReqCoverage
	tamperedCoverage := *receipt
	tamperedCoverage.ReqCoverage = 0.95
	if _, err := VerifyAuditReceipt(&tamperedCoverage); err == nil {
		t.Error("expected verification failure on tampered requirement coverage")
	}

	// 5. Verify tampering detection on ProbesExecuted
	tamperedProbes := *receipt
	tamperedProbes.ProbesExecuted = 10
	tamperedProbes.ProbesPassed = 10
	if _, err := VerifyAuditReceipt(&tamperedProbes); err == nil {
		t.Error("expected verification failure on tampered probe count")
	}

	// 6. Verify tampering detection on CertifiedAt
	tamperedTime := *receipt
	tamperedTime.CertifiedAt = receipt.CertifiedAt.Add(time.Hour)
	if _, err := VerifyAuditReceipt(&tamperedTime); err == nil {
		t.Error("expected verification failure on tampered timestamp")
	}

	// 7. Verify unsigned receipt is rejected
	unsignedReceipt := *receipt
	unsignedReceipt.AuditorSignature = ""
	if _, err := VerifyAuditReceipt(&unsignedReceipt); err == nil {
		t.Error("expected verification failure on unsigned receipt")
	}
	unsignedReceipt2 := *receipt
	unsignedReceipt2.AuditorPubKey = ""
	if _, err := VerifyAuditReceipt(&unsignedReceipt2); err == nil {
		t.Error("expected verification failure on receipt missing public key")
	}

	// 8. Verify malformed signature is rejected
	malformedSig := *receipt
	malformedSig.AuditorSignature = "malformed-base64-not-64-bytes"
	if _, err := VerifyAuditReceipt(&malformedSig); err == nil {
		t.Error("expected verification failure on malformed signature")
	}

	_ = pub
}
