package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

func TestSaveGateTestLog(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	out := []byte("--- FAIL: TestExample (0.00s)\n    example_test.go:1: boom\n")
	path := saveGateTestLog(out)
	if path == "" {
		t.Fatal("expected a gate test log path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gate test log: %v", err)
	}
	if string(data) != string(out) {
		t.Fatalf("gate test log contents = %q, want %q", data, out)
	}
}

func TestGateFormatClean(t *testing.T) {
	if _, err := exec.LookPath("gofmt"); err != nil {
		t.Skip("gofmt not found on PATH")
	}
	origWd, _ := os.Getwd()
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	defer os.Chdir(origWd)

	if check := gateFormat(); check.Status != "pass" {
		t.Fatalf("expected gofmt-clean tree, got %+v", check)
	}
}

func TestReadmeMissingCommandsDetectsGaps(t *testing.T) {
	// Build a command-table section that omits three known words.
	var b strings.Builder
	b.WriteString("## CLI Command Reference\n")
	omit := map[string]bool{"overlaps": true, "intro": true, "completions": true}
	seen := map[string]bool{}
	for _, c := range commandHelp {
		w := strings.Fields(c[0])[0]
		if seen[w] {
			continue
		}
		seen[w] = true
		if omit[w] {
			continue
		}
		b.WriteString("| **`" + w + "`** | usage | desc |\n")
	}
	b.WriteString("\n## Architecture\n")

	missing := readmeMissingCommands(b.String())
	if len(missing) != 3 {
		t.Fatalf("expected 3 missing commands, got %v", missing)
	}
	for _, want := range []string{"overlaps", "intro", "completions"} {
		if !hasString(missing, want) {
			t.Errorf("expected %q in missing list, got %v", want, missing)
		}
	}
}

func TestReadmeMissingCommandsComplete(t *testing.T) {
	var b strings.Builder
	b.WriteString("## CLI Command Reference\n")
	seen := map[string]bool{}
	for _, c := range commandHelp {
		w := strings.Fields(c[0])[0]
		if seen[w] {
			continue
		}
		seen[w] = true
		b.WriteString("| **`" + w + "`** | usage | desc |\n")
	}
	b.WriteString("\n## Architecture\n")

	if missing := readmeMissingCommands(b.String()); len(missing) != 0 {
		t.Fatalf("expected no missing commands, got %v", missing)
	}
}

func TestReadmeMissingCommandsNoSection(t *testing.T) {
	missing := readmeMissingCommands("# sila\n\nno table here\n")
	if len(missing) == 0 {
		t.Fatal("expected all commands flagged when no table section exists")
	}
}

func TestReadmeMissingCommandsIgnoresProseOutsideTable(t *testing.T) {
	// "completions" appears in prose (shell integration) but not in the table,
	// so it must still be flagged — the check is scoped to the table section.
	readme := "## Shell Prompt Integration\n\neval \"$(sila completions zsh)\"\n\n" +
		"## CLI Command Reference\n" +
		"| **`doctor`** | usage | desc |\n" +
		"\n## Architecture\n"

	missing := readmeMissingCommands(readme)
	if !hasString(missing, "completions") {
		t.Errorf("expected completions flagged when absent from table, got %v", missing)
	}
	if hasString(missing, "doctor") {
		t.Errorf("doctor should not be flagged (present in table), got %v", missing)
	}
}

func hasString(list []string, target string) bool {
	return slices.Contains(list, target)
}

func TestRunGateInstall(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	ui := render.New(os.Stdout, true)
	// Test installing hook and remote
	runGateInstall(ui, true, false, false)

	// Check hook file
	hookPath := filepath.Join(dir, ".git", "hooks", "pre-push")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook file: %v", err)
	}
	if !strings.Contains(string(data), "sila gate") {
		t.Errorf("expected hook to contain 'sila gate', got %s", string(data))
	}

	// Check remote
	out, err := exec.Command("git", "remote", "get-url", "sila-gate").Output()
	if err != nil {
		t.Fatalf("git remote get-url sila-gate: %v", err)
	}
	if strings.TrimSpace(string(out)) != dir {
		t.Errorf("expected remote url %s, got %s", dir, strings.TrimSpace(string(out)))
	}
}

func TestGateSentry(t *testing.T) {
	// Nil provider returns skip
	check := gateSentry(nil)
	if check.Status != "skip" {
		t.Errorf("expected skip for nil provider, got %s", check.Status)
	}

	// Mock provider returns valid sentry status based on working tree state
	mock := assist.NewMockAdapter()
	check = gateSentry(mock)
	validStatuses := map[string]bool{"skip": true, "pass": true, "fail": true, "partial": true, "unavailable": true}
	if !validStatuses[check.Status] {
		t.Errorf("expected valid sentry status, got %+v", check)
	}
	if check.Name != "sentry" {
		t.Errorf("expected check name 'sentry', got %s", check.Name)
	}
}

func TestGateClean_SentryStatuses(t *testing.T) {
	baseChecks := []gateCheck{
		{Name: "tests", Status: "pass"},
		{Name: "format", Status: "pass"},
		{Name: "changelog", Status: "pass"},
		{Name: "binary", Status: "pass"},
		{Name: "readme", Status: "pass"},
	}

	// 1. Sentry pass: clean
	withPass := append(baseChecks, gateCheck{Name: "sentry", Status: "pass"})
	if !gateClean(withPass) {
		t.Errorf("expected gateClean=true with sentry pass")
	}

	// 2. Sentry fail (finding): NOT clean (blocks commit)
	withFail := append(baseChecks, gateCheck{Name: "sentry", Status: "fail", Detail: "secret leaked"})
	if gateClean(withFail) {
		t.Errorf("expected gateClean=false with sentry finding/fail")
	}

	// 3. Sentry unavailable: fail-open (allows development to proceed honestly)
	withUnavailable := append(baseChecks, gateCheck{Name: "sentry", Status: "unavailable", Detail: "timeout"})
	if !gateClean(withUnavailable) {
		t.Errorf("expected gateClean=true for fail-open unavailable sentry")
	}

	// 4. Sentry partial: fail-open
	withPartial := append(baseChecks, gateCheck{Name: "sentry", Status: "partial", Detail: "truncated"})
	if !gateClean(withPartial) {
		t.Errorf("expected gateClean=true for partial sentry")
	}

	// 5. Sentry skip: clean
	withSkip := append(baseChecks, gateCheck{Name: "sentry", Status: "skip", Detail: "unconsented"})
	if !gateClean(withSkip) {
		t.Errorf("expected gateClean=true for skipped sentry")
	}

	// 6. Mandatory check failure (e.g. tests fail) always fails closed regardless of sentry
	failingTest := []gateCheck{
		{Name: "tests", Status: "fail"},
		{Name: "sentry", Status: "pass"},
	}
	if gateClean(failingTest) {
		t.Errorf("expected gateClean=false when mandatory tests fail")
	}
}

func TestGateParity(t *testing.T) {
	origWd, _ := os.Getwd()
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	defer os.Chdir(origWd)

	check := gateParity()
	if check.Status != "pass" {
		t.Fatalf("gateParity failed: %s", check.Detail)
	}
}

func TestIsDocsOnlyDiff(t *testing.T) {
	isDocs, count := isDocsOnlyDiff()
	t.Logf("isDocsOnlyDiff returned: isDocs=%v, count=%d", isDocs, count)
}

func TestDocsOnlyDiffAndExternalScope(t *testing.T) {
	t.Run("isCodeOrManifestFile classification", func(t *testing.T) {
		codeFiles := []string{
			"main.go", "src/index.ts", "components/Button.tsx", "app.js", "utils.mjs",
			"script.py", "lib.rs", "Handler.java", "main.c", "server.sh", "schema.sql",
			"go.mod", "go.sum", "package.json", "pnpm-lock.yaml", "tsconfig.json",
			"tsconfig.app.json", "vite.config.ts", "wrangler.jsonc", "Cargo.toml",
			"src/index.mjs", "cmd/sila/main.go",
		}
		for _, f := range codeFiles {
			if !isCodeOrManifestFile(f) {
				t.Errorf("expected %q to be classified as code/manifest", f)
			}
		}

		nonCodeFiles := []string{
			"README.md", "goals/my-goal/goal.md", "goals/my-goal/claim.json",
			"goals/my-goal/plan.md", "docs/index.html", "assets/logo.svg",
			"styles/main.css", "LICENSE", "CHANGELOG.md", ".gitignore",
			"goals/my-goal/receipts/audit.mjs", "goals/my-goal/evidence/T-001/verify.sh",
			"goals/my-goal/tasks/run.py", "goals/my-goal/audit-receipt.json",
			"goals/my-goal/baseline.json", ".sila/baseline.json",
		}
		for _, f := range nonCodeFiles {
			if isCodeOrManifestFile(f) {
				t.Errorf("expected %q NOT to be classified as code/manifest", f)
			}
		}
	})

	t.Run("isDocOrAssetFile classification", func(t *testing.T) {
		docFiles := []string{
			"README.md", "docs/guide.md", "goals/my-goal/goal.md",
			"goals/my-goal/claim.json", "goals/my-goal/plan.md", "goals/README.md",
			"LICENSE", "CREDITS.md", "CHANGELOG.md", "assets/image.png",
			"icons/fav.ico", ".ui-craft/preview.html", ".github/workflows/ci.yml",
			"goals/my-goal/receipts/audit.mjs", "goals/my-goal/evidence/T-001/verify.sh",
			"goals/my-goal/tasks/run.py", "goals/my-goal/audit-receipt.json",
			"goals/my-goal/baseline.json", ".sila/baseline.json",
		}
		for _, f := range docFiles {
			if !isDocOrAssetFile(f) {
				t.Errorf("expected %q to be classified as doc/asset", f)
			}
		}

		nonDocFiles := []string{
			"src/index.ts", "cmd/sila/main.go", "package.json", "wrangler.jsonc",
			"src/index.mjs",
		}
		for _, f := range nonDocFiles {
			if isDocOrAssetFile(f) {
				t.Errorf("expected %q NOT to be classified as doc/asset", f)
			}
		}
	})

	t.Run("external repo docs and goals skip failing test suite in pre-commit", func(t *testing.T) {
		tmp := t.TempDir()
		// Init git repo
		_ = exec.Command("git", "-C", tmp, "init").Run()
		_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@test.com").Run()
		_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()

		// Write package.json with a failing test script (simulating red suite like example.com)
		pkgJSON := `{"name":"test-project","scripts":{"test":"exit 1"}}`
		if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("git", "-C", tmp, "add", "package.json").Run()
		_ = exec.Command("git", "-C", tmp, "commit", "-m", "init").Run()

		// 1. Goal registration / docs only: create goals/my-feature/goal.md and claim.json
		goalDir := filepath.Join(tmp, "goals", "my-feature")
		if err := os.MkdirAll(goalDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(goalDir, "goal.md"), []byte("# Goal"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(goalDir, "claim.json"), []byte(`{"agent":"test"}`), 0644); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("git", "-C", tmp, "add", "goals/").Run()

		// Check isDocsOnlyDiffForDir
		isDocs, count := isDocsOnlyDiffForDir(tmp)
		if !isDocs || count == 0 {
			t.Errorf("expected isDocsOnlyDiffForDir=true for goals registration, got isDocs=%v, count=%d", isDocs, count)
		}

		// Check gateExternalTestsPreCommit
		chk := gateExternalTestsPreCommit(tmp)
		if chk.Status != "pass" {
			t.Errorf("expected gateExternalTestsPreCommit to pass (skip) for goals, got %q (%s)", chk.Status, chk.Detail)
		}

		// 2. Now add a real code file (src/index.ts) - test should run and fail because suite is red
		srcDir := filepath.Join(tmp, "src")
		if err := os.MkdirAll(srcDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "index.ts"), []byte("export const a = 1;"), 0644); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("git", "-C", tmp, "add", "src/index.ts").Run()

		isDocsCode, _ := isDocsOnlyDiffForDir(tmp)
		if isDocsCode {
			t.Errorf("expected isDocsOnlyDiffForDir=false when code is modified")
		}

		chkCode := gateExternalTestsPreCommit(tmp)
		if chkCode.Status != "fail" {
			t.Errorf("expected gateExternalTestsPreCommit to fail when code changes with failing test runner, got %q", chkCode.Status)
		}
	})

	t.Run("goal receipts and evidence artifacts with code extensions skip failing test suite in pre-commit", func(t *testing.T) {
		tmp := t.TempDir()
		_ = exec.Command("git", "-C", tmp, "init").Run()
		_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@test.com").Run()
		_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()

		pkgJSON := `{"name":"test-project","scripts":{"test":"exit 1"}}`
		if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(pkgJSON), 0644); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("git", "-C", tmp, "add", "package.json").Run()
		_ = exec.Command("git", "-C", tmp, "commit", "-m", "init").Run()

		receiptDir := filepath.Join(tmp, "goals", "my-feature", "receipts")
		evidenceDir := filepath.Join(tmp, "goals", "my-feature", "evidence", "T-001")
		if err := os.MkdirAll(receiptDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(evidenceDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(receiptDir, "audit.mjs"), []byte("// receipt script\nprocess.exit(0);"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "verify.sh"), []byte("#!/bin/sh\nexit 0"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "goals", "my-feature", "audit-receipt.json"), []byte(`{"status":"certified"}`), 0644); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("git", "-C", tmp, "add", "goals/").Run()

		isDocs, count := isDocsOnlyDiffForDir(tmp)
		if !isDocs || count == 0 {
			t.Errorf("expected isDocsOnlyDiffForDir=true for receipts and evidence diff, got isDocs=%v, count=%d", isDocs, count)
		}

		chk := gateExternalTestsPreCommit(tmp)
		if chk.Status != "pass" {
			t.Errorf("expected gateExternalTestsPreCommit to pass (skip) for goal receipts, got %q (%s)", chk.Status, chk.Detail)
		}
	})

	t.Run("hasExternalSourceChanges and getChangedFilesForDir", func(t *testing.T) {
		tmp := t.TempDir()
		_ = exec.Command("git", "-C", tmp, "init").Run()
		_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@test.com").Run()
		_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()

		if hasExternalSourceChanges(tmp) {
			t.Errorf("expected false for empty tree")
		}
		if paths := getChangedFilesForDir(tmp); len(paths) != 0 {
			t.Errorf("expected 0 files, got %v", paths)
		}

		// Stage a markdown file
		if err := os.WriteFile(filepath.Join(tmp, "README.md"), []byte("# Hello"), 0644); err != nil {
			t.Fatal(err)
		}
		_ = exec.Command("git", "-C", tmp, "add", "README.md").Run()

		if hasExternalSourceChanges(tmp) {
			t.Errorf("expected false for README.md staged")
		}
		if paths := getChangedFilesForDir(tmp); len(paths) != 1 || paths[0] != "README.md" {
			t.Errorf("expected [README.md], got %v", paths)
		}

		// Test gateExternalTests on docs only
		chk := gateExternalTests(tmp)
		if chk.Status != "pass" {
			t.Errorf("expected pass for docs-only diff in gateExternalTests, got %q", chk.Status)
		}
	})
}

func TestGateExternalChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SILA_DB", filepath.Join(dir, "test.db"))
	// Write dummy go.mod for an external repo
	goMod := "module github.com/example/other\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	// Write simple passing test file
	testFile := "package main\n\nimport \"testing\"\n\nfunc TestDummy(t *testing.T) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(testFile), 0644); err != nil {
		t.Fatal(err)
	}

	checks := gateExternalChecks(dir)
	if len(checks) == 0 {
		t.Fatalf("expected gate checks for external repository, got none")
	}

	for _, c := range checks {
		if c.Status == "fail" {
			t.Fatalf("check %q failed unexpectedly: %s", c.Name, c.Detail)
		}
	}
}

func TestGateTelemetry(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(filepath.Join("..", "..")); err != nil {
		t.Fatalf("chdir to repo root: %v", err)
	}
	defer os.Chdir(origWd)

	check := gateTelemetry()
	if check.Status != "pass" {
		t.Fatalf("gateTelemetry failed: %s", check.Detail)
	}
}

func TestGateLicense(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(origWd)

	check := gateLicense()
	if check.Status != "pass" {
		t.Fatalf("gateLicense failed: %s", check.Detail)
	}
	if check.Name != "license" {
		t.Errorf("expected check name 'license', got %q", check.Name)
	}
}

func TestGateScarTissueClean(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(origWd)

	check := gateScarTissue()
	if check.Name != "scar-tissue" {
		t.Errorf("expected check name 'scar-tissue', got %q", check.Name)
	}
	if check.Status == "fail" {
		t.Fatalf("gateScarTissue failed on clean tree: %s", check.Detail)
	}
}

func TestGateScarTissueCatchesProhibitedCGO(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	// Write prohibited CGO file in worktree
	cgoFile := "package main\n\nimport \"C\"\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "leak.go"), []byte(cgoFile), 0644); err != nil {
		t.Fatalf("write leak.go: %v", err)
	}

	check := gateScarTissue()
	if check.Status != "fail" {
		t.Fatalf("expected gateScarTissue to fail on prohibited CGO in working tree, got status %q", check.Status)
	}
	if !strings.Contains(check.Detail, "No CGO Dependencies") {
		t.Errorf("expected detail to mention No CGO Dependencies, got: %s", check.Detail)
	}
}

func TestRenderGateTOON(t *testing.T) {
	checks := []gateCheck{
		{Name: "tests", Status: "pass", Detail: "all tests passed"},
		{Name: "scar-tissue", Status: "pass", Detail: "diff clean across 9 rules"},
	}
	toon := renderGateTOON(checks, true)
	if !strings.Contains(toon, "gate_summary:") {
		t.Errorf("expected TOON gate_summary, got:\n%s", toon)
	}
	if !strings.Contains(toon, "scar-tissue") {
		t.Errorf("expected check name scar-tissue in TOON, got:\n%s", toon)
	}
}

func TestGateDocCoverageClean(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(origWd)

	check := gateDocCoverage()
	if check.Name != "doc-coverage" {
		t.Errorf("expected check name 'doc-coverage', got %q", check.Name)
	}
	if check.Status != "pass" {
		t.Fatalf("gateDocCoverage failed on clean codebase: %s", check.Detail)
	}
}

func TestGateDocCoverageDetectsMissing(t *testing.T) {
	dir := setupWorktreeRepo(t)
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWd)

	subDir := filepath.Join(dir, "cmd", "sample")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir cmd/sample: %v", err)
	}
	badGo := "package main\n\nfunc ExportedNoDoc() {}\n"
	if err := os.WriteFile(filepath.Join(subDir, "main.go"), []byte(badGo), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	check := gateDocCoverage()
	if check.Status != "fail" {
		t.Fatalf("expected gateDocCoverage to fail on missing doc comment, got status %q (detail: %s)", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "ExportedNoDoc") {
		t.Errorf("expected detail to mention ExportedNoDoc, got: %s", check.Detail)
	}
}

func TestGateReportCardClean(t *testing.T) {
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer os.Chdir(origWd)

	check := gateReportCard()
	if check.Name != "report-card" {
		t.Errorf("expected check name 'report-card', got %q", check.Name)
	}
	if check.Status != "pass" {
		t.Fatalf("gateReportCard failed on clean codebase: %s", check.Detail)
	}
}

func TestGateCoverageClean(t *testing.T) {
	covPath := gateCoverageProfilePath()
	data := "mode: set\ngithub.com/jadmadi/sila/internal/store/db.go:10.1,20.1 10 1\n"
	if err := os.WriteFile(covPath, []byte(data), 0644); err != nil {
		t.Fatalf("failed to write coverage profile: %v", err)
	}
	defer os.Remove(covPath)

	check := gateCoverage()
	if check.Name != "coverage" {
		t.Errorf("expected check name 'coverage', got %q", check.Name)
	}
	if check.Status != "pass" {
		t.Fatalf("gateCoverage failed on valid profile: %s", check.Detail)
	}
}

func TestGateCoverageDetectsLowThreshold(t *testing.T) {
	covPath := gateCoverageProfilePath()
	data := "mode: set\ngithub.com/jadmadi/sila/cmd/sila/main.go:10.1,20.1 9 0\ngithub.com/jadmadi/sila/cmd/sila/main.go:21.1,25.1 1 1\n"
	if err := os.WriteFile(covPath, []byte(data), 0644); err != nil {
		t.Fatalf("failed to write coverage profile: %v", err)
	}
	defer os.Remove(covPath)

	check := gateCoverage()
	if check.Status != "fail" {
		t.Fatalf("expected gateCoverage to fail on 10%% coverage, got status %q (detail: %s)", check.Status, check.Detail)
	}
}

func TestParsePorcelainPath(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"M  cmd/sila/gate.go", "cmd/sila/gate.go"},
		{" M internal/render/table.go", "internal/render/table.go"},
		{"?? newfile.go", "newfile.go"},
		{"R  old.go -> new.go", "new.go"},
		{"M  \"path with spaces/file.go\"", "path with spaces/file.go"},
		{"", ""},
		{"M", ""},
	}
	for _, tc := range tests {
		got := parsePorcelainPath(tc.line)
		if got != tc.want {
			t.Errorf("parsePorcelainPath(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestGatePreCommitReportTier(t *testing.T) {
	checks := []gateCheck{
		{Name: "format", Status: "pass", Detail: "clean"},
		{Name: "tests", Status: "pass", Detail: "ok"},
	}
	rep := gateReport{
		Clean:  true,
		Tier:   "pre-commit",
		Checks: checks,
	}
	if rep.Tier != "pre-commit" {
		t.Fatalf("expected rep.Tier to be 'pre-commit', got %q", rep.Tier)
	}
	if !gateClean(checks) {
		t.Fatalf("expected gateClean to return true for passing checks")
	}
}

func TestGateChangelogSkipHandling(t *testing.T) {
	// Base checks with pass
	baseChecks := []gateCheck{
		{Name: "format", Status: "pass"},
		{Name: "lint", Status: "pass"},
		{Name: "tests", Status: "pass"},
	}

	// 1. With changelog skip
	checksWithSkip := append(baseChecks, gateCheck{Name: "changelog", Status: "skip", Detail: "skipped"})
	if !gateClean(checksWithSkip) {
		t.Errorf("expected gateClean=true when changelog is skipped")
	}

	// 2. With changelog unavailable
	checksWithUnavail := append(baseChecks, gateCheck{Name: "changelog", Status: "unavailable", Detail: "git missing"})
	if !gateClean(checksWithUnavail) {
		t.Errorf("expected gateClean=true when changelog is unavailable")
	}

	// 3. With changelog fail
	checksWithFail := append(baseChecks, gateCheck{Name: "changelog", Status: "fail", Detail: "unreleased missing"})
	if gateClean(checksWithFail) {
		t.Errorf("expected gateClean=false when changelog check fails")
	}

	// 4. Test gateExternalChangelog in directory without CHANGELOG.md
	tempDir := t.TempDir()
	chk := gateExternalChangelog(tempDir)
	if chk.Status != "pass" {
		t.Errorf("expected gateExternalChangelog in dir without CHANGELOG.md to return status pass, got %q", chk.Status)
	}
}

func TestGateRTLSentry(t *testing.T) {
	baseChecks := []gateCheck{
		{Name: "format", Status: "pass"},
		{Name: "lint", Status: "pass"},
		{Name: "tests", Status: "pass"},
	}

	// 1. gateClean with rtl-sentry pass
	checksPass := append(baseChecks, gateCheck{Name: "rtl-sentry", Status: "pass"})
	if !gateClean(checksPass) {
		t.Errorf("expected gateClean=true when rtl-sentry passes")
	}

	// 2. gateClean with rtl-sentry skip
	checksSkip := append(baseChecks, gateCheck{Name: "rtl-sentry", Status: "skip", Detail: "no RTL invariant"})
	if !gateClean(checksSkip) {
		t.Errorf("expected gateClean=true when rtl-sentry is skipped")
	}

	// 3. gateClean with rtl-sentry fail
	checksFail := append(baseChecks, gateCheck{Name: "rtl-sentry", Status: "fail", Detail: "physical CSS detected"})
	if gateClean(checksFail) {
		t.Errorf("expected gateClean=false when rtl-sentry fails")
	}

	// 4. gateRTLSentry in clean non-RTL repo skips
	tempDir := t.TempDir()
	chk := gateRTLSentry(tempDir)
	if chk.Status != "skip" {
		t.Errorf("expected gateRTLSentry to return skip in non-git or non-RTL dir, got %s", chk.Status)
	}
}

func TestGateCFWorkerSentry(t *testing.T) {
	baseChecks := []gateCheck{
		{Name: "format", Status: "pass"},
		{Name: "lint", Status: "pass"},
		{Name: "tests", Status: "pass"},
	}

	// 1. gateClean with cf-worker-sentry pass
	checksPass := append(baseChecks, gateCheck{Name: "cf-worker-sentry", Status: "pass"})
	if !gateClean(checksPass) {
		t.Errorf("expected gateClean=true when cf-worker-sentry passes")
	}

	// 2. gateClean with cf-worker-sentry skip
	checksSkip := append(baseChecks, gateCheck{Name: "cf-worker-sentry", Status: "skip", Detail: "no Cloudflare Worker configuration"})
	if !gateClean(checksSkip) {
		t.Errorf("expected gateClean=true when cf-worker-sentry is skipped")
	}

	// 3. gateClean with cf-worker-sentry fail
	checksFail := append(baseChecks, gateCheck{Name: "cf-worker-sentry", Status: "fail", Detail: "secret 500 masking detected"})
	if gateClean(checksFail) {
		t.Errorf("expected gateClean=false when cf-worker-sentry fails")
	}

	// 4. gateCFWorkerSentry in non-worker repo skips
	tempDir := t.TempDir()
	chk := gateCFWorkerSentry(tempDir)
	if chk.Status != "skip" {
		t.Errorf("expected gateCFWorkerSentry to return skip in non-git or non-worker dir, got %s", chk.Status)
	}

	// 5. gateClean with cfclean pass
	checksCFCleanPass := append(baseChecks, gateCheck{Name: "cfclean", Status: "pass"})
	if !gateClean(checksCFCleanPass) {
		t.Errorf("expected gateClean=true when cfclean passes")
	}

	// 6. gateClean with cfclean skip
	checksCFCleanSkip := append(baseChecks, gateCheck{Name: "cfclean", Status: "skip", Detail: "no Cloudflare Worker configuration"})
	if !gateClean(checksCFCleanSkip) {
		t.Errorf("expected gateClean=true when cfclean is skipped")
	}

	// 7. gateClean with cfclean fail
	checksCFCleanFail := append(baseChecks, gateCheck{Name: "cfclean", Status: "fail", Detail: "unmasked credentials detected"})
	if gateClean(checksCFCleanFail) {
		t.Errorf("expected gateClean=false when cfclean fails")
	}

	// 8. gateCFCleanSentry in non-worker repo skips
	chkCF := gateCFCleanSentry(tempDir)
	if chkCF.Status != "skip" {
		t.Errorf("expected gateCFCleanSentry to return skip in non-git or non-worker dir, got %s", chkCF.Status)
	}
}

func TestGateRuleBudget(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Clean directory with valid AGENTS.md and GEMINI.md symlink
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte("# Rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("AGENTS.md", filepath.Join(tempDir, "GEMINI.md")); err != nil {
		t.Fatal(err)
	}

	chk := gateRuleBudget(tempDir)
	if chk.Status != "pass" {
		t.Errorf("expected pass for clean rules within budget, got: %s (detail: %s)", chk.Status, chk.Detail)
	}

	// 2. Oversized AGENTS.md (> 24,000 bytes) fails
	oversized := strings.Repeat("X", 25000)
	if err := os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(oversized), 0644); err != nil {
		t.Fatal(err)
	}

	chkFail := gateRuleBudget(tempDir)
	if chkFail.Status != "fail" {
		t.Errorf("expected fail for oversized AGENTS.md, got: %s", chkFail.Status)
	}
	if !strings.Contains(chkFail.Detail, "exceeds Antigravity 24 KB ceiling") {
		t.Errorf("expected detail to mention 24 KB ceiling, got: %s", chkFail.Detail)
	}
}

func TestGateDatabaseConcurrency_Checks(t *testing.T) {
	// 1. Verify gateSmokeAB runs and passes using disposable sandbox
	smokeCheck := gateSmokeAB()
	if smokeCheck.Status != "pass" {
		t.Fatalf("expected gateSmokeAB to pass, got: %+v", smokeCheck)
	}

	// 2. Verify gateKnowledge runs non-mutating inspection
	kCheck := gateKnowledge()
	if kCheck.Status == "fail" {
		t.Fatalf("gateKnowledge failed: %+v", kCheck)
	}

	// 3. Verify gateDecisionSentry runs non-mutating inspection
	dir := t.TempDir()
	dCheck := gateDecisionSentry(dir)
	if dCheck.Status == "fail" {
		t.Fatalf("gateDecisionSentry failed: %+v", dCheck)
	}

	// 4. Verify gateArchSentry runs without mutation
	archCheck := gateArchSentry(dir, true)
	if archCheck.Status == "fail" {
		t.Fatalf("gateArchSentry failed: %+v", archCheck)
	}

	// 5. Verify gateProvenanceSentry runs
	provCheck := gateProvenanceSentry(dir)
	if provCheck.Status == "fail" {
		t.Fatalf("gateProvenanceSentry failed: %+v", provCheck)
	}
}

func TestGateSmokeAB_NonMutatingStoreInvariant(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live_store.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	p, err := st.EnsureProject(dir)
	if err != nil {
		t.Fatalf("EnsureProject failed: %v", err)
	}
	if err := st.InsertHandoff(store.Handoff{
		ProjectID: p.ID,
		Agent:     "test",
		Summary:   "Initial handoff",
		CreatedAt: "2026-10-08T00:00:00Z",
	}); err != nil {
		t.Fatalf("InsertHandoff failed: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("st.Close failed: %v", err)
	}

	// Record stat, permission before gate check
	fiBefore, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Open for inspection and run quick check
	inspSt, ok, err := store.OpenInspection(dbPath)
	if err != nil || !ok || inspSt == nil {
		t.Fatalf("OpenInspection failed: %v", err)
	}
	if err := inspSt.QuickCheck(); err != nil {
		t.Fatalf("QuickCheck failed: %v", err)
	}
	_ = inspSt.Close()

	// Verify stat and permission after inspection
	fiAfter, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if fiAfter.Mode().Perm() != fiBefore.Mode().Perm() {
		t.Errorf("permissions changed: was %v, now %v", fiBefore.Mode().Perm(), fiAfter.Mode().Perm())
	}
}

func TestGateTestArgs(t *testing.T) {
	t.Setenv("SILA_GATE_TIMEOUT", "")
	args := gateTestArgs("coverage.out")
	expected := []string{"test", "-race", "-timeout=20m", "-coverprofile=coverage.out", "./..."}
	if !slices.Equal(args, expected) {
		t.Fatalf("gateTestArgs default = %v, want %v", args, expected)
	}

	t.Setenv("SILA_GATE_TIMEOUT", "45m")
	argsOverride := gateTestArgs("cov.out")
	expectedOverride := []string{"test", "-race", "-timeout=45m", "-coverprofile=cov.out", "./..."}
	if !slices.Equal(argsOverride, expectedOverride) {
		t.Fatalf("gateTestArgs override = %v, want %v", argsOverride, expectedOverride)
	}

	t.Setenv("SILA_GATE_TIMEOUT", "15m")
	if to := gateTestTimeout(); to != "15m" {
		t.Fatalf("gateTestTimeout = %q, want %q", to, "15m")
	}
	t.Setenv("SILA_GATE_TIMEOUT", "")
	if to := gateTestTimeout(); to != "20m" {
		t.Fatalf("gateTestTimeout default = %q, want %q", to, "20m")
	}
	argsNoCov := gateTestArgs("")
	expectedNoCov := []string{"test", "-race", "-timeout=20m", "./..."}
	if !slices.Equal(argsNoCov, expectedNoCov) {
		t.Fatalf("gateTestArgs without coverage = %v, want %v", argsNoCov, expectedNoCov)
	}
}

func TestGateEmitHeartbeats_CleanStoreDatabase(t *testing.T) {
	repoDir := setupWorktreeRepo(t)

	dbPath := filepath.Join(t.TempDir(), "test_clean_gate.db")
	t.Setenv("SILA_DB", dbPath)
	t.Setenv("SILA_DEBUG", "1")

	checks := []gateCheck{
		{Name: "scar-tissue", Status: "pass", Detail: "clean"},
		{Name: "arch-sentry", Status: "pass", Detail: "clean"},
		{Name: "telemetry", Status: "pass", Detail: "clean"},
	}

	emitGateHeartbeats(repoDir, checks)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	defer st.Close()

	// Verify project record exists in projects table
	var projectCount int
	if err := st.DB.QueryRow(`SELECT count(*) FROM projects`).Scan(&projectCount); err != nil {
		t.Fatalf("query projects table failed: %v", err)
	}
	if projectCount == 0 {
		t.Fatal("expected project row inserted into clean database, got 0")
	}

	// Verify epistemic heartbeats recorded without foreign key errors
	var heartbeatCount int
	if err := st.DB.QueryRow(`SELECT count(*) FROM epistemic_heartbeats`).Scan(&heartbeatCount); err != nil {
		t.Fatalf("query epistemic_heartbeats failed: %v", err)
	}
	if heartbeatCount == 0 {
		t.Fatal("expected heartbeats recorded in clean database, got 0")
	}

	rows, err := st.DB.Query(`SELECT project_id, session_id, agent, invariant_rule, evidence_type, evidence_ref, status FROM epistemic_heartbeats`)
	if err != nil {
		t.Fatalf("query heartbeats failed: %v", err)
	}
	defer rows.Close()

	foundRules := make(map[string]bool)
	for rows.Next() {
		var projID, sessionID, agent, rule, evType, evRef, status string
		if err := rows.Scan(&projID, &sessionID, &agent, &rule, &evType, &evRef, &status); err != nil {
			t.Fatalf("scan heartbeat failed: %v", err)
		}
		if projID == "" {
			t.Errorf("expected non-empty project_id, got %q", projID)
		}
		if sessionID != "gate" {
			t.Errorf("expected session_id 'gate', got %q", sessionID)
		}
		if agent != "sila-gate" {
			t.Errorf("expected agent 'sila-gate', got %q", agent)
		}
		if evType != "gate" {
			t.Errorf("expected evidence_type 'gate', got %q", evType)
		}
		if evRef == "" {
			t.Errorf("expected non-empty evidence_ref for rule %q", rule)
		}
		if status != "pass" {
			t.Errorf("expected status 'pass', got %q for rule %q", status, rule)
		}
		foundRules[rule] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration failed: %v", err)
	}

	for _, want := range []string{"gate:scar-tissue", "gate:arch-sentry", "gate:telemetry"} {
		if !foundRules[want] {
			t.Errorf("expected heartbeat rule %q in %+v", want, foundRules)
		}
	}
}

func TestGateSilenceSentryIntegration(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "gate_silence.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.EnsureProject(tmp)
	if err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	t.Setenv("SILA_DB", dbPath)
	check := gateSilenceSentry(tmp)
	if check.Status != "pass" {
		t.Fatalf("expected pass on clean store, got %+v", check)
	}

	// Test fail-closed on missing store
	t.Setenv("SILA_DB", filepath.Join(tmp, "does_not_exist.db"))
	checkFail := gateSilenceSentry(tmp)
	if checkFail.Status != "fail" {
		t.Fatalf("expected fail on missing store, got %+v", checkFail)
	}
}
