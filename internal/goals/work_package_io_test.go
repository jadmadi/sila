package goals

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkPackageIO(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "sample-goal"

	// AC-004: Missing package returns identifiable absence error
	_, err := LoadWorkPackage(tmpDir, slug)
	if err == nil {
		t.Fatalf("expected error loading absent package, got nil")
	}
	if !IsWorkPackageNotFound(err) {
		t.Errorf("expected IsWorkPackageNotFound to be true, got err: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected errors.Is(err, os.ErrNotExist) to be true, got: %v", err)
	}

	// AC-005: Save and round-trip valid package
	pkg := validWorkPackageFixture()
	pkg.GoalSlug = slug
	pkg.Revision = 1

	// Ensure files referenced exist in tmpDir
	_ = os.MkdirAll(filepath.Join(tmpDir, "goals"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "internal", "goals"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "internal", "goals", "types.go"), []byte("package goals\n"), 0644)

	if err := SaveWorkPackage(tmpDir, slug, pkg); err != nil {
		t.Fatalf("SaveWorkPackage: %v", err)
	}

	loaded, err := LoadWorkPackage(tmpDir, slug)
	if err != nil {
		t.Fatalf("LoadWorkPackage: %v", err)
	}
	if loaded.GoalSlug != slug {
		t.Errorf("expected slug %q, got %q", slug, loaded.GoalSlug)
	}
	if loaded.Revision != 1 {
		t.Errorf("expected revision 1, got %d", loaded.Revision)
	}
	if len(loaded.Tasks) != len(pkg.Tasks) {
		t.Fatalf("expected %d tasks, got %d", len(pkg.Tasks), len(loaded.Tasks))
	}
	if loaded.Tasks[0].ID != pkg.Tasks[0].ID {
		t.Errorf("expected task ID %q, got %q", pkg.Tasks[0].ID, loaded.Tasks[0].ID)
	}

	// Verify file mode
	pkgPath := WorkPackagePath(tmpDir, slug)
	fi, err := os.Stat(pkgPath)
	if err != nil {
		t.Fatalf("Stat work-package.json: %v", err)
	}
	if fi.Mode().Perm() != 0644 {
		t.Errorf("expected file mode 0644, got %o", fi.Mode().Perm())
	}

	// AC-004: Malformed existing package fails closed and does not return ErrNotExist
	if err := os.WriteFile(pkgPath, []byte(`{"schema_version":"broken`), 0644); err != nil {
		t.Fatalf("write malformed json: %v", err)
	}
	_, err = LoadWorkPackage(tmpDir, slug)
	if err == nil {
		t.Fatalf("expected error loading malformed package, got nil")
	}
	if IsWorkPackageNotFound(err) {
		t.Errorf("malformed package must not be treated as not found: %v", err)
	}
	if !strings.Contains(err.Error(), "corrupt or invalid work package") {
		t.Errorf("expected corrupt error message, got: %v", err)
	}
}

func TestWorkPackagePathBoundary(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "boundary-goal"

	_ = os.MkdirAll(filepath.Join(tmpDir, "goals"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "internal", "goals"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "internal", "goals", "types.go"), []byte("package goals\n"), 0644)

	// AC-006: Invalid slug rejection
	badSlugs := []string{
		"../escape",
		"UPPERCASE",
		"has/slash",
		"has_underscore",
		"-leading-dash",
	}
	pkg := validWorkPackageFixture()
	for _, bs := range badSlugs {
		pkg.GoalSlug = bs
		if err := SaveWorkPackage(tmpDir, bs, pkg); err == nil {
			t.Errorf("expected error for bad slug %q, got nil", bs)
		}
		if _, err := LoadWorkPackage(tmpDir, bs); err == nil {
			t.Errorf("expected error loading bad slug %q, got nil", bs)
		}
	}

	pkg.GoalSlug = slug

	// AC-006: Absolute path rejection
	badAbsPkg := cloneWorkPackage(pkg)
	badAbsPkg.Tasks[0].Files.Read = []string{"/etc/passwd"}
	if err := SaveWorkPackage(tmpDir, slug, badAbsPkg); err == nil {
		t.Errorf("expected error on absolute read path, got nil")
	}

	// AC-006: Traversal path rejection
	badTraversalPkg := cloneWorkPackage(pkg)
	badTraversalPkg.Tasks[0].Files.Read = []string{"../../outside.txt"}
	if err := SaveWorkPackage(tmpDir, slug, badTraversalPkg); err == nil {
		t.Errorf("expected error on traversal read path, got nil")
	}

	// AC-006: Symlink escape rejection
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	_ = os.WriteFile(outsideFile, []byte("secret"), 0644)

	symlinkPath := filepath.Join(tmpDir, "escape-link")
	_ = os.Symlink(outsideFile, symlinkPath)

	badSymlinkPkg := cloneWorkPackage(pkg)
	badSymlinkPkg.Tasks[0].Files.Read = []string{"escape-link"}
	if err := SaveWorkPackage(tmpDir, slug, badSymlinkPkg); err == nil {
		t.Errorf("expected error on symlink escape path, got nil")
	}

	// Intermediate directory symlink escape rejection (existing and non-existent leaf)
	symlinkDir := filepath.Join(tmpDir, "escape-dir")
	_ = os.Symlink(outsideDir, symlinkDir)

	badDirSymlinkPkg := cloneWorkPackage(pkg)
	badDirSymlinkPkg.Tasks[0].Files.Read = []string{"escape-dir/secret.txt"}
	if err := SaveWorkPackage(tmpDir, slug, badDirSymlinkPkg); err == nil {
		t.Errorf("expected error on intermediate directory symlink escape path, got nil")
	}

	badDirSymlinkNonExistent := cloneWorkPackage(pkg)
	badDirSymlinkNonExistent.Tasks[0].Files.Edit = []string{"escape-dir/nonexistent.txt"}
	if err := SaveWorkPackage(tmpDir, slug, badDirSymlinkNonExistent); err == nil {
		t.Errorf("expected error on intermediate directory symlink escape with non-existent leaf, got nil")
	}

	// AC-006: Create path with non-existent parent
	badCreatePkg := cloneWorkPackage(pkg)
	badCreatePkg.Tasks[0].Files.Create = []string{"nonexistent-parent/dir/file.go"}
	if err := SaveWorkPackage(tmpDir, slug, badCreatePkg); err == nil {
		t.Errorf("expected error on create path without existing parent, got nil")
	}
}

func TestWorkPackageRevisionConflict(t *testing.T) {
	tmpDir := t.TempDir()
	slug := "revision-goal"

	_ = os.MkdirAll(filepath.Join(tmpDir, "goals"), 0755)
	_ = os.MkdirAll(filepath.Join(tmpDir, "internal", "goals"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "internal", "goals", "types.go"), []byte("package goals\n"), 0644)

	pkg := validWorkPackageFixture()
	pkg.GoalSlug = slug
	pkg.Revision = 1

	// Initial save revision 1 succeeds
	if err := SaveWorkPackage(tmpDir, slug, pkg); err != nil {
		t.Fatalf("Save initial revision 1: %v", err)
	}

	// AC-006: Attempting to save revision 1 again must conflict
	conflictPkg := cloneWorkPackage(pkg)
	conflictPkg.Revision = 1
	conflictPkg.Purpose = "Overwriting revision 1 without incrementing"
	err := SaveWorkPackage(tmpDir, slug, conflictPkg)
	if err == nil {
		t.Fatalf("expected revision conflict saving revision 1 over revision 1, got nil")
	}
	if !strings.Contains(err.Error(), "revision conflict") {
		t.Errorf("expected 'revision conflict' in error, got: %v", err)
	}

	// Verify original file contents were NOT overwritten
	loaded, err := LoadWorkPackage(tmpDir, slug)
	if err != nil {
		t.Fatalf("LoadWorkPackage: %v", err)
	}
	if loaded.Purpose != pkg.Purpose {
		t.Errorf("original purpose was overwritten! got %q, want %q", loaded.Purpose, pkg.Purpose)
	}

	// AC-006: Skipping revisions (e.g. 1 -> 3) fails
	skipPkg := cloneWorkPackage(pkg)
	skipPkg.Revision = 3
	if err := SaveWorkPackage(tmpDir, slug, skipPkg); err == nil {
		t.Fatalf("expected revision conflict skipping revision to 3, got nil")
	}

	// Saving revision 2 (exact R + 1) succeeds
	nextPkg := cloneWorkPackage(pkg)
	nextPkg.Revision = 2
	nextPkg.Purpose = "Updated purpose for revision 2"
	if err := SaveWorkPackage(tmpDir, slug, nextPkg); err != nil {
		t.Fatalf("SaveWorkPackage revision 2: %v", err)
	}

	loaded2, err := LoadWorkPackage(tmpDir, slug)
	if err != nil {
		t.Fatalf("LoadWorkPackage revision 2: %v", err)
	}
	if loaded2.Revision != 2 || loaded2.Purpose != nextPkg.Purpose {
		t.Errorf("loaded revision 2 failed: %+v", loaded2)
	}
}
