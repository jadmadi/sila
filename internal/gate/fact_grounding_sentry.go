package gate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jadmadi/sila/internal/store"
)

// FactGroundingWarning represents an active epistemic memory or invariant whose referenced file is deleted in a staged commit.
type FactGroundingWarning struct {
	FilePath string `json:"file_path"`
	FactID   string `json:"fact_id"`
	Claim    string `json:"claim"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
}

// Format returns a human-readable advisory warning message.
func (w FactGroundingWarning) Format() string {
	return fmt.Sprintf("▲ [fact-grounding] Deleted file %q is referenced by active fact [%s]: %s\n  Remedy: Update the memo via 'sila memo' or retire stale fact if intentionally deprecated.",
		w.FilePath, w.FactID, w.Claim)
}

// FactGroundingResult encapsulates the result of auditing deleted files against durable architectural facts.
type FactGroundingResult struct {
	Clean    bool                   `json:"clean"`
	Warnings []FactGroundingWarning `json:"warnings"`
}

// CheckFactGrounding compares deleted or renamed file paths against active facts and invariants in the store.
func CheckFactGrounding(st *store.Store, deletedPaths []string) FactGroundingResult {
	res := FactGroundingResult{
		Clean:    true,
		Warnings: make([]FactGroundingWarning, 0),
	}

	if st == nil || len(deletedPaths) == 0 {
		return res
	}

	facts, err := st.FactsAll()
	if err != nil {
		// Non-fatal: if facts cannot be read, do not block
		return res
	}

	// Normalize deleted paths for fast lookup
	deletedMap := make(map[string]string)
	for _, p := range deletedPaths {
		norm := filepath.ToSlash(strings.TrimSpace(p))
		if norm != "" {
			deletedMap[norm] = p
		}
	}

	for _, f := range facts {
		// Only check active grounded facts or invariants
		isGroundedOrInvariant := f.FileRefsExist ||
			f.Confidence == "grounded" ||
			f.Confidence == "invariant" ||
			f.Kind == "invariant"

		if !isGroundedOrInvariant || len(f.RepoFiles) == 0 {
			continue
		}

		for _, ref := range f.RepoFiles {
			normRef := filepath.ToSlash(strings.TrimSpace(ref))
			if normRef == "" {
				continue
			}

			// Match exact path or suffix match (e.g. cli/internal/gate vs internal/gate)
			for normDel, origDel := range deletedMap {
				if normDel == normRef ||
					strings.HasSuffix(normDel, "/"+normRef) ||
					strings.HasSuffix(normRef, "/"+normDel) {
					res.Warnings = append(res.Warnings, FactGroundingWarning{
						FilePath: origDel,
						FactID:   f.ID,
						Claim:    f.Claim,
						Kind:     f.Kind,
						Message: fmt.Sprintf(
							"Staged commit deletes %q which is referenced by active architectural fact [%s] (%s): %q. Update the memo via 'sila memo' or confirm deprecation.",
							origDel, f.ID, f.Kind, f.Claim,
						),
					})
					break
				}
			}
		}
	}

	return res
}

// ExtractDeletedPaths parses git diff --cached --name-status output into a list of deleted or renamed-from paths.
func ExtractDeletedPaths(nameStatusOutput string) []string {
	var deleted []string
	lines := strings.Split(nameStatusOutput, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		status := fields[0]
		if strings.HasPrefix(status, "D") && len(fields) >= 2 {
			deleted = append(deleted, fields[1])
		} else if strings.HasPrefix(status, "R") && len(fields) >= 3 {
			// In rename R100 old_path new_path, old_path was moved/deleted
			deleted = append(deleted, fields[1])
		}
	}
	return deleted
}
