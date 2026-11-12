package agents

import (
	"os"
	"path/filepath"
	"strings"
)

// CandidateProject represents a registered project discovered from the knowledge store.
type CandidateProject struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// ProjectSyncResult captures sync execution or preview for a single project.
type ProjectSyncResult struct {
	ProjectID    string      `json:"project_id"`
	Path         string      `json:"path"`
	Result       *SyncResult `json:"result,omitempty"`
	Error        string      `json:"error,omitempty"`
	Atomic       bool        `json:"atomic"`
	RolledBack   bool        `json:"rolled_back,omitempty"`
	FilesSkipped int         `json:"files_skipped,omitempty"`
	FailureStage string      `json:"failure_stage,omitempty"`
	Skipped      bool        `json:"skipped,omitempty"`
	SkipReason   string      `json:"skip_reason,omitempty"`
}

// MultiSyncResult contains aggregate sync outcomes across all projects.
type MultiSyncResult struct {
	Projects       []ProjectSyncResult `json:"projects"`
	Total          int                 `json:"total"`
	Succeeded      int                 `json:"succeeded"`
	Failed         int                 `json:"failed"`
	Skipped        int                 `json:"skipped"`
	AtomicFailures int                 `json:"atomic_failures"`
	FilesWritten   int                 `json:"files_written"`
	Preview        bool                `json:"preview"`
	ChangedOnly    bool                `json:"changed_only"`
	Forced         bool                `json:"forced"`
	Written        int                 `json:"written"`
}

// ProjectCheckResult captures drift detection for a single project.
type ProjectCheckResult struct {
	ProjectID string       `json:"project_id"`
	Path      string       `json:"path"`
	Report    *DriftReport `json:"report,omitempty"`
	Error     string       `json:"error,omitempty"`
}

// MultiCheckResult contains aggregate drift audit outcomes across all projects.
type MultiCheckResult struct {
	Projects        []ProjectCheckResult `json:"projects"`
	Total           int                  `json:"total"`
	InSync          int                  `json:"in_sync"`
	Drifted         int                  `json:"drifted"`
	HashDrift       int                  `json:"hash_drift"`
	Legacy          int                  `json:"legacy"`
	MissingTarget   int                  `json:"missing_target"`
	PrinciplesDrift int                  `json:"principles_drift"`
	Failed          int                  `json:"failed"`
}

// FilterValidProjects validates candidate project paths against on-disk git/rules boundaries:
// 1. Checks that the directory exists and is a directory.
// 2. Checks for a .git directory or an existing AGENTS.md.
// 3. Silently skips root directories (/ or user home directly) or non-existent directories.
// 4. De-duplicates canonicalized paths to avoid duplicate passes on symlinked roots.
func FilterValidProjects(candidates []CandidateProject) []CandidateProject {
	var valid []CandidateProject
	seen := make(map[string]bool)

	home, _ := os.UserHomeDir()
	cleanHome := ""
	if home != "" {
		cleanHome = filepath.Clean(home)
	}

	for _, c := range candidates {
		p := strings.TrimSpace(c.Path)
		if p == "" {
			continue
		}

		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		realPath, err := filepath.EvalSymlinks(abs)
		if err != nil {
			realPath = filepath.Clean(abs)
		}

		if realPath == "/" || realPath == "." || realPath == "/tmp" || realPath == "/var/tmp" {
			continue
		}
		if cleanHome != "" && (realPath == cleanHome || isNonRepoHomeDir(cleanHome, realPath)) {
			continue
		}

		fi, err := os.Stat(realPath)
		if err != nil || !fi.IsDir() {
			continue
		}

		gitDir := filepath.Join(realPath, ".git")
		agentsFile := filepath.Join(realPath, "AGENTS.md")
		hasGit := false
		if gfi, err := os.Stat(gitDir); err == nil && (gfi.IsDir() || !gfi.IsDir()) {
			hasGit = true
		}
		hasAgents := false
		if afi, err := os.Stat(agentsFile); err == nil && !afi.IsDir() {
			hasAgents = true
		}

		if !hasGit && !hasAgents {
			continue
		}

		if seen[realPath] {
			continue
		}
		seen[realPath] = true

		id := strings.TrimSpace(c.ID)
		if id == "" {
			id = filepath.Base(realPath)
		}

		valid = append(valid, CandidateProject{
			ID:   id,
			Path: realPath,
		})
	}

	return valid
}

var nonRepoHomePrefixes = []string{
	".agents", ".gemini", ".claude", ".cursor", ".devin", ".zcode",
	".config", ".local", ".cache", "Downloads", "Pictures", "Documents",
	"Music", "Videos", "Desktop", "temp", "temp-agy", "temp-mise", "tmp",
}

func isNonRepoHomeDir(cleanHome, cleanDir string) bool {
	rel, err := filepath.Rel(cleanHome, cleanDir)
	if err != nil {
		return false
	}
	relSlash := filepath.ToSlash(rel)
	if relSlash == ".." || strings.HasPrefix(relSlash, "../") {
		return false
	}
	for _, p := range nonRepoHomePrefixes {
		if relSlash == p || strings.HasPrefix(relSlash, p+"/") {
			_, gitErr := os.Stat(filepath.Join(cleanDir, ".git"))
			return gitErr != nil
		}
	}
	return false
}

// SyncAllProjects executes DeployTargets across all provided valid projects.
// If baseOpts.Preview is true, previews writes without changing disks.
// Per-project errors are isolated and captured in ProjectSyncResult.Error.
func SyncAllProjects(projects []CandidateProject, baseOpts CompileOptions) *MultiSyncResult {
	res := &MultiSyncResult{
		Projects:    make([]ProjectSyncResult, 0, len(projects)),
		Total:       len(projects),
		Preview:     baseOpts.Preview,
		ChangedOnly: !baseOpts.Force,
		Forced:      baseOpts.Force,
	}

	for _, p := range projects {
		opts := baseOpts
		opts.RepoRoot = p.Path
		opts.ProjectName = p.ID

		item := ProjectSyncResult{
			ProjectID: p.ID,
			Path:      p.Path,
		}

		syncRes, err := DeployTargets(p.Path, opts, []string{"all"})
		if err != nil {
			item.Error = err.Error()
			item.FailureStage = "plan"
			item.Atomic = false
			res.Failed++
			res.AtomicFailures++
		} else {
			item.Result = syncRes
			item.Atomic = syncRes.Atomic
			item.RolledBack = syncRes.RolledBack
			item.FilesSkipped = syncRes.FilesSkipped
			item.FailureStage = syncRes.FailureStage
			item.Skipped = len(syncRes.FilesWritten) == 0 && len(syncRes.Symlinks) == 0 && len(syncRes.Errors) == 0
			if item.Skipped {
				for _, p := range syncRes.Plan {
					if p.Action == "skip" && p.SkipReason != "" {
						item.SkipReason = p.SkipReason
						break
					}
				}
				if item.SkipReason == "" {
					item.SkipReason = "up to date"
				}
			}
			res.FilesWritten += len(syncRes.FilesWritten)
			res.Written += syncRes.Written
			if len(syncRes.Errors) > 0 {
				res.Failed++
				if !syncRes.Atomic || syncRes.FailureStage == "plan" || syncRes.FailureStage == "rollback" {
					res.AtomicFailures++
				}
			} else {
				res.Succeeded++
				if len(syncRes.FilesWritten) == 0 && len(syncRes.Symlinks) == 0 && syncRes.FilesSkipped > 0 {
					res.Skipped++
				}
			}
		}
		res.Projects = append(res.Projects, item)
	}

	return res
}

// CheckAllProjects executes DetectDrift across all provided valid projects.
// Per-project errors are isolated and captured in ProjectCheckResult.Error.
func CheckAllProjects(projects []CandidateProject, baseOpts CompileOptions) *MultiCheckResult {
	res := &MultiCheckResult{
		Projects: make([]ProjectCheckResult, 0, len(projects)),
		Total:    len(projects),
	}

	for _, p := range projects {
		opts := baseOpts
		opts.RepoRoot = p.Path
		opts.ProjectName = p.ID

		item := ProjectCheckResult{
			ProjectID: p.ID,
			Path:      p.Path,
		}

		report, err := DetectDrift(p.Path, opts)
		if err != nil {
			item.Error = err.Error()
			res.Failed++
		} else {
			item.Report = report
			if report.HasDrift {
				res.Drifted++
			} else {
				res.InSync++
			}
			for _, kind := range report.DriftKinds {
				switch kind {
				case "managed_hash_drift":
					res.HashDrift++
				case "legacy_block":
					res.Legacy++
				case "missing_target":
					res.MissingTarget++
				case "principles_drift":
					res.PrinciplesDrift++
				}
			}
		}
		res.Projects = append(res.Projects, item)
	}

	return res
}
