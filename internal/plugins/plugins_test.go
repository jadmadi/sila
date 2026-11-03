package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerInitAndBuild(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "sila-plugins")
	binDir := filepath.Join(tempDir, "bin")

	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create dummy go.mod in srcDir
	goModContent := "module github.com/jadmadi/sila-plugins\n\ngo 1.24\n"
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(binDir, srcDir)
	mgr.DataDir = filepath.Join(tempDir, "data")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Test Init
	pkgDir, err := mgr.Init("sample-tool", "Test Author", "MIT")
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Check files created
	expectedFiles := []string{
		"main.go",
		"sampletool.go",
		"sampletool_test.go",
		"README.md",
	}
	for _, f := range expectedFiles {
		if _, err := os.Stat(filepath.Join(pkgDir, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}

	// Run tests on generated scaffold
	testCmd := exec.CommandContext(ctx, "go", "test", "-race", "./cmd/sample-tool/...")
	testCmd.Dir = srcDir
	if out, err := testCmd.CombinedOutput(); err != nil {
		t.Fatalf("generated scaffold tests failed: %v\n%s", err, string(out))
	}

	// 2. Test Install
	installed, err := mgr.Install(ctx, "sample-tool")
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if len(installed) != 1 || installed[0] != "sample-tool" {
		t.Fatalf("unexpected installed output: %v", installed)
	}

	binPath := filepath.Join(binDir, "sample-tool")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("expected binary at %s: %v", binPath, err)
	}

	// 3. Test List
	items, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Name != "sample-tool" || items[0].Status != StatusInstalled {
		t.Errorf("unexpected item: %+v", items[0])
	}
	if items[0].Version != "0.1.0" {
		t.Errorf("expected version 0.1.0, got %s", items[0].Version)
	}

	// 4. Test Lint
	report, err := mgr.Lint(ctx, "sample-tool")
	if err != nil {
		t.Fatalf("Lint failed: %v", err)
	}
	if !report.Clean {
		t.Errorf("expected clean lint report, got errors: %v", report.Errors)
	}
	if report.SchemaVersion != "1.0.0" {
		t.Errorf("expected schema version 1.0.0, got %s", report.SchemaVersion)
	}

	// 5. Test Uninstall
	if err := mgr.Uninstall("sample-tool"); err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Fatalf("expected binary to be removed, but still exists")
	}

	// After uninstall, item should show as StatusAvailable
	itemsAfter, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("List after uninstall failed: %v", err)
	}
	if len(itemsAfter) != 1 || itemsAfter[0].Status != StatusAvailable {
		t.Errorf("expected item status available, got: %+v", itemsAfter)
	}
}

func TestManagerLinkAndUnlink(t *testing.T) {
	tempDir := t.TempDir()
	binDir := filepath.Join(tempDir, "bin")
	srcDir := filepath.Join(tempDir, "src")

	mgr := NewManager(binDir, srcDir)
	mgr.DataDir = filepath.Join(tempDir, "data")
	ctx := context.Background()

	// Create a dummy executable
	dummyPath := filepath.Join(tempDir, "my-external-tool")
	script := "#!/bin/sh\necho ok\n"
	if err := os.WriteFile(dummyPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// 1. Test Link
	name, err := mgr.Link(dummyPath)
	if err != nil {
		t.Fatalf("Link failed: %v", err)
	}
	if name != "my-external-tool" {
		t.Errorf("expected name my-external-tool, got %s", name)
	}

	linkPath := filepath.Join(binDir, "my-external-tool")
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("expected symlink to exist: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink mode, got: %v", fi.Mode())
	}

	// 2. Test List detects linked status
	items, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Status != StatusLinked {
		t.Errorf("expected status linked, got %s", items[0].Status)
	}
	if items[0].TargetLink != dummyPath {
		t.Errorf("expected target link %s, got %s", dummyPath, items[0].TargetLink)
	}

	// 3. Test Unlink
	if err := mgr.Unlink("my-external-tool"); err != nil {
		t.Fatalf("Unlink failed: %v", err)
	}
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Fatalf("expected symlink to be removed")
	}
}

func TestReadCatalogDescriptions(t *testing.T) {
	tempDir := t.TempDir()
	toolsList := `# Tools Catalog

## Tools

- ` + "`cfclean`" + `: Git clean filter for Cloudflare. usage: cfclean [flags]. notes: sample.
- ` + "`d1ops`" + `: D1 database operations. usage: d1ops <cmd>.
`
	if err := os.WriteFile(filepath.Join(tempDir, "tools-list.md"), []byte(toolsList), 0644); err != nil {
		t.Fatal(err)
	}

	descs := ReadCatalogDescriptions(tempDir)
	if descs["cfclean"] != "Git clean filter for Cloudflare" {
		t.Errorf("unexpected cfclean desc: %q", descs["cfclean"])
	}
	if descs["d1ops"] != "D1 database operations" {
		t.Errorf("unexpected d1ops desc: %q", descs["d1ops"])
	}
}
