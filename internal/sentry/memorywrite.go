package sentry

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// MemoryWriteScope is the shared input for pre-commit and stop-gate evaluation.
type MemoryWriteScope struct {
	ProjectID   string
	SessionID   string
	GoalSlug    string
	ActiveGoal  string
	WindowStart time.Time
	Files       []string
}

// MemoryWriteResult is the shared output for both evaluators.
type MemoryWriteResult struct {
	Verdict        string   `json:"verdict"` // pass|fail|skip
	Writes         int      `json:"writes"`
	Memos          int      `json:"memos"`
	Lessons        int      `json:"lessons"`
	Cases          int      `json:"cases"`
	Grounded       int      `json:"grounded"`
	InWindow       int      `json:"in_window,omitempty"`
	UnmatchedFiles []string `json:"unmatched_files,omitempty"`
	Window         string   `json:"window"`
	Diagnostics    []string `json:"diagnostics,omitempty"`
}

// EvaluateMemoryWrites queries at most 500 facts of the four memory kinds and
// counts those grounded to the scope. The ctx is honored by the caller's
// 200ms guard; the query itself is bounded and indexed.
func EvaluateMemoryWrites(ctx context.Context, st *store.Store, scope MemoryWriteScope) (MemoryWriteResult, error) {
	res := MemoryWriteResult{Verdict: "skip"}
	if st == nil {
		res.Diagnostics = []string{"no knowledge store"}
		return res, nil
	}
	if scope.WindowStart.IsZero() {
		scope.WindowStart = time.Now().UTC().Add(-48 * time.Hour)
	}
	res.Window = humanWindow(time.Since(scope.WindowStart))

	files := normalizedSet(scope.Files)
	start := time.Now()
	var facts []store.Fact
	for _, kind := range []string{"memo", "lesson", "case", "operational_learning"} {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		rows, err := st.ListFactsByKind(kind, scope.ProjectID, scope.WindowStart, 500)
		if err != nil {
			return res, fmt.Errorf("MEMORY_STORE_UNAVAILABLE: %w", err)
		}
		facts = append(facts, rows...)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		return res, fmt.Errorf("MEMORY_STORE_SLOW: memory query took %s (budget 200ms)", elapsed)
	}
	if len(facts) > 500 {
		facts = facts[:500]
	}

	seenUnmatched := map[string]bool{}
	for _, fact := range facts {
		if !factGrounded(fact, scope.GoalSlug, files, files, scope.ActiveGoal, scope.SessionID) {
			res.InWindow++
			for _, f := range fact.RepoFiles {
				clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(f)))
				if clean != "" && clean != "." && !seenUnmatched[clean] && len(res.UnmatchedFiles) < 5 {
					seenUnmatched[clean] = true
					res.UnmatchedFiles = append(res.UnmatchedFiles, clean)
				}
			}
			continue
		}
		res.Grounded++
		switch fact.Kind {
		case "case":
			res.Cases++
		case "lesson", "operational_learning":
			res.Lessons++
		default:
			res.Memos++
		}
	}
	res.Writes = res.Memos + res.Lessons + res.Cases
	if res.Writes == 0 {
		res.Verdict = "fail"
		res.Diagnostics = append(res.Diagnostics, "MEMORY_WRITE_MISSING: no grounded memo, lesson, or case (none of the staged files is referenced by a memory write in the window)")
		return res, nil
	}
	res.Verdict = "pass"
	return res, nil
}

// FilesFromList normalizes a file list for the evaluator.
func FilesFromList(files []string) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		if file = strings.TrimSpace(file); file != "" {
			out = append(out, filepath.ToSlash(filepath.Clean(file)))
		}
	}
	return out
}
