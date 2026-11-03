package dream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/assist"
	"github.com/jadmadi/sila/internal/store"
)

// SemanticReconcileResult records both physical file existence and System One semantic verification.
type SemanticReconcileResult struct {
	TotalChecked       int      `json:"total_checked"`
	PhysicalReVerified int      `json:"physical_re_verified"`
	NewlyStale         int      `json:"newly_stale"`
	Recovered          int      `json:"recovered"`
	SemanticAudited    int      `json:"semantic_audited"`
	SemanticVerified   int      `json:"semantic_verified"`
	SemanticDrift      int      `json:"semantic_drift"`
	DriftWarnings      []string `json:"drift_warnings,omitempty"`
	DurationMs         int64    `json:"duration_ms"`
}

// SafeReadProjectExcerpt safely opens and reads a bounded prefix of a repo file within a project root.
// It verifies:
// 1. Path stays strictly inside projectRoot after filepath.Clean and filepath.EvalSymlinks.
// 2. File is a regular file (not directory, socket, fifo, device).
// 3. File is not an excluded sensitive file (.env, private keys, git internals).
// 4. Reads at most maxBytes before allocation using io.LimitReader.
func SafeReadProjectExcerpt(projectRoot, relPath string, maxBytes int64) (string, error) {
	if projectRoot == "" {
		return "", errors.New("project root is not set")
	}
	cleanRoot, err := filepath.Abs(filepath.Clean(projectRoot))
	if err != nil {
		return "", fmt.Errorf("invalid project root: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		realRoot = cleanRoot
	}

	cleanRel := filepath.Clean(relPath)
	if filepath.IsAbs(cleanRel) {
		if !strings.HasPrefix(cleanRel, cleanRoot+string(filepath.Separator)) && cleanRel != cleanRoot {
			return "", fmt.Errorf("absolute path outside project root: %s", relPath)
		}
	} else {
		if strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
			return "", fmt.Errorf("path traversal outside project root: %s", relPath)
		}
	}

	targetPath := filepath.Join(realRoot, cleanRel)
	realTarget, err := filepath.EvalSymlinks(targetPath)
	if err != nil {
		return "", err
	}

	// Strict containment check: realTarget must start with realRoot + Separator
	rootWithSep := realRoot
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	if !strings.HasPrefix(realTarget, rootWithSep) && realTarget != realRoot {
		return "", fmt.Errorf("symlink points outside project root: %s -> %s", relPath, realTarget)
	}

	// Exclude sensitive paths (.env, private keys, git internals)
	base := strings.ToLower(filepath.Base(realTarget))
	if strings.HasPrefix(base, ".env") ||
		strings.Contains(base, "id_rsa") ||
		strings.Contains(base, "id_ed25519") ||
		strings.Contains(base, "id_ecdsa") ||
		strings.HasSuffix(base, ".pem") ||
		strings.HasSuffix(base, ".key") ||
		strings.Contains(filepath.ToSlash(realTarget), "/.git/") {
		return "", fmt.Errorf("access to sensitive path rejected: %s", relPath)
	}

	fi, err := os.Lstat(realTarget)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("non-regular file rejected: %s (mode: %s)", relPath, fi.Mode())
	}

	f, err := os.Open(realTarget)
	if err != nil {
		return "", err
	}
	defer f.Close()

	if maxBytes <= 0 {
		maxBytes = 500
	}
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(io.LimitReader(f, maxBytes), buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	return string(buf[:n]), nil
}

// ReconcileSemanticDrift performs disk-grounded verification followed by System One semantic verification.
func ReconcileSemanticDrift(ctx context.Context, st *store.Store, provider assist.Provider, projectSel string, dryRun bool) (SemanticReconcileResult, error) {
	start := time.Now()
	var out SemanticReconcileResult

	var phys store.ReconcileResult
	var err error
	if dryRun {
		phys, err = st.ReconcileDriftPreview(projectSel)
	} else {
		phys, err = st.ReconcileDrift(projectSel)
	}
	if err != nil {
		return out, err
	}

	out.TotalChecked = phys.TotalChecked
	out.PhysicalReVerified = phys.ReVerified
	out.NewlyStale = phys.NewlyStale
	out.Recovered = phys.Recovered

	if provider == nil {
		out.DurationMs = time.Since(start).Milliseconds()
		return out, nil
	}

	projects, err := st.Projects()
	if err != nil {
		return out, err
	}

	var items []assist.FactVerificationItem
	itemFactMap := make(map[string]*store.Fact)

	for _, p := range projects {
		if projectSel != "" && !store.ProjectSelectorMatches(p, projectSel) {
			continue
		}
		if p.Path == "" {
			continue
		}
		facts, fErr := st.FactsByProject(p.ID)
		if fErr != nil {
			continue
		}
		for i := range facts {
			f := &facts[i]
			if len(f.RepoFiles) == 0 || !f.FileRefsExist {
				continue
			}

			readAny := false
			for _, rf := range f.RepoFiles {
				excerpt, readErr := SafeReadProjectExcerpt(p.Path, rf, 500)
				if readErr != nil {
					continue
				}
				if len(excerpt) > 0 {
					items = append(items, assist.FactVerificationItem{
						ID:          f.ID,
						Claim:       f.Claim,
						RepoFile:    rf,
						CodeExcerpt: excerpt,
					})
					itemFactMap[f.ID] = f
					readAny = true
					break
				}
			}
			if !readAny && len(f.RepoFiles) > 0 {
				out.DriftWarnings = append(out.DriftWarnings, fmt.Sprintf("no readable evidence files found within project root for fact %s", f.ID))
			}

			if len(items) >= 20 {
				break
			}
		}
		if len(items) >= 20 {
			break
		}
	}

	if len(items) == 0 {
		out.DurationMs = time.Since(start).Milliseconds()
		return out, nil
	}

	verCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	vResults, err := assist.VerifyFacts(verCtx, provider, items)
	if err != nil || len(vResults) == 0 {
		out.DurationMs = time.Since(start).Milliseconds()
		return out, nil
	}

	for _, vr := range vResults {
		out.SemanticAudited++
		f, ok := itemFactMap[vr.ID]
		if !ok || f == nil {
			continue
		}

		switch vr.Verdict {
		case "substantiated":
			out.SemanticVerified++
			if !dryRun {
				f.Verified = true
				_ = st.RecordFactFeedback(ctx, f.ID, "helpful")
				if err := st.UpsertFact(*f); err != nil {
					out.DriftWarnings = append(out.DriftWarnings, fmt.Sprintf("failed to persist verification for %s: %v", f.ID, err))
				}
			}
		case "contradicted":
			out.SemanticDrift++
			warn := fmt.Sprintf("%q contradicted by %s (sub: %.2f, contra: %.2f)",
				f.Claim, vr.RepoFile, vr.Substantiated, vr.Contradicted)
			out.DriftWarnings = append(out.DriftWarnings, warn)
			if !dryRun {
				f.Verified = false
				_ = st.RecordFactFeedback(ctx, f.ID, "stale")
				if err := st.UpsertFact(*f); err != nil {
					out.DriftWarnings = append(out.DriftWarnings, fmt.Sprintf("failed to persist drift status for %s: %v", f.ID, err))
				}
			}
		}
	}

	out.DurationMs = time.Since(start).Milliseconds()
	return out, nil
}
