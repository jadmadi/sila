package dream_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/dream"
	"github.com/jadmadi/sila/internal/store"
)

func TestSafeReadProjectExcerpt_SecurityContainment(t *testing.T) {
	tmp := t.TempDir()
	projRoot := filepath.Join(tmp, "project")
	if err := os.MkdirAll(projRoot, 0700); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// 1. Valid regular file within project root
	validFile := filepath.Join(projRoot, "main.go")
	if err := os.WriteFile(validFile, []byte("package main\nfunc main() {}\n"), 0600); err != nil {
		t.Fatalf("failed to write valid file: %v", err)
	}
	content, err := dream.SafeReadProjectExcerpt(projRoot, "main.go", 100)
	if err != nil {
		t.Fatalf("expected valid file to read cleanly, got: %v", err)
	}
	if !strings.HasPrefix(content, "package main") {
		t.Errorf("unexpected content: %q", content)
	}

	// 2. Traversal attempt with ../ outside project root
	_, err = dream.SafeReadProjectExcerpt(projRoot, "../outside.txt", 100)
	if err == nil || !strings.Contains(err.Error(), "traversal outside project root") {
		t.Errorf("expected traversal rejection, got: %v", err)
	}

	// 3. Symlink pointing outside project root
	outsideFile := filepath.Join(tmp, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("SUPER_SECRET_TOKEN"), 0600); err != nil {
		t.Fatalf("failed to write outside file: %v", err)
	}
	symlink := filepath.Join(projRoot, "leak.txt")
	if err := os.Symlink(outsideFile, symlink); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}
	_, err = dream.SafeReadProjectExcerpt(projRoot, "leak.txt", 100)
	if err == nil || !strings.Contains(err.Error(), "symlink points outside project root") {
		t.Errorf("expected outside symlink rejection, got: %v", err)
	}

	// 4. Sensitive file rejection (.env)
	envFile := filepath.Join(projRoot, ".env")
	if err := os.WriteFile(envFile, []byte("DATABASE_URL=postgres://"), 0600); err != nil {
		t.Fatalf("failed to write .env: %v", err)
	}
	_, err = dream.SafeReadProjectExcerpt(projRoot, ".env", 100)
	if err == nil || !strings.Contains(err.Error(), "sensitive path rejected") {
		t.Errorf("expected .env rejection, got: %v", err)
	}

	// 5. Sensitive file rejection (id_rsa)
	keyFile := filepath.Join(projRoot, "id_rsa")
	if err := os.WriteFile(keyFile, []byte("-----BEGIN RSA PRIVATE KEY-----"), 0600); err != nil {
		t.Fatalf("failed to write id_rsa: %v", err)
	}
	_, err = dream.SafeReadProjectExcerpt(projRoot, "id_rsa", 100)
	if err == nil || !strings.Contains(err.Error(), "sensitive path rejected") {
		t.Errorf("expected id_rsa rejection, got: %v", err)
	}

	// 6. Non-regular file rejection (directory)
	subDir := filepath.Join(projRoot, "subpkg")
	if err := os.MkdirAll(subDir, 0700); err != nil {
		t.Fatalf("failed to create subpkg: %v", err)
	}
	_, err = dream.SafeReadProjectExcerpt(projRoot, "subpkg", 100)
	if err == nil || !strings.Contains(err.Error(), "non-regular file rejected") {
		t.Errorf("expected directory rejection, got: %v", err)
	}

	// 7. Large file bounded read before allocation
	largeFile := filepath.Join(projRoot, "large.txt")
	largeData := []byte(strings.Repeat("a", 10000))
	if err := os.WriteFile(largeFile, largeData, 0600); err != nil {
		t.Fatalf("failed to write large file: %v", err)
	}
	excerpt, err := dream.SafeReadProjectExcerpt(projRoot, "large.txt", 250)
	if err != nil {
		t.Fatalf("expected bounded read to succeed, got: %v", err)
	}
	if len(excerpt) != 250 {
		t.Errorf("expected bounded excerpt of length 250, got %d", len(excerpt))
	}
}

type mockContradictingProvider struct{}

func (m *mockContradictingProvider) Name() assist.ModelProvider {
	return assist.ProviderMock
}

func (m *mockContradictingProvider) Evaluate(ctx context.Context, model string, state map[string]any, questions map[string]assist.Question) (*assist.ProviderResponse, error) {
	nouls := make(map[string]assist.NoulAnswer)
	for qID := range questions {
		if strings.HasPrefix(qID, "sub_") {
			nouls[qID] = assist.NoulAnswer{Noul: 0.05}
		} else if strings.HasPrefix(qID, "contra_") {
			nouls[qID] = assist.NoulAnswer{Noul: 0.95}
		}
	}
	return &assist.ProviderResponse{
		Provider: assist.ProviderMock,
		Model:    "mock-contra",
		Nouls:    nouls,
	}, nil
}

func TestReconcileSemanticDrift_PersistenceAndDryRun(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	projDir := filepath.Join(tmp, "project")
	if err := os.MkdirAll(projDir, 0700); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}
	proj, err := st.EnsureProject(projDir)
	if err != nil {
		t.Fatalf("failed to ensure project: %v", err)
	}

	codePath := filepath.Join(projDir, "lib.go")
	if err := os.WriteFile(codePath, []byte("package lib\nconst Port = 8080\n"), 0600); err != nil {
		t.Fatalf("failed to write lib.go: %v", err)
	}

	fact := store.Fact{
		ID:            "fact-port",
		ProjectID:     proj.ID,
		Claim:         "Port is 9090",
		RepoFiles:     []string{"lib.go"},
		FileRefsExist: true,
		Verified:      true,
		ReviewStatus:  "approved",
	}
	if err := st.UpsertFact(fact); err != nil {
		t.Fatalf("failed to insert fact: %v", err)
	}

	ctx := context.Background()
	provider := &mockContradictingProvider{}

	// 1. Dry run must NOT mutate verified flag in database
	preview, err := dream.ReconcileSemanticDrift(ctx, st, provider, proj.ID, true)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if preview.SemanticDrift != 1 {
		t.Errorf("expected 1 drift warning in dry run, got %d", preview.SemanticDrift)
	}
	factsAfterDryRun, err := st.FactsByProject(proj.ID)
	if err != nil || len(factsAfterDryRun) != 1 {
		t.Fatalf("failed to query facts: %v", err)
	}
	if !factsAfterDryRun[0].Verified {
		t.Errorf("dry run must not mutate verified flag in DB")
	}

	// 2. Normal run must persist Verified=false while preserving FileRefsExist=true and authority
	res, err := dream.ReconcileSemanticDrift(ctx, st, provider, proj.ID, false)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if res.SemanticDrift != 1 {
		t.Errorf("expected 1 semantic drift, got %d", res.SemanticDrift)
	}

	factsAfterRun, err := st.FactsByProject(proj.ID)
	if err != nil || len(factsAfterRun) != 1 {
		t.Fatalf("failed to query facts: %v", err)
	}
	f := factsAfterRun[0]
	if f.Verified {
		t.Errorf("expected Verified to be false after contradiction, got true")
	}
	if !f.FileRefsExist {
		t.Errorf("expected FileRefsExist to remain true (file exists on disk), got false")
	}
	if f.ReviewStatus != "approved" {
		t.Errorf("expected fact authority (review_status) to be preserved as approved, got %s", f.ReviewStatus)
	}
}
