package git

import (
	"errors"
	"fmt"
	"os/exec"
	"path"
	"strings"
	"time"
)

// Commit range errors.
var (
	ErrCommitRangeInvalid  = errors.New("invalid commit range")
	ErrCommitRangeTooLarge = errors.New("commit range exceeds the 200-commit bound")
)

// MaxCommitRange bounds how many commits one trust review may read.
const MaxCommitRange = 200

// CommitInfo is the metadata of one reviewed commit.
type CommitInfo struct {
	SHA         string    `json:"sha"`
	Author      string    `json:"author"`
	Email       string    `json:"email"`
	CommittedAt time.Time `json:"committed_at"`
}

// CommitsInRange reads A..B, a single ref, or HEAD in one read-only pass.
func CommitsInRange(repoDir, expression string) ([]CommitInfo, error) {
	expr := strings.TrimSpace(expression)
	if expr == "" {
		expr = "HEAD"
	}
	if strings.HasPrefix(expr, "-") || strings.ContainsAny(expr, " \t\n") {
		return nil, fmt.Errorf("%w: %q", ErrCommitRangeInvalid, expression)
	}
	if parts := strings.Split(expr, ".."); len(parts) == 2 {
		if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("%w: %q", ErrCommitRangeInvalid, expression)
		}
	} else if strings.Contains(expr, "...") {
		return nil, fmt.Errorf("%w: %q", ErrCommitRangeInvalid, expression)
	}
	revArgs := []string{"-C", repoDir, "rev-list", "--max-count=201", expr}
	if !strings.Contains(expr, "..") {
		revArgs = []string{"-C", repoDir, "rev-list", "--max-count=1", expr}
	}
	out, err := exec.Command("git", revArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrCommitRangeInvalid, expression)
	}
	shas := strings.Fields(string(out))
	if len(shas) == 0 {
		return nil, nil
	}
	if len(shas) > MaxCommitRange {
		return nil, fmt.Errorf("%w: %q", ErrCommitRangeTooLarge, expression)
	}
	args := append([]string{"-C", repoDir, "log", "--no-walk=unsorted", "--format=%H%x00%an%x00%ae%x00%aI"}, shas...)
	meta, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("read commit metadata: %w", err)
	}
	var commits []CommitInfo
	for _, line := range strings.Split(strings.TrimSpace(string(meta)), "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 4 {
			continue
		}
		when, _ := time.Parse(time.RFC3339, fields[3])
		commits = append(commits, CommitInfo{SHA: fields[0], Author: fields[1], Email: fields[2], CommittedAt: when.UTC()})
	}
	return commits, nil
}

// ChangedPaths lists repository-relative paths a commit changed, against the
// first parent for merges and from the empty tree for a root commit.
func ChangedPaths(repoDir, sha string) ([]string, error) {
	sha = strings.TrimSpace(sha)
	if sha == "" || strings.HasPrefix(sha, "-") {
		return nil, fmt.Errorf("%w: %q", ErrCommitRangeInvalid, sha)
	}
	out, err := exec.Command("git", "-C", repoDir, "diff-tree", "--root", "-r", "--no-commit-id", "-m", "--first-parent", "--name-only", sha).Output()
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrCommitRangeInvalid, sha)
	}
	var paths []string
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		clean := strings.TrimSpace(line)
		if clean == "" || strings.HasPrefix(clean, "/") || strings.Contains(clean, "..") {
			continue
		}
		clean = path.Clean(clean)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		paths = append(paths, clean)
	}
	return paths, nil
}
