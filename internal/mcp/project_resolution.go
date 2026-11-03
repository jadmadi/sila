package mcp

import (
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

func (s *Server) resolveTargetProject(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		// Directory path on disk: resolve strictly with path boundary, avoiding fuzzy cross-repo matching
		if info, err := os.Stat(raw); err == nil && info.IsDir() {
			if s.store != nil {
				if p, err := s.store.ResolveScope(raw, raw); err == nil && p.ID != "" {
					return p.ID, nil
				}
				return s.store.ProjectID(raw), nil
			}
			canon := store.CanonicalProjectPath(raw)
			return store.ProjectID(canon), nil
		}
		if s.store != nil {
			if p, err := s.store.ResolveScope(raw, ""); err == nil && p.ID != "" {
				return p.ID, nil
			}
			if p, err := s.store.ResolveProjectFuzzy(raw); err == nil && p != nil {
				return p.ID, nil
			}
		}
		return raw, nil
	}

	// Auto-discovery from active working directory / git context.
	// Resolve the workspace first so a multi-repo root selects the same child
	// repository as prime, salam, and goals.
	if identity, err := store.ResolveWorkspaceIdentity(""); err == nil && identity.ActiveDir != "" {
		if s.store != nil {
			if p, err := s.store.ResolveScope("", identity.ActiveDir); err == nil && p.ID != "" {
				return p.ID, nil
			}
		}
		return identity.ProjectID, nil
	}
	if s.store != nil {
		if p, err := s.store.ResolveScope("", ""); err == nil && p.ID != "" {
			return p.ID, nil
		}
	}

	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		canon := store.CanonicalProjectPath(cwd)
		if s.store != nil {
			return s.store.ProjectID(canon), nil
		}
		return store.ProjectID(canon), nil
	}

	return "default", nil
}

// resolveTargetProjectDir resolves a project parameter to a valid repository filesystem directory.
// If raw is non-empty, it resolves registered project path or direct filesystem path.
// If raw is empty, it falls back to cwd or canonical repository root.
func (s *Server) resolveTargetProjectDir(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if fi, err := os.Stat(raw); err == nil && fi.IsDir() {
			return store.CanonicalProjectPath(raw), nil
		}
		if s.store != nil {
			if p, err := s.store.ResolveScope(raw, raw); err == nil && p.Path != "" {
				if fi, err := os.Stat(p.Path); err == nil && fi.IsDir() {
					return p.Path, nil
				}
			}
			if p, err := s.store.ResolveProjectFuzzy(raw); err == nil && p != nil {
				if fi, err := os.Stat(p.Path); err == nil && fi.IsDir() {
					return p.Path, nil
				}
			}
		}
		// A bare project slug may be passed from inside that project's working
		// directory before it is registered. Resolve it to the active workspace
		// directory only when the derived ID matches the selector exactly.
		if identity, idErr := store.ResolveWorkspaceIdentity(""); idErr == nil && identity.ActiveDir != "" {
			if s.store != nil {
				if p, pErr := s.store.ResolveScope("", identity.ActiveDir); pErr == nil &&
					strings.EqualFold(p.ID, raw) {
					if fi, statErr := os.Stat(p.Path); statErr == nil && fi.IsDir() {
						return p.Path, nil
					}
				}
			} else if strings.EqualFold(store.ProjectID(identity.ActiveDir), raw) {
				return identity.ActiveDir, nil
			}
		}
		return raw, nil
	}

	if identity, err := store.ResolveWorkspaceIdentity(""); err == nil && identity.ActiveDir != "" {
		return identity.ActiveDir, nil
	}
	if s.store != nil {
		if p, err := s.store.ResolveScope("", ""); err == nil && p.Path != "" {
			return p.Path, nil
		}
	}

	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		return store.CanonicalProjectPath(cwd), nil
	}

	return ".", nil
}
