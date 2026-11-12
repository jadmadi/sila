package dream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	projects, err := st.Projects()
	if err != nil {
		return out, err
	}
	for _, p := range projects {
		if projectSel != "" && !store.ProjectSelectorMatches(p, projectSel) {
			continue
		}
		facts, fErr := st.FactsByProject(p.ID)
		if fErr != nil {
			continue
		}
		for _, pair := range DetectContradictingFacts(facts) {
			out.DriftWarnings = append(out.DriftWarnings, fmt.Sprintf(
				"contradiction: %q vs %q (scope %s)", pair.ClaimA, pair.ClaimB, pair.ScopeKey))
		}
	}

	if provider == nil {
		out.DurationMs = time.Since(start).Milliseconds()
		return out, nil
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

// ContradictionPair names two active facts that assert opposing directives in
// the same project and scope.
type ContradictionPair struct {
	ProjectID string `json:"project_id"`
	ScopeKey  string `json:"scope_key"`
	FactA     string `json:"fact_a"`
	FactB     string `json:"fact_b"`
	ClaimA    string `json:"claim_a"`
	ClaimB    string `json:"claim_b"`
}

// contradictionNegationRE marks a directive as negative. A contradiction needs
// exactly one side to match.
var contradictionNegationRE = regexp.MustCompile(`(?i)\b(?:not|never|avoid|cannot|can't|won't|doesn't|shouldn't|do not|don't|disable|disabled|disallow|forbid|prohibit|remove|drop|reject|no longer|instead of)\b`)

// DetectContradictingFacts reports active facts in the same project and
// scope_key that assert opposing directives without a supersedes_id edge.
// Facts are grouped by (project, scope) first so the all-pairs scan stays
// bounded by each scope's size rather than the whole store.
func DetectContradictingFacts(facts []store.Fact) []ContradictionPair {
	type groupKey struct{ project, scope string }
	groups := map[groupKey][]store.Fact{}
	var keys []groupKey
	for _, f := range facts {
		if !contradictionCandidate(f) {
			continue
		}
		key := groupKey{project: f.ProjectID, scope: f.ScopeKey}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], f)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].project != keys[j].project {
			return keys[i].project < keys[j].project
		}
		return keys[i].scope < keys[j].scope
	})

	var pairs []ContradictionPair
	for _, key := range keys {
		group := groups[key]
		for i := 0; i < len(group); i++ {
			a := group[i]
			for j := i + 1; j < len(group); j++ {
				b := group[j]
				if a.ID == b.ID || a.SupersedesID == b.ID || b.SupersedesID == a.ID {
					continue
				}
				if !opposingDirectives(a.Claim, b.Claim) {
					continue
				}
				pairs = append(pairs, ContradictionPair{
					ProjectID: a.ProjectID,
					ScopeKey:  a.ScopeKey,
					FactA:     a.ID,
					FactB:     b.ID,
					ClaimA:    a.Claim,
					ClaimB:    b.Claim,
				})
			}
		}
	}
	return pairs
}

func contradictionCandidate(f store.Fact) bool {
	if f.ReviewStatus != store.ReviewApproved || f.IsStaleFlag {
		return false
	}
	if strings.TrimSpace(f.ScopeKey) == "" || strings.TrimSpace(f.Claim) == "" {
		return false
	}
	return true
}

func opposingDirectives(a, b string) bool {
	if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) {
		return false
	}
	aNegated := contradictionNegationRE.MatchString(a)
	bNegated := contradictionNegationRE.MatchString(b)
	if aNegated == bNegated {
		return false
	}
	return Similarity(a, b) >= 0.50
}

// UnmergeResult reports the rows touched by a dream merge rollback.
type UnmergeResult struct {
	MergedID   string `json:"merged_id"`
	RestoredID string `json:"restored_id"`
}

// UnmergeConsolidatedFact reverses a dream merge: it archives the merged fact
// and restores the original it superseded. It refuses when either row changed
// after the merge timestamp, so a post-merge edit is never silently lost.
func UnmergeConsolidatedFact(ctx context.Context, st *store.Store, mergedID string, now time.Time) (*UnmergeResult, error) {
	if st == nil {
		return nil, errors.New("unmerge: store is nil")
	}
	mergedID = strings.TrimSpace(mergedID)
	if mergedID == "" {
		return nil, errors.New("unmerge: merged fact id is required")
	}
	merged, err := st.FactByID(mergedID)
	if err != nil {
		return nil, fmt.Errorf("unmerge: read merged fact: %w", err)
	}
	if merged == nil {
		return nil, fmt.Errorf("unmerge: merged fact %s not found", mergedID)
	}
	if strings.TrimSpace(merged.SupersedesID) == "" {
		return nil, fmt.Errorf("unmerge: fact %s does not supersede another fact", mergedID)
	}
	original, err := st.FactByID(merged.SupersedesID)
	if err != nil {
		return nil, fmt.Errorf("unmerge: read original fact: %w", err)
	}
	if original == nil {
		return nil, fmt.Errorf("unmerge: original fact %s not found", merged.SupersedesID)
	}
	if !original.IsStaleFlag {
		return nil, fmt.Errorf("unmerge: original fact %s is already current", original.ID)
	}

	mergeTime := factTime(merged.PromotedAt)
	if mergeTime.IsZero() {
		mergeTime = factTime(merged.FirstSeen)
	}
	if mergeTime.IsZero() {
		return nil, fmt.Errorf("unmerge: merged fact %s has no merge timestamp", mergedID)
	}
	if factTouchedAfter(merged, mergeTime) {
		return nil, fmt.Errorf("unmerge: merged fact %s was modified after the merge", mergedID)
	}
	if factTouchedAfter(original, mergeTime) {
		return nil, fmt.Errorf("unmerge: original fact %s was modified after the merge", original.ID)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("unmerge: begin transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE facts SET review_status = 'archived', is_stale = 1, supersedes_id = '' WHERE id = ?`, mergedID); err != nil {
		return nil, fmt.Errorf("unmerge: archive merged fact: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE facts SET is_stale = 0 WHERE id = ?`, original.ID); err != nil {
		return nil, fmt.Errorf("unmerge: restore original fact: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("unmerge: commit: %w", err)
	}
	return &UnmergeResult{MergedID: mergedID, RestoredID: original.ID}, nil
}

func factTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func factTouchedAfter(f *store.Fact, cutoff time.Time) bool {
	if f == nil || cutoff.IsZero() {
		return false
	}
	checked := factTime(f.RefsCheckedAt)
	return !checked.IsZero() && checked.After(cutoff)
}
