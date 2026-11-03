package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Stale reference classes. Every scanned fact gets exactly one class.
const (
	StaleMoved     = "moved"
	StaleDeleted   = "deleted"
	StaleGenerated = "generated_artifact"
	StaleWrong     = "wrong_reference"
)

// StaleReference describes one stale fact and its filesystem classification.
type StaleReference struct {
	FactID         string   `json:"fact_id"`
	ProjectID      string   `json:"project_id"`
	Claim          string   `json:"claim"`
	RepoFiles      []string `json:"repo_files"`
	MissingFiles   []string `json:"missing_files"`
	Classification string   `json:"classification"`
	SuggestedFiles []string `json:"suggested_files,omitempty"`
}

// HygieneResult summarizes one scan or repair pass.
type HygieneResult struct {
	Scanned    int              `json:"scanned"`
	Repaired   int              `json:"repaired"`
	Archived   int              `json:"archived"`
	Unresolved int              `json:"unresolved"`
	Before     int              `json:"before"`
	After      int              `json:"after"`
	DryRun     bool             `json:"dry_run"`
	Items      []StaleReference `json:"items,omitempty"`
	Errors     []string         `json:"errors,omitempty"`
}

var hygieneSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"dist": true, "build": true, "out": true, ".next": true, ".nuxt": true,
	"coverage": true, "vendor": true, "target": true, ".cache": true,
	".turbo": true, ".gradle": true, "__pycache__": true, ".venv": true,
	"venv": true, "tmp": true, "temp": true,
}

var hygieneGeneratedDirs = []string{
	"node_modules/", "dist/", "build/", "out/", ".next/", "coverage/",
	"vendor/", "target/", ".cache/", "__pycache__/", ".venv/", "tmp/",
	"temp/", ".turbo/",
}

func isGeneratedReference(path string) bool {
	p := filepath.ToSlash(path)
	for _, dir := range hygieneGeneratedDirs {
		if strings.HasPrefix(p, dir) || strings.Contains(p, "/"+dir) {
			return true
		}
	}
	return false
}

// projectFileIndex maps basenames to repository-relative paths. One walk per
// project keeps classification linear even with thousands of stale claims.
func projectFileIndex(root string) map[string][]string {
	index := make(map[string][]string)
	if root == "" {
		return index
	}
	count := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && hygieneSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		index[d.Name()] = append(index[d.Name()], rel)
		count++
		if count > 250000 {
			return fs.SkipAll
		}
		return nil
	})
	return index
}

func classifyMissingFile(root, missing string, index map[string][]string) (string, []string) {
	if isGeneratedReference(missing) {
		return StaleGenerated, nil
	}
	candidates := index[filepath.Base(missing)]
	parent := filepath.ToSlash(filepath.Dir(missing))
	var sameParent []string
	for _, c := range candidates {
		if filepath.ToSlash(filepath.Dir(c)) == parent {
			sameParent = append(sameParent, c)
		}
	}
	if len(sameParent) == 1 {
		return StaleMoved, sameParent
	}
	if len(candidates) == 1 {
		return StaleMoved, candidates
	}
	if len(candidates) > 1 {
		return StaleWrong, nil
	}
	return StaleDeleted, nil
}

// ScanStaleReferences classifies every active stale fact for the selector.
func (s *Store) ScanStaleReferences(projectSel string, limit int) ([]StaleReference, error) {
	facts, err := s.StaleFacts(projectSel, limit)
	if err != nil {
		return nil, err
	}
	projects, err := s.Projects()
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string, len(projects))
	for _, p := range projects {
		paths[p.ID] = p.Path
	}
	indexes := make(map[string]map[string][]string)
	var out []StaleReference
	for _, f := range facts {
		root := paths[f.ProjectID]
		index, ok := indexes[f.ProjectID]
		if !ok {
			index = projectFileIndex(root)
			indexes[f.ProjectID] = index
		}
		ref := StaleReference{FactID: f.ID, ProjectID: f.ProjectID, Claim: f.Claim, RepoFiles: f.RepoFiles}
		class := ""
		repairable := true
		for _, rf := range f.RepoFiles {
			full := rf
			if !filepath.IsAbs(full) && root != "" {
				full = filepath.Join(root, rf)
			}
			if fi, statErr := os.Stat(full); statErr == nil && !fi.IsDir() {
				continue
			}
			ref.MissingFiles = append(ref.MissingFiles, rf)
			fileClass, suggestions := classifyMissingFile(root, rf, index)
			if class == "" {
				class = fileClass
			}
			if fileClass != StaleMoved {
				repairable = false
				if class != StaleWrong {
					class = fileClass
				}
			}
			ref.SuggestedFiles = append(ref.SuggestedFiles, suggestions...)
		}
		if len(ref.MissingFiles) == 0 {
			// The file reappeared since the last reconcile; do not archive it.
			class = StaleMoved
			repairable = false
		}
		if class == "" {
			class = StaleWrong
		}
		ref.Classification = class
		if !repairable {
			ref.SuggestedFiles = nil
		}
		out = append(out, ref)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].FactID < out[j].FactID
	})
	return out, nil
}

// RepairStaleReferences re-points moved files and archives all other stale
// facts with the non-hashed is_stale marker. It never deletes a fact and never
// changes a hashed field, so the ledger chain stays valid. Archived facts are
// excluded from StaleFacts and therefore from `dream query --stale`.
func (s *Store) RepairStaleReferences(projectSel string, dryRun bool, limit int) (HygieneResult, error) {
	items, err := s.ScanStaleReferences(projectSel, limit)
	if err != nil {
		return HygieneResult{}, err
	}
	res := HygieneResult{Scanned: len(items), Before: len(items), Items: items, DryRun: dryRun}
	if dryRun {
		res.After = len(items)
		return res, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	for _, item := range items {
		if item.Classification == StaleMoved && len(item.SuggestedFiles) > 0 {
			replaced := make([]string, 0, len(item.RepoFiles))
			suggestions := append([]string{}, item.SuggestedFiles...)
			valid := true
			for _, rf := range item.RepoFiles {
				if !containsString(item.MissingFiles, rf) {
					replaced = append(replaced, rf)
					continue
				}
				if len(suggestions) == 0 {
					valid = false
					break
				}
				replaced = append(replaced, suggestions[0])
				suggestions = suggestions[1:]
			}
			if valid {
				if _, err := tx.Exec(`UPDATE facts SET repo_files = ?, file_refs_exist = 1, refs_checked_at = ? WHERE id = ?`, joinJSON(replaced), now, item.FactID); err != nil {
					res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", item.FactID, err))
					continue
				}
				res.Repaired++
				continue
			}
		}
		if _, err := tx.Exec(`UPDATE facts SET is_stale = 1 WHERE id = ?`, item.FactID); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", item.FactID, err))
			continue
		}
		res.Archived++
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	remaining, err := s.StaleFacts(projectSel, 0)
	if err != nil {
		return res, err
	}
	res.After = len(remaining)
	res.Unresolved = res.After
	return res, nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
