package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTestReceiptRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runGit("add", "main.go")
	runGit("commit", "-m", "initial")
	return dir
}

func TestGateGreenReceipt_LifecycleAndInvalidation(t *testing.T) {
	dir := initTestReceiptRepo(t)
	const cmdKey = "go test -race ./..."

	if _, ok := checkGateGreenReceipt(dir, cmdKey, ""); ok {
		t.Fatal("expected initial cache miss before saving receipt")
	}

	covFile := filepath.Join(t.TempDir(), "cov.out")
	if err := os.WriteFile(covFile, []byte("mode: set\n"), 0o600); err != nil {
		t.Fatalf("write cov.out: %v", err)
	}

	saveGateGreenReceipt(dir, cmdKey, "go test -race ./... passed (12 packages)")

	chk, ok := checkGateGreenReceipt(dir, cmdKey, covFile)
	if !ok {
		t.Fatal("expected cache hit on unchanged tree with valid coverage artifact")
	}
	if chk.Status != "pass" || !strings.HasPrefix(chk.Detail, "GATE_SKIPPED: go test -race ./... passed (12 packages)") {
		t.Fatalf("unexpected cached check: %+v", chk)
	}

	// Verify receipt file permissions are 0600
	treeHash, _, gitDir, err := gateTreeStateHash(dir, cmdKey)
	if err != nil {
		t.Fatalf("gateTreeStateHash: %v", err)
	}
	recPath := filepath.Join(gitDir, "sila-gate-receipts", treeHash[:16]+".json")
	fi, err := os.Stat(recPath)
	if err != nil {
		t.Fatalf("stat receipt: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected receipt mode 0600, got %04o", perm)
	}

	cases := []struct {
		name    string
		mutate  func(t *testing.T)
		cmd     string
		covPath string
		wantHit bool
	}{
		{
			name:    "different_command_misses",
			mutate:  func(t *testing.T) {},
			cmd:     "go test ./...",
			covPath: covFile,
			wantHit: false,
		},
		{
			name:    "missing_required_artifact_misses",
			mutate:  func(t *testing.T) {},
			cmd:     cmdKey,
			covPath: filepath.Join(t.TempDir(), "missing.out"),
			wantHit: false,
		},
		{
			name: "untracked_file_invalidates_receipt",
			mutate: func(t *testing.T) {
				if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package main\n"), 0o644); err != nil {
					t.Fatalf("write extra.go: %v", err)
				}
			},
			cmd:     cmdKey,
			covPath: covFile,
			wantHit: false,
		},
		{
			name: "removing_untracked_file_restores_receipt",
			mutate: func(t *testing.T) {
				_ = os.Remove(filepath.Join(dir, "extra.go"))
			},
			cmd:     cmdKey,
			covPath: covFile,
			wantHit: true,
		},
		{
			name: "modified_tracked_file_invalidates_receipt",
			mutate: func(t *testing.T) {
				if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() { println(1) }\n"), 0o644); err != nil {
					t.Fatalf("modify main.go: %v", err)
				}
			},
			cmd:     cmdKey,
			covPath: covFile,
			wantHit: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.mutate(t)
			_, hit := checkGateGreenReceipt(dir, tc.cmd, tc.covPath)
			if hit != tc.wantHit {
				t.Fatalf("checkGateGreenReceipt hit = %v, want %v", hit, tc.wantHit)
			}
		})
	}
}

func TestGateGreenReceipt_EnvBypass(t *testing.T) {
	dir := initTestReceiptRepo(t)
	const cmdKey = "go test ./..."
	saveGateGreenReceipt(dir, cmdKey, "go test ./... passed")

	t.Setenv("SILA_NO_GATE_CACHE", "1")
	if _, ok := checkGateGreenReceipt(dir, cmdKey, ""); ok {
		t.Fatal("expected SILA_NO_GATE_CACHE=1 to bypass green receipt cache")
	}
}

func TestGateExternalTests_UsesGreenReceiptOnUnchangedTree(t *testing.T) {
	dir := initTestReceiptRepo(t)

	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".test_runs\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/testreceipt\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	testSrc := "package main\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestPing(t *testing.T) {\n\tf, err := os.OpenFile(\".test_runs\", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\t_, _ = f.WriteString(\"x\")\n\t_ = f.Close()\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatalf("write main_test.go: %v", err)
	}
	agentsMD := "# AGENTS.md\n\n## Commands\n\n- Test: `go test -count=1 ./...`\n"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsMD), 0o644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	chk1 := gateExternalTests(dir)
	if chk1.Status != "pass" || strings.HasPrefix(chk1.Detail, "GATE_SKIPPED:") {
		t.Fatalf("expected first gateExternalTests run to execute and pass, got %+v", chk1)
	}
	runs1, _ := os.ReadFile(filepath.Join(dir, ".test_runs"))
	if strings.Count(string(runs1), "x") != 1 {
		t.Fatalf("expected 1 test execution, got %d", strings.Count(string(runs1), "x"))
	}

	chk2 := gateExternalTests(dir)
	if chk2.Status != "pass" || !strings.HasPrefix(chk2.Detail, "GATE_SKIPPED:") {
		t.Fatalf("expected second gateExternalTests on unchanged tree to return GATE_SKIPPED, got %+v", chk2)
	}
	runs2, _ := os.ReadFile(filepath.Join(dir, ".test_runs"))
	if strings.Count(string(runs2), "x") != 1 {
		t.Fatalf("expected cached run not to re-execute test command, got %d executions", strings.Count(string(runs2), "x"))
	}

	// Mutate main.go and verify cache invalidates and re-runs
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() { println(99) }\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	chk3 := gateExternalTests(dir)
	if chk3.Status != "pass" || strings.HasPrefix(chk3.Detail, "GATE_SKIPPED:") {
		t.Fatalf("expected third gateExternalTests after edit to re-run, got %+v", chk3)
	}
	runs3, _ := os.ReadFile(filepath.Join(dir, ".test_runs"))
	if strings.Count(string(runs3), "x") != 2 {
		t.Fatalf("expected 2 total test executions after tree edit, got %d", strings.Count(string(runs3), "x"))
	}
}
