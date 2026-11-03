package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// PatchIDResult contains computed patch ID information.
type PatchIDResult struct {
	PatchID   string `json:"patch_id"`
	BaseRef   string `json:"base_ref"`
	MergeBase string `json:"merge_base"`
	HasDiff   bool   `json:"has_diff"`
}

// ComputeTreePatchID computes the semantic patch ID of changes between a base branch
// (defaulting to "main" or "origin/main") and the current working state or HEAD.
// It computes diffs against the merge base (base...HEAD) using git diff and git patch-id --stable.
func ComputeTreePatchID(ctx context.Context, repoDir, baseBranch string, includeStaged bool) (*PatchIDResult, error) {
	if repoDir == "" {
		repoDir = "."
	}

	// 1. Resolve base branch
	baseRef := baseBranch
	if baseRef == "" {
		candidates := []string{"main", "origin/main", "master", "origin/master"}
		for _, c := range candidates {
			cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "--verify", c)
			if err := cmd.Run(); err == nil {
				baseRef = c
				break
			}
		}
	}
	if baseRef == "" {
		baseRef = "HEAD"
	}

	// 2. Find merge base
	var mergeBase string
	mbCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "merge-base", baseRef, "HEAD")
	mbOut, err := mbCmd.Output()
	if err == nil {
		mergeBase = strings.TrimSpace(string(mbOut))
	} else {
		// Fallback to baseRef
		mergeBase = baseRef
	}

	// 3. Obtain diff against merge base
	var diffArgs []string
	if includeStaged {
		// diff between mergeBase and current index / working tree, excluding goal runtime baselines
		diffArgs = []string{"-C", repoDir, "diff", mergeBase, "--", ":(exclude)goals/*/baseline.json"}
	} else {
		// diff between mergeBase and HEAD
		diffArgs = []string{"-C", repoDir, "diff", mergeBase + "...HEAD"}
	}

	diffCmd := exec.CommandContext(ctx, "git", diffArgs...)
	diffOut, err := diffCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	if len(bytes.TrimSpace(diffOut)) == 0 {
		return &PatchIDResult{
			PatchID:   "",
			BaseRef:   baseRef,
			MergeBase: mergeBase,
			HasDiff:   false,
		}, nil
	}

	// 4. Pipe diff through git patch-id --stable
	patchCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "patch-id", "--stable")
	patchCmd.Stdin = bytes.NewReader(diffOut)
	patchOut, err := patchCmd.Output()
	if err != nil {
		// Fallback without --stable flag for older git versions
		patchCmdLegacy := exec.CommandContext(ctx, "git", "-C", repoDir, "patch-id")
		patchCmdLegacy.Stdin = bytes.NewReader(diffOut)
		patchOut, err = patchCmdLegacy.Output()
		if err != nil {
			return nil, fmt.Errorf("git patch-id failed: %w", err)
		}
	}

	// Output format: "<patch-id> <commit-hash>" or "<patch-id>"
	fields := strings.Fields(string(patchOut))
	patchID := ""
	if len(fields) > 0 {
		patchID = fields[0]
	}

	return &PatchIDResult{
		PatchID:   patchID,
		BaseRef:   baseRef,
		MergeBase: mergeBase,
		HasDiff:   patchID != "",
	}, nil
}
