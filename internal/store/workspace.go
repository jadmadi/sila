package store

import (
	"os"

	"github.com/jadmadi/sila/internal/workspace"
)

// WorkspaceIdentity is the resolved active project identity for a directory.
// It is an alias so callers can use store.WorkspaceIdentity while the logic
// lives in the storage-free workspace package (avoiding a store/loaders cycle).
type WorkspaceIdentity = workspace.Identity

// ErrAmbiguousWorkspace is returned when a workspace root has multiple child
// repositories with goal packages and no single preferred child.
var ErrAmbiguousWorkspace = workspace.ErrAmbiguous

// ResolveWorkspaceIdentity resolves the active project identity for dir. It
// uses CanonicalProjectPath first so worktrees and nested subdirectories keep
// the repository identity they already had.
func ResolveWorkspaceIdentity(dir string) (WorkspaceIdentity, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return WorkspaceIdentity{}, err
		}
		dir = cwd
	}
	// Do not call CanonicalProjectPath here: it walks to the nearest .git and
	// would collapse a workspace root nested inside a larger monorepo into the
	// parent. The workspace resolver owns the .code-workspace boundary.
	return workspace.Resolve(dir)
}

// DetectWorkspaceProjectID derives a workspace-prefixed project ID from a
// *.code-workspace file. Kept as a thin compatibility wrapper.
func DetectWorkspaceProjectID(path string) (string, bool) {
	return workspace.DetectProjectID(path)
}
