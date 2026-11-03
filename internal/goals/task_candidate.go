package goals

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Diagnostic codes for artifact validation.
const (
	DiagEvidenceArtifactMissing        = "EVIDENCE_ARTIFACT_MISSING"
	DiagEvidenceArtifactDigestMismatch = "EVIDENCE_ARTIFACT_DIGEST_MISMATCH"
	DiagEvidenceArtifactBoundaryEscape = "EVIDENCE_ARTIFACT_BOUNDARY_ESCAPE"
	DiagEvidenceArtifactSymlinkEscape  = "EVIDENCE_ARTIFACT_SYMLINK_ESCAPE"
	DiagEvidenceArtifactInvalid        = "EVIDENCE_ARTIFACT_INVALID"
)

// isExcludedMutableEvidencePath reports whether a repository-relative path
// belongs to mutable goal evidence, baseline tracking, or generated registry indexes.
func isExcludedMutableEvidencePath(relPath, goalSlug string) bool {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	if clean == "." || clean == "" {
		return false
	}

	// 1. Internal sila state and git metadata
	if strings.HasPrefix(clean, ".sila/") || clean == ".sila" || strings.HasPrefix(clean, ".git/") || clean == ".git" {
		return true
	}

	// 2. Generated goals index
	if clean == "goals/README.md" {
		return true
	}

	// 3. Goal evidence directory (active or any goal)
	if strings.HasPrefix(clean, "goals/") && strings.Contains(clean, "/evidence/") {
		return true
	}
	if strings.HasPrefix(clean, "goals/") && strings.HasSuffix(clean, "/evidence") {
		return true
	}

	// 4. Goal baseline patch record
	if strings.HasPrefix(clean, "goals/") && strings.HasSuffix(clean, "/baseline.json") {
		return true
	}

	return false
}

// CaptureTaskCandidate resolves the current Git candidate commit, validates that
// no uncommitted or untracked source changes exist outside mutable evidence paths,
// and computes a deterministic cryptographic content digest across all tracked files.
func CaptureTaskCandidate(rootDir, slug string) (*TaskCandidate, error) {
	// 1. Resolve commit object ID
	cmdRev := exec.Command("git", "rev-parse", "HEAD")
	cmdRev.Dir = rootDir
	outRev, err := cmdRev.Output()
	if err != nil {
		return nil, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	commit := strings.TrimSpace(string(outRev))
	if len(commit) < 7 {
		return nil, fmt.Errorf("invalid git commit hash %q", commit)
	}

	// 2. Reject dirty source (modified, untracked, or staged non-evidence files)
	cmdStatus := exec.Command("git", "status", "--porcelain", "-uall")
	cmdStatus.Dir = rootDir
	outStatus, err := cmdStatus.Output()
	if err != nil {
		return nil, fmt.Errorf("git status --porcelain: %w", err)
	}

	rawLines := strings.Split(strings.TrimSpace(string(outStatus)), "\n")
	for _, l := range rawLines {
		line := strings.TrimSpace(l)
		if len(line) < 3 {
			continue
		}
		pathPart := strings.TrimSpace(line[3:])
		// Handle rename format R  old -> new
		if strings.Contains(pathPart, " -> ") {
			parts := strings.Split(pathPart, " -> ")
			pathPart = parts[len(parts)-1]
		}
		if strings.HasPrefix(pathPart, "\"") && strings.HasSuffix(pathPart, "\"") {
			if unq, uErr := strconv.Unquote(pathPart); uErr == nil {
				pathPart = unq
			}
		}

		if isExcludedMutableEvidencePath(pathPart, slug) {
			continue
		}

		return nil, fmt.Errorf("dirty source detected in working tree: %s (status %s)", pathPart, line[:2])
	}

	// 3. Compute deterministic content digest across tracked non-evidence files
	cmdFiles := exec.Command("git", "ls-files", "-s")
	cmdFiles.Dir = rootDir
	outFiles, err := cmdFiles.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files -s: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(outFiles)), "\n")
	type fileEntry struct {
		mode string
		sha  string
		path string
	}
	var entries []fileEntry

	for _, l := range lines {
		line := strings.TrimSpace(l)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		mode := parts[0]
		blobSha := parts[1]
		filePath := strings.Join(parts[3:], " ")
		if strings.HasPrefix(filePath, "\"") && strings.HasSuffix(filePath, "\"") {
			if unq, uErr := strconv.Unquote(filePath); uErr == nil {
				filePath = unq
			}
		}

		if isExcludedMutableEvidencePath(filePath, slug) {
			continue
		}

		entries = append(entries, fileEntry{
			mode: mode,
			sha:  blobSha,
			path: filepath.ToSlash(filepath.Clean(filePath)),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s %s %s\n", e.mode, e.sha, e.path)
	}
	contentDigest := hex.EncodeToString(h.Sum(nil))

	// 4. Capture tool versions
	toolVersions := map[string]string{
		"go": runtime.Version(),
	}
	cmdGit := exec.Command("git", "version")
	if outGit, err := cmdGit.Output(); err == nil {
		toolVersions["git"] = strings.TrimSpace(string(outGit))
	}

	return &TaskCandidate{
		Commit:        commit,
		ContentDigest: contentDigest,
		ToolVersions:  toolVersions,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
	}, nil
}

// ValidateEvidenceArtifacts validates that each referenced evidence artifact exists
// within the goal's evidence directory, does not escape through symlinks or path traversal,
// and matches its declared cryptographic SHA-256 digest.
func ValidateEvidenceArtifacts(rootDir, slug string, ev *TaskEvidence) []Diagnostic {
	var diags []Diagnostic

	if ev == nil {
		return diags
	}

	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return []Diagnostic{
			{
				Code:     DiagEvidenceArtifactInvalid,
				Severity: "error",
				GoalSlug: slug,
				Message:  fmt.Sprintf("cannot resolve absolute rootDir: %v", err),
			},
		}
	}

	evidenceDir := filepath.Join(absRootDir, "goals", slug, "evidence")
	absEvidenceDir, err := filepath.Abs(evidenceDir)
	if err != nil {
		return []Diagnostic{
			{
				Code:     DiagEvidenceArtifactInvalid,
				Severity: "error",
				GoalSlug: slug,
				Message:  fmt.Sprintf("cannot resolve absolute evidenceDir: %v", err),
			},
		}
	}

	for _, art := range ev.Artifacts {
		cleanPath := filepath.Clean(art.Path)

		// 1. Boundary escape checks
		if filepath.IsAbs(cleanPath) || strings.HasPrefix(cleanPath, "..") {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactBoundaryEscape,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("artifact path %q escapes evidence root", art.Path),
			})
			continue
		}

		// Normalize paths that might already carry goals/<slug>/evidence prefix
		relPrefix := filepath.Join("goals", slug, "evidence")
		if strings.HasPrefix(cleanPath, relPrefix+string(filepath.Separator)) {
			cleanPath = strings.TrimPrefix(cleanPath, relPrefix+string(filepath.Separator))
		}

		targetPath := filepath.Join(absEvidenceDir, cleanPath)
		absTarget, err := filepath.Abs(targetPath)
		if err != nil {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactInvalid,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("cannot resolve absolute path for artifact %q: %v", art.Path, err),
			})
			continue
		}

		// Ensure target path is strictly within absEvidenceDir
		if !strings.HasPrefix(absTarget, absEvidenceDir+string(filepath.Separator)) && absTarget != absEvidenceDir {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactBoundaryEscape,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("artifact path %q escapes evidence root directory", art.Path),
			})
			continue
		}

		// 2. Existence and symlink escape verification
		fi, err := os.Lstat(absTarget)
		if err != nil {
			if os.IsNotExist(err) {
				diags = append(diags, Diagnostic{
					Code:     DiagEvidenceArtifactMissing,
					Severity: "error",
					GoalSlug: slug,
					Path:     art.Path,
					Message:  fmt.Sprintf("artifact file %q does not exist on disk", art.Path),
				})
			} else {
				diags = append(diags, Diagnostic{
					Code:     DiagEvidenceArtifactInvalid,
					Severity: "error",
					GoalSlug: slug,
					Path:     art.Path,
					Message:  fmt.Sprintf("failed to stat artifact file %q: %v", art.Path, err),
				})
			}
			continue
		}

		// If symlink, verify resolved target does not escape
		evalTarget, err := filepath.EvalSymlinks(absTarget)
		if err != nil {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactInvalid,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("failed to evaluate symlinks for artifact %q: %v", art.Path, err),
			})
			continue
		}

		if !strings.HasPrefix(evalTarget, absEvidenceDir+string(filepath.Separator)) && evalTarget != absEvidenceDir {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactSymlinkEscape,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("artifact %q resolves to symlink target %q outside evidence directory", art.Path, evalTarget),
			})
			continue
		}

		if fi.IsDir() {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactInvalid,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("artifact path %q is a directory, expected regular file", art.Path),
			})
			continue
		}

		// 3. Digest verification
		data, err := os.ReadFile(evalTarget)
		if err != nil {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactInvalid,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("failed to read artifact file %q: %v", art.Path, err),
			})
			continue
		}

		h := sha256.Sum256(data)
		actualSHA := hex.EncodeToString(h[:])
		if actualSHA != art.SHA256 {
			diags = append(diags, Diagnostic{
				Code:     DiagEvidenceArtifactDigestMismatch,
				Severity: "error",
				GoalSlug: slug,
				Path:     art.Path,
				Message:  fmt.Sprintf("artifact %q digest mismatch: expected %q, observed %q", art.Path, art.SHA256, actualSHA),
			})
		}
	}

	return diags
}
