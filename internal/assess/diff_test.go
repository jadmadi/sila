package assess

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditDiffWorkflowWarning(t *testing.T) {
	rules := DefaultHardProhibitions()
	diff := `diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
new file mode 100644
index 0000000..abcdef1
--- /dev/null
+++ b/.github/workflows/ci.yml
@@ -0,0 +1,10 @@
+name: CI
+on: [push]
+jobs:
+  build:
+    runs-on: ubuntu-latest
+`
	report := AuditDiffString(diff, nil, rules)
	if !report.Clean {
		t.Fatalf("expected CI workflow to produce a warning, not a failing violation")
	}
	if len(report.Violations) == 0 {
		t.Fatalf("expected at least one workflow warning")
	}
	v := report.Violations[0]
	if v.RuleID != "no-github-workflows" {
		t.Errorf("expected rule ID no-github-workflows, got %s", v.RuleID)
	}
	if v.Severity != "warning" {
		t.Errorf("expected workflow rule severity warning, got %q", v.Severity)
	}
	if !strings.Contains(v.Citation, "CI workflows are allowed") {
		t.Errorf("expected public-policy citation, got %s", v.Citation)
	}
}

func TestAuditDiffProhibitedCGO(t *testing.T) {
	rules := DefaultHardProhibitions()
	diff := `diff --git a/internal/db/sqlite.go b/internal/db/sqlite.go
index 1234567..89abcdef 100644
--- a/internal/db/sqlite.go
+++ b/internal/db/sqlite.go
@@ -10,3 +10,4 @@ package db
 
 import (
 	"fmt"
+	"C"
 )
`
	report := AuditDiffString(diff, nil, rules)
	if report.Clean {
		t.Fatalf("expected diff to fail due to CGO import")
	}
	found := false
	for _, v := range report.Violations {
		if v.RuleID == "no-cgo" && v.LineNumber == 13 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation on line 13 with rule no-cgo, got %+v", report.Violations)
	}
}

func TestAuditDiffRemovedLinesIgnored(t *testing.T) {
	rules := DefaultHardProhibitions()
	// Diff removes CGO import and adds clean pure-Go import
	diff := `diff --git a/internal/db/sqlite.go b/internal/db/sqlite.go
index 1234567..89abcdef 100644
--- a/internal/db/sqlite.go
+++ b/internal/db/sqlite.go
@@ -10,4 +10,4 @@ package db
 
 import (
-	"C"
+	"modernc.org/sqlite"
 )
`
	report := AuditDiffString(diff, nil, rules)
	if !report.Clean {
		t.Fatalf("expected diff removing forbidden CGO to be clean, but got violations: %+v", report.Violations)
	}
}

func TestAuditDiffProhibitedNPM(t *testing.T) {
	rules := DefaultHardProhibitions()
	diff := `diff --git a/package-lock.json b/package-lock.json
new file mode 100644
index 0000000..abcdef1
--- /dev/null
+++ b/package-lock.json
@@ -0,0 +1,5 @@
+{"name": "test"}
`
	report := AuditDiffString(diff, nil, rules)
	if report.Clean {
		t.Fatalf("expected package-lock.json to trigger violation")
	}

	cmdDiff := `diff --git a/scripts/setup.sh b/scripts/setup.sh
index 1111111..2222222 100644
--- a/scripts/setup.sh
+++ b/scripts/setup.sh
@@ -5,2 +5,3 @@
 echo "setting up"
+npm install
`
	report2 := AuditDiffString(cmdDiff, nil, rules)
	if report2.Clean {
		t.Fatalf("expected npm install in script to trigger violation")
	}
}

func TestAuditDiffPhysicalCSS(t *testing.T) {
	rules := DefaultHardProhibitions()
	badDiff := `diff --git a/ui/styles.css b/ui/styles.css
index 1111111..2222222 100644
--- a/ui/styles.css
+++ b/ui/styles.css
@@ -10,3 +10,4 @@
 .btn {
 	display: block;
+	ml-4;
 }
`
	report := AuditDiffString(badDiff, nil, rules)
	if report.Clean {
		t.Fatalf("expected physical CSS ml-4 to be flagged")
	}

	goodDiff := `diff --git a/ui/styles.css b/ui/styles.css
index 1111111..2222222 100644
--- a/ui/styles.css
+++ b/ui/styles.css
@@ -10,3 +10,4 @@
 .btn {
 	display: block;
+	ms-4;
 }
`
	report2 := AuditDiffString(goodDiff, nil, rules)
	if !report2.Clean {
		t.Fatalf("expected logical CSS ms-4 to pass cleanly, got: %+v", report2.Violations)
	}
}

func TestAuditDiffEasternArabicNumerals(t *testing.T) {
	rules := DefaultHardProhibitions()
	badDiff := `diff --git a/internal/render/table.go b/internal/render/table.go
index 1111111..2222222 100644
--- a/internal/render/table.go
+++ b/internal/render/table.go
@@ -15,3 +15,4 @@ func formatCount(n int) string {
+	return "١٢٣"
 }
`
	report := AuditDiffString(badDiff, nil, rules)
	if report.Clean {
		t.Fatalf("expected Eastern Arabic numerals in code to be flagged")
	}
}

func TestAuditDiffHardcodedSecret(t *testing.T) {
	rules := DefaultHardProhibitions()
	hdr := "api" + "_key"
	val := "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"
	badDiff := "diff --git a/internal/api/client.go b/internal/api/client.go\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/internal/api/client.go\n" +
		"+++ b/internal/api/client.go\n" +
		"@@ -20,3 +20,4 @@ func auth() {\n" +
		"+	" + hdr + " := \"" + val + "\"\n" +
		"}\n"
	report := AuditDiffString(badDiff, nil, rules)
	if report.Clean {
		t.Fatalf("expected hardcoded secret token to be flagged")
	}
}

func TestAuditDiffUntrackedFiles(t *testing.T) {
	rules := DefaultHardProhibitions()
	untracked := map[string]string{
		".github/workflows/deploy.yml": "name: Deploy\n",
		"internal/tools/leak.go":       "package tools\n\nimport \"C\"\n",
	}

	report := AuditDiffString("", untracked, rules)
	if report.Clean {
		t.Fatalf("expected untracked files to trigger violations")
	}
	if len(report.Violations) < 2 {
		t.Fatalf("expected at least 2 violations from untracked files, got %d", len(report.Violations))
	}
}

func TestAuditRepoDiffGitIntegration(t *testing.T) {
	tmp := t.TempDir()

	// Init git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmp
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	_ = exec.Command("git", "-C", tmp, "config", "user.name", "Test").Run()
	_ = exec.Command("git", "-C", tmp, "config", "user.email", "test@example.com").Run()

	// Commit initial file
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	_ = exec.Command("git", "-C", tmp, "add", "main.go").Run()
	_ = exec.Command("git", "-C", tmp, "commit", "-m", "initial commit").Run()

	rules := DefaultHardProhibitions()

	// 1. Clean repo check
	report, err := AuditRepoDiff(context.Background(), tmp, rules)
	if err != nil {
		t.Fatalf("AuditRepoDiff: %v", err)
	}
	if !report.Clean {
		t.Fatalf("expected clean report on fresh repo, got: %+v", report.Violations)
	}

	// 2. Introduce forbidden CGO line in main.go
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n\nimport \"C\"\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go with cgo: %v", err)
	}

	report2, err := AuditRepoDiff(context.Background(), tmp, rules)
	if err != nil {
		t.Fatalf("AuditRepoDiff: %v", err)
	}
	if report2.Clean {
		t.Fatalf("expected report2 to catch CGO modification in working tree")
	}
}

func TestRenderScarTissueTOON(t *testing.T) {
	report := &ScarTissueReport{
		Clean:        false,
		TotalRules:   10,
		FilesScanned: 2,
		LinesScanned: 50,
		Violations: []ScarTissueViolation{
			{
				RuleID:      "no-cgo",
				RuleName:    "No CGO Dependencies",
				File:        "main.go",
				LineNumber:  3,
				LineContent: `import "C"`,
				Source:      "AGENTS.md §0",
				Citation:    "No CGO dependencies",
				Severity:    "error",
			},
		},
	}

	toon := RenderScarTissueTOON(report)
	if !strings.Contains(toon, "scar_tissue_audit:") {
		t.Errorf("expected TOON header scar_tissue_audit:, got:\n%s", toon)
	}
	if !strings.Contains(toon, "no-cgo") {
		t.Errorf("expected rule ID no-cgo in TOON output, got:\n%s", toon)
	}
}
