package goals

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TreeReconcileCandidate is a goal whose implementation is present in the
// current tree but whose attribution commit was lost to a history rewrite.
type TreeReconcileCandidate struct {
	Slug         string   `json:"slug"`
	Status       string   `json:"status"`
	MatchedFiles []string `json:"matched_files"`
	MissingFiles []string `json:"missing_files"`
	PlanChecked  int      `json:"plan_checked"`
	PlanTotal    int      `json:"plan_total"`
	Head         string   `json:"head"`
	Action       string   `json:"action"`
}

// TreeReconcileReport is the stable JSON contract for
// `sila goals reconcile --tree`.
type TreeReconcileReport struct {
	Candidates []TreeReconcileCandidate `json:"candidates"`
	Applied    []TreeReconcileCandidate `json:"applied"`
}

// TreeAttribution records why a goal was attributed without a commit subject.
type TreeAttribution struct {
	Version      string    `json:"version"`
	GoalSlug     string    `json:"goal_slug"`
	Head         string    `json:"head"`
	TreeHash     string    `json:"tree_hash"`
	AttributedBy string    `json:"attributed_by"`
	MatchedFiles []string  `json:"matched_files"`
	AttributedAt time.Time `json:"attributed_at"`
}

// ReconcileTree proposes (and optionally applies) attributions for complete
// goals whose declared files all exist in the current tree. It never certifies.
func ReconcileTree(rootDir string, apply, allowDirty bool) (*TreeReconcileReport, error) {
	rootDir = resolveGoalsRootDir(rootDir)
	head, err := gitOutput(rootDir, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("tree reconcile requires a git repository: %w", err)
	}
	treeHash, err := gitOutput(rootDir, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return nil, fmt.Errorf("resolve head tree: %w", err)
	}
	dirty, err := gitOutput(rootDir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return nil, fmt.Errorf("inspect work tree: %w", err)
	}
	if apply && strings.TrimSpace(dirty) != "" && !allowDirty {
		return nil, fmt.Errorf("work tree has uncommitted changes; commit them or pass --allow-dirty")
	}

	audit, err := ScanGoals(rootDir)
	if err != nil {
		return nil, err
	}
	report := &TreeReconcileReport{Candidates: []TreeReconcileCandidate{}, Applied: []TreeReconcileCandidate{}}
	sort.Slice(audit.Goals, func(i, j int) bool { return audit.Goals[i].Slug < audit.Goals[j].Slug })

	for i := range audit.Goals {
		g := &audit.Goals[i]
		candidate, ok := treeCandidate(rootDir, g, head, treeHash)
		if !ok {
			continue
		}
		if !apply {
			candidate.Action = "propose"
			report.Candidates = append(report.Candidates, candidate)
			continue
		}
		if err := applyTreeAttribution(rootDir, g.Slug, head, treeHash, candidate.MatchedFiles); err != nil {
			return nil, fmt.Errorf("attribute %s: %w", g.Slug, err)
		}
		candidate.Action = "applied"
		report.Applied = append(report.Applied, candidate)
	}
	if apply && len(report.Applied) > 0 {
		if refreshed, err := ScanGoals(rootDir); err == nil {
			_ = SyncReadme(rootDir, refreshed)
		}
	}
	return report, nil
}

func treeCandidate(rootDir string, g *Goal, head, treeHash string) (TreeReconcileCandidate, bool) {
	candidate := TreeReconcileCandidate{Slug: g.Slug, Status: g.Status, Head: head}
	if g.Status == "superseded" || g.VerificationStatus == "verified" || g.Claim != nil {
		return candidate, false
	}
	if hasCurrentTreeAttribution(rootDir, g.Slug, treeHash) {
		return candidate, false
	}
	if cert, _ := GetActiveCertification(rootDir, g.Slug); cert != nil && cert.Verified {
		return candidate, false
	}
	if !g.HasGoalMd || !g.HasPlanMd || !g.HasFactsMd {
		return candidate, false
	}
	candidate.PlanChecked = g.TasksMet
	candidate.PlanTotal = g.TasksTotal
	if g.TasksTotal == 0 || g.TasksMet != g.TasksTotal {
		return candidate, false
	}
	if len(g.FilesToTouch) == 0 {
		return candidate, false
	}
	for _, pattern := range g.FilesToTouch {
		matches := expandTouchPattern(rootDir, pattern)
		if len(matches) == 0 {
			candidate.MissingFiles = append(candidate.MissingFiles, pattern)
			continue
		}
		candidate.MatchedFiles = append(candidate.MatchedFiles, matches...)
	}
	if len(candidate.MissingFiles) > 0 {
		return candidate, false
	}
	candidate.MatchedFiles = dedupeStrings(candidate.MatchedFiles)
	return candidate, true
}

func expandTouchPattern(rootDir, pattern string) []string {
	cleaned := strings.TrimSuffix(strings.TrimSpace(pattern), "/")
	if cleaned == "" {
		return nil
	}
	cleaned = filepath.FromSlash(cleaned)
	if strings.ContainsAny(cleaned, "*?[") {
		matches, _ := filepath.Glob(filepath.Join(rootDir, cleaned))
		out := make([]string, 0, len(matches))
		for _, m := range matches {
			rel, err := filepath.Rel(rootDir, m)
			if err == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		sort.Strings(out)
		return out
	}
	if _, err := os.Stat(filepath.Join(rootDir, cleaned)); err != nil {
		return nil
	}
	return []string{filepath.ToSlash(cleaned)}
}

func hasCurrentTreeAttribution(rootDir, slug, treeHash string) bool {
	data, err := os.ReadFile(filepath.Join(rootDir, "goals", slug, "tree-attribution.json"))
	if err != nil {
		return false
	}
	var attr TreeAttribution
	if json.Unmarshal(data, &attr) != nil {
		return false
	}
	return attr.TreeHash != "" && attr.TreeHash == treeHash
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func applyTreeAttribution(rootDir, slug, head, treeHash string, matched []string) error {
	goalDir := filepath.Join(rootDir, "goals", slug)
	if _, err := os.Stat(goalDir); err != nil {
		return err
	}
	short := head
	if len(short) > 7 {
		short = short[:7]
	}
	retro := RetrospectiveRecord{
		GoalSlug:   slug,
		Status:     "implemented",
		Commit:     short,
		RecordedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(retro, "", "  ")
	if err != nil {
		return err
	}
	if err := AtomicWriteFile(filepath.Join(goalDir, "retrospective.json"), append(data, '\n'), 0o644); err != nil {
		return err
	}
	attribution := TreeAttribution{
		Version:      "sila/tree-attribution/v1",
		GoalSlug:     slug,
		Head:         head,
		TreeHash:     treeHash,
		AttributedBy: "tree-reconcile",
		MatchedFiles: matched,
		AttributedAt: time.Now().UTC(),
	}
	data, err = json.MarshalIndent(attribution, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(filepath.Join(goalDir, "tree-attribution.json"), append(data, '\n'), 0o644)
}

func gitOutput(rootDir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", rootDir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
