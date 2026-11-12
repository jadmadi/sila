package assess

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jadmadi/sila/internal/goals"
)

func TestExtractStrippedContract(t *testing.T) {
	tempDir := t.TempDir()
	goalDir := filepath.Join(tempDir, "goals", "mock-feature")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir goalDir: %v", err)
	}

	goalContent := `# Goal: Mock Feature

## Chain of Custody (Isnad)
- **Registered By**: @author | Session: sess-1 | Date: 2026-10-03
- **Implemented By**: @dev | Session: sess-2 | Date: 2026-10-03

## Goal Description
Provide an epistemic mock capability for testing.

## Target Surface & Contracts
- **CLI Commands**: sila mock run
- **MCP Tools**: mock_tool

## Files to Touch
- internal/assess/mock.go
- cmd/sila/mock.go

## Strict Acceptance Criteria (Non-Negotiable)
1. Must reject invalid slugs with exit code 1.
2. Must handle boundary conditions cleanly.
3. Must support JSON output formatting.
`
	goalMd := filepath.Join(goalDir, "goal.md")
	if err := os.WriteFile(goalMd, []byte(goalContent), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}

	contract, err := ExtractStrippedContract(goalMd)
	if err != nil {
		t.Fatalf("ExtractStrippedContract failed: %v", err)
	}

	if contract.Slug != "mock-feature" {
		t.Errorf("expected slug 'mock-feature', got %q", contract.Slug)
	}
	if contract.Title != "Mock Feature" {
		t.Errorf("expected title 'Mock Feature', got %q", contract.Title)
	}
	if len(contract.AcceptanceCriteria) != 3 {
		t.Errorf("expected 3 acceptance criteria, got %d", len(contract.AcceptanceCriteria))
	}
	if len(contract.FilesToTouch) != 2 {
		t.Errorf("expected 2 files to touch, got %d", len(contract.FilesToTouch))
	}
	if contract.SpecHash == "" {
		t.Errorf("expected non-empty SpecHash")
	}
}

func TestProvisionBlindfoldSandbox(t *testing.T) {
	tempDir := t.TempDir()

	// Create dummy files and subdirs
	_ = os.MkdirAll(filepath.Join(tempDir, "pkg", "core"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "core", "core.go"), []byte("package core\n"), 0644)

	// Add files that must be excluded by the blindfold boundary
	_ = os.MkdirAll(filepath.Join(tempDir, ".git"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, ".git", "config"), []byte("[core]\n"), 0644)
	_ = os.MkdirAll(filepath.Join(tempDir, ".agents"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, ".agents", "secret.md"), []byte("secret developer thoughts\n"), 0644)
	_ = os.MkdirAll(filepath.Join(tempDir, "scratch"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "scratch", "draft.txt"), []byte("scratch note\n"), 0644)

	contract := &StrippedContract{
		Slug:  "test-goal",
		Title: "Test Goal",
	}

	sandbox, err := ProvisionBlindfoldSandbox(tempDir, "test-goal", "", contract)
	if err != nil {
		t.Fatalf("ProvisionBlindfoldSandbox failed: %v", err)
	}
	defer sandbox.Close()

	if sandbox.BlindfoldVerified != IsBubblewrapAvailable() {
		t.Errorf("expected BlindfoldVerified to match IsBubblewrapAvailable (%t), got %t", IsBubblewrapAvailable(), sandbox.BlindfoldVerified)
	}

	// 1. Verify confidential dirs were excluded
	if _, err := os.Stat(filepath.Join(sandbox.Dir, ".agents")); !os.IsNotExist(err) {
		t.Errorf("expected .agents to be excluded from sandbox")
	}
	if _, err := os.Stat(filepath.Join(sandbox.Dir, "scratch")); !os.IsNotExist(err) {
		t.Errorf("expected scratch to be excluded from sandbox")
	}

	// 2. Verify synthetic git repository was initialized
	gitStatusCmd := exec.Command("git", "status", "--porcelain")
	gitStatusCmd.Dir = sandbox.Dir
	out, err := gitStatusCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status in sandbox failed: %v: %s", err, string(out))
	}
	if len(strings.TrimSpace(string(out))) != 0 {
		t.Errorf("expected clean git status in sandbox, got: %s", string(out))
	}

	// 3. Verify timestamp normalization (@1700000000)
	coreFile := filepath.Join(sandbox.Dir, "pkg", "core", "core.go")
	info, err := os.Stat(coreFile)
	if err != nil {
		t.Fatalf("stat core.go: %v", err)
	}
	expectedEpoch := time.Unix(1700000000, 0).UTC()
	if !info.ModTime().UTC().Equal(expectedEpoch) {
		t.Errorf("expected mtime %v, got %v", expectedEpoch, info.ModTime().UTC())
	}
}

func TestProvisionBlindfoldSandbox_FailClosedOnInvalidCommit(t *testing.T) {
	tempDir := t.TempDir()
	contract := &StrippedContract{Slug: "fail-closed", Title: "Fail Closed"}
	_, err := ProvisionBlindfoldSandbox(tempDir, "fail-closed", "invalid-nonexistent-commit-sha", contract)
	if err == nil {
		t.Fatal("expected ProvisionBlindfoldSandbox to fail closed on invalid commit SHA, got nil error")
	}
}

func TestSandbox_DegradedIsolation(t *testing.T) {
	tempDir := t.TempDir()
	contract := &StrippedContract{Slug: "degraded", Title: "Degraded Isolation"}
	sandbox, err := ProvisionBlindfoldSandbox(tempDir, "degraded", "", contract)
	if err != nil {
		t.Fatalf("ProvisionBlindfoldSandbox failed: %v", err)
	}
	defer sandbox.Close()

	if !IsBubblewrapAvailable() && sandbox.BlindfoldVerified {
		t.Errorf("BlindfoldVerified must be false when bwrap is absent")
	}

	probe := Probe{
		ID:               "DEGRADED-001",
		Name:             "Fallback Probe",
		Type:             ProbeHappyPath,
		Surface:          SurfaceCLIBinary,
		Command:          []string{"echo", "degraded"},
		ExpectedExitCode: 0,
	}

	res := RunProbe(context.Background(), sandbox, probe)
	if !res.UsedBwrap && sandbox.BlindfoldVerified {
		t.Errorf("BlindfoldVerified must be false when probe ran without bwrap")
	}
}

func TestCalculateCCDSI(t *testing.T) {
	reqs := []string{"R1", "R2", "R3"}

	results := []ProbeResult{
		{ProbeID: "P1", Type: ProbeContractNegative, RequirementID: "R1", Passed: true},
		{ProbeID: "P2", Type: ProbeContractNegative, RequirementID: "R1", Passed: true},
		{ProbeID: "P3", Type: ProbeContractBoundary, RequirementID: "R2", Passed: true},
		{ProbeID: "P4", Type: ProbeContractBoundary, RequirementID: "R2", Passed: true},
		{ProbeID: "P5", Type: ProbeContractConcurrency, RequirementID: "R3", Passed: true},
		{ProbeID: "P6", Type: ProbeHappyPath, RequirementID: "R3", Passed: true},
	}

	report := CalculateCCDSI(results, reqs)
	if !report.GatingPassed {
		t.Fatalf("expected CCDSI gating to pass, failed with: %s", report.FailureReason)
	}
	if report.TotalProbes != 6 {
		t.Errorf("expected 6 total probes, got %d", report.TotalProbes)
	}
	if report.NegativeProbes != 2 {
		t.Errorf("expected 2 negative probes, got %d", report.NegativeProbes)
	}
	if report.BoundaryProbes != 2 {
		t.Errorf("expected 2 boundary probes, got %d", report.BoundaryProbes)
	}
	if report.ConcurrencyProbes != 1 {
		t.Errorf("expected 1 concurrency probe, got %d", report.ConcurrencyProbes)
	}

	// CCDSI = (2 + 2 + 1) / 6 = 5/6 = 0.833 >= 0.40
	expectedCCDSI := 5.0 / 6.0
	if fmt.Sprintf("%.2f", report.CCDSIScore) != fmt.Sprintf("%.2f", expectedCCDSI) {
		t.Errorf("expected CCDSI score %.2f, got %.2f", expectedCCDSI, report.CCDSIScore)
	}

	// Req coverage = 3/3 = 1.00 >= 0.50
	if report.ReqCoverage != 1.0 {
		t.Errorf("expected ReqCoverage 1.0, got %.2f", report.ReqCoverage)
	}
}

func TestCalculateCCDSIGatingFailures(t *testing.T) {
	reqs := []string{"R1", "R2", "R3"}

	// Test 1: Fewer than 6 probes
	fewProbes := []ProbeResult{
		{ProbeID: "P1", Type: ProbeContractNegative, RequirementID: "R1", Passed: true},
		{ProbeID: "P2", Type: ProbeContractBoundary, RequirementID: "R2", Passed: true},
	}
	rep1 := CalculateCCDSI(fewProbes, reqs)
	if rep1.GatingPassed {
		t.Errorf("expected failure when total probes < 6")
	}

	// Test 2: CCDSI < 0.40 (all happy path probes)
	happyProbes := []ProbeResult{
		{ProbeID: "P1", Type: ProbeHappyPath, RequirementID: "R1", Passed: true},
		{ProbeID: "P2", Type: ProbeHappyPath, RequirementID: "R1", Passed: true},
		{ProbeID: "P3", Type: ProbeHappyPath, RequirementID: "R2", Passed: true},
		{ProbeID: "P4", Type: ProbeHappyPath, RequirementID: "R2", Passed: true},
		{ProbeID: "P5", Type: ProbeHappyPath, RequirementID: "R3", Passed: true},
		{ProbeID: "P6", Type: ProbeHappyPath, RequirementID: "R3", Passed: true},
	}
	rep2 := CalculateCCDSI(happyProbes, reqs)
	if rep2.GatingPassed {
		t.Errorf("expected failure when CCDSI < 0.40")
	}
}

func TestRunProbeHTTP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if r.URL.Path == "/api/protected" {
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"authorized"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	sandbox := &BlindfoldSandbox{}

	// Probe 1: Unauthorized returns 401
	probe1 := Probe{
		ID:             "P1",
		Name:           "Missing Auth",
		Type:           ProbeContractNegative,
		Surface:        SurfaceHTTPEndpoint,
		HTTPMethod:     "GET",
		HTTPPath:       ts.URL + "/api/protected",
		ExpectedStatus: 401,
	}
	res1 := RunProbe(context.Background(), sandbox, probe1)
	if !res1.Passed {
		t.Errorf("expected probe 1 to pass: %s", res1.FailureReason)
	}

	// Probe 2: Health returns 200
	probe2 := Probe{
		ID:             "P2",
		Name:           "Health Check",
		Type:           ProbeHappyPath,
		Surface:        SurfaceHTTPEndpoint,
		HTTPMethod:     "GET",
		HTTPPath:       ts.URL + "/api/health",
		ExpectedStatus: 200,
	}
	res2 := RunProbe(context.Background(), sandbox, probe2)
	if !res2.Passed {
		t.Errorf("expected probe 2 to pass: %s", res2.FailureReason)
	}
}

func TestRunProbeCLI(t *testing.T) {
	t.Setenv("SILA_DISABLE_BWRAP", "1")
	sandbox := &BlindfoldSandbox{
		Dir: t.TempDir(),
		CleanEnv: []string{
			"PATH=" + os.Getenv("PATH"),
			"TERM=dumb",
		},
	}

	probe := Probe{
		ID:                  "CLI-TEST-001",
		Name:                "Echo Command Test",
		Type:                ProbeHappyPath,
		Surface:             SurfaceCLIBinary,
		Command:             []string{"sh", "-c", "echo hello mystery auditor"},
		ExpectedExitCode:    0,
		ExpectedOutputRegex: "mystery auditor",
	}

	res := RunProbe(context.Background(), sandbox, probe)
	if !res.Passed {
		t.Errorf("expected CLI probe to pass: %s", res.FailureReason)
	}
}

func TestExecuteMysteryAudit_PassAndReject(t *testing.T) {
	if !IsBubblewrapAvailable() {
		t.Skip("bubblewrap not available; PASS receipts require blindfold verification")
	}
	tempDir := t.TempDir()
	goalDir := filepath.Join(tempDir, "goals", "test-eval")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	goalContent := `# Goal: Test Evaluation

## Chain of Custody (Isnad)
- **Registered By**: @reg | Session: s1 | Date: 2026-10-03
- **Implemented By**: @impl | Session: s2 | Date: 2026-10-03

## Goal Description
Sample evaluation goal.

## Strict Acceptance Criteria
1. Requirement one must pass.
2. Requirement two must pass.
3. Requirement three must pass.
`
	if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte(goalContent), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}

	// Custom passing probe suite meeting all criteria
	passProbes := []Probe{
		{ID: "P1", Name: "Neg 1", Type: ProbeContractNegative, RequirementID: "R1", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 1"}, ExpectedExitCode: 1},
		{ID: "P2", Name: "Neg 2", Type: ProbeContractNegative, RequirementID: "R1", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 2"}, ExpectedExitCode: 2},
		{ID: "P3", Name: "Bnd 1", Type: ProbeContractBoundary, RequirementID: "R2", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
		{ID: "P4", Name: "Bnd 2", Type: ProbeContractBoundary, RequirementID: "R2", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
		{ID: "P5", Name: "Conc 1", Type: ProbeContractConcurrency, RequirementID: "R3", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
		{ID: "P6", Name: "Happy 1", Type: ProbeHappyPath, RequirementID: "R3", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
	}

	// Initialize git repo in tempDir so git archive can cleanly export candidate commit
	cmdInit := exec.Command("git", "init")
	cmdInit.Dir = tempDir
	_ = cmdInit.Run()
	cmdEmail := exec.Command("git", "config", "user.email", "audit@sila.dev")
	cmdEmail.Dir = tempDir
	_ = cmdEmail.Run()
	cmdName := exec.Command("git", "config", "user.name", "Audit Test")
	cmdName.Dir = tempDir
	_ = cmdName.Run()
	cmdAdd := exec.Command("git", "add", ".")
	cmdAdd.Dir = tempDir
	_ = cmdAdd.Run()
	cmdCommit := exec.Command("git", "commit", "-m", "candidate commit")
	cmdCommit.Dir = tempDir
	_ = cmdCommit.Run()

	out, err := exec.Command("git", "-C", tempDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v", err)
	}
	commitSHA := strings.TrimSpace(string(out))

	cfgPass := MysteryAuditConfig{
		RootDir:        tempDir,
		GoalSlug:       "test-eval",
		CommitSHA:      commitSHA,
		AuditorPersona: "mystery-auditor",
		SessionID:      "test-session",
		SkipCanary:     true,
		CustomProbes:   passProbes,
	}

	evalPass, err := ExecuteMysteryAudit(context.Background(), cfgPass)
	if err != nil {
		t.Fatalf("ExecuteMysteryAudit PASS run failed: %v", err)
	}

	if evalPass.Verdict != "PASS" {
		t.Fatalf("expected verdict PASS, got %q", evalPass.Verdict)
	}
	if evalPass.Receipt == nil {
		t.Fatalf("expected receipt to be generated on PASS")
	}

	// Verify cryptographic signature on receipt
	pubBytes, err := base64.StdEncoding.DecodeString(evalPass.Receipt.AuditorPubKey)
	if err != nil {
		t.Fatalf("decode pubkey: %v", err)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(evalPass.Receipt.AuditorSignature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	digest := FormatReceiptDigest(evalPass.Receipt)
	if !ed25519.Verify(pubBytes, digest, sigBytes) {
		t.Errorf("receipt signature failed verification against canonical digest")
	}
	if ok, err := VerifyAuditReceipt(evalPass.Receipt); !ok || err != nil {
		t.Errorf("VerifyAuditReceipt failed: %v", err)
	}

	// Verify audit-receipt.json exists on disk
	receiptOnDisk, err := goals.GetActiveAuditReceipt(tempDir, "test-eval")
	if err != nil || receiptOnDisk == nil {
		t.Errorf("expected audit-receipt.json to exist on disk: %v", err)
	}

	// Test 2: Failing probe causes REJECT verdict and writes defect dossier
	failProbes := []Probe{
		{ID: "P1", Name: "Failing Probe", Type: ProbeContractNegative, RequirementID: "R1", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 1}, // Mismatch!
		{ID: "P2", Name: "Neg 2", Type: ProbeContractNegative, RequirementID: "R1", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 2"}, ExpectedExitCode: 2},
		{ID: "P3", Name: "Bnd 1", Type: ProbeContractBoundary, RequirementID: "R2", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
		{ID: "P4", Name: "Bnd 2", Type: ProbeContractBoundary, RequirementID: "R2", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
		{ID: "P5", Name: "Conc 1", Type: ProbeContractConcurrency, RequirementID: "R3", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
		{ID: "P6", Name: "Happy 1", Type: ProbeHappyPath, RequirementID: "R3", Surface: SurfaceCLIBinary, Command: []string{"sh", "-c", "exit 0"}, ExpectedExitCode: 0},
	}

	cfgFail := MysteryAuditConfig{
		RootDir:        tempDir,
		GoalSlug:       "test-eval",
		CommitSHA:      commitSHA,
		AuditorPersona: "mystery-auditor",
		SessionID:      "test-session-fail",
		SkipCanary:     true,
		CustomProbes:   failProbes,
	}

	evalFail, err := ExecuteMysteryAudit(context.Background(), cfgFail)
	if err != nil {
		t.Fatalf("ExecuteMysteryAudit REJECT run failed: %v", err)
	}

	if evalFail.Verdict != "REJECT" {
		t.Fatalf("expected verdict REJECT, got %q", evalFail.Verdict)
	}
	if evalFail.Dossier == nil {
		t.Fatalf("expected defect dossier to be generated on REJECT")
	}

	// Verify defect-dossier.json and remediation.md exist
	dossierOnDisk, err := goals.GetActiveDefectDossier(tempDir, "test-eval")
	if err != nil || dossierOnDisk == nil {
		t.Errorf("expected defect-dossier.json to exist on disk: %v", err)
	}

	remediationFile := filepath.Join(goalDir, "remediation.md")
	if _, err := os.Stat(remediationFile); err != nil {
		t.Errorf("expected remediation.md to exist on disk: %v", err)
	}
}

func TestSynthesizeProbes_GoalCoupling(t *testing.T) {
	goalsToTest := []struct {
		slug       string
		wantPrefix string
	}{
		{slug: "cross-session-epistemic-auditing", wantPrefix: "EPIST-"},
		{slug: "gate-silence-sentry-negative-drift", wantPrefix: "SILENCE-"},
		{slug: "hermetic-blindfold-namespace-audits", wantPrefix: "HERMETIC-"},
		{slug: "standard-general-goal", wantPrefix: "CLI-"},
	}

	manifestHashes := make(map[string]string)
	manifestTexts := make(map[string]string)

	reqs := []string{"R1", "R2", "R3", "R4", "R5", "R6"}

	for _, tc := range goalsToTest {
		contract := &StrippedContract{
			Slug:         tc.slug,
			Title:        "Goal " + tc.slug,
			Requirements: reqs,
		}

		probes := SynthesizeProbes(contract, SurfaceCLIBinary, "sila")
		if len(probes) < 6 {
			t.Fatalf("goal %q: expected at least 6 probes, got %d", tc.slug, len(probes))
		}

		var results []ProbeResult
		for _, p := range probes {
			if !strings.HasPrefix(p.ID, tc.wantPrefix) {
				t.Errorf("goal %q: expected probe ID prefix %q, got %q", tc.slug, tc.wantPrefix, p.ID)
			}
			if p.Name == "" {
				t.Errorf("goal %q: probe %s missing name", tc.slug, p.ID)
			}
			if p.RequirementID == "" {
				t.Errorf("goal %q: probe %s missing requirement ID", tc.slug, p.ID)
			}
			if len(p.Command) == 0 {
				t.Errorf("goal %q: probe %s missing command", tc.slug, p.ID)
			}
			results = append(results, ProbeResult{
				ProbeID:       p.ID,
				Type:          p.Type,
				RequirementID: p.RequirementID,
				Passed:        true,
			})
		}

		report := CalculateCCDSI(results, reqs)
		if !report.GatingPassed {
			t.Errorf("goal %q: CCDSI gating failed: %s (score=%.2f, reqs=%.2f)", tc.slug, report.FailureReason, report.CCDSIScore, report.ReqCoverage)
		}

		// Calculate manifest hash
		manifest := CalculateProbeManifest(results)
		hash := CalculateProbeManifestHash(results)

		if prevSlug, exists := manifestTexts[manifest]; exists {
			t.Errorf("manifest collision between goal %q and goal %q:\n%s", tc.slug, prevSlug, manifest)
		}
		if prevSlug, exists := manifestHashes[hash]; exists {
			t.Errorf("manifest hash collision between goal %q and goal %q: hash=%s", tc.slug, prevSlug, hash)
		}

		manifestTexts[manifest] = tc.slug
		manifestHashes[hash] = tc.slug
	}

	if len(manifestHashes) != len(goalsToTest) {
		t.Errorf("expected %d distinct manifest hashes, got %d", len(goalsToTest), len(manifestHashes))
	}
}

func TestRecertifyGoalsWithContractCoupledProbes(t *testing.T) {
	rootDir, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve rootDir: %v", err)
	}

	targets := []struct {
		slug      string
		commitSHA string
		nonce     string
	}{
		{
			slug:      "cross-session-epistemic-auditing",
			commitSHA: "55d82ba78fb86b254d903b8b0ca1f31f21ab4ed8",
			nonce:     "97e692bab24c72ad",
		},
		{
			slug:      "gate-silence-sentry-negative-drift",
			commitSHA: "e205651860e5ea07e00873e94a77b26a6983aa87",
			nonce:     "ae4ed7d871838255",
		},
		{
			slug:      "hermetic-blindfold-namespace-audits",
			commitSHA: "ee1dff1a57bd4580d9bdbaddc6815e66fd475723",
			nonce:     "3a5f451fa2e9d77e",
		},
	}

	for _, tg := range targets {
		goalMdPath := filepath.Join(rootDir, "goals", tg.slug, "goal.md")
		if _, err := os.Stat(goalMdPath); err != nil {
			t.Skipf("private recertified goal fixture not present: %v", err)
		}
		contract, err := ExtractStrippedContract(goalMdPath)
		if err != nil {
			t.Fatalf("ExtractStrippedContract for %q failed: %v", tg.slug, err)
		}

		probes := SynthesizeProbes(contract, SurfaceCLIBinary, "sila")
		if len(probes) < 6 {
			t.Fatalf("goal %q: probe count %d < 6", tg.slug, len(probes))
		}

		var results []ProbeResult
		for _, p := range probes {
			results = append(results, ProbeResult{
				ProbeID:       p.ID,
				Type:          p.Type,
				RequirementID: p.RequirementID,
				Passed:        true,
			})
		}

		ccdsi := CalculateCCDSI(results, contract.Requirements)
		if !ccdsi.GatingPassed {
			t.Fatalf("goal %q: CCDSI failed: %s", tg.slug, ccdsi.FailureReason)
		}

		manifestHash := CalculateProbeManifestHash(results)

		_, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generate keypair for %q: %v", tg.slug, err)
		}

		fixedTime := time.Date(2026, 10, 16, 20, 0, 0, 0, time.UTC)
		receipt := &goals.AuditReceipt{
			Version:            "sila/audit-receipt/v1",
			GoalSlug:           tg.slug,
			CommitSHA:          tg.commitSHA,
			SpecHash:           contract.SpecHash,
			SandboxNonce:       tg.nonce,
			CandidateSelection: "A",
			BlindfoldVerified:  true,
			CanaryPassed:       true,
			CCDSIScore:         ccdsi.CCDSIScore,
			ReqCoverage:        ccdsi.ReqCoverage,
			ProbesExecuted:     ccdsi.TotalProbes,
			ProbesPassed:       ccdsi.PassedProbes,
			ProbeManifestHash:  manifestHash,
			CertifiedAt:        fixedTime,
		}

		if err := SignAuditReceipt(receipt, privKey); err != nil {
			t.Fatalf("SignAuditReceipt for %q: %v", tg.slug, err)
		}

		if ok, err := VerifyAuditReceipt(receipt); !ok || err != nil {
			t.Fatalf("VerifyAuditReceipt failed for %q: %v", tg.slug, err)
		}

		receiptBytes, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			t.Fatalf("marshal receipt for %q: %v", tg.slug, err)
		}
		receiptBytes = append(receiptBytes, '\n')

		tempDir := t.TempDir()
		goalDir := filepath.Join(tempDir, "goals", tg.slug)
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", goalDir, err)
		}
		receiptHyphen := filepath.Join(goalDir, "audit-receipt.json")

		if err := os.WriteFile(receiptHyphen, receiptBytes, 0644); err != nil {
			t.Fatalf("write %s: %v", receiptHyphen, err)
		}

		// Read back via goals.GetActiveAuditReceipt and verify
		loaded, err := goals.GetActiveAuditReceipt(tempDir, tg.slug)
		if err != nil {
			t.Fatalf("GetActiveAuditReceipt for %q: %v", tg.slug, err)
		}
		if loaded == nil {
			t.Fatalf("loaded receipt for %q is nil", tg.slug)
		}
		if err := goals.VerifyAuditReceipt(loaded); err != nil {
			t.Fatalf("goals.VerifyAuditReceipt on loaded receipt failed for %q: %v", tg.slug, err)
		}
	}
}

func TestSynthesizeProbes_DeliverableCommands(t *testing.T) {
	tempDir := t.TempDir()
	goalDir := filepath.Join(tempDir, "goals", "deliverable-probe-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	goalMd := filepath.Join(goalDir, "goal.md")
	goalContent := `# Goal: Deliverable Probe Goal

## Goal Description
Verify deliverable probe synthesis from markdown verification blocks.

## Strict Acceptance Criteria
1. Update ` + "`internal/assess/mystery.go`" + ` and run ` + "`go test ./internal/assess`" + `.
2. Reject invalid inputs cleanly.
3. Maintain CCDSI score >= 0.40.

## Verification Plan
` + "```bash\ncd /workspace/sila/cli && go test -race ./internal/assess/...\nsh -c \"exit 0\"\n```\n"

	if err := os.WriteFile(goalMd, []byte(goalContent), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}

	contract, err := ExtractStrippedContract(goalMd)
	if err != nil {
		t.Fatalf("ExtractStrippedContract failed: %v", err)
	}
	if len(contract.VerificationCommands) < 2 {
		t.Fatalf("expected >= 2 verification commands, got %v", contract.VerificationCommands)
	}
	for _, cmd := range contract.VerificationCommands {
		if cmd == "internal/assess/mystery.go" {
			t.Fatalf("prose file path backtick must not be extracted as command: %v", contract.VerificationCommands)
		}
	}
	if contract.VerificationCommands[0] != "go test ./internal/assess" && contract.VerificationCommands[0] != "go test -race ./internal/assess/..." {
		t.Fatalf("unexpected first verification command: %q", contract.VerificationCommands[0])
	}

	probes := SynthesizeProbes(contract, SurfaceCLIBinary, "sila")
	hasDeliv := false
	var simResults []ProbeResult
	for _, p := range probes {
		if p.ID == "CLI-DELIV-001" {
			hasDeliv = true
		}
		simResults = append(simResults, ProbeResult{
			ProbeID:       p.ID,
			Type:          p.Type,
			RequirementID: p.RequirementID,
			Passed:        true,
		})
	}
	if !hasDeliv {
		t.Fatalf("expected CLI-DELIV-001 in synthesized probes, got %+v", probes)
	}
	ccdsi := CalculateCCDSI(simResults, contract.Requirements)
	if !ccdsi.GatingPassed || ccdsi.CCDSIScore < 0.40 {
		t.Fatalf("expected CCDSI >= 0.40 with deliverable probes, got score=%.3f reason=%s", ccdsi.CCDSIScore, ccdsi.FailureReason)
	}
}

func TestExecuteMysteryAudit_DeliverableFailureRejects(t *testing.T) {
	tempDir := t.TempDir()
	goalDir := filepath.Join(tempDir, "goals", "broken-deliverable-goal")
	if err := os.MkdirAll(goalDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	goalMd := filepath.Join(goalDir, "goal.md")
	goalContent := `# Goal: Broken Deliverable Goal

## Goal Description
Prove that a broken deliverable command in Verification Commands causes REJECT even if generic CLI probes pass.

## Strict Acceptance Criteria
1. Deliverable command must exit 0.
2. Negative probe check.
3. Boundary probe check.

## Verification Commands
` + fmt.Sprintf("```bash\nsh -c \"echo '%s/goals/broken-deliverable-goal failed' >&2; exit 1\"\n```\n", tempDir)

	if err := os.WriteFile(goalMd, []byte(goalContent), 0644); err != nil {
		t.Fatalf("write goal.md: %v", err)
	}

	eval, err := ExecuteMysteryAudit(context.Background(), MysteryAuditConfig{
		RootDir:    tempDir,
		GoalSlug:   "broken-deliverable-goal",
		SkipCanary: true,
	})
	if err != nil {
		t.Fatalf("ExecuteMysteryAudit error: %v", err)
	}
	if eval.Verdict != "REJECT" {
		t.Fatalf("expected Verdict REJECT when deliverable verification command exits 1, got %q", eval.Verdict)
	}
	if eval.Dossier == nil {
		t.Fatalf("expected non-nil DefectDossier on REJECT")
	}
	if strings.Contains(eval.Dossier.StderrSnippet, tempDir) {
		t.Errorf("expected DefectDossier.StderrSnippet to scrub host/temp paths, got %q", eval.Dossier.StderrSnippet)
	}
}

func TestClassifyCanary(t *testing.T) {
	discrimination := fmt.Errorf("%w: mutant passed", ErrCanaryDiscrimination)
	unavailable := fmt.Errorf("%w: candidate binary missing", ErrCanaryUnavailable)
	generic := errors.New("sandbox exploded")

	cases := []struct {
		name       string
		passed     bool
		err        error
		wantStatus string
		wantPart   string
	}{
		{"passed", true, nil, CanaryStatusPassed, ""},
		{"discrimination", false, discrimination, CanaryStatusDiscrimination, "mutant passed"},
		{"unavailable", false, unavailable, CanaryStatusUnavailable, "candidate binary missing"},
		{"error", false, generic, CanaryStatusError, "sandbox exploded"},
		{"false without error", false, nil, CanaryStatusError, "without an error"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, detail := classifyCanary(c.passed, c.err)
			if status != c.wantStatus {
				t.Fatalf("status = %q, want %q", status, c.wantStatus)
			}
			if c.wantPart != "" && !strings.Contains(detail, c.wantPart) {
				t.Fatalf("detail %q does not contain %q", detail, c.wantPart)
			}
		})
	}
}

func TestAuditOutcomeHonesty(t *testing.T) {
	cases := []struct {
		name         string
		ccdsiPassed  bool
		canaryStatus string
		probeFailed  bool
		want         string
	}{
		{"probe failure rejects", true, CanaryStatusPassed, true, VerdictReject},
		{"threshold failure rejects", false, CanaryStatusPassed, false, VerdictReject},
		{"canary discrimination rejects", true, CanaryStatusDiscrimination, false, VerdictReject},
		{"canary unavailable is inconclusive", true, CanaryStatusUnavailable, false, VerdictInconclusive},
		{"canary error is inconclusive", true, CanaryStatusError, false, VerdictInconclusive},
		{"probe failure beats unavailable", true, CanaryStatusUnavailable, true, VerdictReject},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := auditOutcome(c.ccdsiPassed, c.canaryStatus, c.probeFailed); got != c.want {
				t.Fatalf("auditOutcome = %q, want %q", got, c.want)
			}
		})
	}
}
