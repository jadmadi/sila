package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ReconcileResult holds the audit summary of a reconciliation pass.
type ReconcileResult struct {
	TotalChecked       int `json:"total_checked"`
	ReVerified         int `json:"re_verified"`
	NewlyStale         int `json:"newly_stale"`
	Recovered          int `json:"recovered"`
	AssertionsChecked  int `json:"assertions_checked"`
	AssertionsVerified int `json:"assertions_verified"`
}

// ReconcileFactDrift re-checks every fact for a project against the current repository filesystem,
// updating FileRefsExist, RefsCheckedAt, and Corroboration.
func (s *Store) ReconcileFactDrift(projectID, projectDir string) (ReconcileResult, error) {
	return s.reconcileFactDrift(projectID, projectDir, false)
}

// ReconcileFactDriftPreview audits drift without mutating any stored facts.
func (s *Store) ReconcileFactDriftPreview(projectID, projectDir string) (ReconcileResult, error) {
	return s.reconcileFactDrift(projectID, projectDir, true)
}

func (s *Store) reconcileFactDrift(projectID, projectDir string, dryRun bool) (ReconcileResult, error) {
	var res ReconcileResult
	if err := s.validateAssertionProjectRoot(projectDir, projectID); err != nil {
		return res, err
	}
	facts, err := s.FactsByProject(projectID)
	if err != nil {
		return res, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, f := range facts {
		res.TotalChecked++
		f.RefsCheckedAt = now
		if len(f.RepoFiles) == 0 {
			if !dryRun {
				_ = s.UpsertFact(f)
			}
			continue
		}
		existCount := 0
		for _, rf := range f.RepoFiles {
			full := rf
			if projectDir != "" {
				full = filepath.Join(projectDir, rf)
			}
			fi, statErr := os.Stat(full)
			if statErr == nil && !fi.IsDir() {
				existCount++
			}
		}

		ratio := 0.0
		if len(f.RepoFiles) > 0 {
			ratio = float64(existCount) / float64(len(f.RepoFiles))
		}
		isGrounded := ratio >= 0.5
		allExist := existCount == len(f.RepoFiles)

		oldExist := f.FileRefsExist || f.Verified
		oldFileExist := f.FileRefsExist
		f.FileRefsExist = isGrounded
		if !isGrounded {
			f.Verified = false
		} else if !oldFileExist {
			// Recovered from prior disk non-existence
			f.Verified = true
		}
		// When isGrounded and oldFileExist was already true, preserve existing f.Verified
		// so semantic contradictions and advisory stances are not erased by physical reconcile.

		if isGrounded && !oldExist {
			res.Recovered++
		} else if !isGrounded && oldExist {
			res.NewlyStale++
		} else if isGrounded {
			res.ReVerified++
		}

		// Adjust corroboration tier based on disk truth
		if !isGrounded {
			// Cross-Project File Grounding: if 0 files exist in the current project,
			// check if they exist in another known active repository.
			reAttributed := false
			if existCount == 0 && len(f.RepoFiles) > 0 {
				allProjects, _ := s.Projects()
				for _, otherP := range allProjects {
					if otherP.ID == projectID || otherP.Path == "" {
						continue
					}
					otherExist := 0
					for _, rf := range f.RepoFiles {
						full := filepath.Join(otherP.Path, rf)
						if fi, statErr := os.Stat(full); statErr == nil && !fi.IsDir() {
							otherExist++
						}
					}
					if otherExist > 0 && float64(otherExist)/float64(len(f.RepoFiles)) >= 0.5 {
						if !dryRun {
							newFact := f
							newFact.ID = ""
							newFact.ProjectID = otherP.ID
							newFact.FactHash = ""
							newFact.PrevHash = ""
							newFact.FileRefsExist = true
							newFact.Verified = true
							newFact.Confidence = "grounded"
							newFact.Corroboration = "grounded"
							if _, err := s.SupersedeFact(f.ID, newFact); err != nil {
								return res, fmt.Errorf("re-attribute %s to %s: %w", f.ID, otherP.ID, err)
							}
						}
						reAttributed = true
						break
					}
				}
			}
			if reAttributed {
				continue
			}

			if f.Confidence == "corroborated" || f.Confidence == "high" {
				if len(f.Tools) >= 2 {
					f.Confidence = "grounded"
					f.Corroboration = "grounded"
				} else {
					f.Confidence = "ungrounded"
					f.Corroboration = "ungrounded"
				}
			}
		} else {
			if allExist && (len(f.Tools) >= 2 || len(f.RepoFiles) >= 2) {
				f.Confidence = "corroborated"
				f.Corroboration = "corroborated"
			} else {
				f.Confidence = "grounded"
				f.Corroboration = "grounded"
			}
		}
		if !dryRun {
			if err := s.UpsertFact(f); err != nil {
				return res, err
			}
		}
	}
	lineage, err := s.reconcileAssertionLineage(projectDir, projectID, dryRun)
	if err != nil {
		return res, err
	}
	res.AssertionsChecked = lineage.Checked
	res.AssertionsVerified = lineage.Verified
	return res, nil
}

// ReconcileDrift audits drift across matching projects (or all projects if projectSel is empty).
func (s *Store) ReconcileDrift(projectSel string) (ReconcileResult, error) {
	var total ReconcileResult
	projects, err := s.Projects()
	if err != nil {
		return total, err
	}
	for _, p := range projects {
		if projectSel != "" && !ProjectSelectorMatches(p, projectSel) {
			continue
		}
		res, err := s.ReconcileFactDrift(p.ID, p.Path)
		if err != nil {
			return total, err
		}
		total.TotalChecked += res.TotalChecked
		total.ReVerified += res.ReVerified
		total.NewlyStale += res.NewlyStale
		total.Recovered += res.Recovered
		total.AssertionsChecked += res.AssertionsChecked
		total.AssertionsVerified += res.AssertionsVerified
	}
	return total, nil
}

// ReconcileDriftPreview audits drift across matching projects without mutating stored facts.
func (s *Store) ReconcileDriftPreview(projectSel string) (ReconcileResult, error) {
	var total ReconcileResult
	projects, err := s.Projects()
	if err != nil {
		return total, err
	}
	for _, p := range projects {
		if projectSel != "" && !ProjectSelectorMatches(p, projectSel) {
			continue
		}
		res, err := s.ReconcileFactDriftPreview(p.ID, p.Path)
		if err != nil {
			return total, err
		}
		total.TotalChecked += res.TotalChecked
		total.ReVerified += res.ReVerified
		total.NewlyStale += res.NewlyStale
		total.Recovered += res.Recovered
		total.AssertionsChecked += res.AssertionsChecked
		total.AssertionsVerified += res.AssertionsVerified
	}
	return total, nil
}
