package gate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/store"
)

func TestRunArchSentry_SkipFlags(t *testing.T) {
	// 1. Skip via flag
	res := RunArchSentry(ArchSentryOptions{
		SkipArch: true,
	})
	if !res.Clean || !res.Skipped || !strings.Contains(res.SkipReason, "--skip-arch") {
		t.Fatalf("expected skip via flag, got %+v", res)
	}

	// 2. Skip via trailer
	resTrailer := RunArchSentry(ArchSentryOptions{
		CommitMessage: "feat: legacy changes\n\nSila-Skip-Arch: grandfathered legacy code",
	})
	if !resTrailer.Clean || !resTrailer.Skipped || !strings.Contains(resTrailer.SkipReason, "grandfathered legacy code") {
		t.Fatalf("expected skip via trailer, got %+v", resTrailer)
	}

	// 3. Skip via env var
	os.Setenv("SILA_SKIP_ARCH", "1")
	defer os.Unsetenv("SILA_SKIP_ARCH")
	resEnv := RunArchSentry(ArchSentryOptions{})
	if !resEnv.Clean || !resEnv.Skipped || !strings.Contains(resEnv.SkipReason, "SILA_SKIP_ARCH") {
		t.Fatalf("expected skip via env, got %+v", resEnv)
	}
}

func TestRunArchSentry_SRPAndAntiPatterns(t *testing.T) {
	tmpDir := t.TempDir()

	// Create brand new file exceeding 2000 lines
	bigFilePath := filepath.Join(tmpDir, "monolith.go")
	var sb strings.Builder
	sb.WriteString("package monolith\n\n")
	for i := 0; i < 2200; i++ {
		sb.WriteString("// line\n")
	}
	if err := os.WriteFile(bigFilePath, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	nameStatus := "A\tmonolith.go\n"
	diff := `diff --git a/worker/src/api.ts b/worker/src/api.ts
--- a/worker/src/api.ts
+++ b/worker/src/api.ts
@@ -1,5 +1,8 @@
+for (const x of items) {
+  await db.insert(table).values(x);
+}
`

	res := RunArchSentry(ArchSentryOptions{
		RepoRoot:   tmpDir,
		NameStatus: nameStatus,
		Diff:       diff,
	})

	if res.Clean {
		t.Fatal("expected failure due to SRP rejection and D1 anti-pattern")
	}
	if len(res.Errors) < 2 {
		t.Fatalf("expected at least 2 errors, got %d: %+v", len(res.Errors), res.Errors)
	}

	foundSRP := false
	foundD1 := false
	for _, err := range res.Errors {
		if strings.Contains(err, "exceeds 2,000 LOC") {
			foundSRP = true
		}
		if strings.Contains(err, "unbatched-d1-loop") {
			foundD1 = true
		}
	}
	if !foundSRP {
		t.Errorf("missing SRP error in errors: %+v", res.Errors)
	}
	if !foundD1 {
		t.Errorf("missing D1 error in errors: %+v", res.Errors)
	}
}

func TestRunArchSentry_FactGroundingWarningOnly(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sila.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.UpsertFact(store.Fact{
		ID:            "fact_important_file",
		ProjectID:     "cli",
		Claim:         "Important architecture claim",
		Kind:          "invariant",
		Confidence:    "invariant",
		FileRefsExist: true,
		RepoFiles:     []string{"core/important.go"},
	}); err != nil {
		t.Fatal(err)
	}

	nameStatus := "D\tcore/important.go\n"
	res := RunArchSentry(ArchSentryOptions{
		RepoRoot:   tmpDir,
		Store:      st,
		NameStatus: nameStatus,
	})

	// Grounding warnings are advisory; clean should remain true unless there are hard errors
	if !res.Clean {
		t.Fatalf("expected Clean == true for advisory fact grounding warning, got Clean=false: %+v", res)
	}
	if len(res.FactWarnings) != 1 {
		t.Fatalf("expected 1 fact warning, got %d", len(res.FactWarnings))
	}
	if len(res.Warnings) != 1 {
		t.Fatalf("expected 1 warning in res.Warnings, got %d", len(res.Warnings))
	}
}
